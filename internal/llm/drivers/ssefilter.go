package drivers

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/openai/openai-go/option"
)

// streamTerminator records whether a streamed response ended with the SSE
// terminator ("data: [DONE]"). Some gateways never send a finish_reason but do
// terminate correctly, so the terminator is the only way to tell a complete
// response from one the transport cut short.
type streamTerminator struct {
	mu      sync.Mutex
	sawDone bool
	sawSSE  bool
}

// markSSE records that the provider answered with an event stream at all,
// which separates a severed stream from an HTTP-level failure that never
// started one.
func (t *streamTerminator) markSSE() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.sawSSE = true
	t.mu.Unlock()
}

func (t *streamTerminator) SawSSE() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sawSSE
}

func (t *streamTerminator) markDone() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.sawDone = true
	t.mu.Unlock()
}

func (t *streamTerminator) SawDone() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sawDone
}

type streamTerminatorKey struct{}

// withStreamTerminator attaches a per-request terminator to ctx. The SSE
// middleware runs inside the SDK call, so a context value is the only handle
// the caller and the response body can share.
func withStreamTerminator(ctx context.Context) (context.Context, *streamTerminator) {
	t := &streamTerminator{}
	return context.WithValue(ctx, streamTerminatorKey{}, t), t
}

func streamTerminatorFrom(ctx context.Context) *streamTerminator {
	if ctx == nil {
		return nil
	}
	t, _ := ctx.Value(streamTerminatorKey{}).(*streamTerminator)
	return t
}

// filterSSEComments strips SSE comment lines (": keep-alive" heartbeats, e.g.
// ": OPENROUTER PROCESSING") from event-stream responses. The openai-go SSE
// decoder dispatches an empty event for the blank line that follows a
// comment-only block and then fails with "unexpected end of JSON input"
// trying to unmarshal zero bytes, killing the stream.
func filterSSEComments() option.RequestOption {
	return option.WithMiddleware(func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		res, err := next(req)
		if err != nil || res == nil || res.Body == nil || res.StatusCode >= 300 {
			return res, err
		}
		if !strings.Contains(strings.ToLower(res.Header.Get("content-type")), "text/event-stream") {
			return res, err
		}
		terminator := streamTerminatorFrom(req.Context())
		terminator.markSSE()
		res.Body = &sseCommentStripper{
			rc:         res.Body,
			br:         bufio.NewReader(res.Body),
			terminator: terminator,
		}
		return res, err
	})
}

type sseCommentStripper struct {
	rc         io.ReadCloser
	br         *bufio.Reader
	buf        []byte
	sawField   bool
	err        error
	terminator *streamTerminator
}

func (s *sseCommentStripper) Read(p []byte) (int, error) {
	for len(s.buf) == 0 {
		if s.err != nil {
			return 0, s.err
		}
		line, err := s.br.ReadString('\n')
		if err != nil {
			s.err = err
		}
		if line == "" {
			continue
		}
		trimmed := strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(trimmed, ":"):
			// SSE comment / keep-alive: drop.
		case trimmed == "":
			// Blank line dispatches an event; forward it only when the
			// event actually has fields, otherwise the decoder would
			// dispatch an empty event.
			if s.sawField {
				s.buf = append(s.buf, line...)
				s.sawField = false
			}
		default:
			if strings.TrimSpace(strings.TrimPrefix(trimmed, "data:")) == "[DONE]" {
				s.terminator.markDone()
			}
			s.buf = append(s.buf, line...)
			s.sawField = true
		}
	}
	n := copy(p, s.buf)
	s.buf = s.buf[n:]
	return n, nil
}

func (s *sseCommentStripper) Close() error {
	return s.rc.Close()
}
