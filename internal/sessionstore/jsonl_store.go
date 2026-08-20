package sessionstore

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"forge/internal/protocol"
)

type JSONLThreadStore struct {
	root string
}

var jsonlThreadLocks sync.Map

func NewJSONLThreadStore(root string) *JSONLThreadStore {
	return &JSONLThreadStore{root: root}
}

func (s *JSONLThreadStore) threadPath(threadID string) string {
	return filepath.Join(s.root, threadID+".jsonl")
}

func (s *JSONLThreadStore) metadataPath(threadID string) string {
	return filepath.Join(s.root, threadID+".meta.json")
}

func (s *JSONLThreadStore) lockThread(threadID string) func() {
	key := s.threadPath(threadID)
	value, _ := jsonlThreadLocks.LoadOrStore(key, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (s *JSONLThreadStore) AppendItems(ctx context.Context, threadID string, items []protocol.Item) (result AppendResult, err error) {
	if err := ctx.Err(); err != nil {
		return AppendResult{}, err
	}
	unlock := s.lockThread(threadID)
	defer unlock()
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return AppendResult{}, err
	}
	existing, err := s.readItemsNoLock(ctx, threadID)
	if err != nil {
		return AppendResult{}, err
	}
	var maxSeq int64
	for _, item := range existing {
		if item.Seq > maxSeq {
			maxSeq = item.Seq
		}
	}
	normalized := make([]protocol.Item, len(items))
	nextSeq := maxSeq + 1
	if nextSeq <= 0 {
		nextSeq = 1
	}
	for i, item := range items {
		originalSeq := item.Seq
		if item.Version == 0 {
			item.Version = protocol.CurrentItemVersion
		}
		if item.ThreadID == "" {
			item.ThreadID = threadID
		}
		if item.At.IsZero() {
			item.At = time.Now().UTC()
		}
		if item.Seq <= maxSeq {
			item.Seq = nextSeq
			nextSeq++
		} else if item.Seq >= nextSeq {
			nextSeq = item.Seq + 1
		}
		if item.ID == "" || item.Seq != originalSeq {
			item.ID = fmt.Sprintf("%s-%06d", threadID, item.Seq)
		}
		normalized[i] = item
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
	for _, item := range normalized {
		if err := enc.Encode(item); err != nil {
			return AppendResult{}, err
		}
	}
	if err := f.Sync(); err != nil {
		return AppendResult{}, err
	}
	var first, last int64
	if len(normalized) > 0 {
		first = normalized[0].Seq
		last = normalized[len(normalized)-1].Seq
	}
	return AppendResult{ThreadID: threadID, FirstSeq: first, LastSeq: last}, nil
}

func (s *JSONLThreadStore) ReadItems(ctx context.Context, threadID string) (items []protocol.Item, err error) {
	unlock := s.lockThread(threadID)
	defer unlock()
	return s.readItemsNoLock(ctx, threadID)
}

func (s *JSONLThreadStore) readItemsNoLock(ctx context.Context, threadID string) (items []protocol.Item, err error) {
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
	// ponytail: 16MB line cap, raise if items legitimately grow past that
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		var item protocol.Item
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return nil, fmt.Errorf("%s: line %d: %w", threadID, line, err)
		}
		items = append(items, item)
	}
	return items, scanner.Err()
}

func (s *JSONLThreadStore) UpdateThreadMetadata(ctx context.Context, threadID string, patch ThreadMetadataPatch) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	unlock := s.lockThread(threadID)
	defer unlock()
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return err
	}
	if patch.UpdatedAt.IsZero() {
		patch.UpdatedAt = time.Now().UTC()
	}
	// Merge onto what is already stored: callers patch individual fields (a
	// title once the first message names the thread, say) and must not blank
	// out the CWD and model recorded when the thread was created.
	patch = mergeMetadata(s.readMetadata(threadID), patch)
	data, err := json.MarshalIndent(patch, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.CreateTemp(s.root, threadID+".meta.*.tmp")
	if err != nil {
		return err
	}
	tmpPath := f.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.metadataPath(threadID))
}

