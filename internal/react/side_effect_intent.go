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

func looksLikeControlPlaneArtifactContent(content string) bool {
	lower := strings.ToLower(strings.TrimSpace(content))
	strongNeedles := []string{
		"i've successfully created the commit",
		"i have a couple of issues to report",
		"forge_handoff",
		"accidental_write",
		"unresolved push",
	}
	for _, needle := range strongNeedles {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	weakNeedles := []string{
		"remaining_actions",
		"child agent",
		"sub-agent",
		"tool was unavailable",
	}
	hits := 0
	for _, needle := range weakNeedles {
		if strings.Contains(lower, needle) {
			hits++
		}
	}
	return hits >= 2 && containsToolPhrase(lower, "report", "handoff", "issue")
}

func deriveSideEffectIntentFromText(turn int, text string) *SideEffectIntent {
	if inputLooksLikeInjectedSkillPayload(text) {
		return nil
	}
	paths := extractMarkdownAndNamedPaths(text)
	if len(paths) == 0 {
		return nil
	}
	normalized := normalizeToolIntentText(text)
	intent := &SideEffectIntent{
		ID:            fmt.Sprintf("intent-%d", turn),
		SourceTurn:    turn,
		ArtifactPaths: paths,
		AllowedPaths:  paths,
		Remote:        "origin",
	}
	if inputSuggestsFileWrites(normalized) {
		intent.RequiredActions = append(intent.RequiredActions, SideEffectActionWrite)
	}
	if inputSuggestsGitCommit(normalized) {
		intent.RequiredActions = append(intent.RequiredActions, SideEffectActionCommit)
	}
	if inputSuggestsGitPush(normalized) {
		intent.RequiredActions = append(intent.RequiredActions, SideEffectActionPush)
	}
	if len(intent.RequiredActions) == 0 {
		return nil
	}
	intent.TargetBranch = extractTargetBranch(text)
	if intent.TargetBranch == "" && containsSideEffectAction(intent.RequiredActions, SideEffectActionPush) {
		intent.TargetBranch = "main"
	}
	intent.Gates = initialGatesForActions(intent.RequiredActions)
	return intent
}

func inputLooksLikeInjectedSkillPayload(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, "[Skill:") && strings.Contains(trimmed, "]")
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

func extractTargetBranch(text string) string {
	if !inputSuggestsGitCommit(normalizeToolIntentText(text)) && !inputSuggestsGitPush(normalizeToolIntentText(text)) {
		return ""
	}
	fields := strings.Fields(text)
	for i, field := range fields {
		token := strings.ToLower(strings.Trim(field, "`'\".,:;()[]{}<>"))
		if token != "to" && token != "branch" {
			continue
		}
		if i+1 >= len(fields) {
			continue
		}
		branch := trimIntentBranchToken(fields[i+1])
		if normalizeIntentBranch(branch) != "" {
			return branch
		}
	}
	return ""
}

func trimIntentBranchToken(branch string) string {
	branch = strings.TrimSpace(branch)
	branch = strings.Trim(branch, "`'\"()[]{}<>")
	return strings.TrimRight(branch, ",;:")
}

func normalizeIntentBranch(branch string) string {
	branch = strings.TrimSpace(branch)
	if branch == "" || strings.HasPrefix(branch, "-") || strings.HasPrefix(branch, "/") || strings.HasSuffix(branch, "/") {
		return ""
	}
	if branch == "@" || strings.Contains(branch, "@{") || strings.Contains(branch, "//") {
		return ""
	}
	if strings.ContainsAny(branch, "\\~^:?*[") || strings.Contains(branch, "..") || strings.HasSuffix(branch, ".") || strings.HasSuffix(branch, ".lock") {
		return ""
	}
	for _, segment := range strings.Split(branch, "/") {
		if segment == "" || strings.HasPrefix(segment, ".") || strings.HasSuffix(segment, ".lock") {
			return ""
		}
	}
	return branch
}

func initialGatesForActions(actions []SideEffectAction) []SideEffectGate {
	gates := make([]SideEffectGate, 0, len(actions))
	for _, action := range actions {
		name := string(action)
		if name == "" || containsSideEffectGate(gates, name) {
			continue
		}
		gates = append(gates, SideEffectGate{Name: name, Status: SideEffectGatePending})
	}
	return gates
}

func containsSideEffectAction(actions []SideEffectAction, want SideEffectAction) bool {
	return slices.Contains(actions, want)
}

func containsSideEffectGate(gates []SideEffectGate, name string) bool {
	for _, gate := range gates {
		if gate.Name == name {
			return true
		}
	}
	return false
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

func finalResponseClaimsSideEffectSuccess(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	claimPhrases := []string{
		"created the commit",
		"commit created",
		"created the file",
		"created file",
		"created the document",
		"created the report",
		"pushed to",
		"pushed it",
		"pushed the",
		"saved to",
		"saved the",
		"updated the file",
		"uploaded to",
		"remote contains",
	}
	for _, phrase := range claimPhrases {
		if idx := strings.Index(lower, phrase); idx >= 0 && !finalResponseSuccessClaimNegated(lower, idx, len(phrase)) {
			return true
		}
	}
	position := 0
	for _, token := range strings.FieldsFunc(lower, func(r rune) bool { return r < 'a' || r > 'z' }) {
		idx := strings.Index(lower[position:], token)
		if idx < 0 {
			idx = 0
		}
		start := position + idx
		position = start + len(token)
		switch token {
		case "done", "complete", "completed":
			if finalResponseReportsSideEffectFailure(lower) {
				continue
			}
			if !finalResponseSuccessClaimNegated(lower, start, len(token)) {
				return true
			}
		case "added", "changed", "created", "edited", "fixed", "implemented", "modified", "patched", "committed", "pushed", "uploaded", "wrote", "written", "saved", "updated":
			if !finalResponseSuccessClaimNegated(lower, start, len(token)) {
				return true
			}
		}
	}
	return false
}

func finalResponseSuccessClaimNegated(lower string, index, length int) bool {
	start := index - 35
	if start < 0 {
		start = 0
	}
	prefix := lower[start:index]
	if contrast := lastSuccessClaimContrastIndex(prefix); contrast >= 0 {
		start += contrast
	}
	end := index + length + 15
	if end > len(lower) {
		end = len(lower)
	}
	return finalResponseReportsSideEffectFailure(lower[start:end])
}

func lastSuccessClaimContrastIndex(prefix string) int {
	last := -1
	for _, marker := range []string{" but ", " and ", ";", "."} {
		if idx := strings.LastIndex(prefix, marker); idx >= 0 && idx+len(marker) > last {
			last = idx + len(marker)
		}
	}
	return last
}

func finalResponseReportsSideEffectFailure(lower string) bool {
	failurePhrases := []string{
		"blocked",
		"could not",
		"couldn't",
		"cannot",
		"can't",
		"denied",
		"unable to",
		"failed",
		"refused",
		"did not",
		"didn't implement",
		"didnt implement",
		"didn't add",
		"didnt add",
		"didn't edit",
		"didnt edit",
		"didn't fix",
		"didnt fix",
		"didn't change",
		"didnt change",
		"didn't modify",
		"didnt modify",
		"didn't patch",
		"didnt patch",
		"didn't write",
		"didnt write",
		"not complete",
		"not completed",
		"not committed",
		"not pushed",
		"not saved",
		"not written",
		"not done",
		"wasn't written",
		"wasnt written",
		"wasn't changed",
		"wasnt changed",
		"was not changed",
		"not changed",
		"wasn't edited",
		"wasnt edited",
		"was not edited",
		"not edited",
		"wasn't modified",
		"wasnt modified",
		"was not modified",
		"not modified",
		"wasn't patched",
		"wasnt patched",
		"was not patched",
		"not patched",
		"wasn't added",
		"wasnt added",
		"was not added",
		"not added",
		"wasn't fixed",
		"wasnt fixed",
		"was not fixed",
		"not fixed",
		"wasn't implemented",
		"wasnt implemented",
		"was not implemented",
		"not implemented",
		"weren't pushed",
		"werent pushed",
		"isn't saved",
		"isnt saved",
		"aren't saved",
		"arent saved",
		"nothing changed",
		"no changes were made",
		"no changes made",
		"no changes were pushed",
		"no changes pushed",
		"nothing was pushed",
		"nothing pushed",
		"still need",
		"need to",
		"needs to",
		"haven't",
		"hasn't",
	}
	for _, phrase := range failurePhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func sideEffectGateFeedback(intent *SideEffectIntent) string {
	unresolved := unresolvedSideEffectGates(intent)
	if len(unresolved) == 0 {
		return ""
	}
	parts := make([]string, 0, len(unresolved))
	tools := make([]string, 0, len(unresolved))
	for _, gate := range unresolved {
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

func sideEffectIntentFromProtocol(item protocol.SideEffectIntentItem) SideEffectIntent {
	out := SideEffectIntent{
		ID:              item.ID,
		SourceTurn:      item.SourceTurn,
		ArtifactPaths:   append([]string(nil), item.ArtifactPaths...),
		AllowedPaths:    append([]string(nil), item.AllowedPaths...),
		TargetBranch:    item.TargetBranch,
		Remote:          item.Remote,
		WorkspaceRoot:   item.WorkspaceRoot,
		IncidentMode:    item.IncidentMode,
		Reason:          item.Reason,
		RequiredActions: make([]SideEffectAction, 0, len(item.RequiredActions)),
		Gates:           make([]SideEffectGate, 0, len(item.Gates)),
	}
	for _, action := range item.RequiredActions {
		out.RequiredActions = append(out.RequiredActions, SideEffectAction(action))
	}
	for _, gate := range item.Gates {
		out.Gates = append(out.Gates, SideEffectGate{
			Name:     gate.Name,
			Status:   SideEffectGateStatus(gate.Status),
			Evidence: gate.Evidence,
		})
	}
	return out
}
