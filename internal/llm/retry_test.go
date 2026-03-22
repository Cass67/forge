package llm_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"forge/internal/llm"
)

type failNDriver struct {
	failCount int
	called    int
	resp      string
}

func (d *failNDriver) Name() string { return "fail-n" }
func (d *failNDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	d.called++
	if d.called <= d.failCount {
		return fmt.Errorf("transient error")
	}
	out <- llm.Token{Text: d.resp}
	return nil
}

type authErrorDriver struct{}

func (d *authErrorDriver) Name() string { return "auth-err" }
func (d *authErrorDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	return fmt.Errorf("401 unauthorized: invalid_api_key")
}

type slowDriver struct {
	delay time.Duration
}

func (d *slowDriver) Name() string { return "slow" }
func (d *slowDriver) Stream(ctx context.Context, _ []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	select {
	case <-time.After(d.delay):
		out <- llm.Token{Text: "done"}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func collect(out <-chan llm.Token) []llm.Token {
	var tokens []llm.Token
	for tok := range out {
		tokens = append(tokens, tok)
	}
	return tokens
}

func TestRetrySuccessFirstAttempt(t *testing.T) {
	inner := &failNDriver{failCount: 0, resp: "hello"}
	rd := llm.NewRetryDriver(inner, 3, time.Millisecond, time.Millisecond, 0)

	out := make(chan llm.Token, 64)
	err := rd.Stream(context.Background(), nil, out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tokens := collect(out)
	if len(tokens) != 1 || tokens[0].Text != "hello" {
		t.Errorf("unexpected tokens: %v", tokens)
	}
	if inner.called != 1 {
		t.Errorf("expected 1 call, got %d", inner.called)
	}
}

func TestRetrySuccessAfterTransient(t *testing.T) {
	inner := &failNDriver{failCount: 2, resp: "ok"}
	rd := llm.NewRetryDriver(inner, 3, time.Millisecond, time.Millisecond, 0)

	out := make(chan llm.Token, 64)
	err := rd.Stream(context.Background(), nil, out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tokens := collect(out)
	if len(tokens) != 1 || tokens[0].Text != "ok" {
		t.Errorf("unexpected tokens: %v", tokens)
	}
	if inner.called != 3 {
		t.Errorf("expected 3 calls, got %d", inner.called)
	}
}

func TestRetryMaxAttemptsExhausted(t *testing.T) {
	inner := &failNDriver{failCount: 5, resp: "never"}
	rd := llm.NewRetryDriver(inner, 3, time.Millisecond, time.Millisecond, 0)

	out := make(chan llm.Token, 64)
	err := rd.Stream(context.Background(), nil, out)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "all 3 attempts failed") {
		t.Errorf("unexpected error: %v", err)
	}
	if inner.called != 3 {
		t.Errorf("expected 3 calls, got %d", inner.called)
	}
}

func TestRetryNonRetryableError(t *testing.T) {
	inner := &authErrorDriver{}
	rd := llm.NewRetryDriver(inner, 3, time.Millisecond, time.Millisecond, 0)

	out := make(chan llm.Token, 64)
	err := rd.Stream(context.Background(), nil, out)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRetryContextCancelledDuringBackoff(t *testing.T) {
	inner := &failNDriver{failCount: 5, resp: "never"}
	rd := llm.NewRetryDriver(inner, 5, 5*time.Second, 5*time.Second, 0)

	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan llm.Token, 64)

	done := make(chan error, 1)
	go func() {
		done <- rd.Stream(ctx, nil, out)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	err := <-done
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestRetryPerCallTimeout(t *testing.T) {
	inner := &slowDriver{delay: 5 * time.Second}
	rd := llm.NewRetryDriver(inner, 2, time.Millisecond, time.Millisecond, 50*time.Millisecond)

	out := make(chan llm.Token, 64)
	err := rd.Stream(context.Background(), nil, out)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "all 2 attempts failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRetryDelegatesName(t *testing.T) {
	inner := &failNDriver{resp: "x"}
	rd := llm.NewRetryDriver(inner, 1, time.Millisecond, time.Millisecond, 0)
	if rd.Name() != "fail-n" {
		t.Errorf("expected fail-n, got %s", rd.Name())
	}
}
