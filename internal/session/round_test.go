package session_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
		nil,
		nil,
		nil,
	)

	converged, err := r.Run(context.Background(), "system prompt", "system prompt", 1, 1,
		"build a thing", "auto", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if converged {
		t.Fatal("expected non-converged round for generic auditor response")
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

func TestRoundAppliesAuditorCodeBlocks(t *testing.T) {
	base := t.TempDir()
	w, _ := output.NewWriter(base, time.Now())
	store := summarizer.NewStore(filepath.Join(w.Dir(), "summary-store.md"))

	writerResp := "```go:main.go\npackage main\nfunc main() {}\n```"
	auditorResp := "fixing issue\n```go:main.go\npackage main\nfunc main() { println(\"ok\") }\n```"

	events := make(chan llm.Event, 32)
	r := session.NewRound(
		&mockDriver{resp: writerResp},
		&mockDriver{resp: auditorResp},
		summarizer.NewAgent(&mockDriver{resp: "summary body"}, "sys"),
		w,
		store,
		events,
		nil,
		nil,
		nil,
	)

	_, err := r.Run(context.Background(), "system prompt", "system prompt", 1, 1, "build a thing", "auto", "", "")
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(w.Dir(), "code", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "package main\nfunc main() { println(\"ok\") }\n" {
		t.Fatalf("expected auditor patch to be applied, got %q", string(data))
	}
}

func TestRoundIncludesPriorAuditorTurnForWriter(t *testing.T) {
	base := t.TempDir()
	w, _ := output.NewWriter(base, time.Now())
	store := summarizer.NewStore(filepath.Join(w.Dir(), "summary-store.md"))

	var seen [][]llm.Message
	writer := &captureDriver{resp: "```go:main.go\npackage main\n```", seen: &seen}

	events := make(chan llm.Event, 32)
	r := session.NewRound(
		writer,
		&mockDriver{resp: "looks fine"},
		summarizer.NewAgent(&mockDriver{resp: "summary body"}, "sys"),
		w,
		store,
		events,
		nil,
		nil,
		nil,
	)

	_, err := r.Run(context.Background(), "system prompt", "system prompt", 1, 2, "build a thing", "auto", "", "AI-2 says change X")
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) == 0 {
		t.Fatal("expected writer messages to be captured")
	}
	if !strings.Contains(seen[0][1].Content, "AI-2 LAST TURN:\nAI-2 says change X") {
		t.Fatalf("writer prompt missing prior auditor turn: %q", seen[0][1].Content)
	}
}
