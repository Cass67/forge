package sessionstore

import (
	"context"
	"fmt"
	"sync"
	"time"

	"forge/internal/protocol"
)

type LiveSession struct {
	threadID string
	store    ThreadStore
	policy   PersistencePolicy
	mu       sync.Mutex
	nextSeq  int64
}

func NewLiveSession(threadID string, store ThreadStore, policy PersistencePolicy) *LiveSession {
	return &LiveSession{threadID: threadID, store: store, policy: policy}
}

func (l *LiveSession) Append(ctx context.Context, item protocol.Item) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensureNextSeq(ctx); err != nil {
		return err
	}
	if item.Version == 0 {
		item.Version = protocol.CurrentItemVersion
	}
	item.ThreadID = l.threadID
	if item.Seq == 0 {
		item.Seq = l.nextSeq
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
	_, err := l.store.AppendItems(ctx, l.threadID, []protocol.Item{item})
	return err
}

func (l *LiveSession) UpdateMetadata(ctx context.Context, patch ThreadMetadataPatch) error {
	return l.store.UpdateThreadMetadata(ctx, l.threadID, patch)
}

func (l *LiveSession) ThreadID() string {
	return l.threadID
}

func (l *LiveSession) ensureNextSeq(ctx context.Context) error {
	if l.nextSeq > 0 {
		return nil
	}
	items, err := l.store.ReadItems(ctx, l.threadID)
	if err != nil {
		return err
	}
	var maxSeq int64
	for _, item := range items {
		if item.Seq > maxSeq {
			maxSeq = item.Seq
		}
	}
	l.nextSeq = maxSeq + 1
	if l.nextSeq <= 0 {
		l.nextSeq = 1
	}
	return nil
}
