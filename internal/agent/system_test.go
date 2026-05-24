package agent

import (
	"strings"
	"testing"
)

func TestBuildNativeSystemPrompt(t *testing.T) {
	prompt := BuildNativeSystemPrompt("/home/user/project")

	if !strings.Contains(prompt, "/home/user/project") {
		t.Error("missing workDir")
	}
	if !strings.Contains(prompt, "edit_file") {
		t.Error("missing edit_file guideline")
	}
	if !strings.Contains(prompt, "apply_patch") {
		t.Error("missing apply_patch guideline")
	}
	if !strings.Contains(prompt, "update_plan") {
		t.Error("missing update_plan guideline")
	}
	if !strings.Contains(prompt, "KEEP GOING") {
		t.Error("missing autonomy instruction")
	}
	if !strings.Contains(prompt, "Do not narrate intent without acting") {
		t.Error("missing anti-stalling guidance")
	}
	if !strings.Contains(prompt, "Do not wait for confirmation before using non-destructive tools") {
		t.Error("missing act-first guidance")
	}
	// Native prompt must NOT contain XML tool format instructions
	if strings.Contains(prompt, "<tool_call>") {
		t.Error("native prompt should not contain XML tool call format")
	}
}

func TestBuildNativeSystemPromptIncludesCurrentDate(t *testing.T) {
	old := currentDateString
	currentDateString = func() string { return "2026-05-23" }
	t.Cleanup(func() { currentDateString = old })

	prompt := BuildNativeSystemPrompt("/home/user/project")
	if !strings.Contains(prompt, "Current date: 2026-05-23") {
		t.Fatalf("prompt missing current date:\n%s", prompt)
	}
}

func TestBuildNativeSystemPromptOmitsProjectScanSummary(t *testing.T) {
	dir := t.TempDir()
	prompt := BuildNativeSystemPrompt(dir)

	if strings.Contains(prompt, "Files: ~") {
		t.Fatalf("prompt should not include file-count project summary: %q", prompt)
	}
	if strings.Contains(prompt, "Languages: ") {
		t.Fatalf("prompt should not include detected-language summary: %q", prompt)
	}
}

func TestBuildNativeSystemPromptIncludesCodexStyleBehaviorGuidance(t *testing.T) {
	prompt := BuildNativeSystemPrompt("/home/user/project")

	for _, want := range []string{
		"Fix the problem at the root cause",
		"Do not attempt to fix unrelated bugs or broken tests",
		"start as specific as possible",
		"provide progress updates",
		"Do not git commit your changes or create new git branches unless explicitly requested",
		"Treat the surrounding codebase with respect",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildNativeSystemPromptIncludesRicherCodexStyleContract(t *testing.T) {
	prompt := BuildNativeSystemPrompt("/home/user/project")

	for _, want := range []string{
		"If the next step requires tools, emit the tool call directly",
		"one short natural preamble",
		"A brief user-visible sentence before a cluster of related tool calls is good",
		"High-quality plans",
		"Low-quality plans",
		"exactly one in_progress step",
		"enter_plan_mode",
		"ask_user_question",
		"Prefer rg or rg --files",
		"Use git log or git blame",
		"Avoid shotgun alternation patterns",
		"When approval is non-interactive",
		"When approval is interactive",
		"brief natural-language preamble paired with tool calls is allowed",
		"Final answers should be concise",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "Before making tool calls, send a brief progress update") {
		t.Fatalf("prompt still contains stale pre-tool progress instruction:\n%s", prompt)
	}
}

func TestBuildNativeSystemPromptIncludesNativeDelegationGuidance(t *testing.T) {
	prompt := BuildNativeSystemPrompt("/home/user/project")

	for _, want := range []string{
		"spawn_agent",
		"wait_agent",
		"broad repo audits",
		"multiple independent workstreams",
		"without requiring plugins",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}
