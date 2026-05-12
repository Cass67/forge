package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"forge/internal/config"
	"forge/internal/llm"
	reactruntime "forge/internal/react"
)

type debugMockDriver struct {
	response     string
	lastMessages []llm.Message
	params       llm.Params
	usage        llm.Usage
	requestMode  string
	reset        bool
}

func (d *debugMockDriver) Name() string { return "debug-mock" }

func (d *debugMockDriver) Stream(ctx context.Context, messages []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	d.lastMessages = append([]llm.Message(nil), messages...)
	out <- llm.Token{Text: d.response}
	return nil
}

func (d *debugMockDriver) SetParams(p llm.Params) { d.params = p }

func (d *debugMockDriver) LastUsage() llm.Usage { return d.usage }

func (d *debugMockDriver) LastRequestMode() string { return d.requestMode }

func (d *debugMockDriver) ResetConversation() { d.reset = true }

type debugNativeToolDriver struct {
	lastMessages []llm.Message
	lastTools    []llm.ToolDef
	lastOpts     []llm.NativeToolOptions
}

type debugRetryDriver struct {
	calls    int
	firstErr error
}

type debugRetryNativeDriver struct {
	calls int
}

func (d *debugRetryDriver) Name() string { return "debug-retry" }

func (d *debugRetryDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	d.calls++
	if d.calls == 1 {
		if d.firstErr != nil {
			return d.firstErr
		}
		return errors.New("500 temporary provider failure")
	}
	out <- llm.Token{Text: "ok"}
	return nil
}

func (d *debugRetryNativeDriver) Name() string { return "debug-retry-native" }

func (d *debugRetryNativeDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	return nil
}

func (d *debugRetryNativeDriver) StreamWithTools(ctx context.Context, messages []llm.Message, tools []llm.ToolDef, out chan<- llm.Token) error {
	return d.StreamWithToolsOptions(ctx, messages, tools, llm.NativeToolOptions{}, out)
}

func (d *debugRetryNativeDriver) StreamWithToolsOptions(_ context.Context, _ []llm.Message, _ []llm.ToolDef, _ llm.NativeToolOptions, out chan<- llm.Token) error {
	defer close(out)
	d.calls++
	if d.calls == 1 {
		return errors.New("502 temporary tool stream failure")
	}
	out <- llm.Token{ToolCall: &llm.NativeToolCall{ID: "call_retry", Name: "agent_status", ArgsJSON: "{}"}}
	return nil
}

func (d *debugNativeToolDriver) Name() string { return "debug-native-tool" }

func (d *debugNativeToolDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	close(out)
	return nil
}

func (d *debugNativeToolDriver) StreamWithTools(_ context.Context, messages []llm.Message, tools []llm.ToolDef, out chan<- llm.Token) error {
	return d.StreamWithToolsOptions(context.Background(), messages, tools, llm.NativeToolOptions{}, out)
}

func (d *debugNativeToolDriver) StreamWithToolsOptions(_ context.Context, messages []llm.Message, tools []llm.ToolDef, opts llm.NativeToolOptions, out chan<- llm.Token) error {
	defer close(out)
	d.lastMessages = append([]llm.Message(nil), messages...)
	d.lastTools = append([]llm.ToolDef(nil), tools...)
	d.lastOpts = append(d.lastOpts, opts)
	out <- llm.Token{ToolCall: &llm.NativeToolCall{
		ID:       "call_1",
		Name:     "run_command",
		ArgsJSON: `{"command":"git status --short"}`,
	}}
	return nil
}

