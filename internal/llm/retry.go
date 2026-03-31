package llm

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"sync"
	"time"

	"forge/internal/resilience/errors"
)

var (
	retryNow               = time.Now
	retryRateLimitCooldown = 10 * time.Second
	retrySleep             = func(ctx context.Context, d time.Duration) error {
		select {
		case <-time.After(d):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	retryCooldownMu sync.Mutex
	retryCooldowns  = map[string]time.Time{}
)

type RetryDriver struct {
	inner             Driver
	maxAttempts       int
	initialWait       time.Duration
	maxWait           time.Duration
	timeout           time.Duration
	streamIdleTimeout time.Duration
}

func NewRetryDriver(inner Driver, maxAttempts int, initialWait, maxWait, timeout time.Duration) *RetryDriver {
	return NewRetryDriverWithIdleTimeout(inner, maxAttempts, initialWait, maxWait, timeout, 0)
}

func NewRetryDriverWithIdleTimeout(inner Driver, maxAttempts int, initialWait, maxWait, timeout, streamIdleTimeout time.Duration) *RetryDriver {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	return &RetryDriver{
		inner:             inner,
		maxAttempts:       maxAttempts,
		initialWait:       initialWait,
		maxWait:           maxWait,
		timeout:           timeout,
		streamIdleTimeout: streamIdleTimeout,
	}
}

func (d *RetryDriver) Name() string { return d.inner.Name() }

func (d *RetryDriver) SetParams(p Params) {
	if c, ok := d.inner.(Configurable); ok {
		c.SetParams(p)
	}
}

func (d *RetryDriver) LastUsage() Usage {
	if reporter, ok := d.inner.(UsageReporter); ok {
		return reporter.LastUsage()
	}
	return Usage{}
}

func (d *RetryDriver) LastRequestMode() string {
	if reporter, ok := d.inner.(RequestModeReporter); ok {
		return reporter.LastRequestMode()
	}
	return ""
}

func (d *RetryDriver) ResetConversation() {
	if resetter, ok := d.inner.(ConversationResetter); ok {
		resetter.ResetConversation()
	}
}

func (d *RetryDriver) Stream(ctx context.Context, messages []Message, out chan<- Token) error {
	defer close(out)

	if err := waitForRateLimitCooldown(ctx, d.Name()); err != nil {
		return err
	}

	var lastErr error
	for attempt := 0; attempt < d.maxAttempts; attempt++ {
		if attempt > 0 {
			wait := d.backoff(attempt, lastErr)
			if err := retrySleep(ctx, wait); err != nil {
				return err
			}
		}

		callCtx := ctx
		if d.timeout > 0 {
			var cancel context.CancelFunc
			callCtx, cancel = context.WithTimeout(ctx, d.timeout)
			defer cancel()
		}

		internal := make(chan Token, 64)
		errCh := make(chan error, 1)
		go func() {
			errCh <- d.inner.Stream(callCtx, messages, internal)
		}()

		var emittedAny bool
		for tok := range internal {
			emittedAny = true
			select {
			case out <- tok:
			case <-ctx.Done():
				for range internal {
				}
				<-errCh
				return ctx.Err()
			}
		}

		lastErr = <-errCh
		if lastErr == nil {
			return nil
		}
		if isRateLimited(lastErr) {
			rememberRateLimit(d.Name())
		}
		fe := errors.ClassifyError(lastErr)
		if !fe.Retryable {
			return lastErr
		}
		if emittedAny {
			return lastErr
		}
	}
	return fmt.Errorf("all %d attempts failed: %w", d.maxAttempts, lastErr)
}

func (d *RetryDriver) StreamWithTools(ctx context.Context, messages []Message, tools []ToolDef, out chan<- Token) error {
	caller, ok := d.inner.(NativeToolCaller)
	if !ok {
		close(out)
		return fmt.Errorf("inner driver %q does not support native tool calling", d.inner.Name())
	}
	defer close(out)

	if err := waitForRateLimitCooldown(ctx, d.Name()); err != nil {
		return err
	}

	var lastErr error
	for attempt := 0; attempt < d.maxAttempts; attempt++ {
		if attempt > 0 {
			wait := d.backoff(attempt, lastErr)
			if err := retrySleep(ctx, wait); err != nil {
				return err
			}
		}

		callCtx := ctx
		if d.timeout > 0 {
			var cancel context.CancelFunc
			callCtx, cancel = context.WithTimeout(ctx, d.timeout)
			defer cancel()
		}

		internal := make(chan Token, 64)
		errCh := make(chan error, 1)
		go func() {
			errCh <- caller.StreamWithTools(callCtx, messages, tools, internal)
		}()

		var emittedAny bool
		for tok := range internal {
			emittedAny = true
			select {
			case out <- tok:
			case <-ctx.Done():
				for range internal {
				}
				<-errCh
				return ctx.Err()
			}
		}

		lastErr = <-errCh
		if lastErr == nil {
			return nil
		}
		if isRateLimited(lastErr) {
			rememberRateLimit(d.Name())
		}
		fe := errors.ClassifyError(lastErr)
		if !fe.Retryable {
			return lastErr
		}
		if emittedAny {
			return lastErr
		}
	}
	return fmt.Errorf("all %d attempts failed: %w", d.maxAttempts, lastErr)
}

func (d *RetryDriver) backoff(attempt int, lastErr error) time.Duration {
	if lastErr != nil {
		if delay, ok := errors.ParseRetryAfterDelay(lastErr.Error()); ok {
			dur := time.Duration(delay * float64(time.Second))
			if dur > d.maxWait {
				dur = d.maxWait
			}
			return dur
		}
	}

	base := float64(d.initialWait) * math.Pow(2, float64(attempt-1))
	if base > float64(d.maxWait) {
		base = float64(d.maxWait)
	}
	jitter := base * (0.5 + rand.Float64()*0.5)
	return time.Duration(jitter)
}

func isRateLimited(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "429") ||
		strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "rate limit exceeded") ||
		strings.Contains(msg, "rate limited")
}

func waitForRateLimitCooldown(ctx context.Context, key string) error {
	retryCooldownMu.Lock()
	until, ok := retryCooldowns[key]
	retryCooldownMu.Unlock()
	if !ok {
		return nil
	}
	now := retryNow()
	if !until.After(now) {
		retryCooldownMu.Lock()
		delete(retryCooldowns, key)
		retryCooldownMu.Unlock()
		return nil
	}
	return retrySleep(ctx, until.Sub(now))
}

func rememberRateLimit(key string) {
	retryCooldownMu.Lock()
	defer retryCooldownMu.Unlock()
	retryCooldowns[key] = retryNow().Add(retryRateLimitCooldown)
}
