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

type quotaErrorDriver struct {
	called int
}

func (d *quotaErrorDriver) Name() string { return "quota-err" }
func (d *quotaErrorDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	d.called++
	return fmt.Errorf(`received error while streaming: {"type":"insufficient_quota","code":"insufficient_quota","message":"quota exceeded"}`)
}

type invalidModelDriver struct {
	called int
	err    string
}

func (d *invalidModelDriver) Name() string { return "invalid-model" }
func (d *invalidModelDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	d.called++
	return fmt.Errorf("%s", d.err)
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

type usageDriver struct {
	failNDriver
	usage llm.Usage
	mode  string
	reset bool
}

func (d *usageDriver) LastUsage() llm.Usage    { return d.usage }
func (d *usageDriver) LastRequestMode() string { return d.mode }
func (d *usageDriver) ResetConversation()      { d.reset = true }

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

func TestRetryDoesNotRetryInsufficientQuota(t *testing.T) {
	inner := &quotaErrorDriver{}
	rd := llm.NewRetryDriver(inner, 3, time.Millisecond, time.Millisecond, 0)

	out := make(chan llm.Token, 64)
	err := rd.Stream(context.Background(), nil, out)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "insufficient_quota") {
		t.Fatalf("unexpected error: %v", err)
	}
	if inner.called != 1 {
		t.Fatalf("expected 1 call, got %d", inner.called)
	}
}

func TestRetryDoesNotRetryInvalidModelErrors(t *testing.T) {
	inner := &invalidModelDriver{err: `POST "https://openrouter.ai/api/v1/chat/completions": 400 Bad Request {"message":"free is not a valid model ID","code":400}`}
	rd := llm.NewRetryDriver(inner, 3, time.Millisecond, time.Millisecond, 0)

	out := make(chan llm.Token, 64)
	err := rd.Stream(context.Background(), nil, out)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "not a valid model id") {
		t.Fatalf("unexpected error: %v", err)
	}
	if inner.called != 1 {
		t.Fatalf("expected 1 call, got %d", inner.called)
	}
}

func TestRetryDoesNotRetryPrivacyRestrictionErrors(t *testing.T) {
	inner := &invalidModelDriver{err: `POST "https://openrouter.ai/api/v1/chat/completions": 404 Not Found {"message":"No endpoints available matching your guardrail restrictions and data policy.","code":404}`}
	rd := llm.NewRetryDriver(inner, 3, time.Millisecond, time.Millisecond, 0)

	out := make(chan llm.Token, 64)
	err := rd.Stream(context.Background(), nil, out)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "guardrail restrictions") {
		t.Fatalf("unexpected error: %v", err)
	}
	if inner.called != 1 {
		t.Fatalf("expected 1 call, got %d", inner.called)
	}
}

func TestRetryDoesNotRetryGoneErrors(t *testing.T) {
	inner := &invalidModelDriver{err: `POST "https://integrate.api.nvidia.com/v1/chat/completions": 410 Gone`}
	rd := llm.NewRetryDriver(inner, 3, time.Millisecond, time.Millisecond, 0)

	out := make(chan llm.Token, 64)
	err := rd.Stream(context.Background(), nil, out)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "410 gone") {
		t.Fatalf("unexpected error: %v", err)
	}
	if inner.called != 1 {
		t.Fatalf("expected 1 call, got %d", inner.called)
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

func TestRetryDelegatesUsageReporter(t *testing.T) {
	inner := &usageDriver{usage: llm.Usage{InputTokens: 120, OutputTokens: 30}}
	rd := llm.NewRetryDriver(inner, 1, time.Millisecond, time.Millisecond, 0)

	reporter, ok := any(rd).(llm.UsageReporter)
	if !ok {
		t.Fatal("RetryDriver should implement UsageReporter")
	}
	got := reporter.LastUsage()
	if got.InputTokens != 120 || got.OutputTokens != 30 {
		t.Fatalf("usage = %+v", got)
	}
}

func TestRetryDelegatesRequestModeReporter(t *testing.T) {
	inner := &usageDriver{mode: "responses full input"}
	rd := llm.NewRetryDriver(inner, 1, time.Millisecond, time.Millisecond, 0)

	reporter, ok := any(rd).(llm.RequestModeReporter)
	if !ok {
		t.Fatal("RetryDriver should implement RequestModeReporter")
	}
	if got := reporter.LastRequestMode(); got != "responses full input" {
		t.Fatalf("LastRequestMode = %q", got)
	}
}

func TestRetryDelegatesConversationResetter(t *testing.T) {
	inner := &usageDriver{}
	rd := llm.NewRetryDriver(inner, 1, time.Millisecond, time.Millisecond, 0)

	resetter, ok := any(rd).(llm.ConversationResetter)
	if !ok {
		t.Fatal("RetryDriver should implement ConversationResetter")
	}
	resetter.ResetConversation()
	if !inner.reset {
		t.Fatal("ResetConversation not forwarded")
	}
}

type nativeInnerDriver struct {
	callCount int
	lastOpts  []llm.NativeToolOptions
}

func (d *nativeInnerDriver) Name() string { return "native" }
func (d *nativeInnerDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	close(out)
	return nil
}
func (d *nativeInnerDriver) StreamWithTools(_ context.Context, _ []llm.Message, _ []llm.ToolDef, out chan<- llm.Token) error {
	return d.StreamWithToolsOptions(context.Background(), nil, nil, llm.NativeToolOptions{}, out)
}
func (d *nativeInnerDriver) StreamWithToolsOptions(_ context.Context, _ []llm.Message, _ []llm.ToolDef, opts llm.NativeToolOptions, out chan<- llm.Token) error {
	defer close(out)
	d.callCount++
	d.lastOpts = append(d.lastOpts, opts)
	out <- llm.Token{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "git_status", ArgsJSON: "{}"}}
	return nil
}

