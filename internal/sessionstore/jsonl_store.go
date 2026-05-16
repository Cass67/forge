package sessionstore

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"forge/internal/protocol"
)

type JSONLThreadStore struct {
	root string
}

func NewJSONLThreadStore(root string) *JSONLThreadStore {
	return &JSONLThreadStore{root: root}
}

func (s *JSONLThreadStore) threadPath(threadID string) string {
	return filepath.Join(s.root, threadID+".jsonl")
}

func (s *JSONLThreadStore) AppendItems(ctx context.Context, threadID string, items []protocol.Item) (result AppendResult, err error) {
	if err := ctx.Err(); err != nil {
		return AppendResult{}, err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return AppendResult{}, err
	}
	f, err := os.OpenFile(s.threadPath(threadID), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return AppendResult{}, err
	}
	defer func() {
		if closeErr := f.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	enc := json.NewEncoder(f)
	for _, item := range items {
		if err := enc.Encode(item); err != nil {
			return AppendResult{}, err
		}
	}
	if err := f.Sync(); err != nil {
		return AppendResult{}, err
	}
	var first, last int64
	if len(items) > 0 {
		first = items[0].Seq
		last = items[len(items)-1].Seq
	}
	return AppendResult{ThreadID: threadID, FirstSeq: first, LastSeq: last}, nil
}

func (s *JSONLThreadStore) ReadItems(ctx context.Context, threadID string) (items []protocol.Item, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(s.threadPath(threadID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := f.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var item protocol.Item
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, scanner.Err()
}

func (s *JSONLThreadStore) UpdateThreadMetadata(ctx context.Context, threadID string, patch ThreadMetadataPatch) error {
	return ctx.Err()
}

func (s *JSONLThreadStore) ReadThread(ctx context.Context, threadID string) (ThreadRecord, error) {
	items, err := s.ReadItems(ctx, threadID)
	if err != nil {
		return ThreadRecord{}, err
	}
	return ThreadRecord{ThreadID: threadID, ItemCount: len(items)}, nil
}

func (s *JSONLThreadStore) ForkThread(ctx context.Context, sourceThreadID, newThreadID string, opts ForkOptions) error {
	items, err := s.ReadItems(ctx, sourceThreadID)
	if err != nil {
		return err
	}
	excluded := map[string]bool{}
	for _, id := range opts.ExcludeTurnIDs {
		excluded[id] = true
	}
	forked := make([]protocol.Item, 0, len(items))
	for _, item := range items {
		if excluded[item.TurnID] {
			continue
		}
		item.ThreadID = newThreadID
		forked = append(forked, item)
	}
	_, err = s.AppendItems(ctx, newThreadID, forked)
	return err
}

func (s *JSONLThreadStore) ListThreads(ctx context.Context, opts ListOptions) ([]ThreadRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = len(entries)
	}
	records := []ThreadRecord{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		threadID := strings.TrimSuffix(entry.Name(), ".jsonl")
		items, err := s.ReadItems(ctx, threadID)
		if err != nil {
			return nil, err
		}
		records = append(records, ThreadRecord{ThreadID: threadID, ItemCount: len(items)})
		if len(records) >= limit {
			break
		}
	}
	return records, nil
}
