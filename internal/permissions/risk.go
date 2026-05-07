package permissions

import (
	"path/filepath"
	"strings"
)

type RiskLevel string

const (
	RiskLow         RiskLevel = "low"
	RiskMedium      RiskLevel = "medium"
	RiskHigh        RiskLevel = "high"
	RiskDestructive RiskLevel = "destructive"
)

type RiskFacts struct {
	Level            RiskLevel
	MutatesWorkspace bool
	TouchesGitState  bool
	TouchesSecrets   bool
	Network          bool
	Destructive      bool
	ClassifierImmune bool
	Reasons          []string
}

func AnalyzeAction(action Action) RiskFacts {
	facts := RiskFacts{Level: RiskLow}
	tool := strings.TrimSpace(action.Tool)
	summary := strings.TrimSpace(action.Summary)
	detail := strings.TrimSpace(action.Detail)
	path := actionPath(action)

	if mutatingTool(tool) && tool != "run_command" {
		facts.MutatesWorkspace = true
		facts.Level = maxRisk(facts.Level, RiskMedium)
		facts.Reasons = append(facts.Reasons, "mutating tool")
	}

	if tool == "run_command" {
		analyzeCommand(summary, &facts)
	}

	if path != "" {
		analyzePath(path, &facts)
	}
	if detail != "" && looksSecretAdjacent(detail) {
		facts.TouchesSecrets = true
		facts.ClassifierImmune = true
		facts.Level = maxRisk(facts.Level, RiskHigh)
		facts.Reasons = append(facts.Reasons, "secret-adjacent detail")
	}

	if facts.Destructive {
		facts.Level = RiskDestructive
		facts.ClassifierImmune = true
	}
	return facts
}

func analyzeCommand(command string, facts *RiskFacts) {
	lower := strings.ToLower(strings.TrimSpace(command))
	if lower == "" {
		return
	}

	if commandUsesNetwork(lower) {
		facts.Network = true
		facts.Level = maxRisk(facts.Level, RiskMedium)
		facts.Reasons = append(facts.Reasons, "network command")
	}
	if commandMutates(lower) {
		facts.MutatesWorkspace = true
		facts.Level = maxRisk(facts.Level, RiskMedium)
		facts.Reasons = append(facts.Reasons, "mutating command")
	}
	if commandTouchesGitState(lower) {
		facts.TouchesGitState = true
		facts.MutatesWorkspace = true
		facts.Level = maxRisk(facts.Level, RiskMedium)
		facts.Reasons = append(facts.Reasons, "git state command")
	}
	if commandIsDestructive(lower) {
		facts.Destructive = true
		facts.MutatesWorkspace = true
		facts.Reasons = append(facts.Reasons, "destructive command")
	}
}

func analyzePath(path string, facts *RiskFacts) {
	normalized := filepath.ToSlash(strings.TrimSpace(path))
	if normalized == "" {
		return
	}
	if strings.HasPrefix(normalized, ".git/") || normalized == ".git" {
		facts.TouchesGitState = true
		facts.ClassifierImmune = true
		facts.Level = maxRisk(facts.Level, RiskHigh)
		facts.Reasons = append(facts.Reasons, "git state path")
	}
	if looksSecretAdjacent(normalized) {
		facts.TouchesSecrets = true
		facts.ClassifierImmune = true
		facts.Level = maxRisk(facts.Level, RiskHigh)
		facts.Reasons = append(facts.Reasons, "secret-adjacent path")
	}
}

func actionPath(action Action) string {
	if path := strings.TrimSpace(action.Path); path != "" {
		return path
	}
	if isPathTool(action.Tool) {
		return actionPathFromSummary(action.Summary)
	}
	return ""
}

func mutatingTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "write_file", "edit_file", "apply_patch", "artifact_write", "run_command", "exec_session_start":
		return true
	default:
		return false
	}
}

func commandMutates(command string) bool {
	prefixes := []string{
		"git commit", "git add", "git rm", "git mv", "git reset", "git checkout", "git switch", "git merge", "git rebase",
		"go mod tidy", "npm install", "pnpm install", "yarn install", "touch ", "mkdir ", "rm ", "mv ", "cp ",
	}
	for _, prefix := range prefixes {
		if command == strings.TrimSpace(prefix) || strings.HasPrefix(command, prefix) {
			return true
		}
	}
	return strings.Contains(command, ">")
}

func commandTouchesGitState(command string) bool {
	prefixes := []string{"git commit", "git add", "git rm", "git mv", "git reset", "git checkout", "git switch", "git merge", "git rebase", "git config"}
	for _, prefix := range prefixes {
		if command == prefix || strings.HasPrefix(command, prefix+" ") {
			return true
		}
	}
	return false
}

func commandUsesNetwork(command string) bool {
	for _, token := range []string{"curl ", "wget ", "ssh ", "scp ", "rsync ", "git clone", "git fetch", "git pull", "git push", "npm install", "pnpm install", "yarn install"} {
		if strings.Contains(command, token) || strings.HasPrefix(command, strings.TrimSpace(token)+" ") {
			return true
		}
	}
	return strings.Contains(command, "http://") || strings.Contains(command, "https://")
}

func commandIsDestructive(command string) bool {
	patterns := []string{
		"rm -rf /", "sudo ", "| sh", "| bash", "| zsh", "chmod 777", "mkfs", "> /dev/", "dd if=", "curl ", "wget ",
	}
	for _, pattern := range patterns {
		if strings.Contains(command, pattern) {
			if pattern == "curl " || pattern == "wget " {
				return strings.Contains(command, "| sh") || strings.Contains(command, "| bash") || strings.Contains(command, "| zsh")
			}
			return true
		}
	}
	return false
}

func looksSecretAdjacent(value string) bool {
	normalized := strings.ToLower(filepath.ToSlash(strings.TrimSpace(value)))
	if normalized == "" {
		return false
	}
	base := filepath.Base(normalized)
	if strings.HasPrefix(base, ".env") || strings.Contains(base, "secret") || strings.Contains(base, "credential") {
		return true
	}
	for _, suffix := range []string{".pem", ".key", ".p12", ".pfx"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return strings.Contains(normalized, "/.env") || strings.Contains(normalized, "/secrets/") || strings.Contains(normalized, "/credentials/")
}

func maxRisk(a, b RiskLevel) RiskLevel {
	if riskRank(b) > riskRank(a) {
		return b
	}
	return a
}

func riskRank(level RiskLevel) int {
	switch level {
	case RiskLow:
		return 1
	case RiskMedium:
		return 2
	case RiskHigh:
		return 3
	case RiskDestructive:
		return 4
	default:
		return 0
	}
}
