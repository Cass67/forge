package agent

import (
	"strings"
	"testing"

	"forge/internal/agent/tools"
)

func TestBuildSystemPrompt(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{Name: "read_file", Description: "Read a file"})
	reg.Register(tools.Tool{Name: "tool_help", Description: "Reveal specialized tools on demand"})
	reg.Register(tools.Tool{Name: "web_search", Description: "Search the web", PromptVisibility: tools.PromptHidden})

	prompt := BuildSystemPrompt("/home/user/project", reg, "")

	if !strings.Contains(prompt, "/home/user/project") {
		t.Error("missing workDir")
	}
	if !strings.Contains(prompt, "read_file") {
		t.Error("missing tool description")
	}
	if strings.Contains(prompt, "web_search") {
		t.Error("hidden tool should not appear in default prompt")
	}
	if !strings.Contains(prompt, "tool_help") {
		t.Error("missing tool_help in prompt")
	}
	if !strings.Contains(prompt, "edit_file") {
		t.Error("missing edit_file guideline")
	}
	if !strings.Contains(prompt, "tool_call") {
		t.Error("missing tool call format instructions")
	}
	if !strings.Contains(prompt, "Continue working after progress updates") {
		t.Error("missing continue-by-default instruction")
	}
	if !strings.Contains(prompt, "Do not narrate intent without acting") {
		t.Error("missing anti-stalling guidance")
	}
	if !strings.Contains(prompt, "Do not wait for confirmation before using non-destructive tools") {
		t.Error("missing act-first guidance")
	}
}
