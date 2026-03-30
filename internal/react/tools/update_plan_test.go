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
