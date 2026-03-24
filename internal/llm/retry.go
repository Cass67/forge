package llm

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"
)

type RetryDriver struct {
	inner       Driver
	maxAttempts int
	initialWait time.Duration
	maxWait     time.Duration
	timeout     time.Duration
}

func NewRetryDriver(inner Driver, maxAttempts int, initialWait, maxWait, timeout time.Duration) *RetryDriver {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	return &RetryDriver{
		inner:       inner,
		maxAttempts: maxAttempts,
		initialWait: initialWait,
		maxWait:     maxWait,
		timeout:     timeout,
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

	var lastErr error
	for attempt := 0; attempt < d.maxAttempts; attempt++ {
		if attempt > 0 {
			wait := d.backoff(attempt)
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return ctx.Err()
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

		var tokens []Token
		for tok := range internal {
			tokens = append(tokens, tok)
		}

		lastErr = <-errCh
		if lastErr == nil {
			for _, tok := range tokens {
				select {
				case out <- tok:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		}

		if !isRetryable(lastErr) {
			return lastErr
		}
	}
	return fmt.Errorf("all %d attempts failed: %w", d.maxAttempts, lastErr)
}

func (d *RetryDriver) backoff(attempt int) time.Duration {
	base := float64(d.initialWait) * math.Pow(2, float64(attempt-1))
	if base > float64(d.maxWait) {
		base = float64(d.maxWait)
	}
	jitter := base * (0.5 + rand.Float64()*0.5)
	return time.Duration(jitter)
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	if err == context.Canceled {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, s := range []string{
		"400 bad request",
		"404 not found",
		"410 gone",
		"401",
		"403",
		"invalid_api_key",
		"authentication",
		"insufficient_quota",
		"quota exceeded",
		"billing",
		"context_length_exceeded",
		"maximum context length",
		"not a valid model id",
		"no endpoints available matching your guardrail restrictions",
		"data policy",
	} {
		if strings.Contains(msg, s) {
			return false
		}
	}
	return true
}
