package sessionstore

import (
	"context"
	"testing"

	"forge/internal/protocol"
)

func TestThreadStoreForkCopiesHistoryWithNewThreadID(t *testing.T) {
	store := NewJSONLThreadStore(t.TempDir())
	ctx := context.Background()
	if _, err := store.AppendItems(ctx, "source", []protocol.Item{{Version: 1, ID: "source-1", ThreadID: "source", Seq: 1, Kind: protocol.ItemUserMessage, Message: &protocol.MessageItem{Role: "user", Text: "hello"}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.ForkThread(ctx, "source", "forked", ForkOptions{}); err != nil {
		t.Fatal(err)
	}
	items, err := store.ReadItems(ctx, "forked")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ThreadID != "forked" {
		t.Fatalf("forked items = %#v", items)
	}
}

func TestThreadStoreListReturnsMetadataOnly(t *testing.T) {
	store := NewJSONLThreadStore(t.TempDir())
	ctx := context.Background()
	if _, err := store.AppendItems(ctx, "thread-1", []protocol.Item{{Version: 1, ID: "item-1", ThreadID: "thread-1", Seq: 1, Kind: protocol.ItemUserMessage, Message: &protocol.MessageItem{Role: "user", Text: "hello"}}}); err != nil {
		t.Fatal(err)
	}
	threads, err := store.ListThreads(ctx, ListOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 || threads[0].ThreadID != "thread-1" || threads[0].ItemCount != 1 {
		t.Fatalf("threads = %#v", threads)
	}
}
