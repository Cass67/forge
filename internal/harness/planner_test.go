package harness

import "testing"

func TestPlanUsesReaderWorkerForPlainInspectTurns(t *testing.T) {
	step := Plan(Classification{Family: FamilyInspect, TopicKey: "workspace:directory"}, SessionState{})
	if step.Kind != StepWorker {
		t.Fatalf("step = %#v", step)
	}
	if step.Lane != LaneWorkerSidecar {
		t.Fatalf("lane = %q, want %q", step.Lane, LaneWorkerSidecar)
	}
	if step.Worker != WorkerReader {
		t.Fatalf("worker = %q", step.Worker)
	}
}

func TestPlanUsesEditorWorkerForImplementationTurns(t *testing.T) {
	step := Plan(Classification{
		Family:       FamilyImplement,
		TopicKey:     "workspace:directory",
		WantsAction:  true,
		CanStayLocal: true,
	}, SessionState{})
	if step.Kind != StepWorker {
		t.Fatalf("step = %#v", step)
	}
	if step.Lane != LaneWorkerSidecar {
		t.Fatalf("lane = %q, want %q", step.Lane, LaneWorkerSidecar)
	}
	if step.Worker != WorkerEditor {
		t.Fatalf("worker = %q", step.Worker)
	}
}

func TestPlanKeepsCollaborativeIdeationPromptsOnMainPath(t *testing.T) {
	class := Classify(UserTurn{Text: "i dont like the theme in this app, i need some ideas, can you mock up 3 for me and help me decide, id like you to spin up a web server and show me your ideas, update me at every step whats going on"}, SessionState{})
	if !class.PrefersVisibleExecution {
		t.Fatalf("expected visible execution preference: %#v", class)
	}
	step := Plan(class, SessionState{})
	if step.Kind != StepStrictLocal || step.Worker != WorkerNone {
		t.Fatalf("step = %#v, class = %#v", step, class)
	}
	if step.Lane != LaneStrictAction {
		t.Fatalf("lane = %q, want %q", step.Lane, LaneStrictAction)
	}
}

func TestPlanKeepsPreviewFollowUpsOnStrictLocalPath(t *testing.T) {
	session := SessionState{
		Turn: 2,
		LastPreview: PreviewSnapshot{
			Turn:   1,
			Status: "live",
			URL:    "http://127.0.0.1:4173/themes_preview.html",
			Path:   "themes_preview.html",
			Port:   4173,
		},
	}

	class := Classify(UserTurn{Text: "fix that and show me again"}, session)
	if !class.PrefersVisibleExecution {
		t.Fatalf("expected preview follow-up to prefer visible execution: %#v", class)
	}

	step := Plan(class, session)
	if step.Kind != StepStrictLocal || step.Worker != WorkerNone {
		t.Fatalf("step = %#v, class = %#v", step, class)
	}
	if step.Lane != LaneStrictAction {
		t.Fatalf("lane = %q, want %q", step.Lane, LaneStrictAction)
	}
}

func TestPlanKeepsReferentialPreviewTroubleshootingOnStrictLocalPath(t *testing.T) {
	session := SessionState{
		Turn: 2,
		LastPreview: PreviewSnapshot{
			Turn:   1,
			Status: "live",
			URL:    "http://127.0.0.1:4173/themes_preview.html",
			Path:   "themes_preview.html",
			Port:   4173,
		},
	}

	class := Classify(UserTurn{Text: "it still looks wrong"}, session)
	if !class.PrefersVisibleExecution {
		t.Fatalf("expected referential preview follow-up to prefer visible execution: %#v", class)
	}

	step := Plan(class, session)
	if step.Kind != StepStrictLocal || step.Worker != WorkerNone {
		t.Fatalf("step = %#v, class = %#v", step, class)
	}
	if step.Lane != LaneStrictAction {
		t.Fatalf("lane = %q, want %q", step.Lane, LaneStrictAction)
	}
}

func TestPlanKeepsPreviewReplayFollowUpOnStrictLocalPath(t *testing.T) {
	session := SessionState{
		Turn: 2,
		LastPreview: PreviewSnapshot{
			Turn:   1,
			Status: "live",
			URL:    "http://127.0.0.1:4173/themes_preview.html",
			Path:   "themes_preview.html",
			Port:   4173,
		},
	}

	class := Classify(UserTurn{Text: "show me again"}, session)
	if !class.PrefersVisibleExecution {
		t.Fatalf("expected preview replay follow-up to prefer visible execution: %#v", class)
	}

	step := Plan(class, session)
	if step.Kind != StepStrictLocal || step.Worker != WorkerNone {
		t.Fatalf("step = %#v, class = %#v", step, class)
	}
	if step.Lane != LaneStrictAction {
		t.Fatalf("lane = %q, want %q", step.Lane, LaneStrictAction)
	}
}