// midStreamFailDriver emits tokens and then returns a retryable error.
type midStreamFailDriver struct {
	called int
	tokens []string
}

func (d *midStreamFailDriver) Name() string { return "mid-stream-fail" }
func (d *midStreamFailDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	d.called++
	for _, t := range d.tokens {
		out <- llm.Token{Text: t}
	}
	return fmt.Errorf("mid-stream network error")
}

// midStreamFailNativeDriver emits a text token then returns a retryable error.
type midStreamFailNativeDriver struct {
	called int
}

func (d *midStreamFailNativeDriver) Name() string { return "mid-stream-fail-native" }
func (d *midStreamFailNativeDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	close(out)
	return nil
}
func (d *midStreamFailNativeDriver) StreamWithTools(_ context.Context, _ []llm.Message, _ []llm.ToolDef, out chan<- llm.Token) error {
	defer close(out)
	d.called++
	out <- llm.Token{Text: "partial response"}
	return fmt.Errorf("mid-stream network error")
}

func TestRetryDriverForwardsNativeToolCaller(t *testing.T) {
	inner := &nativeInnerDriver{}
	retry := llm.NewRetryDriver(inner, 1, 0, 0, 0)

	caller, ok := any(retry).(llm.NativeToolCaller)
	if !ok {
		t.Fatal("RetryDriver should implement NativeToolCaller when inner driver does")
	}
	out := make(chan llm.Token, 4)
	err := caller.StreamWithTools(context.Background(), nil, nil, out)
	if err != nil {
		t.Fatal(err)
	}
	var toks []llm.Token
	for tok := range out {
		toks = append(toks, tok)
	}
	if len(toks) != 1 || toks[0].ToolCall == nil {
		t.Fatal("expected one tool call token")
	}
	if inner.callCount != 1 {
		t.Fatalf("inner callCount = %d, want 1", inner.callCount)
	}
}

func TestRetryDriverForwardsNativeToolOptions(t *testing.T) {
	inner := &nativeInnerDriver{}
	retry := llm.NewRetryDriver(inner, 1, 0, 0, 0)

	caller, ok := any(retry).(llm.NativeToolCallerWithOptions)
	if !ok {
		t.Fatal("RetryDriver should implement NativeToolCallerWithOptions when inner driver does")
	}
	out := make(chan llm.Token, 4)
	err := caller.StreamWithToolsOptions(context.Background(), nil, nil, llm.NativeToolOptions{RequireToolCall: true}, out)
	if err != nil {
		t.Fatal(err)
	}
	for range out {
	}
	if len(inner.lastOpts) != 1 || !inner.lastOpts[0].RequireToolCall {
		t.Fatalf("inner.lastOpts = %#v, want RequireToolCall=true", inner.lastOpts)
	}
}

func TestRetryDoesNotRetryAfterMidStreamFailure(t *testing.T) {
	inner := &midStreamFailDriver{tokens: []string{"hello", " world"}}
	rd := llm.NewRetryDriver(inner, 3, time.Millisecond, time.Millisecond, 0)

	out := make(chan llm.Token, 64)
	err := rd.Stream(context.Background(), nil, out)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "mid-stream network error") {
		t.Errorf("unexpected error: %v", err)
	}
	// Must NOT retry after tokens were forwarded — caller state would be corrupted.
	if inner.called != 1 {
		t.Errorf("expected 1 call (no retry after tokens emitted), got %d", inner.called)
	}
	// Partial tokens should have been forwarded live.
	tokens := collect(out)
	if len(tokens) != 2 {
		t.Errorf("expected 2 forwarded tokens, got %d: %v", len(tokens), tokens)
	}
}

func TestRetryStreamWithToolsDoesNotRetryAfterTokensEmitted(t *testing.T) {
	inner := &midStreamFailNativeDriver{}
	rd := llm.NewRetryDriver(inner, 3, time.Millisecond, time.Millisecond, 0)

	caller, ok := any(rd).(llm.NativeToolCaller)
	if !ok {
		t.Fatal("RetryDriver should implement NativeToolCaller")
	}
	out := make(chan llm.Token, 64)
	err := caller.StreamWithTools(context.Background(), nil, nil, out)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "mid-stream network error") {
		t.Errorf("unexpected error: %v", err)
	}
	// Must NOT retry after tokens were forwarded.
	if inner.called != 1 {
		t.Errorf("expected 1 call (no retry after tokens emitted), got %d", inner.called)
	}
	// Partial token should have been forwarded live.
	tokens := collect(out)
	if len(tokens) != 1 || tokens[0].Text != "partial response" {
		t.Errorf("expected partial token to be forwarded, got: %v", tokens)
	}
}
