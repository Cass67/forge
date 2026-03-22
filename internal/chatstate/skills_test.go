package chatstate

import "testing"

func TestSkillActivationLifecycle(t *testing.T) {
	s := New()
	if s.SkillActivated("brainstorming") {
		t.Fatal("skill should start inactive")
	}
	s.ActivateSkill("brainstorming")
	if !s.SkillActivated("brainstorming") {
		t.Fatal("expected skill to be active")
	}
	s.Clear()
	if s.SkillActivated("brainstorming") {
		t.Fatal("expected clear to reset activation")
	}
}
