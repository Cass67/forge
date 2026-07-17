package drivers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/openai/openai-go/option"
)

// legibleErrorBodies rewrites HTTP >=400 responses whose body is not standard
// {"error":{...}} JSON into that shape, embedding the status code and a body
// snippet. Without this the openai-go SDK fails to parse the error body and
// surfaces a bare "unexpected end of JSON input", losing the real status and
// message (e.g. a 429 or an HTML gateway error page).
func legibleErrorBodies() option.RequestOption {
	return option.WithMiddleware(func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		res, err := next(req)
		if err != nil || res == nil || res.StatusCode < 400 || res.Body == nil {
			return res, err
		}
		body, readErr := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		_ = res.Body.Close()
		if readErr != nil {
			res.Body = io.NopCloser(bytes.NewReader(body))
			return res, err
		}
		var probe struct {
			Error json.RawMessage `json:"error"`
		}
		if json.Unmarshal(body, &probe) == nil && len(probe.Error) > 0 && string(probe.Error) != "null" {
			res.Body = io.NopCloser(bytes.NewReader(body))
			return res, err
		}
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 300 {
			snippet = snippet[:300] + "…"
		}
		msg := fmt.Sprintf("HTTP %d %s", res.StatusCode, http.StatusText(res.StatusCode))
		if snippet != "" {
			msg += ": " + snippet
		}
		replacement, _ := json.Marshal(map[string]any{"error": map[string]any{"message": msg}})
		res.Body = io.NopCloser(bytes.NewReader(replacement))
		res.ContentLength = int64(len(replacement))
		res.Header.Del("Content-Length")
		return res, err
	})
}
