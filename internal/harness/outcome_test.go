package harness

import "testing"

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
