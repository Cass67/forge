package react

import (
	"context"
	"errors"

	agenttools "forge/internal/agent/tools"
)

type ToolRunStatus string

const (
	ToolRunSucceeded ToolRunStatus = "succeeded"
	ToolRunFailed    ToolRunStatus = "failed"
	ToolRunTimedOut  ToolRunStatus = "timed_out"
	ToolRunCancelled ToolRunStatus = "cancelled"
)

type ToolRunRequest struct {
	TurnID int
	CallID string
	Tool   agenttools.Tool
	Args   map[string]any
}

type ToolRunResult struct {
	Status ToolRunStatus
	Result string
	Diff   string
	Error  error
}

type ToolOrchestrator struct{}

func (ToolOrchestrator) Run(ctx context.Context, req ToolRunRequest) ToolRunResult {
	runCtx := ctx
	cancel := func() {}
	if timeout := req.Tool.EffectiveTimeout(); timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	type executeResult struct {
		result string
		err    error
	}
	executeDone := make(chan executeResult, 1)
	go func() {
		result, err := req.Tool.Execute(runCtx, req.Args)
		executeDone <- executeResult{result: result, err: err}
	}()

	var result string
	var err error
	select {
	case done := <-executeDone:
		result = done.result
		err = done.err
	case <-runCtx.Done():
		err = runCtx.Err()
	}

	if err != nil {
		status := ToolRunFailed
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			status = ToolRunCancelled
		} else if errors.Is(err, context.DeadlineExceeded) || errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			status = ToolRunTimedOut
		}
		return ToolRunResult{Status: status, Result: result, Error: err}
	}

	diff := ""
	if req.Tool.LastDiff != nil {
		diff = req.Tool.LastDiff()
	}
	return ToolRunResult{Status: ToolRunSucceeded, Result: result, Diff: diff}
}
