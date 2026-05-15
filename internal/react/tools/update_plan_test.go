package reacttools

import (
	"context"
	"strings"
	"testing"

	"forge/internal/react"
)

func TestUpdatePlanStoresPlanInSession(t *testing.T) {
	session := react.NewSession()
	tool := NewUpdatePlan(session)
	result, err := tool.Execute(context.Background(), map[string]any{
		"steps": []any{
			map[string]any{"step": "Inspect files", "status": "completed"},
			map[string]any{"step": "Patch runtime", "status": "in_progress"},
		},
		"explanation": "Refining the runtime path",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "[in_progress] Patch runtime") {
		t.Fatalf("result = %q", result)
	}
	snap := session.Snapshot()
	if snap.PlanState == nil || len(snap.PlanState.Steps) != 2 {
		t.Fatalf("plan state = %#v", snap.PlanState)
	}
}

func TestUpdatePlanRejectsMultipleInProgressSteps(t *testing.T) {
	session := react.NewSession()
	tool := NewUpdatePlan(session)
	_, err := tool.Execute(context.Background(), map[string]any{
		"steps": []any{
			map[string]any{"step": "Inspect files", "status": "in_progress"},
			map[string]any{"step": "Patch runtime", "status": "in_progress"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "exactly one active step") {
		t.Fatalf("err = %v", err)
	}
}

func TestUpdatePlanAutoPromotesFirstPendingWhenNoInProgress(t *testing.T) {
	session := react.NewSession()
	tool := NewUpdatePlan(session)
	result, err := tool.Execute(context.Background(), map[string]any{
		"steps": []any{
			map[string]any{"step": "Inspect files", "status": "completed"},
			map[string]any{"step": "Patch runtime", "status": "pending"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "[in_progress] Patch runtime") {
		t.Fatalf("expected pending step auto-promoted to in_progress, got %q", result)
	}
}

func TestUpdatePlanAutoPromotesFirstStepWhenAllPending(t *testing.T) {
	session := react.NewSession()
	tool := NewUpdatePlan(session)
	result, err := tool.Execute(context.Background(), map[string]any{
		"steps": []any{
			map[string]any{"step": "Remove stray files", "status": "pending"},
			map[string]any{"step": "Update .gitignore", "status": "pending"},
			map[string]any{"step": "Run lint", "status": "pending"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "[in_progress] Remove stray files") {
		t.Fatalf("expected first step auto-promoted, got %q", result)
	}
	if strings.Contains(result, "[in_progress] Update .gitignore") || strings.Contains(result, "[in_progress] Run lint") {
		t.Fatalf("expected only first step promoted, got %q", result)
	}
}

func TestUpdatePlanAcceptsBlockedStepWithBlocker(t *testing.T) {
	session := react.NewSession()
	tool := NewUpdatePlan(session)
	result, err := tool.Execute(context.Background(), map[string]any{
		"steps": []any{
			map[string]any{"step": "Wait for approval on plan", "status": "blocked", "blocker": "need user sign-off before editing"},
			map[string]any{"step": "Patch runtime", "status": "pending"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "[blocked] Wait for approval on plan") {
		t.Fatalf("result = %q", result)
	}
	if !strings.Contains(result, "blocker: need user sign-off before editing") {
		t.Fatalf("result = %q", result)
	}
	snap := session.Snapshot()
	if snap.PlanState == nil || len(snap.PlanState.Steps) != 2 {
		t.Fatalf("plan state = %#v", snap.PlanState)
	}
	if got := snap.PlanState.Steps[0].Blocker; got != "need user sign-off before editing" {
		t.Fatalf("blocker = %q", got)
	}
}

func TestUpdatePlanRejectsBlockedStepWithoutBlocker(t *testing.T) {
	session := react.NewSession()
	tool := NewUpdatePlan(session)
	_, err := tool.Execute(context.Background(), map[string]any{
		"steps": []any{map[string]any{"step": "Wait for approval on plan", "status": "blocked"}},
	})
	if err == nil || !strings.Contains(err.Error(), "blocked") || !strings.Contains(err.Error(), "blocker") {
		t.Fatalf("err = %v", err)
	}
}
