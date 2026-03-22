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
	tool := NewWebSearch("")
	result, err := tool.Execute(context.Background(), map[string]any{"query": "golang"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "unavailable") {
		t.Errorf("expected unavailable message, got: %s", result)
	}
}

func TestWebSearchMocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Subscription-Token") != "test-key" {
			t.Error("missing API key header")
		}
		q := r.URL.Query().Get("q")
		if q != "golang tutorial" {
			t.Errorf("unexpected query: %s", q)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"web": {
				"results": [
					{"title": "Go Tutorial", "url": "https://go.dev/tour", "description": "A tour of Go"},
					{"title": "Learn Go", "url": "https://go.dev/learn", "description": "Getting started with Go"}
				]
			}
		}`)
	}))
	defer srv.Close()

	tool := newWebSearchWithEndpoint("test-key", srv.URL)
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

func TestWebSearchCountParam(t *testing.T) {
	requested := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := r.URL.Query().Get("count")
		if count != "3" {
			t.Errorf("expected count=3, got %s", count)
		}
		requested++
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"web":{"results":[]}}`)
	}))
	defer srv.Close()

	tool := newWebSearchWithEndpoint("key", srv.URL)
	_, _ = tool.Execute(context.Background(), map[string]any{"query": "test", "count": float64(3)})
	if requested != 1 {
		t.Error("server not called")
	}
}
