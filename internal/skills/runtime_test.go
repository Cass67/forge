package skills

import (
	"testing"
)

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

func TestRuntimeResolveAutoDoesNotInjectTDDForCrossRepoComparisonDoc(t *testing.T) {
	t.Parallel()
	loaded := []Skill{
		{Name: "test-driven-development", Description: "write tests first", Body: "Start with a failing test."},
		{Name: "requesting-code-review", Description: "review work", Body: "Findings first."},
	}
	input := "take a look at this repo and compare it to claude, codex, opencode, deepseek. look at forge features and give a deep comparison. Write me a nice doc when done"

	if got, ok := DetectAuto(loaded, input); ok {
		t.Fatalf("DetectAuto() = %#v, want no auto skill for cross-repo comparison doc", got)
	}
}
