package tools

import (
	"regexp"
	"strings"
)

type GuardianDecision string

const (
	GuardianAllow GuardianDecision = "allow"
	GuardianWarn  GuardianDecision = "warn"
	GuardianBlock GuardianDecision = "block"
)

type GuardianReview struct {
	Decision GuardianDecision
	Reason   string
}

// carriesShellCommand reports whether the action's Detail is a shell command.
// Destructive-command rules only run for these tools, so a diff or file body
// that merely mentions "rm -rf /" (e.g. a patch to this file) never trips them.
func carriesShellCommand(tool string) bool {
	return tool == "run_command" || tool == "exec_session_start"
}

// cmdRule anchors a pattern to a command position — start of line, after a
// separator (; & | subshell backtick), or after sudo — so dangerous strings
// inside arguments (echo "rm -rf /") don't match.
func cmdRule(pattern string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)(?:^\s*|[;&|(` + "`" + `]\s*|\bsudo\s+)` + pattern)
}

var guardianBlockRules = []struct {
	re     *regexp.Regexp
	reason string
}{
	{cmdRule(`rm\s+(?:-{1,2}[^\s;&|]+\s+)*(?:/|/\*|~|~/|\$home|/tmp/?)\s*(?:$|[;&|)])`), "rm targeting a root, home, or temp path"},
	{cmdRule(`rm\b[^\n;&|]*--no-preserve-root`), "rm with --no-preserve-root"},
	{cmdRule(`git\s+push\b[^\n;&|]*\s(?:--force(?:-with-lease)?|-f)\b`), "git force push rewrites remote history"},
	{cmdRule(`git\s+push\b[^\n;&|]*\s(?:--delete|-d)\b`), "git push --delete removes remote refs"},
	{cmdRule(`git\s+reset\b[^\n;&|]*--hard\b`), "git reset --hard discards local changes"},
	{cmdRule(`git\s+clean\b[^\n;&|]*\s-\w*[fx]`), "git clean -f/-x deletes untracked files"},
	{cmdRule(`docker\s+system\s+prune\b[^\n;&|]*(?:--force\b|\s-\w*f)`), "forced docker prune wipes containers, images, and volumes"},
	{cmdRule(`truncate\s+[^\n;&|]*(?:-s\s*0|--size[=\s]*0)\b`), "truncate to zero destroys file contents"},
	{cmdRule(`chmod\s+(?:-{1,2}\S+\s+)*0?777\b`), "chmod 777 grants world write"},
	{cmdRule(`mkfs(?:\.\w+)?\s`), "mkfs formats a filesystem"},
	{cmdRule(`dd\b[^\n;&|]*\bof=/dev/`), "dd writing to a raw device"},
	{cmdRule(`heroku\s+pg:reset\b`), "heroku pg:reset wipes a database"},
}

var gitHighImpactRe = cmdRule(`git\s+(?:push|rebase|merge)\b`)

var mutatingCommandRes = []*regexp.Regexp{
	cmdRule(`git\s+(?:add|commit|checkout|switch|merge|rebase|push)\b`),
	cmdRule(`(?:rm|mv|cp|tee)\s`),
	cmdRule(`(?:sed|perl)\s+-i\b`),
}

var (
	// Shell interpreters running an inline command string (bash -c "..."),
	// plus eval, which executes its argument the same way.
	shellPayloadRe = regexp.MustCompile(`(?m)(?:^\s*|[;&|(` + "`" + `]\s*|\bsudo\s+)(?:(?:ba|da|k|z|fi)?sh\s+(?:-{1,2}[^c\s]\S*\s+)*-\S*c\S*\s+|eval\s+)("(?:\\.|[^"\\])*"|'[^']*'|\S+)`)
	// <<TAG heredoc opener; (^|[^<]) excludes <<< herestrings.
	heredocOpenRe = regexp.MustCompile(`(?:^|[^<])<<-?\s*["']?([A-Za-z_]\w*)["']?`)
	// Redirects that don't mutate files: fd dups and /dev sinks.
	redirectNoiseRe = regexp.MustCompile(`\d?>&\d?|>{1,2}\s*/dev/\S*`)
	redirectRe      = regexp.MustCompile(`>>?`)
)

// unwrapShellPayloads appends the command strings passed to `sh -c`, `bash -c`,
// eval, etc. as their own lines, so the (?m)^ anchor in cmdRule sees them as
// commands. Loops to peel nested wrappers (sh -c 'sh -c "..."'), capped.
func unwrapShellPayloads(command string) string {
	for range 4 {
		matches := shellPayloadRe.FindAllStringSubmatch(command, -1)
		var extracted []string
		for _, m := range matches {
			p := m[1]
			switch {
			case strings.HasPrefix(p, `'`) && len(p) >= 2:
				p = p[1 : len(p)-1]
			case strings.HasPrefix(p, `"`) && len(p) >= 2:
				p = strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(p[1 : len(p)-1])
			}
			if !strings.Contains(command, "\n"+p) {
				extracted = append(extracted, p)
			}
		}
		if len(extracted) == 0 {
			return command
		}
		command += "\n" + strings.Join(extracted, "\n")
	}
	return command
}

// stripHeredocBodies removes heredoc content so text fed to a command (like a
// patch being written with cat <<EOF) is not mistaken for a command.
func stripHeredocBodies(command string) string {
	if !strings.Contains(command, "<<") {
		return command
	}
	lines := strings.Split(command, "\n")
	kept := make([]string, 0, len(lines))
	var term string
	for _, line := range lines {
		if term != "" {
			if strings.TrimSpace(line) == term {
				term = ""
			}
			continue
		}
		kept = append(kept, line)
		if m := heredocOpenRe.FindStringSubmatch(line); m != nil {
			term = m[1]
		}
	}
	return strings.Join(kept, "\n")
}

// ReviewApprovalAction performs a compact, deterministic pre-approval review.
// It is intentionally conservative: obviously destructive commands are blocked,
// risky mutations without session context are warned, and ordinary edits pass.
func ReviewApprovalAction(transcript string, action Action) GuardianReview {
	hasContext := strings.TrimSpace(transcript) != ""

	if carriesShellCommand(action.Tool) {
		command := unwrapShellPayloads(stripHeredocBodies(strings.ToLower(strings.TrimSpace(action.Detail))))
		for _, rule := range guardianBlockRules {
			if rule.re.MatchString(command) {
				return GuardianReview{
					Decision: GuardianBlock,
					Reason:   rule.reason + "; not auto-approvable",
				}
			}
		}
		if !hasContext {
			if gitHighImpactRe.MatchString(command) {
				return GuardianReview{
					Decision: GuardianWarn,
					Reason:   "high-impact command has no compact task context",
				}
			}
			if commandLooksMutating(command) {
				return GuardianReview{
					Decision: GuardianWarn,
					Reason:   "mutating command is missing task context",
				}
			}
		}
		return GuardianReview{Decision: GuardianAllow}
	}

	switch action.Tool {
	case "write_file", "edit_file", "apply_patch", "ast_edit", "artifact_write":
		if strings.TrimSpace(action.Detail) == "" {
			return GuardianReview{
				Decision: GuardianWarn,
				Reason:   "file mutation has no diff or content detail",
			}
		}
	}

	return GuardianReview{Decision: GuardianAllow}
}

func commandLooksMutating(command string) bool {
	for _, re := range mutatingCommandRes {
		if re.MatchString(command) {
			return true
		}
	}
	return redirectRe.MatchString(redirectNoiseRe.ReplaceAllString(command, ""))
}
