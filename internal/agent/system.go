package agent

import (
	"strings"
	"time"

	"forge/internal/agent/promptcomposer"
)

var currentDateString = func() string { return time.Now().Format("2006-01-02") }

// BuildNativeSystemPrompt builds the system prompt for models using provider-native
// tool calling. Tool descriptions are omitted — the model receives them via the API
// tools parameter. XML format instructions are not included.
func BuildNativeSystemPrompt(workDir string) string {
	return withCurrentDate(promptcomposer.Compose(promptcomposer.ForgeCorePrompt(workDir), nil))
}

func BuildNativeSystemPromptForMode(workDir, mode string, taskActive bool) string {
	if strings.EqualFold(strings.TrimSpace(mode), "chat") && !taskActive {
		return promptcomposer.Compose(promptcomposer.ForgeChatPrompt(workDir), nil)
	}
	return BuildNativeSystemPrompt(workDir)
}

func withCurrentDate(prompt string) string {
	if currentDate := strings.TrimSpace(currentDateString()); currentDate != "" {
		return strings.TrimSpace(prompt) + "\n\nCurrent date: " + currentDate
	}
	return prompt
}
