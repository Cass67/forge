package reacttools

import (
	"context"
	"strings"
	"testing"

	"forge/internal/react"
)

func TestEnterPlanModeSetsPlanTaskState(t *testing.T) {
	session := react.NewSession()
	tool := NewEnterPlanMode(session)
	result, err := tool.Execute(context.Background(), map[string]any{
		"objective": "design a richer prompt architecture",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Entered plan mode") {
		t.Fatalf("result = %q", result)
	}
	snap := session.Snapshot()
	if snap.TaskState == nil {
		t.Fatal("expected task state")
	}
	if snap.TaskState.Operation != "plan" {
		t.Fatalf("operation = %q", snap.TaskState.Operation)
	}
	if snap.TaskState.Objective != "design a richer prompt architecture" {
		t.Fatalf("objective = %q", snap.TaskState.Objective)
	}
}

func TestExitPlanModePromotesPlanToImplementationTaskState(t *testing.T) {
	session := react.NewSession()
	session.SetTaskState(react.TaskState{
		Objective:            "design a richer prompt architecture",
		Operation:            "plan",
		RequiredVerification: "produce a plan",
	})
	session.SetPlanState(react.PlanState{
		Steps: []react.PlanStep{
			{Step: "Inspect prompt flow", Status: "completed"},
			{Step: "Implement composer", Status: "in_progress"},
		},
	})

	tool := NewExitPlanMode(session)
	result, err := tool.Execute(context.Background(), map[string]any{
		"implementation_objective": "implement the prompt composer",
		"required_verification":    "run focused tests for prompt composition and integration",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Exited plan mode") {
		t.Fatalf("result = %q", result)
	}
	snap := session.Snapshot()
	if snap.TaskState == nil {
		t.Fatal("expected task state")
	}
	if snap.TaskState.Operation != "implement" {
		t.Fatalf("operation = %q", snap.TaskState.Operation)
	}
	if snap.TaskState.Objective != "implement the prompt composer" {
		t.Fatalf("objective = %q", snap.TaskState.Objective)
	}
	if snap.TaskState.RequiredVerification != "run focused tests for prompt composition and integration" {
		t.Fatalf("required verification = %q", snap.TaskState.RequiredVerification)
	}
}
