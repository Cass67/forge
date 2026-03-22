package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func noSSRF(_ string) error { return nil }

func TestWebFetchJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"key": "value"}`)
	}))
	defer srv.Close()

	tool := newWebFetch(noSSRF)
	result, err := tool.Execute(context.Background(), map[string]any{"url": srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, `"key"`) {
		t.Errorf("expected JSON content, got: %s", result)
	}
}

func TestWebFetchHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, `<html><body><p>Hello world</p><script>evil()</script></body></html>`)
	}))
	defer srv.Close()

	tool := newWebFetch(noSSRF)
	result, err := tool.Execute(context.Background(), map[string]any{"url": srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Hello world") {
		t.Errorf("expected extracted text, got: %s", result)
	}
	if strings.Contains(result, "evil") {
		t.Error("should strip script tags")
	}
}

func TestWebFetchPlainText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, "plain text content")
	}))
	defer srv.Close()

	tool := newWebFetch(noSSRF)
	result, err := tool.Execute(context.Background(), map[string]any{"url": srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "plain text content") {
		t.Errorf("expected plain text, got: %s", result)
	}
}

func TestWebFetchTruncation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, strings.Repeat("x", 1000))
	}))
	defer srv.Close()

	tool := newWebFetch(noSSRF)
	result, err := tool.Execute(context.Background(), map[string]any{
		"url":        srv.URL,
		"max_length": float64(100),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "truncated") {
		t.Errorf("expected truncation, got: %s", result)
	}
}

func TestWebFetchBinaryRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte{0x89, 0x50, 0x4e, 0x47})
	}))
	defer srv.Close()

	tool := newWebFetch(noSSRF)
	result, err := tool.Execute(context.Background(), map[string]any{"url": srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "binary") || !strings.Contains(result, "image/png") {
		t.Errorf("expected binary rejection, got: %s", result)
	}
}

func TestWebFetchInvalidScheme(t *testing.T) {
	tool := NewWebFetch()
	result, err := tool.Execute(context.Background(), map[string]any{"url": "file:///etc/passwd"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "http") && !strings.Contains(result, "scheme") {
		t.Errorf("expected scheme error, got: %s", result)
	}
}

func TestWebFetchPrivateIP(t *testing.T) {
	tool := NewWebFetch()
	for _, url := range []string{
		"http://127.0.0.1/",
		"http://10.0.0.1/",
		"http://172.16.0.1/",
		"http://192.168.1.1/",
		"http://169.254.169.254/latest/meta-data/",
	} {
		result, err := tool.Execute(context.Background(), map[string]any{"url": url})
		if err != nil {
			t.Fatalf("url=%s: %v", url, err)
		}
		if !strings.Contains(result, "private") && !strings.Contains(result, "blocked") {
			t.Errorf("url=%s: expected private IP rejection, got: %s", url, result)
		}
	}
}

func TestWebSearchNoKey(t *testing.T) {
	// DDG needs no key - just verify the tool is registered and works with a mock
	tool := NewWebSearch()
	if tool.Name != "web_search" {
		t.Fatalf("expected name 'web_search', got %q", tool.Name)
	}
	if !tool.AutoApprove {
		t.Fatal("web_search should be auto-approved")
	}
}

func TestWebSearchMocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		_ = r.ParseForm()
		if r.Form.Get("q") != "golang tutorial" {
			t.Errorf("unexpected query: %s", r.Form.Get("q"))
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, `<html><body>
            <div class="result">
                <h2 class="result__title">
                    <a class="result__a" href="https://go.dev/tour">Go Tutorial</a>
                </h2>
                <a class="result__snippet">A tour of Go programming language</a>
            </div>
            <div class="result">
                <h2 class="result__title">
                    <a class="result__a" href="https://go.dev/learn">Learn Go</a>
                </h2>
                <a class="result__snippet">Getting started with Go</a>
            </div>
        </body></html>`)
	}))
	defer srv.Close()

	tool := newWebSearchWithEndpoint(srv.URL)
	result, err := tool.Execute(context.Background(), map[string]any{"query": "golang tutorial"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Go Tutorial") {
		t.Errorf("expected title in results, got: %s", result)
	}
	if !strings.Contains(result, "go.dev/tour") {
		t.Errorf("expected URL in results, got: %s", result)
	}
}

func TestWebSearchCountLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		var body strings.Builder
		body.WriteString("<html><body>")
		for i := 1; i <= 5; i++ {
			_, _ = fmt.Fprintf(&body, `<div class="result">
                <a class="result__a" href="https://example.com/%d">Result %d</a>
                <a class="result__snippet">Snippet %d</a>
            </div>`, i, i, i)
		}
		body.WriteString("</body></html>")
		_, _ = fmt.Fprint(w, body.String())
	}))
	defer srv.Close()

	tool := newWebSearchWithEndpoint(srv.URL)
	result, err := tool.Execute(context.Background(), map[string]any{"query": "test", "count": float64(2)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result, "Result 3") {
		t.Errorf("expected max 2 results, got more: %s", result)
	}
	if !strings.Contains(result, "Result 1") {
		t.Errorf("expected Result 1, got: %s", result)
	}
}
