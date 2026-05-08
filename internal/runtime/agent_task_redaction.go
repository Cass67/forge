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
