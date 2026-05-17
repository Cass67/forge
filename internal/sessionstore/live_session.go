package sessionstore

import (
	"context"
	"fmt"
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
	if item.ThreadID == "" {
		item.ThreadID = l.threadID
	}
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
	_, err := l.store.AppendItems(ctx, l.threadID, []protocol.Item{item})
	return err
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
	for _, item := range items {
		if item.Seq >= l.nextSeq {
			l.nextSeq = item.Seq + 1
		}
	}
	l.initialized = true
	return nil
}
