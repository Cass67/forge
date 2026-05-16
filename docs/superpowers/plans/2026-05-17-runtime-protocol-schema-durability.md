# Runtime Protocol, Schema, and Durability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move Forge from patched runtime robustness to Codex-style durable, replayable, schema-checked runtime behavior.

**Architecture:** Introduce a small canonical protocol layer, a thread-store boundary, a live-session persistence facade, JSONL item durability, generated schema fixtures, and replay reducers. Runtime/TUI compatibility stays intact while tests move from string-transcript assertions to structured item assertions.

**Tech Stack:** Go, `internal/protocol`, `internal/react`, `internal/agent/tools`, `internal/llm`, JSONL files, existing Go test suite.

---

## Codex References Used

- `/Users/cass/git/codex/codex-rs/thread-store/README.md`: separates raw history append, metadata patching, `LiveThread`, JSONL rollout storage, and metadata indexing.
- `/Users/cass/git/codex/codex-rs/rollout/src/recorder.rs`: ordered JSONL writer with flush/shutdown and retry semantics.
- `/Users/cass/git/codex/codex-rs/rollout/src/policy.rs`: persistence policy and truncation behavior.
- `/Users/cass/git/codex/codex-rs/protocol/src/protocol.rs`: `Submission`, `Op`, `Event`, `EventMsg`, rollout line/item model, non-exhaustive protocol design.
- `/Users/cass/git/codex/codex-rs/docs/protocol_v1.md`: turn/task/session terminology, `response_id` bookmark, SQ/EQ lifecycle, compatibility aliases.
- `/Users/cass/git/codex/codex-rs/app-server-protocol/tests/schema_fixtures.rs`: generated schema fixture drift tests.
- `/Users/cass/git/codex/codex-rs/tools/src/json_schema.rs`: schema sanitization and OpenAI-compatible JSON Schema subset.

## Gaps In The Previous Plan

- It called in-memory `SessionSnapshot.Items` “durability”; Codex-level durability needs append-only storage, flush barriers, reload, and replay tests.
- It put failure classification in `internal/react`, which cannot safely be consumed by TUI events. Failure classes need to live in `internal/protocol` or `internal/llm`.
- It did not define item-emission ownership. Without that, `Session` and `Runner` can duplicate or miss durable items.
- It exported schemas but did not generate checked-in fixtures or fail tests on drift.
- It did not define a `ThreadStore` boundary, `LiveSession` facade, metadata patching, or list/read/resume/fork semantics.
- It did not include redaction/truncation policy before persistence.
- It did not cover response IDs/bookmarks, turn context, sequence numbers, schema versioning, or terminal item invariants.

## File Structure

- Create `internal/protocol/failure.go`: canonical failure classes and recoverability decisions shared by runtime/TUI/session durability.
- Create `internal/protocol/items.go`: versioned durable item envelope and item payload types.
- Create `internal/protocol/schema.go`: JSON Schema export/sanitization helpers for tool and protocol schemas.
- Create `internal/protocol/schema_fixtures_test.go`: generated schema fixture drift tests.
- Create `internal/protocol/schemas/forge_protocol.schema.json`: checked-in generated protocol schema fixture.
- Create `internal/protocol/schemas/forge_tools.schema.json`: checked-in generated tool schema fixture.
- Create `internal/sessionstore/store.go`: `ThreadStore`, `ThreadMetadataPatch`, `ThreadRecord`, `AppendResult` interfaces/types.
- Create `internal/sessionstore/jsonl_store.go`: local JSONL append/read implementation.
- Create `internal/sessionstore/live_session.go`: active session facade that applies persistence policy, appends items, and updates metadata.
- Create `internal/sessionstore/replay.go`: reducer from durable items to turn summaries and replayable LLM history.
- Modify `internal/react/session.go`: add optional durable sink and item snapshot projection without making storage decisions inside session state.
- Modify `internal/react/loop.go`: emit classified failures/retries/tool items through the session facade.
- Modify `internal/agent/event_render.go` and `internal/llm/types.go`: carry failure classes in events without importing `react` into TUI.
- Modify `internal/runtime/chat.go`: construct local thread store/live session and wire it into `react.Session`.

---

## Task 1: Canonical Failure Classes In Protocol

**Files:**
- Create: `internal/protocol/failure.go`
- Test: `internal/protocol/failure_test.go`
- Modify later: `internal/react/loop.go`, `internal/tui/chatmodel.go`, `internal/llm/types.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/protocol/failure_test.go`:

```go
package protocol

import (
	"errors"
	"testing"
)

func TestClassifyToolArgFailureIsRecoverableAndHidden(t *testing.T) {
	d := ClassifyToolArgFailure("error: read_file.path is required")
	if d.Class != FailureToolArgsInvalid || !d.Recoverable || d.UserVisible {
		t.Fatalf("decision = %#v", d)
	}
}

func TestClassifyAskUserQuestionExecutionFailureIsRecoverable(t *testing.T) {
	d := ClassifyToolExecutionFailure("ask_user_question", errors.New("at least two options are required"))
	if d.Class != FailureToolArgsInvalid || !d.Recoverable || d.UserVisible {
		t.Fatalf("decision = %#v", d)
	}
}

func TestClassifyWriteFileExecutionFailureIsFatal(t *testing.T) {
	d := ClassifyToolExecutionFailure("write_file", errors.New("disk unavailable"))
	if d.Class != FailureToolRuntimeFailed || d.Recoverable || !d.UserVisible {
		t.Fatalf("decision = %#v", d)
	}
}
```

- [ ] **Step 2: Run the test and verify it fails**

Run: `go test -count=1 ./internal/protocol -run TestClassify`

Expected: FAIL because `internal/protocol` and classifier functions do not exist.

- [ ] **Step 3: Add the classifier**

Create `internal/protocol/failure.go`:

```go
package protocol

import "strings"

type FailureClass string

const (
	FailureNone                FailureClass = "none"
	FailureModelOutputInvalid  FailureClass = "model_output_invalid"
	FailureToolArgsInvalid     FailureClass = "tool_args_invalid"
	FailurePolicyBlocked       FailureClass = "policy_blocked"
	FailureToolRuntimeFailed   FailureClass = "tool_runtime_failed"
	FailureProviderUnavailable FailureClass = "provider_unavailable"
	FailureUserCancelled       FailureClass = "user_cancelled"
)

type FailureDecision struct {
	Class       FailureClass `json:"class"`
	Recoverable bool         `json:"recoverable"`
	UserVisible bool         `json:"user_visible"`
	Feedback    string       `json:"feedback,omitempty"`
}

func ClassifyToolArgFailure(feedback string) FailureDecision {
	return FailureDecision{Class: FailureToolArgsInvalid, Recoverable: true, UserVisible: false, Feedback: strings.TrimSpace(feedback)}
}

func ClassifyModelOutputFailure(feedback string) FailureDecision {
	return FailureDecision{Class: FailureModelOutputInvalid, Recoverable: true, UserVisible: false, Feedback: strings.TrimSpace(feedback)}
}

func ClassifyPolicyBlocked(feedback string) FailureDecision {
	return FailureDecision{Class: FailurePolicyBlocked, Recoverable: true, UserVisible: true, Feedback: strings.TrimSpace(feedback)}
}

func ClassifyToolExecutionFailure(toolName string, err error) FailureDecision {
	if err == nil {
		return FailureDecision{Class: FailureNone}
	}
	feedback := "error: " + err.Error()
	if toolName == "ask_user_question" {
		return FailureDecision{Class: FailureToolArgsInvalid, Recoverable: true, UserVisible: false, Feedback: feedback}
	}
	return FailureDecision{Class: FailureToolRuntimeFailed, Recoverable: false, UserVisible: true, Feedback: feedback}
}
```

- [ ] **Step 4: Run the test and verify it passes**

Run: `go test -count=1 ./internal/protocol -run TestClassify`

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git add internal/protocol/failure.go internal/protocol/failure_test.go
git commit -m "feat: add protocol failure classification"
```

Expected: commit succeeds.

---

## Task 2: Versioned Durable Item Protocol

**Files:**
- Create: `internal/protocol/items.go`
- Test: `internal/protocol/items_test.go`

- [ ] **Step 1: Write item serialization tests**

Create `internal/protocol/items_test.go`:

```go
package protocol

import (
	"encoding/json"
	"testing"
	"time"
)