func TestEnableChatDebugWrapsDriverAndLogsRequestResponse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat-debug.jsonl")
	inner := &debugMockDriver{
		response:    "hello world",
		usage:       llm.Usage{InputTokens: 11, OutputTokens: 7},
		requestMode: "responses full input",
	}
	setup := &ChatSetup{
		ChatModel: "openai/gpt-5",
		WorkDir:   t.TempDir(),
		Driver:    inner,
	}

	gotPath, err := EnableChatDebug(setup, path)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != path {
		t.Fatalf("path = %q, want %q", gotPath, path)
	}

	msgs := []llm.Message{{Role: llm.RoleUser, Content: "hi"}}
	out := make(chan llm.Token, 4)
	errCh := make(chan error, 1)
	go func() { errCh <- setup.Driver.Stream(context.Background(), msgs, out) }()
	var sb strings.Builder
	for tok := range out {
		sb.WriteString(tok.Text)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	if sb.String() != "hello world" {
		t.Fatalf("response = %q", sb.String())
	}
	if len(inner.lastMessages) != 1 || inner.lastMessages[0].Content != "hi" {
		t.Fatalf("inner.lastMessages = %#v", inner.lastMessages)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"chat.debug.enabled", "llm.request", "llm.response", "message_count", "response_chars"} {
		if !strings.Contains(text, want) {
			t.Fatalf("debug log missing %q: %s", want, text)
		}
	}
	for _, forbidden := range []string{`"Content":"hi"`, "hello world"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("debug log should not contain raw prompt/response content %q: %s", forbidden, text)
		}
	}
}

func TestEnableChatDebugDoesNotLogSecretContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat-debug.jsonl")
	secret := "TOKEN=" + strings.Repeat("x", 24)
	setup := &ChatSetup{
		ChatModel: "openai/gpt-5",
		WorkDir:   t.TempDir(),
		Driver:    &debugMockDriver{response: "response " + secret},
	}
	if _, err := EnableChatDebug(setup, path); err != nil {
		t.Fatal(err)
	}

	msgs := []llm.Message{{Role: llm.RoleUser, Content: "prompt " + secret}}
	out := make(chan llm.Token, 4)
	errCh := make(chan error, 1)
	go func() { errCh <- setup.Driver.Stream(context.Background(), msgs, out) }()
	for range out {
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("debug log leaked secret content: %s", data)
	}
}

func TestEnableChatDebugLogsNativeToolCalls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat-debug.jsonl")
	inner := &debugNativeToolDriver{}
	setup := &ChatSetup{
		ChatModel: "openai/gpt-5",
		WorkDir:   t.TempDir(),
		Driver:    inner,
	}

	if _, err := EnableChatDebug(setup, path); err != nil {
		t.Fatal(err)
	}

	msgs := []llm.Message{{Role: llm.RoleUser, Content: "check repo status"}}
	tools := []llm.ToolDef{{Name: "run_command", Description: "run a shell command"}}
	out := make(chan llm.Token, 4)
	errCh := make(chan error, 1)
	go func() {
		native := setup.Driver.(llm.NativeToolCaller)
		errCh <- native.StreamWithTools(context.Background(), msgs, tools, out)
	}()
	for range out {
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}

	lines := readDebugLines(t, path)
	var requestEntry map[string]any
	for _, line := range lines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		if entry["msg"] == "llm.request" {
			requestEntry = entry
			break
		}
	}
	if requestEntry == nil {
		t.Fatalf("expected llm.request entry, got %v", lines)
	}
	fields, _ := requestEntry["fields"].(map[string]any)
	if got := fields["tool_count"]; got != float64(1) {
		t.Fatalf("tool_count = %#v, want 1", got)
	}
	if got := fields["tool_choice_required"]; got != false {
		t.Fatalf("tool_choice_required = %#v, want false", got)
	}
	var responseEntry map[string]any
	for i := len(lines) - 1; i >= 0; i-- {
		var entry map[string]any
		if err := json.Unmarshal([]byte(lines[i]), &entry); err != nil {
			t.Fatal(err)
		}
		if entry["msg"] == "llm.response" {
			responseEntry = entry
			break
		}
	}
	if responseEntry == nil {
		t.Fatalf("expected llm.response entry, got %v", lines)
	}
	responseFields, _ := responseEntry["fields"].(map[string]any)
	toolCalls, _ := responseFields["tool_calls"].([]any)
	if len(toolCalls) != 1 {
		t.Fatalf("tool_calls = %#v, want one native tool call", responseFields["tool_calls"])
	}
	first, _ := toolCalls[0].(map[string]any)
	if got := first["name"]; got != "run_command" {
		t.Fatalf("tool call name = %#v, want run_command", got)
	}
	if got := first["args_chars"]; got != "32" {
		t.Fatalf("tool call args_chars = %#v, want 32", got)
	}
}

