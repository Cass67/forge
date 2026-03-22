package tui

import (
	"testing"

	"forge/internal/chatstate"
	"forge/internal/skills"
)

func TestSubmitInputWarnsWhenRequiredSkillMissing(t *testing.T) {
	inputCh := make(chan string, 1)
	m := chatLiveModel{
		inputBuf: "please plan the architecture first",
		inputPos: len([]rune("please plan the architecture first")),
		skills:   []skills.Skill{{Name: "brainstorming", Description: "Planning"}},
		state:    chatstate.New(),
	}

	result, done := m.submitInput(inputCh)
	if done {
		t.Fatal("expected chat to remain open")
	}
	if result != (ChatLiveResult{}) {
		t.Fatalf("unexpected result: %+v", result)
	}
	if got := m.display.flash; got != "required skill: /brainstorming" {
		t.Fatalf("flash = %q, want %q", got, "required skill: /brainstorming")
	}
	select {
	case got := <-inputCh:
		t.Fatalf("expected no submitted input, got %q", got)
	default:
	}
}

func TestSubmitInputAllowsActivatedRequiredSkill(t *testing.T) {
	inputCh := make(chan string, 1)
	state := chatstate.New()
	state.ActivateSkill("brainstorming")
	m := chatLiveModel{
		inputBuf: "please plan the architecture first",
		inputPos: len([]rune("please plan the architecture first")),
		skills:   []skills.Skill{{Name: "brainstorming", Description: "Planning"}},
		state:    state,
	}

	_, done := m.submitInput(inputCh)
	if done {
		t.Fatal("expected chat to remain open")
	}
	if !m.busy {
		t.Fatal("expected activated skill to allow submission")
	}
	select {
	case got := <-inputCh:
		if got != "please plan the architecture first" {
			t.Fatalf("submitted input = %q", got)
		}
	default:
		t.Fatal("expected submitted input")
	}
}