func (s *JSONLThreadStore) ReadThread(ctx context.Context, threadID string) (ThreadRecord, error) {
	items, err := s.ReadItems(ctx, threadID)
	if err != nil {
		return ThreadRecord{}, err
	}
	metadata, err := s.readThreadMetadata(ctx, threadID)
	if err != nil {
		return ThreadRecord{}, err
	}
	return ThreadRecord{ThreadID: threadID, Metadata: metadata, ItemCount: len(items)}, nil
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
	seen := map[string]bool{}
	records := []ThreadRecord{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		threadID := ""
		switch {
		case strings.HasSuffix(name, ".jsonl"):
			threadID = strings.TrimSuffix(name, ".jsonl")
		case strings.HasSuffix(name, ".meta.json"):
			threadID = strings.TrimSuffix(name, ".meta.json")
		default:
			continue
		}
		if seen[threadID] {
			continue
		}
		seen[threadID] = true
		record, err := s.ReadThread(ctx, threadID)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	sort.SliceStable(records, func(i, j int) bool {
		iUpdated := records[i].Metadata.UpdatedAt
		jUpdated := records[j].Metadata.UpdatedAt
		if !iUpdated.Equal(jUpdated) {
			return iUpdated.After(jUpdated)
		}
		return records[i].ThreadID < records[j].ThreadID
	})
	if len(records) > limit {
		records = records[:limit]
	}
	return records, nil
}

func (s *JSONLThreadStore) readThreadMetadata(ctx context.Context, threadID string) (ThreadMetadataPatch, error) {
	if err := ctx.Err(); err != nil {
		return ThreadMetadataPatch{}, err
	}
	data, err := os.ReadFile(s.metadataPath(threadID))
	if errors.Is(err, os.ErrNotExist) {
		return ThreadMetadataPatch{}, nil
	}
	if err != nil {
		return ThreadMetadataPatch{}, err
	}
	var metadata ThreadMetadataPatch
	if err := json.Unmarshal(data, &metadata); err != nil {
		return ThreadMetadataPatch{}, fmt.Errorf("%s metadata: %w", threadID, err)
	}
	return metadata, nil
}

// readMetadata returns the stored metadata for a thread, or the zero value
// when the thread has none yet.
func (s *JSONLThreadStore) readMetadata(threadID string) ThreadMetadataPatch {
	var cur ThreadMetadataPatch
	data, err := os.ReadFile(filepath.Join(s.root, threadID+".meta.json"))
	if err != nil {
		return cur
	}
	_ = json.Unmarshal(data, &cur)
	return cur
}

// mergeMetadata overlays the non-zero fields of patch onto cur.
func mergeMetadata(cur, patch ThreadMetadataPatch) ThreadMetadataPatch {
	if strings.TrimSpace(patch.Title) != "" {
		cur.Title = patch.Title
	}
	if strings.TrimSpace(patch.Preview) != "" {
		cur.Preview = patch.Preview
	}
	if strings.TrimSpace(patch.CWD) != "" {
		cur.CWD = patch.CWD
	}
	if strings.TrimSpace(patch.Model) != "" {
		cur.Model = patch.Model
	}
	if !patch.UpdatedAt.IsZero() {
		cur.UpdatedAt = patch.UpdatedAt
	}
	return cur
}

// DeleteThread removes a thread's items and metadata. Deleting a thread that
// does not exist is not an error.
func (s *JSONLThreadStore) DeleteThread(ctx context.Context, threadID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validThreadID(threadID); err != nil {
		return err
	}
	unlock := s.lockThread(threadID)
	defer unlock()
	for _, path := range []string{s.threadPath(threadID), s.metadataPath(threadID)} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// validThreadID rejects anything that would escape the thread directory. The
// id reaches this package from the UI, so it is untrusted input.
func validThreadID(threadID string) error {
	id := strings.TrimSpace(threadID)
	if id == "" {
		return errors.New("empty thread id")
	}
	if id != filepath.Base(id) || strings.Contains(id, string(filepath.Separator)) {
		return fmt.Errorf("invalid thread id %q", threadID)
	}
	if id == "." || id == ".." || strings.HasPrefix(id, ".") {
		return fmt.Errorf("invalid thread id %q", threadID)
	}
	return nil
}

// SetThreadTitle renames a thread. The id comes from the UI, so it is
// validated rather than joined onto the thread directory as given.
func (s *JSONLThreadStore) SetThreadTitle(ctx context.Context, threadID, title string) error {
	if err := validThreadID(threadID); err != nil {
		return err
	}
	name := strings.TrimSpace(title)
	if name == "" {
		return errors.New("empty thread title")
	}
	if len([]rune(name)) > 120 {
		name = strings.TrimSpace(string([]rune(name)[:120]))
	}
	return s.UpdateThreadMetadata(ctx, threadID, ThreadMetadataPatch{Title: name, UpdatedAt: time.Now().UTC()})
}
