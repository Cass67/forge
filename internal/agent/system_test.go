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

func TestBuildWorkerSystemPromptUsesStrictSingleToolContract(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{Name: "read_file", Description: "Read a file"})

	prompt := BuildWorkerSystemPrompt("/home/user/project", reg, "reader", nil)

	if !strings.Contains(prompt, "/home/user/project") {
		t.Error("missing workDir")
	}
	if !strings.Contains(prompt, "read_file") {
		t.Error("missing tool description")
	}
	if strings.Contains(prompt, "If you give a short progress update") {
		t.Error("worker prompt should not inherit chat progress-update guidance")
	}
	if strings.Contains(prompt, "Continue working after progress updates") {
		t.Error("worker prompt should not inherit chat progress guidance")
	}
	if strings.Contains(prompt, "You may call multiple tools") {
		t.Error("worker prompt should not advertise multi-tool turns")
	}
	if !strings.Contains(prompt, "Call at most one tool in a single response") {
		t.Error("worker prompt should require single-tool turns")
	}
	if !strings.Contains(prompt, "Every non-final turn must be exactly one valid <tool_call>...</tool_call> block and nothing else") {
		t.Error("worker prompt should require tool-only non-final turns")
	}
	if !strings.Contains(prompt, "Never mix a tool call with JSON, analysis, status text, or prose in the same response") {
		t.Error("worker prompt should forbid mixed tool-call responses")
	}
	if !strings.Contains(prompt, "Seeing a file name in list_dir or glob does not count as inspecting that file") {
		t.Error("worker prompt should distinguish filename discovery from read_file evidence")
	}
}

func TestBuildStrictLocalSystemPromptUsesSingleToolContract(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{Name: "artifact_write", Description: "Write a tracked artifact"})
	reg.Register(tools.Tool{Name: "preview_server_ensure", Description: "Ensure a localhost preview server"})

	prompt := BuildStrictLocalSystemPrompt("/home/user/project", reg, nil)

	if !strings.Contains(prompt, "/home/user/project") {
		t.Error("missing workDir")
	}
	if !strings.Contains(prompt, "preview_server_ensure") {
		t.Error("missing preview lifecycle tool")
	}
	if strings.Contains(prompt, "You may call multiple tools") {
		t.Error("strict local prompt should not advertise multi-tool turns")
	}
	if !strings.Contains(prompt, "Call at most one tool in a single response") {
		t.Error("strict local prompt should require single-tool turns")
	}
	if !strings.Contains(prompt, "Every working turn must be exactly one valid <tool_call>...</tool_call> block and nothing else") {
		t.Error("strict local prompt should require tool-only working turns")
	}
	if !strings.Contains(prompt, "Final turn must be plain user-facing text only") {
		t.Error("strict local prompt should require a plain-text final answer")
	}
	if !strings.Contains(prompt, "Prefer artifact_write and preview_server_ensure") {
		t.Error("strict local prompt should steer preview requests to host-owned tools")
	}
}
