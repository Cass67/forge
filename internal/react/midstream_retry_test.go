package react

import (
	"context"
	"errors"
	"testing"

	"forge/internal/llm"
)

// midStreamFailDriver emits a token and then drops the connection, which is the
// dominant failure mode for long streaming turns.
type midStreamFailDriver struct {
	calls int
}

func (d *midStreamFailDriver) Name() string { return "mid-stream-fail" }

func (d *midStreamFailDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	d.calls++
	if d.calls == 1 {
		out <- llm.Token{Text: "partial answer"}
		return errors.New("connection reset by peer")
	}
	out <- llm.Token{Text: "complete answer"}
	return nil
}

func TestMidStreamFailureIsRetried(t *testing.T) {
	driver := &midStreamFailDriver{}
	r := NewRunner(Config{Driver: driver, Session: NewSession()})
	if err := r.Run(context.Background(), "hello"); err != nil {
		t.Fatalf("run failed after mid-stream drop: %v", err)
	}
	if driver.calls < 2 {
		t.Fatalf("driver called %d times; mid-stream failure was not retried", driver.calls)
	}
	if got := r.LastResponse(); got != "complete answer" {
		t.Fatalf("last response = %q, want the retried answer", got)
	}
}
