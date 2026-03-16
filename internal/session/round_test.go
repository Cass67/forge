package session_test

import (
    "context"
    "os"
    "path/filepath"
    "testing"
    "time"

    "forge/internal/llm"
    "forge/internal/output"
    "forge/internal/session"
    "forge/internal/summarizer"
)

func TestRoundWritesCode(t *testing.T) {
    base := t.TempDir()
    w, _ := output.NewWriter(base, time.Now())
    store := summarizer.NewStore(filepath.Join(w.Dir(), "summary-store.md"))

    writerResp := "some prose\n```go:main.go\npackage main\n```"
    auditorResp := "looks fine"

    events := make(chan llm.Event, 32)
    r := session.NewRound(
        &mockDriver{resp: writerResp},
        &mockDriver{resp: auditorResp},
        summarizer.NewAgent(&mockDriver{resp: "summary body"}, "sys"),
        w,
        store,
        events,
    )

    err := r.Run(context.Background(), "system prompt", "system prompt", 1, 1,
        "build a thing", "")
    if err != nil {
        t.Fatal(err)
    }

    // code file should be on disk
    if _, err := os.Stat(filepath.Join(w.Dir(), "code", "main.go")); err != nil {
        t.Fatalf("code file not written: %v", err)
    }

    // summary store should have an entry
    text, _ := store.ReadAll()
    if text == "" {
        t.Error("summary store is empty after round")
    }
}
