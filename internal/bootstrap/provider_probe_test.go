package bootstrap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProbeCompatibleModelTreatsHTTP200AsHealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/chat/completions"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"ok"}`))
	}))
	defer srv.Close()

	if err := probeCompatibleModel(context.Background(), srv.Client(), srv.URL, "token", "nvidia", "nvidia/moonshotai/kimi-k2-instruct-0905"); err != nil {
		t.Fatalf("probeCompatibleModel() error = %v", err)
	}
}

func TestProbeCompatibleProviderModelsUpdatesHealthOrdering(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	t.Setenv("HOME", home)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/chat/completions":
			if r.Header.Get("Authorization") != "Bearer token" {
				t.Fatalf("Authorization header missing")
			}
			buf := make([]byte, 512)
			n, _ := r.Body.Read(buf)
			body := string(buf[:n])
			if strings.Contains(body, "moonshotai/kimi-k2-instruct-0905") {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"id":"ok"}`))
				return
			}
			w.WriteHeader(http.StatusGone)
			_, _ = w.Write([]byte(`{"message":"model is gone"}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	models := []string{
		"nvidia/deepseek-ai/deepseek-r1-0528",
		"nvidia/moonshotai/kimi-k2-instruct-0905",
	}

	probeCompatibleProviderModels(context.Background(), srv.Client(), srv.URL, "token", "nvidia", models)

	sorted := sortModelsByHealth(models)
	if got, want := sorted[0], "nvidia/moonshotai/kimi-k2-instruct-0905"; got != want {
		t.Fatalf("first model = %q, want %q", got, want)
	}
	if got, want := sorted[1], "nvidia/deepseek-ai/deepseek-r1-0528"; got != want {
		t.Fatalf("second model = %q, want %q", got, want)
	}
}
