package runtime

import (
	"forge/internal/react"
	"forge/internal/secscan"
)

func redactAgentTaskState(state react.AgentTaskState) react.AgentTaskState {
	scanner := secscan.NewDefaultScanner()
	redact := func(text string) string {
		if text == "" {
			return ""
		}
		return secscan.Redact(text, scanner.Scan(text))
	}

	state.ID = redact(state.ID)
	state.Role = redact(state.Role)
	state.Description = redact(state.Description)
	state.Prompt = redact(state.Prompt)
	state.Result = redact(state.Result)
	state.Error = redact(state.Error)
	state.Handoff = redactAgentTaskHandoff(state.Handoff, redact)
	state.LastToolName = redact(state.LastToolName)
	if len(state.RecentActivity) > 0 {
		state.RecentActivity = append([]react.AgentTaskActivity(nil), state.RecentActivity...)
		for i := range state.RecentActivity {
			state.RecentActivity[i].ToolName = redact(state.RecentActivity[i].ToolName)
			state.RecentActivity[i].Summary = redact(state.RecentActivity[i].Summary)
		}
	}
	return state
}

func redactAgentTaskHandoff(handoff *react.AgentHandoff, redact func(string) string) *react.AgentHandoff {
	if handoff == nil {
		return nil
	}
	out := &react.AgentHandoff{
		RemainingActions: append([]react.AgentFollowupAction(nil), handoff.RemainingActions...),
		Incidents:        make([]react.AgentWorkspaceIncident, 0, len(handoff.Incidents)),
	}
	for i := range out.RemainingActions {
		out.RemainingActions[i].TargetPath = redact(out.RemainingActions[i].TargetPath)
		out.RemainingActions[i].Description = redact(out.RemainingActions[i].Description)
		out.RemainingActions[i].SuggestedCommand = redact(out.RemainingActions[i].SuggestedCommand)
	}
	for _, incident := range handoff.Incidents {
		incident.Description = redact(incident.Description)
		incident.Paths = append([]string(nil), incident.Paths...)
		for i := range incident.Paths {
			incident.Paths[i] = redact(incident.Paths[i])
		}
		out.Incidents = append(out.Incidents, incident)
	}
	if len(out.Incidents) == 0 {
		out.Incidents = nil
	}
	return out
}
