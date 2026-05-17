package react

import (
	"context"
	"errors"
	"testing"
	"time"

	agenttools "forge/internal/agent/tools"
)

func TestToolOrchestratorRunTimesOut(t *testing.T) {
	orchestrator := ToolOrchestrator{}
	tool := agenttools.Tool{
		Name:    "slow_tool",
		Timeout: 10 * time.Millisecond,
		Execute: func(ctx context.Context, _ map[string]any) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		},
	}

	result := orchestrator.Run(context.Background(), ToolRunRequest{
		TurnID: 1,
		CallID: "call-1",
		Tool:   tool,
		Args:   map[string]any{"input": "value"},
	})

	if result.Status != ToolRunTimedOut {
		t.Fatalf("status = %q, want %q", result.Status, ToolRunTimedOut)
	}
	if !errors.Is(result.Error, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline exceeded", result.Error)
	}
}

func TestToolOrchestratorRunTimesOutWhenToolIgnoresContext(t *testing.T) {
	orchestrator := ToolOrchestrator{}
	tool := agenttools.Tool{
		Name:    "stuck_tool",
		Timeout: 10 * time.Millisecond,
		Execute: func(context.Context, map[string]any) (string, error) {
			select {}
		},
	}

	started := time.Now()
	result := orchestrator.Run(context.Background(), ToolRunRequest{
		TurnID: 1,
		CallID: "call-stuck",
		Tool:   tool,
		Args:   map[string]any{},
	})

	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("Run returned after %v, want prompt timeout", elapsed)
	}
	if result.Status != ToolRunTimedOut {
		t.Fatalf("status = %q, want %q", result.Status, ToolRunTimedOut)
	}
	if !errors.Is(result.Error, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline exceeded", result.Error)
	}
}

func TestToolOrchestratorRunCancelledByParent(t *testing.T) {
	orchestrator := ToolOrchestrator{}
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	tool := agenttools.Tool{
		Name:    "cancelled_tool",
		Timeout: time.Minute,
		Execute: func(ctx context.Context, _ map[string]any) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		},
	}

	result := orchestrator.Run(parent, ToolRunRequest{
		TurnID: 2,
		CallID: "call-2",
		Tool:   tool,
		Args:   map[string]any{},
	})

	if result.Status != ToolRunCancelled {
		t.Fatalf("status = %q, want %q", result.Status, ToolRunCancelled)
	}
	if !errors.Is(result.Error, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", result.Error)
	}
}

func TestToolOrchestratorRunCancelledByParentWhenToolIgnoresContext(t *testing.T) {
	orchestrator := ToolOrchestrator{}
	parent, cancel := context.WithCancel(context.Background())
	tool := agenttools.Tool{
		Name:    "stuck_cancelled_tool",
		Timeout: time.Minute,
		Execute: func(context.Context, map[string]any) (string, error) {
			select {}
		},
	}
	cancel()

	started := time.Now()
	result := orchestrator.Run(parent, ToolRunRequest{
		TurnID: 2,
		CallID: "call-stuck-cancelled",
		Tool:   tool,
		Args:   map[string]any{},
	})

	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("Run returned after %v, want prompt cancellation", elapsed)
	}
	if result.Status != ToolRunCancelled {
		t.Fatalf("status = %q, want %q", result.Status, ToolRunCancelled)
	}
	if !errors.Is(result.Error, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", result.Error)
	}
}

func TestToolOrchestratorRunSucceededIncludesResultAndDiff(t *testing.T) {
	orchestrator := ToolOrchestrator{}
	tool := agenttools.Tool{
		Name: "successful_tool",
		Execute: func(context.Context, map[string]any) (string, error) {
			return "tool result", nil
		},
		LastDiff: func() string { return "diff output" },
	}

	result := orchestrator.Run(context.Background(), ToolRunRequest{
		TurnID: 3,
		CallID: "call-3",
		Tool:   tool,
		Args:   map[string]any{"input": "value"},
	})

	if result.Status != ToolRunSucceeded {
		t.Fatalf("status = %q, want %q", result.Status, ToolRunSucceeded)
	}
	if result.Result != "tool result" {
		t.Fatalf("result = %q, want %q", result.Result, "tool result")
	}
	if result.Diff != "diff output" {
		t.Fatalf("diff = %q, want %q", result.Diff, "diff output")
	}
	if result.Error != nil {
		t.Fatalf("error = %v, want nil", result.Error)
	}
}