func TestItemEnvelopeRoundTrips(t *testing.T) {
	item := Item{
		Version: 1,
		ID: "item-1",
		ThreadID: "thread-1",
		TurnID: "turn-1",
		Seq: 1,
		Kind: ItemToolCall,
		At: time.Unix(10, 0).UTC(),
		ToolCall: &ToolCallItem{ToolName: "read_file", ToolCallID: "call-1", Args: map[string]any{"path": "README.md"}},
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Item
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Version != 1 || decoded.Kind != ItemToolCall || decoded.ToolCall.ToolName != "read_file" {
		t.Fatalf("decoded = %#v", decoded)
	}
}

func TestTerminalItemsAreExplicit(t *testing.T) {
	complete := Item{Version: 1, Kind: ItemTurnComplete, TurnComplete: &TurnCompleteItem{Status: TurnStatusCompleted}}
	failure := Item{Version: 1, Kind: ItemFailure, Failure: &FailureItem{Decision: FailureDecision{Class: FailureToolRuntimeFailed}}}
	if !complete.IsTerminal() || !failure.IsTerminal() {
		t.Fatalf("terminal checks failed: complete=%v failure=%v", complete.IsTerminal(), failure.IsTerminal())
	}
}
```

- [ ] **Step 2: Run the test and verify it fails**

Run: `go test -count=1 ./internal/protocol -run 'TestItemEnvelopeRoundTrips|TestTerminalItemsAreExplicit'`

Expected: FAIL because item types do not exist.

- [ ] **Step 3: Add item protocol types**

Create `internal/protocol/items.go`:

```go
package protocol

import (
	"time"
	"forge/internal/llm"
)

const CurrentItemVersion = 1

type ItemKind string

const (
	ItemSessionMeta      ItemKind = "session_meta"
	ItemTurnContext      ItemKind = "turn_context"
	ItemUserMessage      ItemKind = "user_message"
	ItemAssistantMessage ItemKind = "assistant_message"
	ItemToolCall         ItemKind = "tool_call"
	ItemToolResult       ItemKind = "tool_result"
	ItemRetry            ItemKind = "retry"
	ItemFailure          ItemKind = "failure"
	ItemStats            ItemKind = "stats"
	ItemCompaction       ItemKind = "compaction"
	ItemTurnComplete     ItemKind = "turn_complete"
)

type TurnStatus string

const (
	TurnStatusCompleted   TurnStatus = "completed"
	TurnStatusFailed      TurnStatus = "failed"
	TurnStatusInterrupted TurnStatus = "interrupted"
)

type Item struct {
	Version          int                `json:"version"`
	ID               string             `json:"id"`
	ThreadID         string             `json:"thread_id"`
	TurnID           string             `json:"turn_id,omitempty"`
	Seq              int64              `json:"seq"`
	Kind             ItemKind           `json:"kind"`
	At               time.Time          `json:"at"`
	SessionMeta      *SessionMetaItem   `json:"session_meta,omitempty"`
	TurnContext      *TurnContextItem   `json:"turn_context,omitempty"`
	Message          *MessageItem       `json:"message,omitempty"`
	ToolCall         *ToolCallItem      `json:"tool_call,omitempty"`
	ToolResult       *ToolResultItem    `json:"tool_result,omitempty"`
	Retry            *RetryItem         `json:"retry,omitempty"`
	Failure          *FailureItem       `json:"failure,omitempty"`
	Stats            *StatsItem         `json:"stats,omitempty"`
	Compaction       *CompactionItem    `json:"compaction,omitempty"`
	TurnComplete     *TurnCompleteItem  `json:"turn_complete,omitempty"`
}

type SessionMetaItem struct {
	Source string `json:"source,omitempty"`
	CWD    string `json:"cwd,omitempty"`
	Model  string `json:"model,omitempty"`
}

type TurnContextItem struct {
	Input      string `json:"input,omitempty"`
	Mode       string `json:"mode,omitempty"`
	ResponseID string `json:"response_id,omitempty"`
}

type MessageItem struct {
	Role string `json:"role"`
	Text string `json:"text,omitempty"`
}

type ToolCallItem struct {
	ToolName   string         `json:"tool_name"`
	ToolCallID string         `json:"tool_call_id"`
	Args       map[string]any `json:"args,omitempty"`
}

type ToolResultItem struct {
	ToolName        string `json:"tool_name"`
	ToolCallID      string `json:"tool_call_id"`
	Text            string `json:"text,omitempty"`
	Diff            string `json:"diff,omitempty"`
	IsError         bool   `json:"is_error,omitempty"`
	Truncated       bool   `json:"truncated,omitempty"`
	OriginalBytes   int    `json:"original_bytes,omitempty"`
}

type RetryItem struct {
	Attempt int    `json:"attempt"`
	Reason  string `json:"reason,omitempty"`
}

type FailureItem struct {
	Decision FailureDecision `json:"decision"`
}

type StatsItem struct {
	DurationMillis int64     `json:"duration_ms,omitempty"`
	Usage          llm.Usage `json:"usage"`
}

type CompactionItem struct {
	Summary string `json:"summary,omitempty"`
}

type TurnCompleteItem struct {
	Status     TurnStatus `json:"status"`
	ResponseID string     `json:"response_id,omitempty"`
}

func (i Item) IsTerminal() bool {
	return i.Kind == ItemTurnComplete || i.Kind == ItemFailure
}
```

- [ ] **Step 4: Run the test and verify it passes**

Run: `go test -count=1 ./internal/protocol -run 'TestItemEnvelopeRoundTrips|TestTerminalItemsAreExplicit'`

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git add internal/protocol/items.go internal/protocol/items_test.go
git commit -m "feat: add durable runtime item protocol"
```

Expected: commit succeeds.

---

## Task 3: Persistence Policy With Redaction And Truncation

**Files:**
- Create: `internal/sessionstore/policy.go`
- Test: `internal/sessionstore/policy_test.go`

- [ ] **Step 1: Write policy tests**

Create `internal/sessionstore/policy_test.go`:

```go
package sessionstore

import (
	"strings"
	"testing"
	"forge/internal/protocol"
)

func TestPersistencePolicyTruncatesLargeToolResult(t *testing.T) {
	policy := DefaultPersistencePolicy()
	item := protocol.Item{Kind: protocol.ItemToolResult, ToolResult: &protocol.ToolResultItem{Text: strings.Repeat("x", policy.MaxToolResultBytes+10)}}
	out := policy.Apply(item)
	if !out.ToolResult.Truncated || len(out.ToolResult.Text) > policy.MaxToolResultBytes {
		t.Fatalf("tool result was not truncated: %#v", out.ToolResult)
	}
}

func TestPersistencePolicyRedactsSecretLookingValues(t *testing.T) {
	policy := DefaultPersistencePolicy()
	item := protocol.Item{Kind: protocol.ItemToolCall, ToolCall: &protocol.ToolCallItem{Args: map[string]any{"token": "sk-secret-value"}}}
	out := policy.Apply(item)
	if out.ToolCall.Args["token"] == "sk-secret-value" {
		t.Fatalf("secret-looking arg was not redacted: %#v", out.ToolCall.Args)
	}
}
```

- [ ] **Step 2: Run the test and verify it fails**

Run: `go test -count=1 ./internal/sessionstore -run TestPersistencePolicy`

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Add the policy**

Create `internal/sessionstore/policy.go`:

```go
package sessionstore

import (
	"strings"
	"forge/internal/protocol"
)

type PersistencePolicy struct {
	MaxToolResultBytes int
}

func DefaultPersistencePolicy() PersistencePolicy {
	return PersistencePolicy{MaxToolResultBytes: 10 * 1024}
}

func (p PersistencePolicy) Apply(item protocol.Item) protocol.Item {
	if item.ToolResult != nil && p.MaxToolResultBytes > 0 && len(item.ToolResult.Text) > p.MaxToolResultBytes {
		item.ToolResult.OriginalBytes = len(item.ToolResult.Text)
		item.ToolResult.Text = item.ToolResult.Text[:p.MaxToolResultBytes]
		item.ToolResult.Truncated = true
	}
	if item.ToolCall != nil && len(item.ToolCall.Args) > 0 {
		redacted := map[string]any{}
		for k, v := range item.ToolCall.Args {
			if strings.Contains(strings.ToLower(k), "token") || strings.Contains(strings.ToLower(k), "secret") || strings.Contains(strings.ToLower(k), "key") {
				redacted[k] = "<REDACTED>"
				continue
			}
			redacted[k] = v
		}
		item.ToolCall.Args = redacted
	}
	return item
}
```

- [ ] **Step 4: Run the test and verify it passes**

Run: `go test -count=1 ./internal/sessionstore -run TestPersistencePolicy`

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git add internal/sessionstore/policy.go internal/sessionstore/policy_test.go
git commit -m "feat: add durable item persistence policy"
```

Expected: commit succeeds.

---

## Task 4: ThreadStore Boundary And JSONL Append

**Files:**
- Create: `internal/sessionstore/store.go`
- Create: `internal/sessionstore/jsonl_store.go`
- Test: `internal/sessionstore/jsonl_store_test.go`

- [ ] **Step 1: Write JSONL store tests**

Create `internal/sessionstore/jsonl_store_test.go`:

```go
package sessionstore

import (
	"context"
	"testing"
	"forge/internal/protocol"
)

func TestJSONLThreadStoreAppendReadRoundTrip(t *testing.T) {
	store := NewJSONLThreadStore(t.TempDir())
	ctx := context.Background()
	threadID := "thread-1"
	items := []protocol.Item{{Version: protocol.CurrentItemVersion, ID: "item-1", ThreadID: threadID, Seq: 1, Kind: protocol.ItemUserMessage, Message: &protocol.MessageItem{Role: "user", Text: "hello"}}}
	if _, err := store.AppendItems(ctx, threadID, items); err != nil {
		t.Fatal(err)
	}
	got, err := store.ReadItems(ctx, threadID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "item-1" {
		t.Fatalf("items = %#v", got)
	}
}

func TestJSONLThreadStoreFlushesBeforeRead(t *testing.T) {
	store := NewJSONLThreadStore(t.TempDir())
	ctx := context.Background()
	threadID := "thread-1"
	if _, err := store.AppendItems(ctx, threadID, []protocol.Item{{Version: 1, ID: "item-1", ThreadID: threadID, Seq: 1, Kind: protocol.ItemRetry, Retry: &protocol.RetryItem{Attempt: 1}}}); err != nil {
		t.Fatal(err)
	}
	got, err := store.ReadItems(ctx, threadID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("items after append = %d, want 1", len(got))
	}
}
```

- [ ] **Step 2: Run the test and verify it fails**

Run: `go test -count=1 ./internal/sessionstore -run TestJSONLThreadStore`

Expected: FAIL because store types do not exist.

- [ ] **Step 3: Add store interfaces**

Create `internal/sessionstore/store.go`:

```go
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
```

- [ ] **Step 4: Add JSONL local store**

Create `internal/sessionstore/jsonl_store.go` with append-only write, explicit `Sync`, and readback:

```go
package sessionstore

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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

func (s *JSONLThreadStore) AppendItems(ctx context.Context, threadID string, items []protocol.Item) (AppendResult, error) {
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
	defer f.Close()
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

func (s *JSONLThreadStore) ReadItems(ctx context.Context, threadID string) ([]protocol.Item, error) {
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
	defer f.Close()
	scanner := bufio.NewScanner(f)
	var items []protocol.Item
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
```

- [ ] **Step 5: Run the test and verify it passes**

Run: `go test -count=1 ./internal/sessionstore -run TestJSONLThreadStore`

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add internal/sessionstore/store.go internal/sessionstore/jsonl_store.go internal/sessionstore/jsonl_store_test.go
git commit -m "feat: add JSONL thread store"
```

Expected: commit succeeds.

---

## Task 5: LiveSession Facade And Metadata Sync

**Files:**
- Create: `internal/sessionstore/live_session.go`
- Test: `internal/sessionstore/live_session_test.go`

- [ ] **Step 1: Write LiveSession tests**

Create `internal/sessionstore/live_session_test.go`:

```go
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
```

- [ ] **Step 2: Run the test and verify it fails**

Run: `go test -count=1 ./internal/sessionstore -run TestLiveSession`

Expected: FAIL because `LiveSession` does not exist.

- [ ] **Step 3: Add LiveSession**

Create `internal/sessionstore/live_session.go`:

```go
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
```

- [ ] **Step 4: Run the test and verify it passes**

Run: `go test -count=1 ./internal/sessionstore -run TestLiveSession`

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git add internal/sessionstore/live_session.go internal/sessionstore/live_session_test.go
git commit -m "feat: add live session persistence facade"
```

Expected: commit succeeds.

---

## Task 6: Replay Reducer And Terminal Invariants

**Files:**
- Create: `internal/sessionstore/replay.go`
- Test: `internal/sessionstore/replay_test.go`

- [ ] **Step 1: Write replay reducer tests**

Create `internal/sessionstore/replay_test.go`:

```go
package sessionstore

import (
	"testing"
	"forge/internal/protocol"
)

func TestReplayBuildsTurnFromDurableItems(t *testing.T) {
	items := []protocol.Item{
		{TurnID: "turn-1", Seq: 1, Kind: protocol.ItemUserMessage, Message: &protocol.MessageItem{Role: "user", Text: "read README"}},
		{TurnID: "turn-1", Seq: 2, Kind: protocol.ItemToolCall, ToolCall: &protocol.ToolCallItem{ToolName: "read_file", ToolCallID: "c1"}},
		{TurnID: "turn-1", Seq: 3, Kind: protocol.ItemToolResult, ToolResult: &protocol.ToolResultItem{ToolName: "read_file", ToolCallID: "c1", Text: "ok"}},
		{TurnID: "turn-1", Seq: 4, Kind: protocol.ItemTurnComplete, TurnComplete: &protocol.TurnCompleteItem{Status: protocol.TurnStatusCompleted}},
	}
	replay, err := ReplayItems(items)
	if err != nil {
		t.Fatal(err)
	}
	if len(replay.Turns) != 1 || len(replay.Turns[0].ToolCalls) != 1 || replay.Turns[0].Status != protocol.TurnStatusCompleted {
		t.Fatalf("replay = %#v", replay)
	}
}

func TestReplayRejectsMultipleTerminalItemsForTurn(t *testing.T) {
	items := []protocol.Item{
		{TurnID: "turn-1", Seq: 1, Kind: protocol.ItemTurnComplete, TurnComplete: &protocol.TurnCompleteItem{Status: protocol.TurnStatusCompleted}},
		{TurnID: "turn-1", Seq: 2, Kind: protocol.ItemFailure, Failure: &protocol.FailureItem{Decision: protocol.FailureDecision{Class: protocol.FailureToolRuntimeFailed}}},
	}
	if _, err := ReplayItems(items); err == nil {
		t.Fatal("expected multiple terminal item error")
	}
}
```

- [ ] **Step 2: Run the test and verify it fails**

Run: `go test -count=1 ./internal/sessionstore -run TestReplay`

Expected: FAIL because `ReplayItems` does not exist.

- [ ] **Step 3: Add replay reducer**

Create `internal/sessionstore/replay.go`:

```go
package sessionstore

import (
	"fmt"
	"sort"
	"forge/internal/protocol"
)

type Replay struct {
	Turns []ReplayTurn
}

type ReplayTurn struct {
	TurnID    string
	Input     string
	Status    protocol.TurnStatus
	ToolCalls []protocol.ToolCallItem
	Results   []protocol.ToolResultItem
}

func ReplayItems(items []protocol.Item) (Replay, error) {
	sorted := append([]protocol.Item(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Seq < sorted[j].Seq })
	turns := map[string]*ReplayTurn{}
	order := []string{}
	terminal := map[string]bool{}
	for _, item := range sorted {
		turnID := item.TurnID
		if turnID == "" {
			turnID = "session"
		}
		turn := turns[turnID]
		if turn == nil {
			turn = &ReplayTurn{TurnID: turnID}
			turns[turnID] = turn
			order = append(order, turnID)
		}
		switch item.Kind {
		case protocol.ItemUserMessage:
			if item.Message != nil {
				turn.Input = item.Message.Text
			}
		case protocol.ItemToolCall:
			if item.ToolCall != nil {
				turn.ToolCalls = append(turn.ToolCalls, *item.ToolCall)
			}
		case protocol.ItemToolResult:
			if item.ToolResult != nil {
				turn.Results = append(turn.Results, *item.ToolResult)
			}
		case protocol.ItemTurnComplete:
			if terminal[turnID] {
				return Replay{}, fmt.Errorf("turn %s has multiple terminal items", turnID)
			}
			terminal[turnID] = true
			if item.TurnComplete != nil {
				turn.Status = item.TurnComplete.Status
			}
		case protocol.ItemFailure:
			if terminal[turnID] {
				return Replay{}, fmt.Errorf("turn %s has multiple terminal items", turnID)
			}
			terminal[turnID] = true
			turn.Status = protocol.TurnStatusFailed
		}
	}
	var out Replay
	for _, id := range order {
		out.Turns = append(out.Turns, *turns[id])
	}
	return out, nil
}
```

- [ ] **Step 4: Run the test and verify it passes**

Run: `go test -count=1 ./internal/sessionstore -run TestReplay`

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git add internal/sessionstore/replay.go internal/sessionstore/replay_test.go
git commit -m "feat: add durable item replay reducer"
```

Expected: commit succeeds.

---

## Task 7: Session Item Emission Ownership

**Files:**
- Modify: `internal/react/session.go`
- Modify: `internal/react/loop.go`
- Test: `internal/react/session_test.go`
- Test: `internal/react/loop_test.go`

- [ ] **Step 1: Add ownership rule to tests**

Add to `internal/react/session_test.go`:

```go
func TestSessionRecordsUserAndAssistantItemsOnce(t *testing.T) {
	s := NewSession()
	turn := s.RecordInput("hello")
	s.AppendAssistantMessage("hi")
	s.CompleteTurn(turn, "hi", nil, nil)
	snap := s.Snapshot()
	var user, assistant, terminal int
	for _, item := range snap.Items {
		switch item.Kind {
		case protocol.ItemUserMessage:
			user++
		case protocol.ItemAssistantMessage:
			assistant++
		case protocol.ItemTurnComplete:
			terminal++
		}
	}
	if user != 1 || assistant != 1 || terminal != 1 {
		t.Fatalf("item counts user=%d assistant=%d terminal=%d items=%#v", user, assistant, terminal, snap.Items)
	}
}
```

- [ ] **Step 2: Run and verify failure**

Run: `go test -count=1 ./internal/react -run TestSessionRecordsUserAndAssistantItemsOnce`

Expected: FAIL because session snapshots do not include items.

- [ ] **Step 3: Implement ownership rule**

Update `internal/react/session.go`:
- `RecordInput`/`RecordInputWithParts` owns `ItemUserMessage` emission.
- `AppendAssistantMessage` owns `ItemAssistantMessage` emission.
- `AppendNativeToolResult` owns `ItemToolResult` emission only when called with a tool call ID.
- `CompleteTurn` owns `ItemTurnComplete` or fatal `ItemFailure` emission.
- `loop.go` owns `ItemToolCall`, `ItemRetry`, recoverable `ItemFailure`, `ItemStats`, and `ItemCompaction` emission.

- [ ] **Step 4: Add loop recovery item test**

Add to `internal/react/loop_test.go`:

```go
func TestRunnerMalformedArgsEmitsDurableFailureItem(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "bad-json", Name: "read_file", ArgsJSON: `{"path":`}}},
		{{Text: "recovered"}},
	}}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{Name: "read_file", Description: "read file", Parameters: []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}}, AutoApprove: true, Execute: func(context.Context, map[string]any) (string, error) { return "", nil }})
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})
	if err := r.Run(context.Background(), "read"); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range session.Snapshot().Items {
		if item.Kind == protocol.ItemFailure && item.Failure.Decision.Class == protocol.FailureToolArgsInvalid && item.Failure.Decision.Recoverable {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing recoverable failure item: %#v", session.Snapshot().Items)
	}
}
```

- [ ] **Step 5: Run and verify**

Run: `go test -count=1 ./internal/react -run 'TestSessionRecordsUserAndAssistantItemsOnce|TestRunnerMalformedArgsEmitsDurableFailureItem'`

Expected: PASS after implementation.

- [ ] **Step 6: Commit**

Run:

```bash
git add internal/react/session.go internal/react/session_test.go internal/react/loop.go internal/react/loop_test.go
git commit -m "feat: emit canonical runtime items"
```

Expected: commit succeeds.

---

## Task 8: Generated Schema Fixtures With Drift Tests

**Files:**
- Create: `internal/protocol/schema.go`
- Create: `internal/protocol/schema_test.go`
- Create: `internal/protocol/schemas/forge_protocol.schema.json`
- Create: `internal/protocol/schemas/forge_tools.schema.json`
- Modify: `internal/runtime/chat_test.go`

- [ ] **Step 1: Add schema generation tests**

Create `internal/protocol/schema_test.go`:

```go
package protocol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestProtocolSchemaFixtureMatchesGenerated(t *testing.T) {
	generated, err := json.MarshalIndent(GenerateProtocolSchema(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile(filepath.Join("schemas", "forge_protocol.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(expected) != string(generated)+"\n" {
		t.Fatalf("protocol schema fixture differs; regenerate internal/protocol/schemas/forge_protocol.schema.json")
	}
}
```

- [ ] **Step 2: Run and verify failure**

Run: `go test -count=1 ./internal/protocol -run TestProtocolSchemaFixtureMatchesGenerated`

Expected: FAIL because schema generator and fixture do not exist.

- [ ] **Step 3: Add generator and fixture**

Create `internal/protocol/schema.go`:

```go
package protocol

import "forge/internal/llm"

type JSONSchema map[string]any

func GenerateProtocolSchema() JSONSchema {
	return JSONSchema{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://forge.local/schemas/forge_protocol.schema.json",
		"title": "Forge Durable Runtime Protocol",
		"type": "object",
		"required": []string{"version", "id", "thread_id", "seq", "kind", "at"},
		"additionalProperties": false,
		"properties": JSONSchema{
			"version": JSONSchema{"type": "integer", "const": CurrentItemVersion},
			"id": JSONSchema{"type": "string"},
			"thread_id": JSONSchema{"type": "string"},
			"turn_id": JSONSchema{"type": "string"},
			"seq": JSONSchema{"type": "integer"},
			"kind": JSONSchema{"type": "string", "enum": []string{string(ItemSessionMeta), string(ItemTurnContext), string(ItemUserMessage), string(ItemAssistantMessage), string(ItemToolCall), string(ItemToolResult), string(ItemRetry), string(ItemFailure), string(ItemStats), string(ItemCompaction), string(ItemTurnComplete)}},
			"at": JSONSchema{"type": "string", "format": "date-time"},
			"session_meta": objectSchema(map[string]any{"source": stringSchema(), "cwd": stringSchema(), "model": stringSchema()}),
			"turn_context": objectSchema(map[string]any{"input": stringSchema(), "mode": stringSchema(), "response_id": stringSchema()}),
			"message": objectSchema(map[string]any{"role": stringSchema(), "text": stringSchema()}),
			"tool_call": objectSchema(map[string]any{"tool_name": stringSchema(), "tool_call_id": stringSchema(), "args": JSONSchema{"type": "object"}}),
			"tool_result": objectSchema(map[string]any{"tool_name": stringSchema(), "tool_call_id": stringSchema(), "text": stringSchema(), "diff": stringSchema(), "is_error": JSONSchema{"type": "boolean"}, "truncated": JSONSchema{"type": "boolean"}, "original_bytes": JSONSchema{"type": "integer"}}),
			"retry": objectSchema(map[string]any{"attempt": JSONSchema{"type": "integer"}, "reason": stringSchema()}),
			"failure": objectSchema(map[string]any{"decision": FailureDecisionSchema()}),
			"stats": objectSchema(map[string]any{"duration_ms": JSONSchema{"type": "integer"}, "usage": JSONSchema{"type": "object"}}),
			"compaction": objectSchema(map[string]any{"summary": stringSchema()}),
			"turn_complete": objectSchema(map[string]any{"status": JSONSchema{"type": "string", "enum": []string{string(TurnStatusCompleted), string(TurnStatusFailed), string(TurnStatusInterrupted)}}, "response_id": stringSchema()}),
		},
	}
}

func FailureDecisionSchema() JSONSchema {
	return objectSchema(map[string]any{
		"class": JSONSchema{"type": "string", "enum": []string{string(FailureNone), string(FailureModelOutputInvalid), string(FailureToolArgsInvalid), string(FailurePolicyBlocked), string(FailureToolRuntimeFailed), string(FailureProviderUnavailable), string(FailureUserCancelled)}},
		"recoverable": JSONSchema{"type": "boolean"},
		"user_visible": JSONSchema{"type": "boolean"},
		"feedback": stringSchema(),
	})
}

func ToolSchemaToJSONSchema(schema *llm.ToolSchema) JSONSchema {
	if schema == nil {
		return nil
	}
	out := JSONSchema{"type": schema.Type}
	if schema.Description != "" {
		out["description"] = schema.Description
	}
	if len(schema.Properties) > 0 {
		props := JSONSchema{}
		for name, prop := range schema.Properties {
			props[name] = ToolSchemaToJSONSchema(prop)
		}
		out["properties"] = props
	}
	if schema.Items != nil {
		out["items"] = ToolSchemaToJSONSchema(schema.Items)
	}
	if len(schema.Required) > 0 {
		out["required"] = schema.Required
	}
	if len(schema.Enum) > 0 {
		out["enum"] = schema.Enum
	}
	if schema.AdditionalProperties != nil {
		out["additionalProperties"] = *schema.AdditionalProperties
	}
	return out
}

func objectSchema(properties map[string]any) JSONSchema {
	return JSONSchema{"type": "object", "additionalProperties": false, "properties": properties}
}

func stringSchema() JSONSchema {
	return JSONSchema{"type": "string"}
}
```

Create `internal/protocol/schemas/forge_protocol.schema.json` by running a temporary local helper or by copying the formatted output from `json.MarshalIndent(GenerateProtocolSchema(), "", "  ")`. The file must exactly match `TestProtocolSchemaFixtureMatchesGenerated`.

- [ ] **Step 4: Add tool schema fixture test**

In `internal/runtime/chat_test.go`, add:

```go
func TestToolSchemaFixtureMatchesGenerated(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "forge.toml"))
	if err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	session := reactruntime.NewSession()
	approve := func(tools.Action) (bool, error) { return true, nil }
	registerTools(reg, t.TempDir(), cfg, session, approve, nil, nil)

	generated := map[string]any{}
	for _, tool := range reg.All() {
		if tool.Schema == nil {
			continue
		}
		generated[tool.Name] = protocol.ToolSchemaToJSONSchema(tool.Schema)
	}
	encoded, err := json.MarshalIndent(generated, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile(filepath.Join("..", "protocol", "schemas", "forge_tools.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(expected) != string(encoded)+"\n" {
		t.Fatal("tool schema fixture differs; regenerate internal/protocol/schemas/forge_tools.schema.json")
	}
}
```

Create `internal/protocol/schemas/forge_tools.schema.json` from the formatted output of the `generated` map in this test.

- [ ] **Step 5: Verify**

Run: `go test -count=1 ./internal/protocol ./internal/runtime -run 'SchemaFixture|ToolSchemaFixture'`

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add internal/protocol/schema.go internal/protocol/schema_test.go internal/protocol/schemas internal/runtime/chat_test.go
git commit -m "test: add protocol schema drift fixtures"
```

Expected: commit succeeds.

---

## Task 9: Tool Schema Sanitization Before Provider Advertisement

**Files:**
- Create: `internal/protocol/tool_schema_sanitize.go`
- Test: `internal/protocol/tool_schema_sanitize_test.go`
- Modify: `internal/agent/tools/registry.go`

- [ ] **Step 1: Write sanitizer edge-case tests**

Create `internal/protocol/tool_schema_sanitize_test.go`:

```go
package protocol

import (
	"testing"
	"forge/internal/llm"
)

func TestSanitizeToolSchemaDefaultsObjectAdditionalPropertiesFalse(t *testing.T) {
	schema := &llm.ToolSchema{Type: "object", Properties: map[string]*llm.ToolSchema{"path": {Type: "string"}}}
	out := SanitizeToolSchema(schema)
	if out.AdditionalProperties == nil || *out.AdditionalProperties != false {
		t.Fatalf("additionalProperties = %#v, want false", out.AdditionalProperties)
	}
}

func TestSanitizeToolSchemaAddsArrayItemsObject(t *testing.T) {
	schema := &llm.ToolSchema{Type: "array"}
	out := SanitizeToolSchema(schema)
	if out.Items == nil || out.Items.Type != "object" {
		t.Fatalf("items = %#v, want object fallback", out.Items)
	}
}
```

- [ ] **Step 2: Run and verify failure**

Run: `go test -count=1 ./internal/protocol -run TestSanitizeToolSchema`

Expected: FAIL because sanitizer does not exist.

- [ ] **Step 3: Add sanitizer**

Create `internal/protocol/tool_schema_sanitize.go`:

```go
package protocol

import "forge/internal/llm"

func SanitizeToolSchema(schema *llm.ToolSchema) *llm.ToolSchema {
	if schema == nil {
		return nil
	}
	out := *schema
	if schema.Properties != nil {
		out.Properties = map[string]*llm.ToolSchema{}
		for name, prop := range schema.Properties {
			out.Properties[name] = SanitizeToolSchema(prop)
		}
	}
	if schema.Items != nil {
		out.Items = SanitizeToolSchema(schema.Items)
	}
	if out.Type == "object" && out.AdditionalProperties == nil {
		additional := false
		out.AdditionalProperties = &additional
	}
	if out.Type == "array" && out.Items == nil {
		out.Items = &llm.ToolSchema{Type: "object"}
	}
	return &out
}
```

- [ ] **Step 4: Use sanitizer when converting tool defs**

In `internal/agent/tools/registry.go`, when producing `llm.ToolDef`, call `protocol.SanitizeToolSchema(tool.Schema)` before returning schema-backed definitions.

- [ ] **Step 5: Verify**

Run: `go test -count=1 ./internal/protocol ./internal/agent/tools ./internal/llm/drivers`

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add internal/protocol/tool_schema_sanitize.go internal/protocol/tool_schema_sanitize_test.go internal/agent/tools/registry.go
git commit -m "fix: sanitize native tool schemas"
```

Expected: commit succeeds.

---

## Task 10: Read, Resume, Fork, And List Semantics

**Files:**
- Modify: `internal/sessionstore/store.go`
- Modify: `internal/sessionstore/jsonl_store.go`
- Test: `internal/sessionstore/thread_ops_test.go`

- [ ] **Step 1: Write thread operation tests**

Create `internal/sessionstore/thread_ops_test.go`:

```go
package sessionstore

import (
	"context"
	"testing"
	"forge/internal/protocol"
)

func TestThreadStoreForkCopiesHistoryWithNewThreadID(t *testing.T) {
	store := NewJSONLThreadStore(t.TempDir())
	ctx := context.Background()
	if _, err := store.AppendItems(ctx, "source", []protocol.Item{{Version: 1, ID: "source-1", ThreadID: "source", Seq: 1, Kind: protocol.ItemUserMessage, Message: &protocol.MessageItem{Role: "user", Text: "hello"}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.ForkThread(ctx, "source", "forked", ForkOptions{}); err != nil {
		t.Fatal(err)
	}
	items, err := store.ReadItems(ctx, "forked")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ThreadID != "forked" {
		t.Fatalf("forked items = %#v", items)
	}
}

func TestThreadStoreListReturnsMetadataOnly(t *testing.T) {
	store := NewJSONLThreadStore(t.TempDir())
	ctx := context.Background()
	if _, err := store.AppendItems(ctx, "thread-1", []protocol.Item{{Version: 1, ID: "item-1", ThreadID: "thread-1", Seq: 1, Kind: protocol.ItemUserMessage, Message: &protocol.MessageItem{Role: "user", Text: "hello"}}}); err != nil {
		t.Fatal(err)
	}
	threads, err := store.ListThreads(ctx, ListOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 || threads[0].ThreadID != "thread-1" || threads[0].ItemCount != 1 {
		t.Fatalf("threads = %#v", threads)
	}
}
```

- [ ] **Step 2: Run and verify failure**

Run: `go test -count=1 ./internal/sessionstore -run 'TestThreadStoreFork|TestThreadStoreList'`

Expected: FAIL because `ForkThread`, `ListThreads`, `ForkOptions`, and `ListOptions` do not exist.

- [ ] **Step 3: Add operations**

Extend `ThreadStore` with:

```go
type ForkOptions struct {
	ExcludeTurnIDs []string
}

type ListOptions struct {
	Limit int
}

func (s *JSONLThreadStore) ForkThread(ctx context.Context, sourceThreadID, newThreadID string, opts ForkOptions) error
func (s *JSONLThreadStore) ListThreads(ctx context.Context, opts ListOptions) ([]ThreadRecord, error)
```

Add this implementation to `internal/sessionstore/jsonl_store.go`:

```go
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
```

Add `strings` to the imports in `internal/sessionstore/jsonl_store.go`.

- [ ] **Step 4: Verify**

Run: `go test -count=1 ./internal/sessionstore -run 'TestThreadStoreFork|TestThreadStoreList'`

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git add internal/sessionstore/store.go internal/sessionstore/jsonl_store.go internal/sessionstore/thread_ops_test.go
git commit -m "feat: add durable thread read list and fork operations"
```

Expected: commit succeeds.

---

## Task 11: Runtime Wiring Without UI Regression

**Files:**
- Modify: `internal/runtime/chat.go`
- Modify: `internal/react/session.go`
- Test: `internal/runtime/chat_test.go`
- Test: `internal/react/loop_test.go`

- [ ] **Step 1: Add runtime wiring test**

Add to `internal/runtime/chat_test.go`:

```go
func TestChatRuntimeCreatesDurableThreadStoreWhenOutputDirConfigured(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "forge.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Session.OutputDir = t.TempDir()
	reg := tools.NewRegistry()
	session := reactruntime.NewSession()
	approve := func(tools.Action) (bool, error) { return true, nil }
	registerTools(reg, t.TempDir(), cfg, session, approve, nil, nil)
	if session.DurableSink() == nil {
		t.Fatal("expected durable sink to be configured")
	}
}
```

- [ ] **Step 2: Run and verify failure**

Run: `go test -count=1 ./internal/runtime -run TestChatRuntimeCreatesDurableThreadStoreWhenOutputDirConfigured`

Expected: FAIL because `DurableSink` is not wired.

- [ ] **Step 3: Add durable sink interface to session**

In `internal/react/session.go`, add:

```go
type DurableSink interface {
	Append(context.Context, protocol.Item) error
}

func (s *Session) SetDurableSink(sink DurableSink)
func (s *Session) DurableSink() DurableSink
```

Implement the accessors in `internal/react/session.go`:

```go
func (s *Session) SetDurableSink(sink DurableSink) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.durableSink = sink
}

func (s *Session) DurableSink() DurableSink {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.durableSink
}
```

Add a private helper that appends in-memory first and best-effort persists after releasing the mutex:

```go
func (s *Session) appendItem(item protocol.Item) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if item.ID == "" {
		item.ID = fmt.Sprintf("session-item-%d", len(s.items)+1)
	}
	if item.At.IsZero() {
		item.At = time.Now().UTC()
	}
	s.items = append(s.items, item)
	sink := s.durableSink
	s.mu.Unlock()
	if sink != nil {
		_ = sink.Append(context.Background(), item)
	}
}
```

Do not call `appendItem` while already holding `s.mu`; build the item inside the lock, release the lock, then call `appendItem`, or add a `lockedAppendItem` variant that never calls the sink.

- [ ] **Step 4: Wire in runtime**

In `internal/runtime/chat.go`, construct:

```go
store := sessionstore.NewJSONLThreadStore(filepath.Join(cfg.Session.OutputDir, "threads"))
live := sessionstore.NewLiveSession(threadID, store, sessionstore.DefaultPersistencePolicy())
session.SetDurableSink(live)
```

Add a deterministic helper in `internal/runtime/chat.go`:

```go
func durableThreadID(session *reactruntime.Session) string {
	if session != nil {
		if snap := session.Snapshot(); strings.TrimSpace(snap.InitialInput) != "" {
			return fmt.Sprintf("thread-%x", sha256.Sum256([]byte(snap.InitialInput)))[:24]
		}
	}
	return fmt.Sprintf("thread-%d", time.Now().UTC().UnixNano())
}
```

Add `crypto/sha256` to imports. Use this helper for the initial implementation; replace it with persisted session IDs when Forge grows explicit thread start/resume commands.

- [ ] **Step 5: Verify runtime and loop tests**

Run: `go test -count=1 ./internal/runtime ./internal/react`

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add internal/runtime/chat.go internal/runtime/chat_test.go internal/react/session.go internal/react/loop_test.go
git commit -m "feat: wire durable session sink"
```

Expected: commit succeeds.

---

## Task 12: Acceptance Gates

**Files:**
- Modify: `internal/sessionstore/replay_test.go`
- Modify: `internal/protocol/schema_test.go`
- Modify: `internal/react/loop_test.go`

- [ ] **Step 1: Add crash/reload acceptance test**

Add to `internal/sessionstore/replay_test.go`:

```go
func TestJSONLDurableItemsReplayAfterStoreReopen(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	store := NewJSONLThreadStore(dir)
	threadID := "thread-1"
	items := []protocol.Item{
		{Version: 1, ID: "item-1", ThreadID: threadID, TurnID: "turn-1", Seq: 1, Kind: protocol.ItemUserMessage, Message: &protocol.MessageItem{Role: "user", Text: "hello"}},
		{Version: 1, ID: "item-2", ThreadID: threadID, TurnID: "turn-1", Seq: 2, Kind: protocol.ItemTurnComplete, TurnComplete: &protocol.TurnCompleteItem{Status: protocol.TurnStatusCompleted}},
	}
	if _, err := store.AppendItems(ctx, threadID, items); err != nil {
		t.Fatal(err)
	}
	reopened := NewJSONLThreadStore(dir)
	loaded, err := reopened.ReadItems(ctx, threadID)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := ReplayItems(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(replay.Turns) != 1 || replay.Turns[0].Status != protocol.TurnStatusCompleted {
		t.Fatalf("replay after reopen = %#v", replay)
	}
}
```

- [ ] **Step 2: Add generated schema drift acceptance**

Add this acceptance test to `internal/protocol/schema_test.go`:

```go
func TestGeneratedSchemasHaveStableEnvelopeFields(t *testing.T) {
	schema := GenerateProtocolSchema()
	props := schema["properties"].(JSONSchema)
	for _, field := range []string{"version", "id", "thread_id", "seq", "kind", "at"} {
		if _, ok := props[field]; !ok {
			t.Fatalf("missing stable envelope field %s in %#v", field, props)
		}
	}
}
```

- [ ] **Step 3: Add runtime structured assertion acceptance**

Add this test to `internal/react/loop_test.go`:

```go
func TestMalformedModelOutputAssertionsUseDurableItems(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "bad-json", Name: "read_file", ArgsJSON: `{"path":`}}},
		{{Text: "recovered"}},
	}}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{Name: "read_file", Description: "read file", Parameters: []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}}, AutoApprove: true, Execute: func(context.Context, map[string]any) (string, error) { return "", nil }})
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})
	if err := r.Run(context.Background(), "read"); err != nil {
		t.Fatal(err)
	}
	for _, item := range session.Snapshot().Items {
		if item.Kind == protocol.ItemFailure && item.Failure.Decision.Class == protocol.FailureToolArgsInvalid {
			return
		}
	}
	t.Fatalf("missing structured malformed-output failure item: %#v", session.Snapshot().Items)
}
```

- [ ] **Step 4: Run package acceptance suite**

Run: `go test -count=1 ./internal/protocol ./internal/sessionstore ./internal/react ./internal/runtime ./internal/tui ./internal/agent/tools ./internal/llm/drivers`

Expected: PASS.

- [ ] **Step 5: Run full suite and build**

Run:

```bash
go test -count=1 ./...
just build
```

Expected: both commands exit 0.

- [ ] **Step 6: Commit**

Run:

```bash
git add internal/protocol internal/sessionstore internal/react internal/runtime internal/tui internal/agent/tools internal/llm/drivers
git commit -m "test: add codex-level durability acceptance gates"
```

Expected: commit succeeds.

---

## Milestones

### Milestone A: Classification And Protocol Types

Complete Tasks 1-3. This makes errors named, item shapes explicit, and persistence policy safe.

### Milestone B: Durable Storage And Replay

Complete Tasks 4-7. This creates actual durability: append, flush, read, replay, terminal invariants, and item ownership.

### Milestone C: Schema Fixtures And Sanitization

Complete Tasks 8-9. This makes protocol/tool contracts drift-detectable like Codex schema fixture tests.

### Milestone D: Runtime Wiring And Operations

Complete Tasks 10-12. This gives read/list/fork/reload behavior and proves crash/reopen replay.

## Done Criteria

- Recoverability decisions are classified by `protocol.FailureClass`, not inferred from strings in TUI code.
- Durable item shapes are versioned and include thread ID, turn ID, sequence, timestamp, terminal states, usage, response IDs, truncation metadata, and failure decisions.
- A `ThreadStore` boundary exists: raw item append is separate from metadata patching.
- A `LiveSession` facade applies persistence policy before appending.
- JSONL storage flushes before reads, supports reopen/replay, and can fork/list threads.
- Replay reducer rejects multiple terminal items per turn.
- Tool and protocol schemas have checked-in generated fixtures with drift tests.
- Tool schemas are sanitized before provider advertisement.
- `go test -count=1 ./...` and `just build` pass.
