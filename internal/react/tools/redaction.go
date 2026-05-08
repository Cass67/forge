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

func redactAgentResults(results []react.AgentResult) []react.AgentResult {
	redacted := make([]react.AgentResult, 0, len(results))
	for _, result := range results {
		redacted = append(redacted, redactAgentResult(result))
	}
	return redacted
}
