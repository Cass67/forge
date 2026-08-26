package tools

import (
	"context"
	"strings"
	"testing"

	"forge/internal/skills"
)

func TestSkillToolLoadsBodyByName(t *testing.T) {
	all := []skills.Skill{
		{Name: "grilling", Description: "Grill the user", Body: "Interview relentlessly."},
		{Name: "domain-modeling", Description: "Domain voice", Body: "Sharpen domain language."},
		{Name: "test-driven-development", Description: "TDD", Body: "Red green refactor."},
	}
	tool := NewSkillTool(func() []skills.Skill { return all })

	// Exact name.
	out, err := tool.Execute(context.Background(), map[string]any{"name": "grilling"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(out, "[Skill: grilling]") || !strings.Contains(out, "Interview relentlessly.") {
		t.Fatalf("exact-name output wrong:\n%s", out)
	}

	// Abbreviation match ("tdd" -> test-driven-development).
	out, err = tool.Execute(context.Background(), map[string]any{"name": "tdd"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(out, "[Skill: test-driven-development]") {
		t.Fatalf("abbreviation output wrong:\n%s", out)
	}

	// Unknown name lists available skills.
	out, err = tool.Execute(context.Background(), map[string]any{"name": "nope"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(out, "No skill named \"nope\"") || !strings.Contains(out, "/grilling") {
		t.Fatalf("unknown-name output wrong:\n%s", out)
	}

	// Missing name lists available skills.
	out, err = tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(out, "Available skills") || !strings.Contains(out, "/grilling") {
		t.Fatalf("missing-name output wrong:\n%s", out)
	}
}
