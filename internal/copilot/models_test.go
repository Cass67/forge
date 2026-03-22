package copilot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestFetchModelsAddsHeadersAndPrefixes(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/models"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("Authorization"), "Bearer gho-test"; got != want {
			t.Fatalf("Authorization = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("Accept"), "application/json"; got != want {
			t.Fatalf("Accept = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("Copilot-Integration-Id"), "copilot-developer-cli"; got != want {
			t.Fatalf("Copilot-Integration-Id = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("Openai-Intent"), "conversation-agent"; got != want {
			t.Fatalf("Openai-Intent = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("X-Initiator"), "user"; got != want {
			t.Fatalf("X-Initiator = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("X-GitHub-Api-Version"), apiVersion; got != want {
			t.Fatalf("X-GitHub-Api-Version = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("User-Agent"), userAgent; got != want {
			t.Fatalf("User-Agent = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o"},{"id":"gpt-4o"},{"id":"  "},{"id":"claude-sonnet-4.5"}]}`))
	}))
	defer srv.Close()

	got, err := fetchModels(context.Background(), srv.Client(), srv.URL, "gho-test")
	if err != nil {
		t.Fatalf("fetchModels() error = %v", err)
	}

	want := []string{"copilot/gpt-4o", "copilot/claude-sonnet-4.5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fetchModels() = %#v, want %#v", got, want)
	}
}

func TestMergeModelListsKeepsLiveOrderAndAppendsKnownAliases(t *testing.T) {
	t.Parallel()

	got := mergeModelLists(
		[]string{"copilot/gpt-4o", "copilot/custom-preview"},
		[]string{"copilot/gpt-4o", "copilot/claude-sonnet-4.5", "copilot/gpt-5"},
	)

	want := []string{
		"copilot/gpt-4o",
		"copilot/custom-preview",
		"copilot/claude-sonnet-4.5",
		"copilot/gpt-5",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mergeModelLists() = %#v, want %#v", got, want)
	}
}
