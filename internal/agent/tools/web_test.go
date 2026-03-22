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
