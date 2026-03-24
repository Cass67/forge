package llm

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

type rateLimitDriver struct {
	name   string
	called int
	err    error
}

func (d *rateLimitDriver) Name() string { return d.name }

func (d *rateLimitDriver) Stream(_ context.Context, _ []Message, out chan<- Token) error {
	defer close(out)
	d.called++
	if d.err != nil {
		return d.err
	}
	out <- Token{Text: "ok"}
	return nil
}

func TestRetryDriverAppliesSharedCooldownAfterRateLimit(t *testing.T) {
	origSleep := retrySleep
	origNow := retryNow
	origCooldown := retryRateLimitCooldown
	origCooldowns := retryCooldowns
	defer func() {
		retrySleep = origSleep
		retryNow = origNow
		retryRateLimitCooldown = origCooldown
		retryCooldowns = origCooldowns
	}()

	var nowMu sync.Mutex
	now := time.Unix(100, 0)
	retryNow = func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		return now
	}
	var slept []time.Duration
	retrySleep = func(_ context.Context, d time.Duration) error {
		nowMu.Lock()
		now = now.Add(d)
		nowMu.Unlock()
		slept = append(slept, d)
		return nil
	}
	retryRateLimitCooldown = 10 * time.Second
	retryCooldowns = map[string]time.Time{}

	firstInner := &rateLimitDriver{
		name: "openrouter/model-a",
		err:  fmt.Errorf(`POST "https://openrouter.ai/api/v1/chat/completions": 429 Too Many Requests {"message":"Rate limit exceeded"}`),
	}
	first := NewRetryDriver(firstInner, 1, time.Millisecond, time.Millisecond, 0)

	err := first.Stream(context.Background(), nil, make(chan Token, 1))
	if err == nil {
		t.Fatal("expected first call to fail")
	}

	secondInner := &rateLimitDriver{name: "openrouter/model-a"}
	second := NewRetryDriver(secondInner, 1, time.Millisecond, time.Millisecond, 0)

	out := make(chan Token, 1)
	if err := second.Stream(context.Background(), nil, out); err != nil {
		t.Fatalf("unexpected second call error: %v", err)
	}
	if len(slept) != 1 || slept[0] != 10*time.Second {
		t.Fatalf("slept = %#v, want [10s]", slept)
	}
	if secondInner.called != 1 {
		t.Fatalf("secondInner.called = %d, want 1", secondInner.called)
	}
}
