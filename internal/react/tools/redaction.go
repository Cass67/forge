package reacttools

import (
	"forge/internal/react"
	"forge/internal/secscan"
)

func redactAgentResult(result react.AgentResult) react.AgentResult {
	scanner := secscan.NewDefaultScanner()
	redact := func(text string) string {
		if text == "" {
			return ""
		}
		return secscan.Redact(text, scanner.Scan(text))
	}

	result.ID = redact(result.ID)
	result.Role = redact(result.Role)
	result.Result = redact(result.Result)
	result.Error = redact(result.Error)
	result.Handoff = redactAgentHandoff(result.Handoff, redact)
	result.ResumeHint = redact(result.ResumeHint)
	result.LastToolName = redact(result.LastToolName)
	if len(result.RecentActivity) > 0 {
		result.RecentActivity = append([]react.AgentTaskActivity(nil), result.RecentActivity...)
		for i := range result.RecentActivity {
			result.RecentActivity[i].ToolName = redact(result.RecentActivity[i].ToolName)
			result.RecentActivity[i].Summary = redact(result.RecentActivity[i].Summary)
		}
	}
	return result
}

func redactAgentHandoff(handoff *react.AgentHandoff, redact func(string) string) *react.AgentHandoff {
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

func redactAgentResults(results []react.AgentResult) []react.AgentResult {
	redacted := make([]react.AgentResult, 0, len(results))
	for _, result := range results {
		redacted = append(redacted, redactAgentResult(result))
	}
	return redacted
}
