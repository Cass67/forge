package skills

import (
	"strings"
	"testing"
)

func TestRuntimeListsStableDescriptors(t *testing.T) {
	t.Parallel()
	rt := NewRuntime([]Skill{
		{Name: "systematic-debugging", Description: "debug carefully", Source: "/tmp/debug/SKILL.md"},
		{Name: "brainstorming", Description: "plan first", Source: "/tmp/brain/SKILL.md"},
	})

	got := rt.ListSkills()
	if len(got) != 2 {
		t.Fatalf("len(ListSkills()) = %d, want 2", len(got))
	}
	if got[0] != (Descriptor{Name: "brainstorming", Description: "plan first", Source: "/tmp/brain/SKILL.md"}) {
		t.Fatalf("first descriptor = %#v", got[0])
	}
	if got[1] != (Descriptor{Name: "systematic-debugging", Description: "debug carefully", Source: "/tmp/debug/SKILL.md"}) {
		t.Fatalf("second descriptor = %#v", got[1])
	}
}

func TestRuntimeReturnsInjectableSkillDocument(t *testing.T) {
	t.Parallel()
	rt := NewRuntime([]Skill{{
		Name:        "brainstorming",
		Description: "plan first",
		Body:        "Do not implement yet.",
		Source:      "/tmp/brain/SKILL.md",
	}})

	skill, ok := rt.ResolveRequired("design the chat ui")
	if !ok || skill.Name != "brainstorming" {
		t.Fatalf("skill = %#v ok=%v", skill, ok)
	}

	msg := rt.InjectableMessage(skill)
	if !strings.HasPrefix(msg, "[Skill: brainstorming]") {
		t.Fatalf("msg = %q", msg)
	}
	if !strings.Contains(msg, "Do not implement yet.") {
		t.Fatalf("msg = %q", msg)
	}
	if strings.TrimSpace(msg) == "/brainstorming" {
		t.Fatalf("injectable message must be a skill document, got %q", msg)
	}
}

func TestRuntimeResolveAutoMatchesDetectAuto(t *testing.T) {
	t.Parallel()
	loaded := []Skill{
		{Name: "brainstorming", Description: "plan first", Body: "Plan first."},
		{Name: "requesting-code-review", Description: "review work", Body: "Findings first."},
		{Name: "test-driven-development", Description: "write tests first", Body: "Start with a failing test."},
	}

	rt := NewRuntime(loaded)
	gotRuntime, ok := rt.ResolveAuto("use brainstorming to plan first")
	if !ok || gotRuntime.Name != "brainstorming" {
		t.Fatalf("ResolveAuto() = %#v ok=%v", gotRuntime, ok)
	}

	gotWrapper, ok := DetectAuto(loaded, "use brainstorming to plan first")
	if !ok || gotWrapper.Name != gotRuntime.Name {
		t.Fatalf("DetectAuto() = %#v ok=%v, want %q", gotWrapper, ok, gotRuntime.Name)
	}
}

func TestRuntimeResolveAutoDoesNotTreatAuditOfExistingPlanAsBrainstorming(t *testing.T) {
	t.Parallel()
	loaded := []Skill{
		{Name: "brainstorming", Description: "plan first", Body: "Plan first."},
		{Name: "requesting-code-review", Description: "review work", Body: "Findings first."},
	}

	input := "forge has had many changes, did they all follow the plan, are there any gaps, whats next, figure this out and write me a nice doc"
	got, ok := DetectAuto(loaded, input)
	if !ok {
		t.Fatal("expected review-oriented auto skill")
	}
	if got.Name != "requesting-code-review" {
		t.Fatalf("DetectAuto() = %#v, want requesting-code-review", got)
	}
}

func TestRuntimeResolveAutoDoesNotTreatCrossRepoGapReportAsCodeReview(t *testing.T) {
	t.Parallel()
	loaded := []Skill{
		{Name: "brainstorming", Description: "plan first", Body: "Plan first."},
		{Name: "requesting-code-review", Description: "review work", Body: "Findings first."},
	}

	input := "Compare repos plus previous docs, reports, and plans to identify gaps and findings, then write the result as a markdown report"
	if got, ok := DetectAuto(loaded, input); ok {
		t.Fatalf("DetectAuto() = %#v, want no auto skill for research report", got)
	}
}

func TestRuntimeLoadByNameSupportsKnownSkillNames(t *testing.T) {
	t.Parallel()
	rt := NewRuntime([]Skill{
		{Name: "test-driven-development", Description: "write tests first", Body: "Start with a failing test."},
	})

	got, ok := rt.LoadByName("test-driven-development")
	if !ok || got.Name != "test-driven-development" {
		t.Fatalf("LoadByName() = %#v ok=%v", got, ok)
	}
}

func TestRuntimeRecordSkillUse(t *testing.T) {
	t.Parallel()
	rt := NewRuntime(nil)

	rt.RecordSkillUse("brainstorming", "reader-1", "applied")
	got := rt.UseRecords()

	if len(got) != 1 {
		t.Fatalf("len(UseRecords()) = %d, want 1", len(got))
	}
	if got[0] != (UseRecord{Name: "brainstorming", WorkerID: "reader-1", Outcome: "applied"}) {
		t.Fatalf("first use record = %#v", got[0])
	}
}
