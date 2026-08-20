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

// A cancelled turn must not leave its reader goroutine blocked in ReadJSON on
// a pooled connection: gorilla/websocket allows one concurrent reader, so the
// next request would start a second one and the two corrupt the connection's
// shared bufio.Reader, panicking with "slice bounds out of range".
func TestWSReadEventsDropsConnectionOnCancel(t *testing.T) {
	upgrader := websocket.Upgrader{}
	served := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		served <- c
		// Deliberately silent: this models a turn cancelled while waiting on
		// the model, which is when the leak used to happen.
		select {}
	}))
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	<-served

	d := &OpenAIDriver{wsConn: conn, wsLastRequestID: "resp_previous"}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	done := make(chan error, 1)
	go func() {
		done <- d.wsReadEvents(ctx, conn, make(chan llm.Token, 1), false)
	}()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "context canceled") {
			t.Fatalf("wsReadEvents error = %v, want context canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wsReadEvents did not return after cancellation (reader goroutine still blocked)")
	}

	// The pooled connection must be gone, so the next request dials a fresh
	// one instead of racing a reader that is still running on this one.
	d.wsMu.Lock()
	pooled := d.wsConn
	lastID := d.wsLastRequestID
	d.wsMu.Unlock()
	if pooled != nil {
		t.Error("connection still pooled after a cancelled turn")
	}
	if lastID != "" {
		t.Errorf("wsLastRequestID = %q, want cleared; the response was never received", lastID)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte("x")); err == nil {
		t.Error("connection still open after a cancelled turn")
	}
}
