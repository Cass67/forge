package session_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"forge/internal/llm"
	"forge/internal/output"
	"forge/internal/session"
	"forge/internal/summarizer"
)

func TestRunnerEmitsDoneEvent(t *testing.T) {
	base := t.TempDir()
	w, _ := output.NewWriter(base, time.Now())
	store := summarizer.NewStore(filepath.Join(w.Dir(), "summary-store.md"))

	writerD := &mockDriver{resp: "```go:main.go\npackage main\n```"}
	auditorD := &mockDriver{resp: "looks good"}
	sumD := &mockDriver{resp: "summary"}

	events := make(chan llm.Event, 128)
	cfg := session.RunnerConfig{
		Passes:      session.DefaultPasses(1),
		Writer:      writerD,
		Auditor:     auditorD,
		SumDriver:   sumD,
		SumPrompt:   "summarize",
		AuditPrompt: "audit",
		WriterW:     w,
		Store:       store,
		Events:      events,
		UserPrompt:  "build a thing",
	}

	runner := session.NewRunner(cfg)
	go runner.Run(context.Background())

	var done bool
	for ev := range events {
		if ev.Kind == llm.EventDone {
			done = true
			break
		}
	}
	if !done {
		t.Error("expected EventDone")
	}
}
