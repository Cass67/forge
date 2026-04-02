package agent

import (
	"strings"

	"forge/internal/agent/promptcomposer"
)

// BuildNativeSystemPrompt builds the system prompt for models using provider-native
// tool calling. Tool descriptions are omitted — the model receives them via the API
// tools parameter. XML format instructions are not included.
func BuildNativeSystemPrompt(workDir string) string {
	return promptcomposer.Compose(promptcomposer.ForgeCorePrompt(workDir), nil)
}

func BuildNativeSystemPromptForMode(workDir, mode string, taskActive bool) string {
	if strings.EqualFold(strings.TrimSpace(mode), "chat") && !taskActive {
		return promptcomposer.Compose(promptcomposer.ForgeChatPrompt(workDir), nil)
	}
	return BuildNativeSystemPrompt(workDir)
}
