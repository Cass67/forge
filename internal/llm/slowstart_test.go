package llm_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"forge/internal/llm"
)

// slowFirstTokenDriver models a healthy backend with a long prompt-processing
// phase: nothing is emitted for a while, then the stream runs normally. A local
// model prefilling a large prompt on a cold cache behaves exactly like this.
type slowFirstTokenDriver struct {
	delay  time.Duration
	calls  int
	tokens []string
}

func (d *slowFirstTokenDriver) Name() string { return "slow-first-token" }

func (d *slowFirstTokenDriver) Stream(ctx context.Context, _ []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	d.calls++
	select {
	case <-time.After(d.delay):
	case <-ctx.Done():
		return ctx.Err()
	}
	for _, tok := range d.tokens {
		select {
		case out <- llm.Token{Text: tok}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (d *slowFirstTokenDriver) StreamWithTools(ctx context.Context, m []llm.Message, _ []llm.ToolDef, out chan<- llm.Token) error {
	return d.Stream(ctx, m, out)
}

func (d *slowFirstTokenDriver) StreamWithToolsOptions(ctx context.Context, m []llm.Message, _ []llm.ToolDef, _ llm.NativeToolOptions, out chan<- llm.Token) error {
	return d.Stream(ctx, m, out)
}

func joinText(tokens []llm.Token) string {
	var sb strings.Builder
	for _, tok := range tokens {
		sb.WriteString(tok.Text)
	}
	return sb.String()
}

// Time to first token is prompt processing, not an idle stream. The idle
// timeout must not fire before anything has been emitted, or a slow start is
// killed and every retry restarts the same work.
func TestStreamIdleTimeoutDoesNotFireBeforeFirstToken(t *testing.T) {
	driver := &slowFirstTokenDriver{delay: 150 * time.Millisecond, tokens: []string{"hello ", "world"}}
	retry := llm.NewRetryDriverWithIdleTimeout(driver, 3, time.Millisecond, time.Millisecond, 10*time.Second, 40*time.Millisecond)

	out := make(chan llm.Token, 64)
	err := retry.Stream(context.Background(), nil, out)
	got := joinText(collect(out))
	if err != nil {
		t.Fatalf("slow first token failed: %v", err)
	}
	if got != "hello world" {
		t.Fatalf("response = %q, want %q", got, "hello world")
	}
	if driver.calls != 1 {
		t.Fatalf("driver called %d times; a slow start should not be retried", driver.calls)
	}
}

func TestStreamWithToolsIdleTimeoutDoesNotFireBeforeFirstToken(t *testing.T) {
	driver := &slowFirstTokenDriver{delay: 150 * time.Millisecond, tokens: []string{"ok"}}
	retry := llm.NewRetryDriverWithIdleTimeout(driver, 3, time.Millisecond, time.Millisecond, 10*time.Second, 40*time.Millisecond)

	out := make(chan llm.Token, 64)
	err := retry.StreamWithTools(context.Background(), nil, nil, out)
	got := joinText(collect(out))
	if err != nil {
		t.Fatalf("slow first token failed: %v", err)
	}
	if got != "ok" {
		t.Fatalf("response = %q, want %q", got, "ok")
	}
	if driver.calls != 1 {
		t.Fatalf("driver called %d times; a slow start should not be retried", driver.calls)
	}
}

// With no request timeout the idle timer is the only wall-clock bound, so it
// must still guard the first token or a dead stream hangs forever.
func TestStreamIdleTimeoutGuardsFirstTokenWithoutRequestTimeout(t *testing.T) {
	driver := &slowFirstTokenDriver{delay: time.Hour, tokens: []string{"never"}}
	retry := llm.NewRetryDriverWithIdleTimeout(driver, 1, time.Millisecond, time.Millisecond, 0, 30*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	out := make(chan llm.Token, 64)
	err := retry.Stream(ctx, nil, out)
	collect(out)

	if err == nil || !strings.Contains(err.Error(), "stream idle timeout") {
		t.Fatalf("error = %v, want stream idle timeout", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("idle timeout took %s; it must bound a silent stream when no request timeout is set", elapsed)
	}
}

// With a request timeout set, that timeout owns the prompt-processing phase.
func TestSilentStreamBoundedByRequestTimeoutNotIdleTimeout(t *testing.T) {
	driver := &slowFirstTokenDriver{delay: time.Hour, tokens: []string{"never"}}
	retry := llm.NewRetryDriverWithIdleTimeout(driver, 1, time.Millisecond, time.Millisecond, 120*time.Millisecond, 20*time.Millisecond)

	start := time.Now()
	out := make(chan llm.Token, 64)
	err := retry.Stream(context.Background(), nil, out)
	collect(out)

	if err == nil {
		t.Fatal("expected the request timeout to end a silent stream")
	}
	if strings.Contains(err.Error(), "stream idle timeout") {
		t.Fatalf("error = %v, want the request timeout to own time to first token", err)
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Fatalf("gave up after %s; the idle timeout fired before the request timeout", elapsed)
	}
}
