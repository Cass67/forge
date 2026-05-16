package sessionstore

import (
	"context"
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
