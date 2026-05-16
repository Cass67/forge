package sessionstore

import (
	"context"
	"fmt"
	"time"

	"forge/internal/protocol"
)

type LiveSession struct {
	threadID string
	store    ThreadStore
	policy   PersistencePolicy
	nextSeq  int64
}

func NewLiveSession(threadID string, store ThreadStore, policy PersistencePolicy) *LiveSession {
	return &LiveSession{threadID: threadID, store: store, policy: policy, nextSeq: 1}
}

func (l *LiveSession) Append(ctx context.Context, item protocol.Item) error {
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
