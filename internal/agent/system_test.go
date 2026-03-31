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
		"Before making tool calls, send a brief progress update",
		"High-quality plans",
		"Low-quality plans",
		"exactly one in_progress step",
		"enter_plan_mode",
		"ask_user_question",
		"Prefer rg or rg --files",
		"Use git log or git blame",
		"When approval is non-interactive",
		"When approval is interactive",
		"Final answers should be concise",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}
