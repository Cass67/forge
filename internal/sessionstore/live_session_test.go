package sessionstore

import (
	"context"
	"sync"
	"testing"

	"forge/internal/protocol"
)

func TestLiveSessionAppliesPolicyBeforeAppend(t *testing.T) {
	store := NewJSONLThreadStore(t.TempDir())
	live := NewLiveSession("thread-1", store, DefaultPersistencePolicy())
	item := protocol.Item{Kind: protocol.ItemToolCall, ToolCall: &protocol.ToolCallItem{Args: map[string]any{"token": "sk-secret-value"}}}
	if err := live.Append(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	items, err := store.ReadItems(context.Background(), "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if items[0].ToolCall.Args["token"] == "sk-secret-value" {
		t.Fatalf("item was not sanitized before append: %#v", items[0])
	}
}

func TestLiveSessionAssignsIDsAndSequence(t *testing.T) {
	store := NewJSONLThreadStore(t.TempDir())
	live := NewLiveSession("thread-1", store, DefaultPersistencePolicy())
	if err := live.Append(context.Background(), protocol.Item{Kind: protocol.ItemRetry, Retry: &protocol.RetryItem{Attempt: 1}}); err != nil {
		t.Fatal(err)
	}
	items, err := store.ReadItems(context.Background(), "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if items[0].ID == "" || items[0].Seq != 1 || items[0].ThreadID != "thread-1" {
		t.Fatalf("item identity not assigned: %#v", items[0])
	}
}

func TestLiveSessionContinuesSequenceAfterExistingItems(t *testing.T) {
	ctx := context.Background()
	store := NewJSONLThreadStore(t.TempDir())
	threadID := "thread-1"
	_, err := store.AppendItems(ctx, threadID, []protocol.Item{
		{Version: 1, ID: "thread-1-000007", ThreadID: threadID, Seq: 7, Kind: protocol.ItemUserMessage, Message: &protocol.MessageItem{Role: "user", Text: "hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	live := NewLiveSession(threadID, store, DefaultPersistencePolicy())
	if err := live.Append(ctx, protocol.Item{Kind: protocol.ItemRetry, Retry: &protocol.RetryItem{Attempt: 1}}); err != nil {
		t.Fatal(err)
	}

	items, err := store.ReadItems(ctx, threadID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %#v, want existing and appended item", items)
	}
	if items[1].Seq != 8 || items[1].ID != "thread-1-000008" {
		t.Fatalf("appended item identity = %#v, want seq/id after existing max", items[1])
	}
}

func TestLiveSessionAppendConcurrentAssignsUniqueOrderedSequences(t *testing.T) {
	store := NewJSONLThreadStore(t.TempDir())
	live := NewLiveSession("thread-1", store, DefaultPersistencePolicy())
	ctx := context.Background()
	const count = 50
	var wg sync.WaitGroup
	start := make(chan struct{})
	errCh := make(chan error, count)

	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errCh <- live.Append(ctx, protocol.Item{Kind: protocol.ItemRetry, Retry: &protocol.RetryItem{Attempt: 1}})
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}

	items, err := store.ReadItems(ctx, "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != count {
		t.Fatalf("items = %d, want %d", len(items), count)
	}
	seen := make(map[int64]bool, count)
	for _, item := range items {
		if item.Seq < 1 || item.Seq > count {
			t.Fatalf("seq out of range: %#v", item)
		}
		if seen[item.Seq] {
			t.Fatalf("duplicate seq %d in items %#v", item.Seq, items)
		}
		seen[item.Seq] = true
	}
	for seq := int64(1); seq <= count; seq++ {
		if !seen[seq] {
			t.Fatalf("missing seq %d in items %#v", seq, items)
		}
	}
}