func TestEnableChatDebugLogsRequiredToolChoiceMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat-debug.jsonl")
	inner := &debugNativeToolDriver{}
	setup := &ChatSetup{
		ChatModel: "openai/gpt-5",
		WorkDir:   t.TempDir(),
		Driver:    inner,
	}

	if _, err := EnableChatDebug(setup, path); err != nil {
		t.Fatal(err)
	}

	msgs := []llm.Message{{Role: llm.RoleUser, Content: "inspect repo"}}
	tools := []llm.ToolDef{{Name: "list_dir", Description: "list a directory"}}
	out := make(chan llm.Token, 4)
	errCh := make(chan error, 1)
	go func() {
		native := setup.Driver.(llm.NativeToolCallerWithOptions)
		errCh <- native.StreamWithToolsOptions(context.Background(), msgs, tools, llm.NativeToolOptions{RequireToolCall: true}, out)
	}()
	for range out {
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}

	lines := readDebugLines(t, path)
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &entry); err != nil {
		t.Fatal(err)
	}
	fields, _ := entry["fields"].(map[string]any)
	if got := fields["tool_choice_required"]; got != true {
		t.Fatalf("tool_choice_required = %#v, want true", got)
	}
}

func TestEnableChatDebugForwardsRequiredToolChoiceThroughRetryDriver(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat-debug.jsonl")
	inner := &debugNativeToolDriver{}
	setup := &ChatSetup{
		ChatModel: "openai/gpt-5",
		WorkDir:   t.TempDir(),
		Driver:    llm.NewRetryDriver(inner, 1, 0, 0, 0),
	}

	if _, err := EnableChatDebug(setup, path); err != nil {
		t.Fatal(err)
	}

	msgs := []llm.Message{{Role: llm.RoleUser, Content: "inspect repo"}}
	tools := []llm.ToolDef{{Name: "list_dir", Description: "list a directory"}}
	out := make(chan llm.Token, 4)
	errCh := make(chan error, 1)
	go func() {
		native := setup.Driver.(llm.NativeToolCallerWithOptions)
		errCh <- native.StreamWithToolsOptions(context.Background(), msgs, tools, llm.NativeToolOptions{RequireToolCall: true}, out)
	}()
	for range out {
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	if len(inner.lastOpts) != 1 || !inner.lastOpts[0].RequireToolCall {
		t.Fatalf("inner.lastOpts = %#v, want RequireToolCall=true", inner.lastOpts)
	}
}

func TestEnableChatDebugLogsRetryAttemptsAndWaits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat-debug.jsonl")
	inner := &debugRetryDriver{}
	setup := &ChatSetup{
		ChatModel: "openai/gpt-5",
		WorkDir:   t.TempDir(),
		Driver:    llm.NewRetryDriver(inner, 2, 0, 0, 0),
	}

	if _, err := EnableChatDebug(setup, path); err != nil {
		t.Fatal(err)
	}

	out := make(chan llm.Token, 4)
	errCh := make(chan error, 1)
	go func() {
		errCh <- setup.Driver.Stream(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, out)
	}()
	for range out {
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	if inner.calls != 2 {
		t.Fatalf("inner calls = %d, want 2", inner.calls)
	}

	attempts := debugEntriesWithMessage(t, path, "llm.retry_attempt")
	if len(attempts) != 2 {
		t.Fatalf("retry attempts = %d, want 2", len(attempts))
	}
	secondFields, _ := attempts[1]["fields"].(map[string]any)
	if got := secondFields["driver"]; got != "debug-retry" {
		t.Fatalf("retry attempt driver = %#v, want debug-retry", got)
	}
	if got := secondFields["operation"]; got != "stream" {
		t.Fatalf("retry attempt operation = %#v, want stream", got)
	}
	if got := secondFields["attempt"]; got != float64(2) {
		t.Fatalf("retry attempt = %#v, want 2", got)
	}

	waits := debugEntriesWithMessage(t, path, "llm.retry_wait")
	if len(waits) != 1 {
		t.Fatalf("retry waits = %d, want 1", len(waits))
	}
	waitFields, _ := waits[0]["fields"].(map[string]any)
	if got := waitFields["next_attempt"]; got != float64(2) {
		t.Fatalf("retry wait next_attempt = %#v, want 2", got)
	}
	if got := waitFields["previous_error_class"]; got != "server" {
		t.Fatalf("retry wait previous_error_class = %#v, want server", got)
	}
	if got := waitFields["previous_error_chars"]; got != float64(len("500 temporary provider failure")) {
		t.Fatalf("retry wait previous_error_chars = %#v", got)
	}
}

func TestEnableChatDebugLogsNativeToolRetryAttemptsAndWaits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat-debug.jsonl")
	inner := &debugRetryNativeDriver{}
	setup := &ChatSetup{
		ChatModel: "openai/gpt-5",
		WorkDir:   t.TempDir(),
		Driver:    llm.NewRetryDriver(inner, 2, 0, 0, 0),
	}

	if _, err := EnableChatDebug(setup, path); err != nil {
		t.Fatal(err)
	}

	out := make(chan llm.Token, 4)
	errCh := make(chan error, 1)
	go func() {
		native := setup.Driver.(llm.NativeToolCallerWithOptions)
		errCh <- native.StreamWithToolsOptions(
			context.Background(),
			[]llm.Message{{Role: llm.RoleUser, Content: "check agents"}},
			[]llm.ToolDef{{Name: "agent_status", Description: "show agents"}},
			llm.NativeToolOptions{RequireToolCall: true},
			out,
		)
	}()
	for range out {
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	if inner.calls != 2 {
		t.Fatalf("inner calls = %d, want 2", inner.calls)
	}

	attempts := debugEntriesWithMessage(t, path, "llm.retry_attempt")
	if len(attempts) != 2 {
		t.Fatalf("retry attempts = %d, want 2", len(attempts))
	}
	secondFields, _ := attempts[1]["fields"].(map[string]any)
	if got := secondFields["driver"]; got != "debug-retry-native" {
		t.Fatalf("retry attempt driver = %#v, want debug-retry-native", got)
	}
	if got := secondFields["operation"]; got != "stream_with_tools" {
		t.Fatalf("retry attempt operation = %#v, want stream_with_tools", got)
	}
	if got := secondFields["attempt"]; got != float64(2) {
		t.Fatalf("retry attempt = %#v, want 2", got)
	}

	waits := debugEntriesWithMessage(t, path, "llm.retry_wait")
	if len(waits) != 1 {
		t.Fatalf("retry waits = %d, want 1", len(waits))
	}
	waitFields, _ := waits[0]["fields"].(map[string]any)
	if got := waitFields["next_attempt"]; got != float64(2) {
		t.Fatalf("retry wait next_attempt = %#v, want 2", got)
	}
	if got := waitFields["previous_error_class"]; got != "server" {
		t.Fatalf("retry wait previous_error_class = %#v, want server", got)
	}
	if got := waitFields["previous_error_chars"]; got != float64(len("502 temporary tool stream failure")) {
		t.Fatalf("retry wait previous_error_chars = %#v", got)
	}
}

func TestEnableChatDebugRedactsRetryPreviousError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat-debug.jsonl")
	dummyToken := "TOKEN=" + strings.Repeat("x", 24)
	inner := &debugRetryDriver{firstErr: errors.New("500 temporary provider failure " + dummyToken)}
	setup := &ChatSetup{
		ChatModel: "openai/gpt-5",
		WorkDir:   t.TempDir(),
		Driver:    llm.NewRetryDriver(inner, 2, 0, 0, 0),
	}

	if _, err := EnableChatDebug(setup, path); err != nil {
		t.Fatal(err)
	}

	out := make(chan llm.Token, 4)
	errCh := make(chan error, 1)
	go func() {
		errCh <- setup.Driver.Stream(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, out)
	}()
	for range out {
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, dummyToken) {
		t.Fatalf("debug log leaked retry error token: %s", text)
	}
	waits := debugEntriesWithMessage(t, path, "llm.retry_wait")
	if len(waits) != 1 {
		t.Fatalf("retry waits = %d, want 1", len(waits))
	}
	waitFields, _ := waits[0]["fields"].(map[string]any)
	if _, ok := waitFields["previous_error"]; ok {
		t.Fatalf("retry wait should not include raw previous_error: %#v", waitFields)
	}
	if got := waitFields["previous_error_secret_rules"]; got != "generic-token" {
		t.Fatalf("retry wait previous_error_secret_rules = %#v, want generic-token", got)
	}
}

func TestEnableChatDebugLogsTaskStateMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat-debug.jsonl")
	inner := &debugNativeToolDriver{}
	setup := &ChatSetup{
		ChatModel: "openai/gpt-5",
		WorkDir:   t.TempDir(),
		Driver:    inner,
	}

	if _, err := EnableChatDebug(setup, path); err != nil {
		t.Fatal(err)
	}

	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleSystem, Content: "Task objective: merge feature/go-rewrite into main\nTask operation: merge\nTask source ref: feature/go-rewrite\nTask target branch: main\nRequired verification: verify branch main contains the resulting HEAD commit"},
		{Role: llm.RoleUser, Content: "do it"},
	}
	tools := []llm.ToolDef{{Name: "run_command", Description: "run a shell command"}}
	out := make(chan llm.Token, 4)
	errCh := make(chan error, 1)
	go func() {
		native := setup.Driver.(llm.NativeToolCaller)
		errCh <- native.StreamWithTools(context.Background(), msgs, tools, out)
	}()
	for range out {
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}

	lines := readDebugLines(t, path)
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &entry); err != nil {
		t.Fatal(err)
	}
	fields, _ := entry["fields"].(map[string]any)
	taskState, _ := fields["task_state"].(map[string]any)
	if taskState == nil {
		t.Fatalf("expected task_state in debug request fields: %#v", fields)
	}
	if got := taskState["target_branch"]; got != "main" {
		t.Fatalf("target_branch = %#v", got)
	}
}

func TestEnableChatDebugRedactsTaskStateMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat-debug.jsonl")
	inner := &debugNativeToolDriver{}
	setup := &ChatSetup{ChatModel: "openai/gpt-5", WorkDir: t.TempDir(), Driver: inner}
	if _, err := EnableChatDebug(setup, path); err != nil {
		t.Fatal(err)
	}
	secret := "TOKEN=" + strings.Repeat("x", 24)

	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "Task objective: audit " + secret + "\nRequired verification: run check with " + secret},
		{Role: llm.RoleUser, Content: "do it"},
	}
	out := make(chan llm.Token, 4)
	errCh := make(chan error, 1)
	go func() {
		native := setup.Driver.(llm.NativeToolCaller)
		errCh <- native.StreamWithTools(context.Background(), msgs, nil, out)
	}()
	for range out {
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, secret) {
		t.Fatalf("debug task_state leaked secret: %s", text)
	}
	if !strings.Contains(text, "REDACTED:generic-token") {
		t.Fatalf("debug task_state missing redaction marker: %s", text)
	}
}

func TestEnableChatDebugWrapsMakeDriverAndDelegatesInterfaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat-debug.jsonl")
	setup := &ChatSetup{
		WorkDir: t.TempDir(),
		MakeDriver: func(model string) llm.Driver {
			return &debugMockDriver{
				response:    model,
				usage:       llm.Usage{InputTokens: 3, OutputTokens: 2},
				requestMode: "responses",
			}
		},
	}
	if _, err := EnableChatDebug(setup, path); err != nil {
		t.Fatal(err)
	}
	got := setup.MakeDriver("scout-model")
	if got == nil {
		t.Fatal("expected wrapped driver")
	}
	if c, ok := got.(llm.Configurable); ok {
		c.SetParams(llm.Params{MaxTokens: 99})
	} else {
		t.Fatal("wrapped driver should implement Configurable")
	}
	if reporter, ok := got.(llm.UsageReporter); !ok || reporter.LastUsage().InputTokens != 3 {
		t.Fatalf("usage reporter = %#v", got)
	}
	if reporter, ok := got.(llm.RequestModeReporter); !ok || reporter.LastRequestMode() != "responses" {
		t.Fatalf("request mode reporter = %#v", got)
	}
	if resetter, ok := got.(llm.ConversationResetter); ok {
		resetter.ResetConversation()
	} else {
		t.Fatal("wrapped driver should implement ConversationResetter")
	}
}

