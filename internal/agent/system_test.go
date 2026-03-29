package agent

import (
	"strings"
	"testing"

	"forge/internal/agent/tools"
	"forge/internal/skills"
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
	if !strings.Contains(prompt, "The host owns visible progress updates") {
		t.Error("missing host-owned progress guidance")
	}
	if !strings.Contains(prompt, "Do not narrate intent without acting") {
		t.Error("missing anti-stalling guidance")
	}
	if !strings.Contains(prompt, "Do not wait for confirmation before using non-destructive tools") {
		t.Error("missing act-first guidance")
	}
}

func TestBuildSystemPromptOmitsProjectScanSummary(t *testing.T) {
	dir := t.TempDir()
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{Name: "read_file", Description: "Read a file"})

	prompt := BuildSystemPrompt(dir, reg, "")

	if strings.Contains(prompt, "Files: ~") {
		t.Fatalf("visible prompt should not include file-count project summary: %q", prompt)
	}
	if strings.Contains(prompt, "Languages: ") {
		t.Fatalf("visible prompt should not include detected-language project summary: %q", prompt)
	}
}

func TestBuildSystemPromptOmitsVisibleSkillCatalog(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{Name: "read_file", Description: "Read a file"})

	prompt := BuildSystemPrompt("/home/user/project", reg, skills.Describe([]skills.Skill{{
		Name:        "brainstorming",
		Description: "plan before implementation",
	}}))

	if strings.Contains(prompt, "Available skills (activate with /") {
		t.Fatalf("visible prompt should not advertise full skill catalog: %q", prompt)
	}
	if strings.Contains(prompt, "/brainstorming") {
		t.Fatalf("visible prompt should not advertise specific skill activators: %q", prompt)
	}
}
