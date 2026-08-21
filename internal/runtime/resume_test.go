package runtime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"forge/internal/config"
	"forge/internal/protocol"
	reactruntime "forge/internal/react"
	"forge/internal/sessionstore"
)

func seedThread(t *testing.T, outputDir, threadID, userText string, updated time.Time) {
	t.Helper()
	store := sessionstore.NewJSONLThreadStore(filepath.Join(outputDir, "threads"))
	live := sessionstore.NewLiveSession(threadID, store, sessionstore.DefaultPersistencePolicy())
	ctx := context.Background()
	if err := live.UpdateMetadata(ctx, sessionstore.ThreadMetadataPatch{
		Title:     "t",
		Preview:   userText,
		UpdatedAt: updated,
	}); err != nil {
		t.Fatal(err)
	}
	items := []protocol.Item{
		{TurnID: "turn-1", Seq: 1, Kind: protocol.ItemUserMessage, Message: &protocol.MessageItem{Role: "user", Text: userText}},
		{TurnID: "turn-1", Seq: 2, Kind: protocol.ItemAssistantMessage, Message: &protocol.MessageItem{Role: "assistant", Text: "done"}},
		{TurnID: "turn-1", Seq: 3, Kind: protocol.ItemTurnComplete, TurnComplete: &protocol.TurnCompleteItem{Status: protocol.TurnStatusCompleted}},
	}
	for _, it := range items {
		if err := live.Append(ctx, it); err != nil {
			t.Fatal(err)
		}
	}
}

func TestResolveResumeThreadID(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Session.OutputDir = dir

	seedThread(t, dir, "thread-old", "first", time.Now().Add(-time.Hour))
	seedThread(t, dir, "thread-new", "second", time.Now())

	// Explicit id wins.
	if id, err := ResolveResumeThreadID(cfg, "thread-old", false); err != nil || id != "thread-old" {
		t.Fatalf("explicit resolve = %q, %v", id, err)
	}
	// --continue picks the most recently updated.
	if id, err := ResolveResumeThreadID(cfg, "", true); err != nil || id != "thread-new" {
		t.Fatalf("continue resolve = %q, %v", id, err)
	}
	// Neither flag → empty, no error.
	if id, err := ResolveResumeThreadID(cfg, "", false); err != nil || id != "" {
		t.Fatalf("no-flag resolve = %q, %v", id, err)
	}
}

func TestResolveWorkspaceResumeThreadID(t *testing.T) {
	dir := t.TempDir()
	workspace := t.TempDir()
	other := t.TempDir()
	cfg := &config.Config{}
	cfg.Session.OutputDir = dir

	// Each thread's UpdatedAt has to survive the CWD patch: the store stamps
	// a zero UpdatedAt with time.Now(), so patching without one would reorder
	// these by whatever order the patches happened to run in.
	threads := []struct {
		id, cwd, text string
		updated       time.Time
	}{
		{"workspace-old", workspace, "first", time.Now().Add(-time.Hour)},
		{"other-new", other, "other", time.Now()},
		{"workspace-new", workspace, "second", time.Now().Add(-time.Minute)},
	}
	store := sessionstore.NewJSONLThreadStore(filepath.Join(dir, "threads"))
	for _, thread := range threads {
		seedThread(t, dir, thread.id, thread.text, thread.updated)
		if err := store.UpdateThreadMetadata(context.Background(), thread.id, sessionstore.ThreadMetadataPatch{
			CWD:       thread.cwd,
			UpdatedAt: thread.updated,
		}); err != nil {
			t.Fatal(err)
		}
	}

	id, err := ResolveWorkspaceResumeThreadID(cfg, workspace)
	if err != nil || id != "workspace-new" {
		t.Fatalf("workspace resume = %q, %v", id, err)
	}
}

func TestAdoptResumeThreadSeedsHistory(t *testing.T) {
	dir := t.TempDir()
	seedThread(t, dir, "thread-1", "read README", time.Now())

	cfg := &config.Config{}
	cfg.Session.OutputDir = dir
	setup := &ChatSetup{Config: cfg, ResumeThreadID: "thread-1"}

	session := reactruntime.NewSession()
	adoptResumeThread(setup, session)

	snap := session.Snapshot()
	if len(snap.History) == 0 {
		t.Fatal("expected resumed history, got none")
	}
	if session.DurableThreadID() != "thread-1" {
		t.Fatalf("durable thread id = %q, want thread-1", session.DurableThreadID())
	}
}

func TestAdoptResumeThreadNoopWhenUnset(t *testing.T) {
	session := reactruntime.NewSession()
	adoptResumeThread(&ChatSetup{Config: &config.Config{}}, session)
	if len(session.Snapshot().History) != 0 {
		t.Fatal("expected no history for empty ResumeThreadID")
	}
}
