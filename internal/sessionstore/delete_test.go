package sessionstore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forge/internal/protocol"
)

func TestDeleteThreadRemovesBothFiles(t *testing.T) {
	root := t.TempDir()
	store := NewJSONLThreadStore(root)
	ctx := context.Background()

	if _, err := store.AppendItems(ctx, "thread-1", []protocol.Item{{Kind: protocol.ItemUserMessage}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := store.UpdateThreadMetadata(ctx, "thread-1", ThreadMetadataPatch{Title: "keep"}); err != nil {
		t.Fatalf("metadata: %v", err)
	}

	if err := store.DeleteThread(ctx, "thread-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for _, name := range []string{"thread-1.jsonl", "thread-1.meta.json"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Errorf("%s still present", name)
		}
	}
	// Deleting again is a no-op, not an error.
	if err := store.DeleteThread(ctx, "thread-1"); err != nil {
		t.Errorf("second delete: %v", err)
	}
}

// The thread id arrives from the UI, so it must not be able to address files
// outside the thread directory.
func TestDeleteThreadRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "..", "victim.jsonl")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	defer func() { _ = os.Remove(outside) }()

	store := NewJSONLThreadStore(filepath.Join(root, "threads"))
	for _, id := range []string{"../victim", "a/b", "", "  ", ".", "..", "/etc/passwd"} {
		err := store.DeleteThread(context.Background(), id)
		if err == nil {
			t.Errorf("DeleteThread(%q) = nil, want rejection", id)
		} else if !strings.Contains(err.Error(), "thread id") {
			t.Errorf("DeleteThread(%q) = %v, want an id rejection", id, err)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("traversal deleted a file outside the thread directory")
	}
}

func TestSetThreadTitleRenamesAndSticks(t *testing.T) {
	root := t.TempDir()
	store := NewJSONLThreadStore(root)
	ctx := context.Background()

	if err := store.UpdateThreadMetadata(ctx, "thread-1", ThreadMetadataPatch{
		Title: "we have a situation where the securecrt", CWD: "/work", Model: "gpt-5",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := store.SetThreadTitle(ctx, "thread-1", "  SecureCRT replacement review  "); err != nil {
		t.Fatalf("SetThreadTitle: %v", err)
	}

	rec, err := store.ReadThread(ctx, "thread-1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if rec.Metadata.Title != "SecureCRT replacement review" {
		t.Fatalf("title = %q, want the trimmed rename", rec.Metadata.Title)
	}
	// A rename must not blank the rest of the record.
	if rec.Metadata.CWD != "/work" || rec.Metadata.Model != "gpt-5" {
		t.Fatalf("rename clobbered metadata: %+v", rec.Metadata)
	}

	if err := store.SetThreadTitle(ctx, "thread-1", "   "); err == nil {
		t.Error("SetThreadTitle accepted an empty title")
	}
	if err := store.SetThreadTitle(ctx, "../escape", "x"); err == nil {
		t.Error("SetThreadTitle accepted a traversing id")
	}
	long := strings.Repeat("x", 400)
	if err := store.SetThreadTitle(ctx, "thread-1", long); err != nil {
		t.Fatalf("long title: %v", err)
	}
	rec, _ = store.ReadThread(ctx, "thread-1")
	if len([]rune(rec.Metadata.Title)) > 120 {
		t.Fatalf("title not capped: %d runes", len([]rune(rec.Metadata.Title)))
	}
}