func TestChatDebugRecorderLogsEventsAndInputs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat-debug.jsonl")
	setup := &ChatSetup{WorkDir: t.TempDir()}
	if _, err := EnableChatDebug(setup, path); err != nil {
		t.Fatal(err)
	}
	setup.debugRec.logInput("user", "inspect repo")
	setup.debugRec.logEvent(llm.Event{Kind: llm.EventToolCall, Agent: "delegate", Text: "→ scout"})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 log lines, got %d", len(lines))
	}
	var last map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatal(err)
	}
	if last["msg"] != "chat.event" {
		t.Fatalf("last msg = %#v", last)
	}
	fields, _ := last["fields"].(map[string]any)
	if _, ok := fields["text"]; ok {
		t.Fatalf("chat event should not expose raw text fields: %#v", fields)
	}
}

func TestEnableChatDebugTruncatesExistingLogFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat-debug.jsonl")
	if err := os.WriteFile(path, []byte("old line that should be removed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	setup := &ChatSetup{
		ChatModel: "openai/gpt-5",
		WorkDir:   t.TempDir(),
		Driver:    &debugMockDriver{response: "ok"},
	}
	if _, err := EnableChatDebug(setup, path); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "old line that should be removed") {
		t.Fatalf("expected existing debug file contents to be truncated, got %q", text)
	}
	if !strings.Contains(text, "chat.debug.enabled") {
		t.Fatalf("debug log missing enable event after truncation: %q", text)
	}
}

func TestEnableChatDebugDefaultsOutsideWorkDir(t *testing.T) {
	workDir := t.TempDir()
	setup := &ChatSetup{
		ChatModel: "openai/gpt-5",
		WorkDir:   workDir,
		Driver:    &debugMockDriver{response: "ok"},
	}

	gotPath, err := EnableChatDebug(setup, "")
	if err != nil {
		t.Fatal(err)
	}

	cleanPath := filepath.Clean(gotPath)
	cleanWorkDir := filepath.Clean(workDir)
	if strings.HasPrefix(cleanPath, cleanWorkDir+string(os.PathSeparator)) {
		t.Fatalf("default debug path should not live under work dir: path=%q workdir=%q", cleanPath, cleanWorkDir)
	}

	cleanTempDir := filepath.Clean(os.TempDir())
	if !strings.HasPrefix(cleanPath, cleanTempDir+string(os.PathSeparator)) && cleanPath != cleanTempDir {
		t.Fatalf("default debug path should live under temp dir: path=%q tempdir=%q", cleanPath, cleanTempDir)
	}

	if _, err := os.Stat(gotPath); err != nil {
		t.Fatalf("expected debug log to be created at %q: %v", gotPath, err)
	}
}

