package drivers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"forge/internal/llm"
)

// Reproduces the opencode/openrouter gateway stream shape that killed the
// pipeline: keep-alive comment events before the first chunk, plus a cost
// trailer event after [DONE].
func TestStreamSurvivesSSECommentHeartbeats(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(": OPENROUTER PROCESSING\n\n"))
		_, _ = w.Write([]byte(": OPENROUTER PROCESSING\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"gen-1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"}}]}\n\n"))
		_, _ = w.Write([]byte(": OPENROUTER PROCESSING\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"gen-1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\" world\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[],\"x-opencode-type\":\"inference-cost\",\"cost\":\"0.0005\"}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[],\"cost\":\"0\"}\n\n"))
	}))
	defer srv.Close()

	d := NewCustomCompatProvider("opencode-go", "sk-test", srv.URL, "opencode-go/mimo-v2.5-pro", "mimo-v2.5-pro", false, nil)
	out := make(chan llm.Token, 16)
	err := d.Stream(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, out)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	var sb strings.Builder
	for tok := range out {
		sb.WriteString(tok.Text)
	}
	if got := sb.String(); got != "hello world" {
		t.Fatalf("streamed text = %q, want %q", got, "hello world")
	}
}

// OpenRouter-style gateways stream reasoning as delta.reasoning (not
// reasoning_content). Those chunks must surface as tokens — they are what
// keeps the stream-idle watchdog alive during a long thinking phase.
func TestStreamEmitsOpenRouterStyleReasoningTokens(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"gen-1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\",\"reasoning\":\"thinking hard\",\"reasoning_details\":[{\"type\":\"reasoning.text\",\"text\":\"thinking hard\"}]}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"gen-1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"answer\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	d := NewCustomCompatProvider("opencode-go", "sk-test", srv.URL, "opencode-go/mimo-v2.5-pro", "mimo-v2.5-pro", false, nil)
	out := make(chan llm.Token, 16)
	if err := d.Stream(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, out); err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	var reasoning, text strings.Builder
	for tok := range out {
		reasoning.WriteString(tok.ReasoningContent)
		text.WriteString(tok.Text)
	}
	if got := reasoning.String(); got != "thinking hard" {
		t.Fatalf("reasoning = %q, want %q", got, "thinking hard")
	}
	if got := text.String(); got != "answer" {
		t.Fatalf("text = %q, want %q", got, "answer")
	}
}
