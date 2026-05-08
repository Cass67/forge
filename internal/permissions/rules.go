package permissions

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

type Behavior string

const (
	BehaviorAllow Behavior = "allow"
	BehaviorAsk   Behavior = "ask"
	BehaviorDeny  Behavior = "deny"
)

type Rule struct {
	Scope    Scope
	Behavior Behavior
	Tool     string
	Pattern  string
	Source   string
}

type Action struct {
	Tool    string
	Summary string
	Detail  string
	Path    string
}

type Decision struct {
	Behavior Behavior
	Rule     Rule
	Matched  bool
}

func Evaluate(rules []Rule, action Action) Decision {
	var best Decision
	bestScope := 0
	bestBehavior := 0
	for _, rule := range rules {
		if err := ValidateRule(rule); err != nil {
			continue
		}
		if !ruleMatches(rule, action) {
			continue
		}
		rank := scopeRank(rule.Scope)
		priority := behaviorPriority(rule.Behavior)
		if !best.Matched || rank > bestScope || (rank == bestScope && priority > bestBehavior) {
			best = Decision{Behavior: rule.Behavior, Rule: rule, Matched: true}
			bestScope = rank
			bestBehavior = priority
		}
	}
	return best
}

func ValidateRule(rule Rule) error {
	if scopeRank(rule.Scope) == 0 {
		return fmt.Errorf("unknown permission scope %q", rule.Scope)
	}
	switch rule.Behavior {
	case BehaviorAllow, BehaviorAsk, BehaviorDeny:
	default:
		return fmt.Errorf("unknown permission behavior %q", rule.Behavior)
	}
	tool := strings.TrimSpace(rule.Tool)
	if tool == "" {
		return fmt.Errorf("permission tool is required")
	}
	if !supportedTool(tool) {
		return fmt.Errorf("unknown permission tool %q", tool)
	}
	return nil
}

func behaviorPriority(behavior Behavior) int {
	switch behavior {
	case BehaviorDeny:
		return 3
	case BehaviorAsk:
		return 2
	case BehaviorAllow:
		return 1
	default:
		return 0
	}
}

func ruleMatches(rule Rule, action Action) bool {
	if !strings.EqualFold(strings.TrimSpace(rule.Tool), strings.TrimSpace(action.Tool)) {
		return false
	}
	pattern := strings.TrimSpace(rule.Pattern)
	if pattern == "" {
		return true
	}
	if isPathTool(action.Tool) {
		path := strings.TrimSpace(action.Path)
		if path == "" {
			path = actionPathFromSummary(action.Summary)
		}
		return matchPathPattern(pattern, path)
	}
	return matchCommandPattern(pattern, action.Summary)
}

func isPathTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "read_file", "write_file", "edit_file", "apply_patch", "artifact_write", "artifact_read":
		return true
	default:
		return false
	}
}

func actionPathFromSummary(summary string) string {
	fields := strings.Fields(strings.TrimSpace(summary))
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}

func matchPathPattern(pattern, path string) bool {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	path = filepath.ToSlash(strings.TrimSpace(path))
	if pattern == "" || path == "" {
		return false
	}
	re, err := globPatternRegex(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(path)
}

func matchCommandPattern(pattern, command string) bool {
	pattern = normalizeCommandPattern(pattern)
	command = normalizeCommandPattern(command)
	if pattern == "" || command == "" {
		return false
	}
	if strings.HasSuffix(pattern, " *") {
		base := strings.TrimSpace(strings.TrimSuffix(pattern, " *"))
		return command == base || strings.HasPrefix(command, base+" ")
	}
	re, err := commandPatternRegex(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(command)
}

func normalizeCommandPattern(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, ":", " ")
	return strings.Join(strings.Fields(value), " ")
}

func commandPatternRegex(pattern string) (*regexp.Regexp, error) {
	var out strings.Builder
	out.WriteString("^")
	escaped := false
	lastSpace := false
	for _, r := range pattern {
		switch {
		case escaped:
			out.WriteString(regexp.QuoteMeta(string(r)))
			escaped = false
			lastSpace = false
		case r == '\\':
			escaped = true
		case unicode.IsSpace(r):
			if !lastSpace {
				out.WriteString(`\s+`)
				lastSpace = true
			}
		case r == '*':
			out.WriteString(`.*`)
			lastSpace = false
		default:
			out.WriteString(regexp.QuoteMeta(string(r)))
			lastSpace = false
		}
	}
	if escaped {
		return nil, fmt.Errorf("command pattern ends with escape")
	}
	out.WriteString("$")
	return regexp.Compile(out.String())
}

func globPatternRegex(pattern string) (*regexp.Regexp, error) {
	var out strings.Builder
	out.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		switch ch {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				if i+2 < len(pattern) && pattern[i+2] == '/' {
					out.WriteString(`(?:.*/)?`)
					i += 2
					continue
				}
				out.WriteString(`.*`)
				i++
				continue
			}
			out.WriteString(`[^/]*`)
		case '?':
			out.WriteString(`[^/]`)
		default:
			out.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	out.WriteString("$")
	return regexp.Compile(out.String())
}

func supportedTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "apply_patch", "artifact_read", "artifact_write", "code_search", "edit_file", "exec_session_start",
		"glob", "lsp_definition", "lsp_document_symbols", "lsp_hover", "lsp_references", "read_file",
		"run_command", "search", "view_image", "web_fetch", "write_file":
		return true
	default:
		return strings.HasPrefix(tool, "mcp__") || strings.Contains(tool, "__")
	}
}
