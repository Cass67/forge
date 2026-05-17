package react

import (
	"encoding/json"
	"fmt"
	"strings"
)

type AgentActionKind string

const (
	AgentActionWriteFile       AgentActionKind = "write_file"
	AgentActionRestoreFile     AgentActionKind = "restore_file"
	AgentActionRunVerification AgentActionKind = "run_verification"
	AgentActionAskUser         AgentActionKind = "ask_user"
)

type AgentFollowupAction struct {
	Kind             AgentActionKind `json:"kind"`
	TargetPath       string          `json:"target_path,omitempty"`
	Description      string          `json:"description,omitempty"`
	SuggestedCommand string          `json:"suggested_command,omitempty"`
	Blocking         bool            `json:"blocking,omitempty"`
}

type AgentIncidentKind string

const (
	AgentIncidentAccidentalWrite AgentIncidentKind = "accidental_write"
	AgentIncidentMissingTool     AgentIncidentKind = "missing_tool"
)

type AgentWorkspaceIncident struct {
	Kind        AgentIncidentKind `json:"kind"`
	Paths       []string          `json:"paths,omitempty"`
	Description string            `json:"description,omitempty"`
	Blocking    bool              `json:"blocking,omitempty"`
}

type AgentHandoff struct {
	RemainingActions []AgentFollowupAction    `json:"remaining_actions,omitempty"`
	Incidents        []AgentWorkspaceIncident `json:"incidents,omitempty"`
}

func AgentHandoffInstructions() string {
	return strings.Join([]string{
		"When your task is complete, return your normal report first.",
		"If follow-up work remains, append exactly one fenced block named ```forge_handoff containing JSON with remaining_actions and incidents.",
		"The parent/orchestrator owns writes, repairs, verification, commits, and user questions.",
		"Do not tell the user to run shell or git commands because you cannot repair the workspace; report blockers to the parent/orchestrator in forge_handoff.",
		`Example: ` + "```forge_handoff\n" + `{"remaining_actions":[{"kind":"write_file","target_path":"docs/report.md","description":"Save report","blocking":true}],"incidents":[{"kind":"accidental_write","paths":["README.md"],"description":"Accidental overwrite","blocking":true}]}` + "\n```",
	}, "\n")
}

func (h AgentHandoff) Blocking() bool {
	for _, action := range h.RemainingActions {
		if action.Blocking {
			return true
		}
	}
	for _, incident := range h.Incidents {
		if incident.Blocking {
			return true
		}
	}
	return false
}

func (h AgentHandoff) Empty() bool {
	return len(h.RemainingActions) == 0 && len(h.Incidents) == 0
}

func ParseAgentHandoff(raw string) (string, AgentHandoff, error) {
	const fence = "```forge_handoff"
	start := strings.Index(raw, fence)
	if start < 0 {
		return raw, AgentHandoff{}, nil
	}
	next := strings.Index(raw[start+len(fence):], fence)
	if next >= 0 {
		return raw, AgentHandoff{}, fmt.Errorf("agent handoff contains multiple forge_handoff blocks")
	}
	contentStart := start + len(fence)
	if contentStart < len(raw) && raw[contentStart] == '\r' {
		contentStart++
	}
	if contentStart < len(raw) && raw[contentStart] == '\n' {
		contentStart++
	}
	endRel := strings.Index(raw[contentStart:], "```")
	if endRel < 0 {
		return raw, AgentHandoff{}, fmt.Errorf("agent handoff block is not closed")
	}
	end := contentStart + endRel
	var handoff AgentHandoff
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw[contentStart:end])), &handoff); err != nil {
		return raw, AgentHandoff{}, fmt.Errorf("decode agent handoff: %w", err)
	}
	report := strings.TrimSpace(raw[:start] + raw[end+len("```"):])
	return report, handoff, nil
}

func sanitizeAgentReportForHandoff(report string, handoff AgentHandoff) string {
	if !handoff.Blocking() {
		return strings.TrimSpace(report)
	}
	var lines []string
	for _, line := range strings.Split(report, "\n") {
		normalized := strings.ToLower(strings.TrimSpace(line))
		if strings.Contains(normalized, "git restore") || strings.Contains(normalized, "git checkout") || strings.Contains(normalized, "git reset") {
			continue
		}
		if strings.Contains(normalized, "run `") || strings.Contains(normalized, "run $") || strings.Contains(normalized, "run:") {
			continue
		}
		if strings.Contains(normalized, "execute ") && (strings.Contains(normalized, "apply_patch") || strings.Contains(normalized, "rm ") || strings.Contains(normalized, "git ")) {
			continue
		}
		lines = append(lines, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
