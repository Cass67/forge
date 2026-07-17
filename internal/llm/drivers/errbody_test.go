package drivers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"forge/internal/llm"
)

func TestNonJSONErrorBodySurfacesStatusCode(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("<html>rate limited by gateway</html>"))
	}))
	defer srv.Close()

	d := NewCustomCompatProvider("opencode-go", "sk-test", srv.URL, "opencode-go/mimo-v2.5-pro", "mimo-v2.5-pro", false, nil)
	out := make(chan llm.Token, 8)
	err := d.Stream(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, out)
	if err == nil {
		t.Fatal("expected error from 429 response")
	}
	if strings.Contains(err.Error(), "unexpected end of JSON input") {
		t.Fatalf("raw JSON parse error leaked instead of real status: %v", err)
	}
	if !strings.Contains(err.Error(), "429") {
		t.Fatalf("error should carry the HTTP status, got: %v", err)
	}
	if !strings.Contains(err.Error(), "rate limited by gateway") {
		t.Fatalf("error should carry the body snippet, got: %v", err)
	}
}

func TestEmptyErrorBodySurfacesStatusCode(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	d := NewCustomCompatProvider("opencode-go", "sk-test", srv.URL, "opencode-go/mimo-v2.5-pro", "mimo-v2.5-pro", false, nil)
	out := make(chan llm.Token, 8)
	err := d.Stream(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, out)
	if err == nil {
		t.Fatal("expected error from 503 response")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Fatalf("error should carry the HTTP status, got: %v", err)
	}
}

func TestJSONErrorBodyPassedThroughUnchanged(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"model overloaded, try later","type":"invalid_request_error"}}`))
	}))
	defer srv.Close()

	d := NewCustomCompatProvider("opencode-go", "sk-test", srv.URL, "opencode-go/mimo-v2.5-pro", "mimo-v2.5-pro", false, nil)
	out := make(chan llm.Token, 8)
	err := d.Stream(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, out)
	if err == nil {
		t.Fatal("expected error from 400 response")
	}
	if !strings.Contains(err.Error(), "model overloaded") {
		t.Fatalf("provider error message should pass through, got: %v", err)
	}
}
