package sessionstore

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"forge/internal/protocol"
)

type LiveSession struct {
	mu          sync.Mutex
	threadID    string
	store       ThreadStore
	policy      PersistencePolicy
	nextSeq     int64
	initialized bool
	titled      bool
}

func NewLiveSession(threadID string, store ThreadStore, policy PersistencePolicy) *LiveSession {
	return &LiveSession{threadID: threadID, store: store, policy: policy, nextSeq: 1}
}

func (l *LiveSession) Append(ctx context.Context, item protocol.Item) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.initNextSeqLocked(ctx); err != nil {
		return err
	}

	if item.Version == 0 {
		item.Version = protocol.CurrentItemVersion
	}
	item.ThreadID = l.threadID
	if item.Seq == 0 {
		item.Seq = l.nextSeq
		l.nextSeq++
	}
	if item.Seq >= l.nextSeq {
		l.nextSeq = item.Seq + 1
	}
	if item.ID == "" {
		item.ID = fmt.Sprintf("%s-%06d", l.threadID, item.Seq)
	}
	if item.At.IsZero() {
		item.At = time.Now().UTC()
	}
	item = l.policy.Apply(item)
	if _, err := l.store.AppendItems(ctx, l.threadID, []protocol.Item{item}); err != nil {
		return err
	}
	l.nameFromFirstMessageLocked(ctx, item)
	return nil
}

// nameFromFirstMessageLocked titles the thread after the first thing the user
// asked, so saved threads are distinguishable in a list. Without it every
// thread is stored under the generic name given at creation time.
func (l *LiveSession) nameFromFirstMessageLocked(ctx context.Context, item protocol.Item) {
	if l.titled || item.Kind != protocol.ItemUserMessage || item.Message == nil {
		return
	}
	l.titled = true
	text := strings.TrimSpace(item.Message.Text)
	if text == "" {
		return
	}
	// Resuming an already-named thread must not rename it from whatever the
	// user happens to type next.
	if record, err := l.store.ReadThread(ctx, l.threadID); err == nil {
		if name := strings.TrimSpace(record.Metadata.Title); name != "" && name != defaultThreadTitle {
			return
		}
	}
	_ = l.store.UpdateThreadMetadata(ctx, l.threadID, ThreadMetadataPatch{
		Title:     ThreadTitle(text),
		Preview:   truncate(text, 140),
		UpdatedAt: time.Now().UTC(),
	})
}

// defaultThreadTitle is the placeholder a thread is created with, before the
// first message gives it a real name.
const defaultThreadTitle = "Forge chat"

// ThreadTitle condenses a user message into a short thread name.
func ThreadTitle(text string) string {
	line := strings.TrimSpace(text)
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	// A leading slash command is the skill name, which is a fine title on its
	// own, but the words after it say more about the thread.
	line = strings.TrimSpace(strings.TrimPrefix(line, "/"))
	line = strings.Join(strings.Fields(line), " ")
	if line == "" {
		return defaultThreadTitle
	}
	return truncate(line, 60)
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max-1])) + "\u2026"
}

func (l *LiveSession) UpdateMetadata(ctx context.Context, patch ThreadMetadataPatch) error {
	return l.store.UpdateThreadMetadata(ctx, l.threadID, patch)
}

func (l *LiveSession) ThreadID() string {
	return l.threadID
}

func (l *LiveSession) initNextSeqLocked(ctx context.Context) error {
	if l.initialized || l.store == nil {
		l.initialized = true
		return nil
	}
	items, err := l.store.ReadItems(ctx, l.threadID)
	if err != nil {
		return err
	}
	if l.nextSeq <= 0 {
		l.nextSeq = 1
	}
	for _, item := range items {
		if item.Seq >= l.nextSeq {
			l.nextSeq = item.Seq + 1
		}
	}
	l.initialized = true
	return nil
}
