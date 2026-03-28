package harness

import (
	"strings"
	"testing"
)

func TestResolveExpectedDeliverableMetaQuestionOnPreviewThreadUsesAnswerOnly(t *testing.T) {
	class := Classification{
		Family:       FamilyAnswer,
		ThreadIntent: TurnIntentMetaQuestion,
		IsFollowUp:   true,
		TaskText:     "did you already update the code?",
	}
	session := SessionState{
		Threads: ThreadLedger{
			Active: ThreadState{
				ID:          "thread-1",
				Kind:        ThreadPreviewCollaboration,
				Status:      ThreadAwaitingUserFeedback,
				Deliverable: DeliverablePreviewAvailableAndRenderable,
			},
		},
	}

	got := resolveExpectedDeliverable(class, session, LaneStrictAction)
	if got != DeliverableAnswerOnly {
		t.Fatalf("deliverable = %q, want %q", got, DeliverableAnswerOnly)
	}
}

func TestNormalizeObservationBlocksUngroundedBranchClaim(t *testing.T) {
	class := Classification{Family: FamilyAnswer, CanStayLocal: true}
	step := Step{Lane: LaneConversational, Kind: StepLocal}
	obs := Observation{
		Status:   ObservationComplete,
		Response: "Branch created: obsidian-term",
		Summary:  "Branch created: obsidian-term",
	}

	got := normalizeObservation(step, class, SessionState{}, obs)
	if got.Status != ObservationBlocked {
		t.Fatalf("status = %q, want %q", got.Status, ObservationBlocked)
	}
}

func TestNormalizeObservationAllowsGroundedBranchClaim(t *testing.T) {
	class := Classification{Family: FamilyAnswer, CanStayLocal: true}
	step := Step{Lane: LaneConversational, Kind: StepLocal}
	obs := Observation{
		Status:   ObservationComplete,
		Response: "Created and switched to branch forge/obsidian-theme",
		Summary:  "Created and switched to branch forge/obsidian-theme",
		ToolCalls: []ObservedToolCall{
			{
				Name: "run_command",
				Args: map[string]any{"command": "git checkout -b forge/obsidian-theme"},
			},
		},
	}

	got := normalizeObservation(step, class, SessionState{}, obs)
	if got.Status != ObservationComplete {
		t.Fatalf("status = %q, want %q", got.Status, ObservationComplete)
	}
}

func TestNormalizeObservationBlocksUngroundedCommitClaim(t *testing.T) {
	class := Classification{Family: FamilyAnswer, CanStayLocal: true}
	step := Step{Lane: LaneConversational, Kind: StepLocal}
	obs := Observation{
		Status:   ObservationComplete,
		Response: "Committed the changes with message: update theme colors",
		Summary:  "Committed the changes with message: update theme colors",
	}

	got := normalizeObservation(step, class, SessionState{}, obs)
	if got.Status != ObservationBlocked {
		t.Fatalf("status = %q, want %q", got.Status, ObservationBlocked)
	}
}

func TestNormalizeObservationBlocksUngroundedLivePreviewClaim(t *testing.T) {
	class := Classification{Family: FamilyAnswer, CanStayLocal: true}
	step := Step{Lane: LaneConversational, Kind: StepLocal}
	obs := Observation{
		Status:   ObservationComplete,
		Response: "Preview is live at http://127.0.0.1:4173/themes_preview.html",
		Summary:  "Preview is live at http://127.0.0.1:4173/themes_preview.html",
	}

	got := normalizeObservation(step, class, SessionState{}, obs)
	if got.Status != ObservationBlocked {
		t.Fatalf("status = %q, want %q", got.Status, ObservationBlocked)
	}
}

func TestNormalizeObservationBlocksSelectedDirectionMismatch(t *testing.T) {
	class := Classification{
		Family:                  FamilyImplement,
		WantsAction:             true,
		PrefersVisibleExecution: true,
		IsFollowUp:              true,
		ThreadIntent:            TurnIntentContinueThread,
		TaskText:                "apply the selected direction",
	}
	step := Step{Lane: LaneStrictAction, Kind: StepStrictLocal}
	session := SessionState{
		Threads: ThreadLedger{
			Active: ThreadState{
				ID:                "thread-1",
				Kind:              ThreadPreviewCollaboration,
				Status:            ThreadAwaitingUserFeedback,
				Phase:             ThreadPhaseApply,
				Deliverable:       DeliverablePreviewAvailableAndRenderable,
				SelectedDirection: "obsidian",
			},
		},
	}
	obs := Observation{
		Status:   ObservationComplete,
		Response: "Applied the nexus theme in app code.",
		Summary:  "Applied the nexus theme in app code.",
		Runtime: LocalRuntimeSnapshot{
			Preview: PreviewSnapshot{
				Status: "live",
				Path:   "themes_preview.html",
				Port:   4173,
				URL:    "http://127.0.0.1:4173/themes_preview.html",
			},
		},
	}

	got := normalizeObservation(step, class, session, obs)
	if got.Status != ObservationBlocked {
		t.Fatalf("status = %q, want %q", got.Status, ObservationBlocked)
	}
	if !strings.Contains(strings.ToLower(got.Summary), "selected direction mismatch") {
		t.Fatalf("summary = %q, want selected-direction mismatch reason", got.Summary)
	}
}
