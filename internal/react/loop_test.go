package react

import (
	"context"
	"testing"
)

type stubTurnRunner struct {
	calls int
	input string
	err   error
}

func (s *stubTurnRunner) Run(_ context.Context, input string) error {
	s.calls++
	s.input = input
	return s.err
}

func TestRunnerRunInvokesAgentAndProgress(t *testing.T) {
	stub := &stubTurnRunner{}
	session := NewSession()
	var progress string
	r := NewRunner(Config{
		Agent:   stub,
		Session: session,
		Progress: func(text string) {
			progress = text
		},
	})

	if err := r.Run(context.Background(), "  inspect this file  "); err != nil {
		t.Fatal(err)
	}
	if stub.calls != 1 {
		t.Fatalf("calls = %d, want 1", stub.calls)
	}
	if stub.input != "inspect this file" {
		t.Fatalf("input = %q", stub.input)
	}
	if progress == "" {
		t.Fatal("expected progress callback")
	}
	snap := session.Snapshot()
	if snap.Turn != 1 {
		t.Fatalf("turn = %d, want 1", snap.Turn)
	}
}

func TestRunnerRunReturnsErrorWhenAgentMissing(t *testing.T) {
	r := NewRunner(Config{})
	if err := r.Run(context.Background(), "inspect"); err == nil {
		t.Fatal("expected error when agent is nil")
	}
}

func TestRunnerRunSkipsEmptyInput(t *testing.T) {
	stub := &stubTurnRunner{}
	r := NewRunner(Config{Agent: stub})
	if err := r.Run(context.Background(), "   "); err != nil {
		t.Fatal(err)
	}
	if stub.calls != 0 {
		t.Fatalf("calls = %d, want 0", stub.calls)
	}
}
