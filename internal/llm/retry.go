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
	observer          RetryObserver
}

type RetryEventKind string

const (
	RetryEventAttempt RetryEventKind = "attempt"
	RetryEventWait    RetryEventKind = "wait"
)

type RetryEvent struct {
	Kind              RetryEventKind
	Driver            string
	Operation         string
	Attempt           int
	NextAttempt       int
	MaxAttempts       int
	Wait              time.Duration
	Timeout           time.Duration
	StreamIdleTimeout time.Duration
	EmittedAny        bool
	Err               error
}

type RetryObserver func(RetryEvent)

type RetryAttemptsExhaustedError struct {
	Attempts int
	Timeout  time.Duration
	Err      error
}

func (e *RetryAttemptsExhaustedError) Error() string {
	if e == nil {
		return "retry attempts exhausted"
	}
	if e.Err == nil {
		return fmt.Sprintf("all %d attempts failed", e.Attempts)
	}
	if e.Err == context.DeadlineExceeded {
		return fmt.Sprintf("all %d attempts failed: LLM call timed out after %ds timeout (adjust retry.timeout_seconds in config.toml)", e.Attempts, int(e.Timeout.Seconds()))
	}
	return fmt.Sprintf("all %d attempts failed: %v", e.Attempts, e.Err)
}

func (e *RetryAttemptsExhaustedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
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

func (d *RetryDriver) SetRetryObserver(observer RetryObserver) {
	d.observer = observer
}

func (d *RetryDriver) observe(event RetryEvent) {
	if d.observer == nil {
		return
	}
	d.observer(event)
}

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
			d.observe(RetryEvent{
				Kind:              RetryEventWait,
				Driver:            d.Name(),
				Operation:         "stream",
				NextAttempt:       attempt + 1,
				MaxAttempts:       d.maxAttempts,
				Wait:              wait,
				Timeout:           d.timeout,
				StreamIdleTimeout: d.streamIdleTimeout,
				Err:               lastErr,
			})
			if err := retrySleep(ctx, wait); err != nil {
				return err
			}
		}
		d.observe(RetryEvent{
			Kind:              RetryEventAttempt,
			Driver:            d.Name(),
			Operation:         "stream",
			Attempt:           attempt + 1,
			MaxAttempts:       d.maxAttempts,
			Timeout:           d.timeout,
			StreamIdleTimeout: d.streamIdleTimeout,
		})

		callCtx := ctx
		var cancel context.CancelFunc = func() {}
		if d.timeout > 0 {
			callCtx, cancel = context.WithTimeout(ctx, d.timeout)
		} else if d.streamIdleTimeout > 0 {
			callCtx, cancel = context.WithCancel(ctx)
		}

		internal := make(chan Token, 64)
		errCh := make(chan error, 1)
		go func() {
			errCh <- d.inner.Stream(callCtx, messages, internal)
		}()

		// The idle timeout detects a stream that stopped producing, so it
		// should not be armed until something has been produced. Time to first
		// token is prompt processing, and a large prompt on a cold cache can
		// legitimately sit silent for minutes; arming early cancels that work,
		// and because every retry restarts it the request can never finish.
		//
		// The request timeout already bounds that phase, so when one is set
		// the idle timer waits for the first token. With no request timeout
		// the idle timer is the only wall-clock bound, so it stays armed from
		// the start rather than letting a dead stream hang forever. A nil
		// timer yields a nil channel, which blocks forever in the select.
		var idleTimer *time.Timer
		var idleC <-chan time.Time
		if d.timeout <= 0 {
			idleTimer, idleC = newStreamIdleTimer(d.streamIdleTimeout)
		}
		var emittedAny bool
		var idleErr error
		for {
			select {
			case tok, ok := <-internal:
				if !ok {
					goto done
				}
				if idleTimer == nil {
					idleTimer, idleC = newStreamIdleTimer(d.streamIdleTimeout)
				} else {
					resetStreamIdleTimer(idleTimer, d.streamIdleTimeout)
				}
				emittedAny = true
				select {
				case out <- tok:
				case <-ctx.Done():
					stopStreamIdleTimer(idleTimer)
					cancel()
					return ctx.Err()
				}
			case <-ctx.Done():
				stopStreamIdleTimer(idleTimer)
				cancel()
				return ctx.Err()
			case <-idleC:
				idleErr = fmt.Errorf("stream idle timeout after %s", d.streamIdleTimeout)
				cancel()
				goto done
			}
		}
	done:
		stopStreamIdleTimer(idleTimer)

		if idleErr != nil {
			lastErr = idleErr
		} else {
			lastErr = <-errCh
		}
		cancel()
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
			if lastErr == context.DeadlineExceeded || strings.Contains(lastErr.Error(), "context deadline exceeded") {
				return fmt.Errorf("LLM call timed out after %v (adjust retry.timeout_seconds in config.toml): %w", d.timeout, context.DeadlineExceeded)
			}
			return lastErr
		}
	}
	return &RetryAttemptsExhaustedError{Attempts: d.maxAttempts, Timeout: d.timeout, Err: lastErr}
}

