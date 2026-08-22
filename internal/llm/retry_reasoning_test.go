package llm

import (
	"context"
	"errors"
	"testing"
	"time"
)

type reasoningThenFailDriver struct {
	attempts int
}

func (d *reasoningThenFailDriver) Name() string { return "test" }

func (d *reasoningThenFailDriver) Stream(ctx context.Context, messages []Message, out chan<- Token) error {
	defer close(out)
	d.attempts++
	out <- Token{ReasoningContent: "thinking..."}
	if d.attempts == 1 {
		return ErrTruncatedStream
	}
	out <- Token{Text: "done"}
	return nil
}

// A stream that dies after emitting only reasoning has committed nothing to
// the caller, so the attempt must be repeated rather than reported.
func TestRetryRepeatsAfterReasoningOnlyFailure(t *testing.T) {
	inner := &reasoningThenFailDriver{}
	d := NewRetryDriver(inner, 3, time.Millisecond, time.Millisecond, 0)

	out := make(chan Token, 16)
	if err := d.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, out); err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}
	var text string
	for tok := range out {
		text += tok.Text
	}
	if inner.attempts != 2 {
		t.Fatalf("attempts = %d, want 2", inner.attempts)
	}
	if text != "done" {
		t.Fatalf("text = %q, want %q", text, "done")
	}
}

func TestIsRateLimitedIgnores429InsideLongerNumbers(t *testing.T) {
	if isRateLimited(errors.New("request 6042913 failed: token limit 142900 exceeded")) {
		t.Fatal("digit run containing 429 must not read as a rate limit")
	}
	if !isRateLimited(errors.New("unexpected status 429 from provider")) {
		t.Fatal("status 429 must read as a rate limit")
	}
}
