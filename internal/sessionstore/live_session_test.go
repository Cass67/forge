package sessionstore

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestLiveSessionContinuesSequenceAfterStoreReopen(t *testing.T) {
	dir := t.TempDir()
	store := NewJSONLThreadStore(dir)
	live := NewLiveSession("thread-1", store, DefaultPersistencePolicy())
	if err := live.Append(context.Background(), protocol.Item{Kind: protocol.ItemRetry, Retry: &protocol.RetryItem{Attempt: 1}}); err != nil {
		t.Fatal(err)
	}
	reopened := NewLiveSession("thread-1", NewJSONLThreadStore(dir), DefaultPersistencePolicy())
	if err := reopened.Append(context.Background(), protocol.Item{Kind: protocol.ItemRetry, Retry: &protocol.RetryItem{Attempt: 2}}); err != nil {
		t.Fatal(err)
	}
	items, err := store.ReadItems(context.Background(), "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Seq != 1 || items[1].Seq != 2 {
		t.Fatalf("items = %#v", items)
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

type barrierThreadStore struct {
	mu        sync.Mutex
	readers   int
	items     []protocol.Item
	releaseCh chan struct{}
}

func newBarrierThreadStore() *barrierThreadStore {
	return &barrierThreadStore{releaseCh: make(chan struct{})}
}

func (s *barrierThreadStore) AppendItems(_ context.Context, _ string, items []protocol.Item) (AppendResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = append(s.items, items...)
	return AppendResult{}, nil
}

func (s *barrierThreadStore) ReadItems(ctx context.Context, _ string) ([]protocol.Item, error) {
	s.mu.Lock()
	s.readers++
	if s.readers == 2 {
		close(s.releaseCh)
	}
	s.mu.Unlock()
	select {
	case <-s.releaseCh:
	case <-time.After(20 * time.Millisecond):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]protocol.Item(nil), s.items...), nil
}

func (s *barrierThreadStore) UpdateThreadMetadata(context.Context, string, ThreadMetadataPatch) error {
	return nil
}

func (s *barrierThreadStore) ReadThread(context.Context, string) (ThreadRecord, error) {
	return ThreadRecord{}, nil
}

func TestLiveSessionConcurrentAppendsUseUniqueSequence(t *testing.T) {
	store := newBarrierThreadStore()
	live := NewLiveSession("thread-1", store, DefaultPersistencePolicy())
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := live.Append(context.Background(), protocol.Item{Kind: protocol.ItemRetry, Retry: &protocol.RetryItem{Attempt: 1}}); err != nil {
				t.Errorf("append failed: %v", err)
			}
		}()
	}
	wg.Wait()
	if len(store.items) != 2 || store.items[0].Seq == store.items[1].Seq {
		t.Fatalf("items = %#v", store.items)
	}
}

func TestLiveSessionConcurrentInstancesUseUniqueSequence(t *testing.T) {
	dir := t.TempDir()
	store := NewJSONLThreadStore(dir)
	first := NewLiveSession("thread-1", store, DefaultPersistencePolicy())
	second := NewLiveSession("thread-1", NewJSONLThreadStore(dir), DefaultPersistencePolicy())
	var wg sync.WaitGroup
	for _, live := range []*LiveSession{first, second} {
		wg.Add(1)
		go func(live *LiveSession) {
			defer wg.Done()
			if err := live.Append(context.Background(), protocol.Item{Kind: protocol.ItemRetry, Retry: &protocol.RetryItem{Attempt: 1}}); err != nil {
				t.Errorf("append failed: %v", err)
			}
		}(live)
	}
	wg.Wait()
	items, err := store.ReadItems(context.Background(), "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	for _, item := range items {
		if seen[item.Seq] {
			t.Fatalf("duplicate seq in items: %#v", items)
		}
		seen[item.Seq] = true
	}
	if len(items) != 2 || !seen[1] || !seen[2] {
		t.Fatalf("items = %#v", items)
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

func TestThreadTitle(t *testing.T) {
	cases := []struct{ in, want string }{
		{"add a workspace picker", "add a workspace picker"},
		{"  first line\nsecond line  ", "first line"},
		{"/review the diff", "review the diff"},
		{"", "Forge chat"},
		{strings.Repeat("x", 80), strings.Repeat("x", 59) + "…"},
	}
	for _, c := range cases {
		if got := ThreadTitle(c.in); got != c.want {
			t.Errorf("ThreadTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
