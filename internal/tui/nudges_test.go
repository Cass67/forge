package tui

import (
	"strings"
	"testing"
)

func TestSelectNudgeReturnsEmptyForDefaultChatMode(t *testing.T) {
	nudge := SelectNudge("chat", "", "")
	if nudge.Kind != NudgeNone || nudge.Label != "" || nudge.Flash != "" {
		t.Fatalf("expected empty nudge for chat mode, got %+v", nudge)
	}
}

func TestSelectNudgeReturnsModeForPlanMode(t *testing.T) {
	nudge := SelectNudge("plan", "", "")
	if nudge.Kind != NudgeMode {
		t.Fatalf("expected NudgeMode, got %q", nudge.Kind)
	}
	if nudge.Label != "[plan]" {
		t.Fatalf("label = %q, want [plan]", nudge.Label)
	}
}

func TestSelectNudgeReturnsModeForImplementMode(t *testing.T) {
	nudge := SelectNudge("implement", "", "")
	if nudge.Kind != NudgeMode || nudge.Label != "[implementing]" {
		t.Fatalf("expected implementing badge, got %+v", nudge)
	}
}

func TestSelectNudgeReturnsModeForReviewMode(t *testing.T) {
	nudge := SelectNudge("review", "", "")
	if nudge.Kind != NudgeMode || nudge.Label != "[review]" {
		t.Fatalf("expected review badge, got %+v", nudge)
	}
}

func TestSelectNudgeReturnsModeForValidateMode(t *testing.T) {
	nudge := SelectNudge("validate", "", "")
	if nudge.Kind != NudgeMode || nudge.Label != "[validate]" {
		t.Fatalf("expected validate badge, got %+v", nudge)
	}
}

func TestSelectNudgeSuggestsPlanModeWhenTaskOpIsPlan(t *testing.T) {
	nudge := SelectNudge("chat", "plan", "")
	if nudge.Kind != NudgePlanMode {
		t.Fatalf("expected NudgePlanMode, got %q", nudge.Kind)
	}
	if !strings.Contains(nudge.Flash, "enter_plan_mode") {
		t.Fatalf("flash should mention enter_plan_mode, got %q", nudge.Flash)
	}
}

func TestSelectNudgeSuggestsVerificationWhenTaskOpIsValidate(t *testing.T) {
	nudge := SelectNudge("chat", "validate", "")
	if nudge.Kind != NudgeVerification {
		t.Fatalf("expected NudgeVerification, got %q", nudge.Kind)
	}
	if nudge.Flash == "" {
		t.Fatal("expected non-empty flash for verification nudge")
	}
}

func TestSelectNudgeSurfacesSuggestedSkill(t *testing.T) {
	nudge := SelectNudge("chat", "", "brainstorming")
	if nudge.Kind != NudgeSkill {
		t.Fatalf("expected NudgeSkill, got %q", nudge.Kind)
	}
	if !strings.Contains(nudge.Flash, "/brainstorming") {
		t.Fatalf("flash should mention /brainstorming, got %q", nudge.Flash)
	}
}

func TestSelectNudgeModeOverridesSkillSuggestion(t *testing.T) {
	// Session mode takes priority over a suggested skill.
	nudge := SelectNudge("plan", "", "brainstorming")
	if nudge.Kind != NudgeMode {
		t.Fatalf("expected mode nudge to win over skill suggestion, got %q", nudge.Kind)
	}
}

func TestSelectNudgeIsCaseInsensitive(t *testing.T) {
	nudge := SelectNudge("Plan", "Implement", "")
	if nudge.Kind != NudgeMode || nudge.Label != "[plan]" {
		t.Fatalf("expected plan badge regardless of case, got %+v", nudge)
	}
}

func TestAgentNudgeMsgUpdatesModelBadgeAndFlash(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test-model", WorkDir: "/tmp"})

	nudge := NudgeSuggestion{
		Kind:  NudgeMode,
		Label: "[plan]",
		Flash: "",
	}
	updated, _ := m.Update(agentNudgeMsg(nudge))
	cm := updated.(ChatModel)

	if cm.statusData.AgentMode != "[plan]" {
		t.Fatalf("AgentMode = %q, want [plan]", cm.statusData.AgentMode)
	}
	if cm.currentNudge.Kind != NudgeMode {
		t.Fatalf("currentNudge.Kind = %q, want NudgeMode", cm.currentNudge.Kind)
	}
}

func TestAgentNudgeMsgSetsFlashWhenEmpty(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test-model", WorkDir: "/tmp"})

	nudge := NudgeSuggestion{
		Kind:  NudgeSkill,
		Label: "",
		Flash: "suggested skill: /brainstorming",
	}
	updated, _ := m.Update(agentNudgeMsg(nudge))
	cm := updated.(ChatModel)

	if cm.flash != "suggested skill: /brainstorming" {
		t.Fatalf("flash = %q, want suggested skill: /brainstorming", cm.flash)
	}
}

func TestAgentNudgeMsgDoesNotOverwriteExistingFlash(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test-model", WorkDir: "/tmp"})
	m.flash = "existing flash"

	nudge := NudgeSuggestion{Kind: NudgeSkill, Flash: "suggested skill: /foo"}
	updated, _ := m.Update(agentNudgeMsg(nudge))
	cm := updated.(ChatModel)

	if cm.flash != "existing flash" {
		t.Fatalf("existing flash should not be overwritten, got %q", cm.flash)
	}
}