func TestEnableChatDebugLogsActiveRuntimeMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat-debug.jsonl")
	setup := &ChatSetup{
		ChatModel: "openai/gpt-5",
		WorkDir:   t.TempDir(),
		Driver:    &debugMockDriver{response: "ok"},
		Config:    &config.Config{},
	}

	t.Setenv("FORGE_CHAT_RUNTIME", "")
	if _, err := EnableChatDebug(setup, path); err != nil {
		t.Fatal(err)
	}

	lines := readDebugLines(t, path)
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatal(err)
	}
	fields, _ := entry["fields"].(map[string]any)
	if got := fields["runtime_mode"]; got != "react" {
		t.Fatalf("runtime_mode = %#v, want react", got)
	}
	if got := fields["agents_enabled"]; got != false {
		t.Fatalf("agents_enabled = %#v, want false because visible agents are disabled in react mode", got)
	}
}

func TestEnableChatDebugLogsReactRuntimeMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat-debug.jsonl")
	setup := &ChatSetup{
		ChatModel: "openai/gpt-5",
		WorkDir:   t.TempDir(),
		Driver:    &debugMockDriver{response: "ok"},
		Config:    &config.Config{},
	}

	t.Setenv("FORGE_CHAT_RUNTIME", "react")
	if _, err := EnableChatDebug(setup, path); err != nil {
		t.Fatal(err)
	}

	lines := readDebugLines(t, path)
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatal(err)
	}
	fields, _ := entry["fields"].(map[string]any)
	if got := fields["runtime_mode"]; got != "react" {
		t.Fatalf("runtime_mode = %#v, want react", got)
	}
	if got := fields["agents_enabled"]; got != false {
		t.Fatalf("agents_enabled = %#v, want false because visible agents are disabled in react mode during phase A", got)
	}
}

func TestEnableChatDebugLogsDebugSurfaceMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat-debug.jsonl")
	setup := &ChatSetup{
		ChatModel: "openai/gpt-5",
		WorkDir:   t.TempDir(),
		Driver:    &debugMockDriver{response: "ok"},
	}

	if _, err := EnableChatDebug(setup, path); err != nil {
		t.Fatal(err)
	}

	lines := readDebugLines(t, path)
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatal(err)
	}
	fields, _ := entry["fields"].(map[string]any)
	if got := fields["surface_mode"]; got != "debug" {
		t.Fatalf("surface_mode = %#v, want debug", got)
	}
}

func TestChatDebugRecorderLogsAgentLifecycleWithoutRawPayloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat-debug.jsonl")
	setup := &ChatSetup{WorkDir: t.TempDir()}
	if _, err := EnableChatDebug(setup, path); err != nil {
		t.Fatal(err)
	}
	secret := "TOKEN=" + strings.Repeat("x", 24)

	setup.debugRec.logAgentTask(reactruntime.AgentTaskState{
		ID:           "agent-1",
		Role:         "repo-auditor",
		Description:  "audit " + secret,
		Prompt:       "inspect repo with " + secret,
		Status:       reactruntime.AgentStatusRunning,
		CreatedAt:    time.Unix(100, 0),
		StartedAt:    time.Unix(101, 0),
		ParentTurn:   7,
		Result:       "findings mention " + secret,
		Error:        "failed with " + secret,
		LastToolName: "read_file",
		RecentActivity: []reactruntime.AgentTaskActivity{{
			ToolName: "read_file",
			Summary:  "opened " + secret,
			At:       time.Unix(102, 0),
		}},
	})

	entry := lastDebugEntryWithMessage(t, path, "chat.agent_lifecycle")
	fields, _ := entry["fields"].(map[string]any)
	if got := fields["id"]; got != "agent-1" {
		t.Fatalf("id = %#v, want agent-1", got)
	}
	if got := fields["role"]; got != "repo-auditor" {
		t.Fatalf("role = %#v, want repo-auditor", got)
	}
	if got := fields["status"]; got != "running" {
		t.Fatalf("status = %#v, want running", got)
	}
	if got := fields["parent_turn"]; got != float64(7) {
		t.Fatalf("parent_turn = %#v, want 7", got)
	}
	if got := fields["description_secret_rules"]; got != "generic-token" {
		t.Fatalf("description_secret_rules = %#v, want generic-token", got)
	}
	if got := fields["recent_activity_count"]; got != float64(1) {
		t.Fatalf("recent_activity_count = %#v, want 1", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, secret) || strings.Contains(text, "inspect repo with") || strings.Contains(text, "findings mention") || strings.Contains(text, "opened ") {
		t.Fatalf("agent lifecycle debug log exposed raw payload: %s", text)
	}
}

