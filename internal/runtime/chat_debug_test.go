package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forge/internal/llm"
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
	for _, want := range []string{"chat.debug.enabled", "llm.request", "llm.response", "hello world"} {
		if !strings.Contains(text, want) {
			t.Fatalf("debug log missing %q: %s", want, text)
		}
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
}
