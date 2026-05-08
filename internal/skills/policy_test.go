package skills

import "testing"

func TestSuggestPrefersModeAwareSkillWhenLoaded(t *testing.T) {
	loaded := []Skill{
		{Name: "brainstorming"},
		{Name: "test-driven-development"},
	}

	got, ok := Suggest(loaded, "implement", "please keep going", map[string]bool{})
	if !ok {
		t.Fatal("expected suggestion")
	}
	if got.Name != "test-driven-development" {
		t.Fatalf("suggestion = %#v", got)
	}
}

func TestSuggestFallsBackToInputHeuristics(t *testing.T) {
	loaded := []Skill{
		{Name: "systematic-debugging"},
	}

	got, ok := Suggest(loaded, "", "debug this regression in the runtime", map[string]bool{})
	if !ok {
		t.Fatal("expected suggestion")
	}
	if got.Name != "systematic-debugging" {
		t.Fatalf("suggestion = %#v", got)
	}
}

func TestSuggestPrefersReviewForAuditOfExistingPlan(t *testing.T) {
	loaded := []Skill{
		{Name: "brainstorming"},
		{Name: "requesting-code-review"},
	}

	got, ok := Suggest(loaded, "", "did the changes follow the plan, what gaps remain, audit it", map[string]bool{})
	if !ok {
		t.Fatal("expected suggestion")
	}
	if got.Name != "requesting-code-review" {
		t.Fatalf("suggestion = %#v", got)
	}
}

func TestSuggestSkipsAlreadyActiveSkill(t *testing.T) {
	loaded := []Skill{
		{Name: "brainstorming"},
	}

	_, ok := Suggest(loaded, "plan", "plan this change", map[string]bool{"brainstorming": true})
	if ok {
		t.Fatal("did not expect suggestion for active skill")
	}
}