func TestRedactAgentTaskStateForEventPayload(t *testing.T) {
	secret := "TOKEN=" + strings.Repeat("x", 24)
	state := reactruntime.AgentTaskState{
		ID:           "agent-1",
		Role:         "repo-auditor",
		Description:  "audit " + secret,
		Prompt:       "inspect repo with " + secret,
		Status:       reactruntime.AgentStatusFailed,
		Result:       "findings mention " + secret,
		Error:        "failed with " + secret,
		LastToolName: "run_command",
		RecentActivity: []reactruntime.AgentTaskActivity{{
			ToolName: "run_command",
			Summary:  "printed " + secret,
		}},
	}

	data, err := json.Marshal(redactAgentTaskState(state))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, secret) {
		t.Fatalf("agent task event payload leaked secret: %s", text)
	}
	if !strings.Contains(text, "REDACTED:generic-token") {
		t.Fatalf("agent task event payload missing redaction marker: %s", text)
	}
}

func TestChatDebugRecorderLogsToolExposureDecisionWithoutRawInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat-debug.jsonl")
	setup := &ChatSetup{WorkDir: t.TempDir()}
	if _, err := EnableChatDebug(setup, path); err != nil {
		t.Fatal(err)
	}

	setup.debugRec.logToolExposure(reactruntime.ToolExposureDecision{
		Reason:                "active_agents",
		ToolNames:             []string{"wait_agent", "agent_status", "kill_agent"},
		RequireToolCall:       true,
		OutstandingAgentCount: 1,
		PendingActionKind:     "write_doc",
	})

	entry := lastDebugEntryWithMessage(t, path, "chat.tool_exposure")
	fields, _ := entry["fields"].(map[string]any)
	if got := fields["reason"]; got != "active_agents" {
		t.Fatalf("reason = %#v, want active_agents", got)
	}
	if got := fields["tool_count"]; got != float64(3) {
		t.Fatalf("tool_count = %#v, want 3", got)
	}
	if got := fields["tool_choice_required"]; got != true {
		t.Fatalf("tool_choice_required = %#v, want true", got)
	}
	if got := fields["outstanding_agent_count"]; got != float64(1) {
		t.Fatalf("outstanding_agent_count = %#v, want 1", got)
	}
	if got := fields["pending_action_kind"]; got != "write_doc" {
		t.Fatalf("pending_action_kind = %#v, want write_doc", got)
	}
	if _, ok := fields["last_input"]; ok {
		t.Fatalf("tool exposure debug log should not include raw input: %#v", fields)
	}
}

func readDebugLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func lastDebugEntryWithMessage(t *testing.T, path, msg string) map[string]any {
	t.Helper()
	lines := readDebugLines(t, path)
	for i := len(lines) - 1; i >= 0; i-- {
		var entry map[string]any
		if err := json.Unmarshal([]byte(lines[i]), &entry); err != nil {
			t.Fatal(err)
		}
		if entry["msg"] == msg {
			return entry
		}
	}
	t.Fatalf("expected debug entry %q, got %v", msg, lines)
	return nil
}

func debugEntriesWithMessage(t *testing.T, path, msg string) []map[string]any {
	t.Helper()
	lines := readDebugLines(t, path)
	entries := make([]map[string]any, 0)
	for _, line := range lines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		if entry["msg"] == msg {
			entries = append(entries, entry)
		}
	}
	return entries
}
