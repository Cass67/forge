package react

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"forge/internal/protocol"
)

type SideEffectAction string

const (
	SideEffectActionWrite  SideEffectAction = "write"
	SideEffectActionVerify SideEffectAction = "verify"
	SideEffectActionCommit SideEffectAction = "commit"
	SideEffectActionPush   SideEffectAction = "push"
)

type SideEffectGateStatus string

const (
	SideEffectGatePending SideEffectGateStatus = "pending"
	SideEffectGatePassed  SideEffectGateStatus = "passed"
	SideEffectGateFailed  SideEffectGateStatus = "failed"
)

type SideEffectGate struct {
	Name     string               `json:"name"`
	Status   SideEffectGateStatus `json:"status"`
	Evidence string               `json:"evidence,omitempty"`
}

type SideEffectIntent struct {
	ID              string             `json:"id"`
	SourceTurn      int                `json:"source_turn,omitempty"`
	ArtifactPaths   []string           `json:"artifact_paths,omitempty"`
	AllowedPaths    []string           `json:"allowed_paths,omitempty"`
	RequiredActions []SideEffectAction `json:"required_actions,omitempty"`
	TargetBranch    string             `json:"target_branch,omitempty"`
	Remote          string             `json:"remote,omitempty"`
	WorkspaceRoot   string             `json:"workspace_root,omitempty"`
	Gates           []SideEffectGate   `json:"gates,omitempty"`
	IncidentMode    bool               `json:"incident_mode,omitempty"`
	Reason          string             `json:"reason,omitempty"`
}

func normalizeIntentPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, "`'\".,:;()[]{}<>")
	path = strings.TrimPrefix(path, "@")
	if path == "" || filepath.IsAbs(path) || looksLikeWindowsAbsolutePath(path) || strings.Contains(path, "\\") || strings.Contains(path, ":") {
		return ""
	}
	for _, segment := range strings.Split(filepath.ToSlash(path), "/") {
		if segment == ".." {
			return ""
		}
	}
	cleaned := filepath.Clean(path)
	if cleaned == "." || strings.HasPrefix(cleaned, "..") || strings.Contains(cleaned, string(filepath.Separator)+".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(cleaned)
}

func looksLikeWindowsAbsolutePath(path string) bool {
	if len(path) >= 3 && ((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) && path[1] == ':' && (path[2] == '\\' || path[2] == '/') {
		return true
	}
	return strings.HasPrefix(path, `\\`)
}

func extractMarkdownAndNamedPaths(text string) []string {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '\r':
			return true
		default:
			return false
		}
	})
	paths := make([]string, 0)
	for _, field := range fields {
		candidate := strings.TrimPrefix(field, "path=")
		candidate = strings.TrimPrefix(candidate, "target=")
		candidate = strings.Trim(candidate, "`'\".,:;()[]{}<>")
		path := normalizeIntentPath(candidate)
		if path == "" || !looksLikeExplicitIntentPath(path) || slices.Contains(paths, path) {
			continue
		}
		paths = append(paths, path)
	}
	return paths
}

func looksLikeExplicitIntentPath(path string) bool {
	if strings.Contains(path, "/") {
		return true
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".rs", ".ts", ".tsx", ".js", ".jsx", ".json", ".toml", ".yaml", ".yml", ".html", ".css", ".md", ".txt":
		return true
	default:
		return false
	}
}

func containsSideEffectAction(actions []SideEffectAction, want SideEffectAction) bool {
	return slices.Contains(actions, want)
}

func unresolvedSideEffectGates(intent *SideEffectIntent) []SideEffectGate {
	if intent == nil {
		return nil
	}
	unresolved := make([]SideEffectGate, 0, len(intent.Gates))
	for _, gate := range intent.Gates {
		if strings.TrimSpace(gate.Name) == "" || gate.Status == SideEffectGatePassed {
			continue
		}
		unresolved = append(unresolved, gate)
	}
	return unresolved
}

func sideEffectGateFeedbackExcept(intent *SideEffectIntent, ignored map[string]bool) string {
	unresolved := unresolvedSideEffectGates(intent)
	if len(unresolved) == 0 {
		return ""
	}
	parts := make([]string, 0, len(unresolved))
	tools := make([]string, 0, len(unresolved))
	for _, gate := range unresolved {
		if ignored != nil && ignored[gate.Name] {
			continue
		}
		status := strings.TrimSpace(string(gate.Status))
		if status == "" {
			status = string(SideEffectGatePending)
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", gate.Name, status))
		switch gate.Name {
		case string(SideEffectActionWrite):
			tools = append(tools, "write_file")
		case string(SideEffectActionCommit):
			tools = append(tools, "git_commit")
		case string(SideEffectActionPush):
			tools = append(tools, "git_push")
		case string(SideEffectActionVerify):
			tools = append(tools, "run_command")
		}
	}
	if len(parts) == 0 {
		return ""
	}
	message := "Runtime feedback: unresolved side-effect gates: " + strings.Join(parts, ", ") + "."
	if len(tools) > 0 {
		message += " Use " + strings.Join(uniqueIntentStrings(tools), ", ") + " before claiming completion."
	}
	return message
}

func uniqueIntentStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func copySideEffectIntent(intent *SideEffectIntent) *SideEffectIntent {
	if intent == nil {
		return nil
	}
	copy := *intent
	copy.ArtifactPaths = append([]string(nil), intent.ArtifactPaths...)
	copy.AllowedPaths = append([]string(nil), intent.AllowedPaths...)
	copy.RequiredActions = append([]SideEffectAction(nil), intent.RequiredActions...)
	copy.Gates = append([]SideEffectGate(nil), intent.Gates...)
	return &copy
}

func sideEffectIntentToProtocol(intent SideEffectIntent) protocol.SideEffectIntentItem {
	out := protocol.SideEffectIntentItem{
		ID:              intent.ID,
		SourceTurn:      intent.SourceTurn,
		ArtifactPaths:   append([]string(nil), intent.ArtifactPaths...),
		AllowedPaths:    append([]string(nil), intent.AllowedPaths...),
		TargetBranch:    intent.TargetBranch,
		Remote:          intent.Remote,
		WorkspaceRoot:   intent.WorkspaceRoot,
		IncidentMode:    intent.IncidentMode,
		Reason:          intent.Reason,
		RequiredActions: make([]string, 0, len(intent.RequiredActions)),
		Gates:           make([]protocol.SideEffectGateItem, 0, len(intent.Gates)),
	}
	for _, action := range intent.RequiredActions {
		out.RequiredActions = append(out.RequiredActions, string(action))
	}
	for _, gate := range intent.Gates {
		out.Gates = append(out.Gates, protocol.SideEffectGateItem{
			Name:     gate.Name,
			Status:   string(gate.Status),
			Evidence: gate.Evidence,
		})
	}
	return out
}
