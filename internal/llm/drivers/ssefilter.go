package drivers

import (
	"bufio"
	"io"
	"net/http"
	"strings"

	"github.com/openai/openai-go/option"
)

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
		res.Body = &sseCommentStripper{rc: res.Body, br: bufio.NewReader(res.Body)}
		return res, err
	})
}

type sseCommentStripper struct {
	rc       io.ReadCloser
	br       *bufio.Reader
	buf      []byte
	sawField bool
	err      error
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
