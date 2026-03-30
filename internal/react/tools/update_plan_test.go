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
		"steps_json":  `[{"step":"Inspect files","status":"completed"},{"step":"Patch runtime","status":"in_progress"}]`,
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
		"steps_json": `[{"step":"Inspect files","status":"in_progress"},{"step":"Patch runtime","status":"in_progress"}]`,
	})
	if err == nil || !strings.Contains(err.Error(), "exactly one in_progress") {
		t.Fatalf("err = %v", err)
	}
}

func TestUpdatePlanAutoPromotesFirstPendingWhenNoInProgress(t *testing.T) {
	session := react.NewSession()
	tool := NewUpdatePlan(session)
	result, err := tool.Execute(context.Background(), map[string]any{
		"steps_json": `[{"step":"Inspect files","status":"completed"},{"step":"Patch runtime","status":"pending"}]`,
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
		"steps_json": `[{"step":"Remove stray files","status":"pending"},{"step":"Update .gitignore","status":"pending"},{"step":"Run lint","status":"pending"}]`,
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