func (d *RetryDriver) StreamWithTools(ctx context.Context, messages []Message, tools []ToolDef, out chan<- Token) error {
	return d.StreamWithToolsOptions(ctx, messages, tools, NativeToolOptions{}, out)
}

func (d *RetryDriver) StreamWithToolsOptions(ctx context.Context, messages []Message, tools []ToolDef, opts NativeToolOptions, out chan<- Token) error {
	caller, ok := d.inner.(NativeToolCaller)
	if !ok {
		close(out)
		return fmt.Errorf("inner driver %q does not support native tool calling", d.inner.Name())
	}
	advanced, _ := d.inner.(NativeToolCallerWithOptions)
	defer close(out)

	if err := waitForRateLimitCooldown(ctx, d.Name()); err != nil {
		return err
	}

	var lastErr error
	for attempt := 0; attempt < d.maxAttempts; attempt++ {
		if attempt > 0 {
			wait := d.backoff(attempt, lastErr)
			d.observe(RetryEvent{
				Kind:              RetryEventWait,
				Driver:            d.Name(),
				Operation:         "stream_with_tools",
				NextAttempt:       attempt + 1,
				MaxAttempts:       d.maxAttempts,
				Wait:              wait,
				Timeout:           d.timeout,
				StreamIdleTimeout: d.streamIdleTimeout,
				Err:               lastErr,
			})
			if err := retrySleep(ctx, wait); err != nil {
				return err
			}
		}
		d.observe(RetryEvent{
			Kind:              RetryEventAttempt,
			Driver:            d.Name(),
			Operation:         "stream_with_tools",
			Attempt:           attempt + 1,
			MaxAttempts:       d.maxAttempts,
			Timeout:           d.timeout,
			StreamIdleTimeout: d.streamIdleTimeout,
		})

		callCtx := ctx
		var cancel context.CancelFunc = func() {}
		if d.timeout > 0 {
			callCtx, cancel = context.WithTimeout(ctx, d.timeout)
		} else if d.streamIdleTimeout > 0 {
			callCtx, cancel = context.WithCancel(ctx)
		}

		internal := make(chan Token, 64)
		errCh := make(chan error, 1)
		go func() {
			if advanced != nil {
				errCh <- advanced.StreamWithToolsOptions(callCtx, messages, tools, opts, internal)
				return
			}
			errCh <- caller.StreamWithTools(callCtx, messages, tools, internal)
		}()

		// The idle timeout detects a stream that stopped producing, so it
		// should not be armed until something has been produced. Time to first
		// token is prompt processing, and a large prompt on a cold cache can
		// legitimately sit silent for minutes; arming early cancels that work,
		// and because every retry restarts it the request can never finish.
		//
		// The request timeout already bounds that phase, so when one is set
		// the idle timer waits for the first token. With no request timeout
		// the idle timer is the only wall-clock bound, so it stays armed from
		// the start rather than letting a dead stream hang forever. A nil
		// timer yields a nil channel, which blocks forever in the select.
		var idleTimer *time.Timer
		var idleC <-chan time.Time
		if d.timeout <= 0 {
			idleTimer, idleC = newStreamIdleTimer(d.streamIdleTimeout)
		}
		var emittedAny bool
		var idleErr error
		for {
			select {
			case tok, ok := <-internal:
				if !ok {
					goto done
				}
				if idleTimer == nil {
					idleTimer, idleC = newStreamIdleTimer(d.streamIdleTimeout)
				} else {
					resetStreamIdleTimer(idleTimer, d.streamIdleTimeout)
				}
				emittedAny = true
				select {
				case out <- tok:
				case <-ctx.Done():
					stopStreamIdleTimer(idleTimer)
					cancel()
					return ctx.Err()
				}
			case <-ctx.Done():
				stopStreamIdleTimer(idleTimer)
				cancel()
				return ctx.Err()
			case <-idleC:
				idleErr = fmt.Errorf("stream idle timeout after %s", d.streamIdleTimeout)
				cancel()
				goto done
			}
		}
	done:
		stopStreamIdleTimer(idleTimer)

		if idleErr != nil {
			lastErr = idleErr
		} else {
			lastErr = <-errCh
		}
		cancel()
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
			if lastErr == context.DeadlineExceeded || strings.Contains(lastErr.Error(), "context deadline exceeded") {
				return fmt.Errorf("LLM call timed out after %v (adjust retry.timeout_seconds in config.toml): %w", d.timeout, context.DeadlineExceeded)
			}
			return lastErr
		}
	}
	return &RetryAttemptsExhaustedError{Attempts: d.maxAttempts, Timeout: d.timeout, Err: lastErr}
}

func newStreamIdleTimer(timeout time.Duration) (*time.Timer, <-chan time.Time) {
	if timeout <= 0 {
		return nil, nil
	}
	timer := time.NewTimer(timeout)
	return timer, timer.C
}

func resetStreamIdleTimer(timer *time.Timer, timeout time.Duration) {
	if timer == nil || timeout <= 0 {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(timeout)
}

func stopStreamIdleTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
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
