package sessionstore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"forge/internal/protocol"
)

func TestJSONLThreadStoreAppendReadRoundTrip(t *testing.T) {
	store := NewJSONLThreadStore(t.TempDir())
	ctx := context.Background()
	threadID := "thread-1"
	items := []protocol.Item{{Version: protocol.CurrentItemVersion, ID: "item-1", ThreadID: threadID, Seq: 1, Kind: protocol.ItemUserMessage, Message: &protocol.MessageItem{Role: "user", Text: "hello"}}}
	if _, err := store.AppendItems(ctx, threadID, items); err != nil {
		t.Fatal(err)
	}
	got, err := store.ReadItems(ctx, threadID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "item-1" {
		t.Fatalf("items = %#v", got)
	}
}

func TestJSONLThreadStoreFlushesBeforeRead(t *testing.T) {
	store := NewJSONLThreadStore(t.TempDir())
	ctx := context.Background()
	threadID := "thread-1"
	if _, err := store.AppendItems(ctx, threadID, []protocol.Item{{Version: 1, ID: "item-1", ThreadID: threadID, Seq: 1, Kind: protocol.ItemRetry, Retry: &protocol.RetryItem{Attempt: 1}}}); err != nil {
		t.Fatal(err)
	}
	got, err := store.ReadItems(ctx, threadID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("items after append = %d, want 1", len(got))
	}
}

func TestJSONLThreadStoreMetadataRoundTripAndList(t *testing.T) {
	store := NewJSONLThreadStore(t.TempDir())
	ctx := context.Background()
	threadID := "thread-1"
	patch := ThreadMetadataPatch{
		Title:     "Durable chat",
		Preview:   "hello world",
		CWD:       "/tmp/forge",
		Model:     "test/model",
		UpdatedAt: time.Unix(100, 0).UTC(),
	}
	if err := store.UpdateThreadMetadata(ctx, threadID, patch); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendItems(ctx, threadID, []protocol.Item{{Version: 1, ID: "item-1", ThreadID: threadID, Seq: 1, Kind: protocol.ItemUserMessage, Message: &protocol.MessageItem{Role: "user", Text: "hello"}}}); err != nil {
		t.Fatal(err)
	}

	rec, err := store.ReadThread(ctx, threadID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Metadata.Title != patch.Title || rec.Metadata.Model != patch.Model || rec.Metadata.CWD != patch.CWD || rec.ItemCount != 1 {
		t.Fatalf("record = %#v", rec)
	}

	list, err := store.ListThreads(ctx, ListOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ThreadID != threadID || list[0].Metadata.Preview != patch.Preview {
		t.Fatalf("list = %#v", list)
	}
}

func TestJSONLThreadStoreReadsLineOver64KB(t *testing.T) {
	store := NewJSONLThreadStore(t.TempDir())
	ctx := context.Background()
	big := strings.Repeat("x", 200*1024)
	if _, err := store.AppendItems(ctx, "thread-1", []protocol.Item{{Version: 1, ID: "item-1", ThreadID: "thread-1", Seq: 1, Kind: protocol.ItemUserMessage, Message: &protocol.MessageItem{Role: "user", Text: big}}}); err != nil {
		t.Fatal(err)
	}
	got, err := store.ReadItems(ctx, "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Message.Text != big {
		t.Fatalf("items = %d", len(got))
	}
}

func TestJSONLThreadStoreCorruptLineReportsLineNumber(t *testing.T) {
	dir := t.TempDir()
	store := NewJSONLThreadStore(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "thread-1.jsonl")
	if err := os.WriteFile(path, []byte("{bad json}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := store.ReadItems(context.Background(), "thread-1")
	if err == nil || !strings.Contains(err.Error(), "thread-1: line 1") {
		t.Fatalf("error = %v", err)
	}
}

func TestJSONLThreadStoreConcurrentMetadataUpdatesDoNotLoseFile(t *testing.T) {
	store := NewJSONLThreadStore(t.TempDir())
	ctx := context.Background()
	errCh := make(chan error, 20)
	for i := range 20 {
		go func(i int) {
			errCh <- store.UpdateThreadMetadata(ctx, "thread-1", ThreadMetadataPatch{Title: "title", Preview: string(rune('a' + i))})
		}(i)
	}
	for range 20 {
		if err := <-errCh; err != nil {
			t.Fatalf("metadata update failed: %v", err)
		}
	}
	rec, err := store.ReadThread(ctx, "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Metadata.Title != "title" || strings.TrimSpace(rec.Metadata.Preview) == "" {
		t.Fatalf("metadata = %#v", rec.Metadata)
	}
}
