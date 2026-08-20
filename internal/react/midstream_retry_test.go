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

// twoContextErrorsDriver overflows twice, which a long run does whenever one
// compaction pass does not free enough room.
type twoContextErrorsDriver struct {
	calls int
}

func (d *twoContextErrorsDriver) Name() string { return "two-context-errors" }

func (d *twoContextErrorsDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	d.calls++
	if d.calls <= 2 {
		return errors.New("context_length_exceeded")
	}
	out <- llm.Token{Text: "recovered"}
	return nil
}

// A long run overflows more than once: one compaction pass often lands still
// over the limit, because the freshest steps it must protect are themselves
// large. Recovery has to be able to run again within the same turn.
func TestRecoversFromRepeatedContextOverflow(t *testing.T) {
	// Tool results large enough that shadowing the old span still leaves an
	// oversized protected tail, so a second pass has real work to do.
	session, _ := buildLongSingleTurnSession(t, 120, 70*1024)
	driver := &twoContextErrorsDriver{}
	r := NewRunner(Config{Driver: driver, Session: session})
	if err := r.Run(context.Background(), "continue"); err != nil {
		t.Fatalf("run failed on the second overflow: %v", err)
	}
	if got := r.LastResponse(); got != "recovered" {
		t.Fatalf("last response = %q, want recovery after two overflows", got)
	}
}
