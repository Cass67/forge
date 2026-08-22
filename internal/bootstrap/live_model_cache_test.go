package bootstrap

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDiscoverCompatibleModelsServesCuratedWhileFetchingInBackground(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"live-model"}]}`))
	}))
	defer srv.Close()

	start := time.Now()
	models := DiscoverOpenAICompatibleModels(srv.URL, "key", "cachetest", []string{"curated-model"}, func(string) bool { return true })
	elapsed := time.Since(start)

	if elapsed > 100*time.Millisecond {
		t.Fatalf("discovery blocked on the network for %v", elapsed)
	}
	if len(models) != 1 || models[0] != "cachetest/curated-model" {
		t.Fatalf("models = %#v, want the curated list", models)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		if cached, ok := loadLiveModelCache("cachetest"); ok {
			if len(cached) != 1 || cached[0] != "cachetest/live-model" {
				t.Fatalf("cached = %#v, want the live list", cached)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background fetch never wrote the cache")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if got := DiscoverOpenAICompatibleModels(srv.URL, "key", "cachetest", []string{"curated-model"}, func(string) bool { return true }); len(got) != 1 || got[0] != "cachetest/live-model" {
		t.Fatalf("second call = %#v, want the cached live list", got)
	}
}
