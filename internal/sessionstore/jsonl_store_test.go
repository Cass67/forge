package sessionstore

import (
	"context"
	"testing"

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
