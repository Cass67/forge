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
	if strings.Contains(prompt, "tool_help") {
		t.Error("native prompt should not reference tool_help")
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
