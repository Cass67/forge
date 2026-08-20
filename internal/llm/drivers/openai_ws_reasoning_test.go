package drivers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"forge/internal/llm"
)

// Reasoning summaries arrive as separate parts, each with its own bold header.
// Without a break emitted on the part boundary they concatenate into one run
// ("...imports****Configuring...").
func TestWSReadEventsSeparatesReasoningSummaryParts(t *testing.T) {
	events := []string{
		`{"type":"response.reasoning_summary_part.added"}`,
		`{"type":"response.reasoning_summary_text.delta","delta":"**First part**"}`,
		`{"type":"response.reasoning_summary_part.done"}`,
		`{"type":"response.reasoning_summary_part.added"}`,
		`{"type":"response.reasoning_summary_text.delta","delta":"**Second part**"}`,
		`{"type":"response.completed","response":{"id":"resp_1","output":[]}}`,
	}

	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		for _, ev := range events {
			if err := c.WriteMessage(websocket.TextMessage, []byte(ev)); err != nil {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	d := &OpenAIDriver{wsConn: conn}
	out := make(chan llm.Token, 16)
	if err := d.wsReadEvents(context.Background(), conn, out, false); err != nil {
		t.Fatalf("wsReadEvents: %v", err)
	}
	close(out)

	var got strings.Builder
	for tok := range out {
		got.WriteString(tok.ReasoningContent)
	}
	if want := "**First part**\n\n**Second part**"; got.String() != want {
		t.Fatalf("reasoning = %q, want %q", got.String(), want)
	}
}
