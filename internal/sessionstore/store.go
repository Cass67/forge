package sessionstore

import (
	"context"
	"time"

	"forge/internal/protocol"
)

type AppendResult struct {
	ThreadID string
	FirstSeq int64
	LastSeq  int64
}

type ThreadMetadataPatch struct {
	Title     string
	Preview   string
	CWD       string
	Model     string
	UpdatedAt time.Time
}

type ThreadRecord struct {
	ThreadID  string
	Metadata  ThreadMetadataPatch
	ItemCount int
}

type ThreadStore interface {
	AppendItems(ctx context.Context, threadID string, items []protocol.Item) (AppendResult, error)
	ReadItems(ctx context.Context, threadID string) ([]protocol.Item, error)
	UpdateThreadMetadata(ctx context.Context, threadID string, patch ThreadMetadataPatch) error
	ReadThread(ctx context.Context, threadID string) (ThreadRecord, error)
}

type ForkOptions struct {
	ExcludeTurnIDs []string
}

type ListOptions struct {
	Limit int
}
