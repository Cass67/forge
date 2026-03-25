package harness

import (
	"context"
	"errors"
	"testing"
)

type stubLocalExecutor struct {
	obs   Observation
	err   error
	calls int
}

func (s *stubLocalExecutor) Execute(_ context.Context, _ UserTurn, _ Classification) (Observation, error) {
	s.calls++
	return s.obs, s.err
}

func TestRunnerLocalInspectUsesLocalStep(t *testing.T) {
	local := &stubLocalExecutor{
		obs: Observation{
			Status:   ObservationComplete,
			Response: "Directory contains cmd and internal.",
			Summary:  "directory overview",
			TopicKey: "workspace:directory",
		},
	}
	runner := NewRunner(RunnerConfig{
		Session: NewSession(),
		Trace:   NewRecorder(),
		Local:   local,
	})

	result, err := runner.Run(context.Background(), "describe this directory")
	if err != nil {
		t.Fatal(err)
	}
	if local.calls != 1 {
		t.Fatalf("local calls = %d", local.calls)
	}
	if result.Classification.Family != FamilyInspect {
		t.Fatalf("family = %q", result.Classification.Family)
	}
	if result.Step.Kind != StepLocal {
		t.Fatalf("step = %#v", result.Step)
	}
	if result.Response != "Directory contains cmd and internal." {
		t.Fatalf("response = %q", result.Response)
	}
	if len(result.Trace) == 0 {
		t.Fatal("expected trace records")
	}
}

func TestRunnerLocalImplementationUsesLocalStep(t *testing.T) {
	local := &stubLocalExecutor{
		obs: Observation{
			Status:   ObservationComplete,
			Response: "Updated the auth handler and added tests.",
			Summary:  "implementation complete",
			TopicKey: "path:internal/auth/handler.go",
		},
	}
	runner := NewRunner(RunnerConfig{
		Session: NewSession(),
		Trace:   NewRecorder(),
		Local:   local,
	})

	result, err := runner.Run(context.Background(), "implement the auth handler fix")
	if err != nil {
		t.Fatal(err)
	}
	if result.Classification.Family != FamilyImplement {
		t.Fatalf("family = %q", result.Classification.Family)
	}
	if result.Step.Kind != StepLocal || result.Step.Worker != WorkerNone {
		t.Fatalf("step = %#v", result.Step)
	}
}

func TestRunnerBlocksOnLocalFailure(t *testing.T) {
	local := &stubLocalExecutor{
		obs: Observation{
			Status:  ObservationBlocked,
			Summary: "execution failed",
		},
		err: errors.New("execution failed"),
	}
	runner := NewRunner(RunnerConfig{
		Session: NewSession(),
		Trace:   NewRecorder(),
		Local:   local,
	})

	result, err := runner.Run(context.Background(), "describe this directory")
	if err == nil {
		t.Fatal("expected error")
	}
	if result.Decision.FinalState != StateBlocked {
		t.Fatalf("decision = %#v", result.Decision)
	}
}
