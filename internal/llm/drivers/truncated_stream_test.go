package drivers

import (
	"context"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"forge/internal/llm"
)

// A gateway that drops the connection mid-response — no finish_reason, no
// [DONE], last line cut in half — must be reported as a truncated stream, not
// silently treated as the model answering with nothing.
func TestChatStreamReportsTruncationWhenTerminatorMissing(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"reasoning_content\":\"thinking\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"read_fil"))
	}))
	defer srv.Close()

	d := NewCustomCompatProvider("opencode-go", "sk-test", srv.URL, "opencode-go/ox-alpha-free", "ox-alpha-free", false, nil)
	out := make(chan llm.Token, 16)
	err := d.Stream(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, out)
	if !stderrors.Is(err, llm.ErrTruncatedStream) {
		t.Fatalf("Stream() error = %v, want ErrTruncatedStream", err)
	}
	for range out {
	}
}

// The same guard must not fire on gateways that never send finish_reason but
// do terminate the stream properly.
func TestChatStreamAcceptsDoneTerminatorWithoutFinishReason(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	d := NewCustomCompatProvider("opencode-go", "sk-test", srv.URL, "opencode-go/ox-alpha-free", "ox-alpha-free", false, nil)
	out := make(chan llm.Token, 16)
	if err := d.Stream(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, out); err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}
	var text string
	for tok := range out {
		text += tok.Text
	}
	if text != "hello" {
		t.Fatalf("text = %q, want %q", text, "hello")
	}
}

// A truncated stream carrying a half-written tool call must emit nothing: the
// accumulated name is garbage and executing it would be worse than retrying.
func TestChatToolsStreamDropsPartialToolCallOnTruncation(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"read_fil\",\"arguments\":\"\"}}]}}]}\n\n"))
	}))
	defer srv.Close()

	d := NewCustomCompatProvider("opencode-go", "sk-test", srv.URL, "opencode-go/ox-alpha-free", "ox-alpha-free", false, nil)
	out := make(chan llm.Token, 16)
	err := d.StreamWithTools(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		[]llm.ToolDef{{Name: "read_file", Description: "read"}}, out)
	if !stderrors.Is(err, llm.ErrTruncatedStream) {
		t.Fatalf("StreamWithTools() error = %v, want ErrTruncatedStream", err)
	}
	for tok := range out {
		if tok.ToolCall != nil {
			t.Fatalf("emitted partial tool call %+v", tok.ToolCall)
		}
	}
}

// A gateway that gives up mid-request reports its own finish_reason with no
// content. Calling that an empty model answer blamed the model for a provider
// failure and burned the runtime's re-prompt budget; the reason must surface.
func TestChatStreamReportsProviderFinishReasonOnEmptyResponse(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"},\"finish_reason\":\"network_error\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	d := NewCustomCompatProvider("opencode-go", "sk-test", srv.URL, "opencode-go/x", "x", false, nil)
	out := make(chan llm.Token, 8)
	err := d.Stream(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, out)
	for range out {
	}
	if err == nil {
		t.Fatal("Stream() error = nil, want the provider failure reported")
	}
	if !strings.Contains(err.Error(), "network_error") {
		t.Fatalf("Stream() error = %v, want it to name finish_reason network_error", err)
	}
}

// A model that legitimately answers with nothing and stops normally is not a
// provider failure: that stays the runtime's business to re-prompt.
func TestChatStreamAllowsEmptyAnswerOnNormalStop(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	d := NewCustomCompatProvider("opencode-go", "sk-test", srv.URL, "opencode-go/x", "x", false, nil)
	out := make(chan llm.Token, 8)
	err := d.Stream(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, out)
	for range out {
	}
	// This provider retries an empty stream without streaming, which the test
	// server does not serve — so an error here is expected. What matters is
	// that a normal stop is never reported as a provider finish failure.
	if err != nil && strings.Contains(err.Error(), "provider returned no content") {
		t.Fatalf("normal stop reported as provider failure: %v", err)
	}
}
