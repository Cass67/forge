package bootstrap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestFetchOpenAIModelsAddsHeadersAndFiltersResults(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/models"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("Authorization"), "Bearer sk-test"; got != want {
			t.Fatalf("Authorization = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("Accept"), "application/json"; got != want {
			t.Fatalf("Accept = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o"},{"id":"gpt-5.4"},{"id":"o4-mini"},{"id":"whisper-1"},{"id":"  "},{"id":"gpt-4o"}]}`))
	}))
	defer srv.Close()

	got, err := fetchOpenAIModels(context.Background(), srv.Client(), srv.URL, "sk-test")
	if err != nil {
		t.Fatalf("fetchOpenAIModels() error = %v", err)
	}

	want := []string{"gpt-4o", "gpt-5", "o4-mini"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fetchOpenAIModels() = %#v, want %#v", got, want)
	}
}

func TestFetchCompatibleModelsAddsOpenRouterHeaders(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/models"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("Authorization"), "Bearer sk-test"; got != want {
			t.Fatalf("Authorization = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("HTTP-Referer"), "https://github.com/cass/forge"; got != want {
			t.Fatalf("HTTP-Referer = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("X-Title"), "forge"; got != want {
			t.Fatalf("X-Title = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("X-OpenRouter-Title"), "forge"; got != want {
			t.Fatalf("X-OpenRouter-Title = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"minimax/minimax-m2.5:free"}]}`))
	}))
	defer srv.Close()

	got, err := fetchCompatibleModels(context.Background(), srv.Client(), srv.URL, "sk-test", "openrouter", func(m string) bool {
		return strings.Contains(m, "/")
	})
	if err != nil {
		t.Fatalf("fetchCompatibleModels() error = %v", err)
	}

	want := []string{"openrouter/minimax/minimax-m2.5:free"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fetchCompatibleModels() = %#v, want %#v", got, want)
	}
}
