package harness

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type Recorder struct {
	mu      sync.RWMutex
	records []TraceRecord
}

func NewRecorder() *Recorder {
	return &Recorder{}
}

func (r *Recorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = nil
}

func (r *Recorder) Record(record TraceRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now()
	}
	if strings.TrimSpace(record.DebugSummary) == "" {
		record.DebugSummary = FormatDebugSummary(record)
	}
	r.records = append(r.records, record)
}

func (r *Recorder) Add(state RuntimeState, family RequestFamily, step StepKind, worker WorkerKind, reason, topic string) {
	r.Record(TraceRecord{
		State:    state,
		Family:   family,
		Step:     step,
		Worker:   worker,
		Reason:   strings.TrimSpace(reason),
		TopicKey: strings.TrimSpace(topic),
	})
}

func (r *Recorder) Records() []TraceRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]TraceRecord, len(r.records))
	copy(out, r.records)
	return out
}

func FormatDebugSummary(record TraceRecord) string {
	parts := []string{fmt.Sprintf("state=%s", record.State)}
	if record.Family != "" {
		parts = append(parts, fmt.Sprintf("family=%s", record.Family))
	}
	if record.Lane != "" {
		parts = append(parts, fmt.Sprintf("lane=%s", record.Lane))
	}
	if record.Step != "" {
		parts = append(parts, fmt.Sprintf("step=%s", record.Step))
	}
	if record.Worker != "" {
		parts = append(parts, fmt.Sprintf("worker=%s", record.Worker))
	}
	if strings.TrimSpace(record.TopicKey) != "" {
		parts = append(parts, fmt.Sprintf("topic=%s", record.TopicKey))
	}
	if strings.TrimSpace(record.ThreadID) != "" {
		parts = append(parts, fmt.Sprintf("thread_id=%s", record.ThreadID))
	}
	if record.ThreadKind != "" {
		parts = append(parts, fmt.Sprintf("thread_kind=%s", record.ThreadKind))
	}
	if record.ThreadStatus != "" {
		parts = append(parts, fmt.Sprintf("thread_status=%s", record.ThreadStatus))
	}
	if record.ThreadIntent != "" {
		parts = append(parts, fmt.Sprintf("thread_intent=%s", record.ThreadIntent))
	}
	if record.OutcomeKind != "" {
		parts = append(parts, fmt.Sprintf("outcome=%s", record.OutcomeKind))
	}
	if record.DeliverableKind != "" {
		parts = append(parts, fmt.Sprintf("deliverable=%s", record.DeliverableKind))
	}
	if record.DeliverableStatus != "" {
		parts = append(parts, fmt.Sprintf("deliverable_status=%s", record.DeliverableStatus))
	}
	if strings.TrimSpace(record.Reason) != "" {
		parts = append(parts, fmt.Sprintf("reason=%s", record.Reason))
	}
	return strings.Join(parts, " | ")
}
