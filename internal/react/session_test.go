package react

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"forge/internal/hooks"
	"forge/internal/llm"
	"forge/internal/protocol"
	"forge/internal/sessionstore"
)

type fakeDurableSink struct {
	mu    sync.Mutex
	items []protocol.Item
}

func (f *fakeDurableSink) Append(_ context.Context, item protocol.Item) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items = append(f.items, item)
	return nil
}

func (f *fakeDurableSink) Items() []protocol.Item {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]protocol.Item(nil), f.items...)
}

type failingDurableSink struct {
	err error
}

func (f failingDurableSink) Append(_ context.Context, _ protocol.Item) error {
	return f.err
}

func TestSessionRecordsDurableAppendFailure(t *testing.T) {
	s := NewSession()
	s.SetDurableSink(failingDurableSink{err: errors.New("disk full")})
	if err := s.AppendItem(protocol.Item{Kind: protocol.ItemStats, Stats: &protocol.StatsItem{}}); err == nil {
		t.Fatal("expected durable append error")
	}
	snap := s.Snapshot()
	if snap.LastDurableError != "disk full" {
		t.Fatalf("LastDurableError = %q", snap.LastDurableError)
	}
}

func TestAppendAssistantToolTurnReturnsDurableAppendFailure(t *testing.T) {
	s := NewSession()
	s.SetDurableSink(failingDurableSink{err: errors.New("disk full")})
	s.RecordInput("inspect")

	err := s.AppendAssistantToolTurn("checking", []llm.NativeToolCall{{ID: "call-1", Name: "read_file", ArgsJSON: `{"path":"README.md"}`}})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("AppendAssistantToolTurn error = %v, want durable append failure", err)
	}
	if got := s.Snapshot().LastDurableError; got != "disk full" {
		t.Fatalf("LastDurableError = %q, want disk full", got)
	}
}

func mustAppendAssistantMessage(t testing.TB, s *Session, text string) {
	t.Helper()
	if err := s.AppendAssistantMessage(text); err != nil {
		t.Fatal(err)
	}
}

func mustCompleteTurn(t testing.TB, s *Session, turn int, response string, toolCalls []TurnToolCall, turnErr error) {
	t.Helper()
	if err := s.CompleteTurn(turn, response, toolCalls, turnErr); err != nil {
		t.Fatal(err)
	}
}

func TestSessionPersistsRecordInputDurableItems(t *testing.T) {
	for _, tc := range []struct {
		name   string
		record func(*testing.T, *Session) int
	}{
		{name: "RecordInput", record: func(t *testing.T, s *Session) int { return s.RecordInput("hello") }},
		{name: "RecordInputWithParts", record: func(t *testing.T, s *Session) int {
			turn, err := s.RecordInputWithParts("hello", []llm.MessageContentPart{{Type: "text", Text: "hello"}})
			if err != nil {
				t.Fatal(err)
			}
			return turn
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &fakeDurableSink{}
			s := NewSession()
			s.SetDurableSink(sink)

			turn := tc.record(t, s)

			items := sink.Items()
			if turn != 1 {
				t.Fatalf("turn = %d, want 1", turn)
			}
			assertPersistedItemKind(t, items, protocol.ItemUserMessage)
		})
	}
}

func TestSessionPersistsAssistantMessageDurableItem(t *testing.T) {
	sink := &fakeDurableSink{}
	s := NewSession()
	s.SetDurableSink(sink)
	s.RecordInput("hello")

	if err := s.AppendAssistantMessage("hi"); err != nil {
		t.Fatal(err)
	}

	items := sink.Items()
	assertPersistedItemKind(t, items, protocol.ItemAssistantMessage)
}

func TestSessionPersistsNativeToolResultDurableItem(t *testing.T) {
	sink := &fakeDurableSink{}
	s := NewSession()
	s.SetDurableSink(sink)
	s.RecordInput("run ls")
	if err := s.AppendAssistantWithToolCalls([]llm.NativeToolCall{{ID: "c1", Name: "run_command", ArgsJSON: `{"command":"ls"}`}}); err != nil {
		t.Fatal(err)
	}

	if err := s.AppendNativeToolResult("c1", "file1.go"); err != nil {
		t.Fatal(err)
	}

	items := sink.Items()
	assertPersistedItemKind(t, items, protocol.ItemToolResult)
}

func TestSessionAppendToolResultForTurnReturnsDurableAppendError(t *testing.T) {
	sinkErr := errors.New("durable append failed")
	s := NewSession()
	s.RecordInput("run tool")
	_, cancel, err := s.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	s.SetDurableSink(failingDurableSink{err: sinkErr})

	err = s.AppendToolResultForTurn("turn-1", protocol.ToolResultItem{ToolCallID: "call-1", Text: "ok"})
	if !errors.Is(err, sinkErr) {
		t.Fatalf("AppendToolResultForTurn error = %v, want %v", err, sinkErr)
	}
}

func TestSessionPersistsCompleteTurnDurableItems(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		kind protocol.ItemKind
	}{
		{name: "success", kind: protocol.ItemTurnComplete},
		{name: "failure", err: errors.New("boom"), kind: protocol.ItemFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &fakeDurableSink{}
			s := NewSession()
			s.SetDurableSink(sink)
			turn := s.RecordInput("hello")

			if err := s.CompleteTurn(turn, "done", nil, tc.err); err != nil {
				t.Fatal(err)
			}

			items := sink.Items()
			assertPersistedItemKind(t, items, tc.kind)
		})
	}
}

func TestSessionAppendUserMessagePersistsWithoutReplacingTurnInputOnRestore(t *testing.T) {
	s := NewSession()
	s.RecordInput("write the file")
	if err := s.AppendUserMessage("validation failed: gofmt changed files"); err != nil {
		t.Fatal(err)
	}

	snap := s.Snapshot()
	userMessages := 0
	for _, item := range snap.Items {
		if item.Kind == protocol.ItemUserMessage {
			userMessages++
		}
	}
	if userMessages != 2 {
		t.Fatalf("user message items = %d, want original input and validation feedback", userMessages)
	}

	restored, err := RestoreSessionFromItems(snap.Items)
	if err != nil {
		t.Fatal(err)
	}
	restoredSnap := restored.Snapshot()
	if len(restoredSnap.Turns) != 1 || restoredSnap.Turns[0].Input != "write the file" {
		t.Fatalf("restored turns = %#v, want original input preserved", restoredSnap.Turns)
	}
	if len(restoredSnap.History) != 2 || restoredSnap.History[1].Content != "validation failed: gofmt changed files" {
		t.Fatalf("restored history = %#v, want validation feedback restored", restoredSnap.History)
	}
}

func TestSessionDurableSinkOwnsPersistedThreadIdentity(t *testing.T) {
	store := sessionstore.NewJSONLThreadStore(t.TempDir())
	live := sessionstore.NewLiveSession("durable-thread-1", store, sessionstore.DefaultPersistencePolicy())
	s := NewSession()
	s.SetDurableSink(live)

	turn := s.RecordInput("hello")
	if err := s.AppendAssistantMessage("I'll run it."); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendAssistantWithToolCalls([]llm.NativeToolCall{{ID: "c1", Name: "run_command", ArgsJSON: `{"command":"false"}`}}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendNativeToolResult("c1", "exit status 1"); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteTurn(turn, "", nil, errors.New("tool failed")); err != nil {
		t.Fatal(err)
	}

	items, err := store.ReadItems(context.Background(), "durable-thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 5 {
		t.Fatalf("persisted items = %#v, want user, assistant, tool call, tool result, and failure", items)
	}
	assertPersistedItemOrder(t, items, []protocol.ItemKind{
		protocol.ItemUserMessage,
		protocol.ItemAssistantMessage,
		protocol.ItemToolCall,
		protocol.ItemToolResult,
		protocol.ItemFailure,
	})
	for _, item := range items {
		if item.ThreadID != "durable-thread-1" {
			t.Fatalf("persisted ThreadID = %q, want durable-thread-1 for item %#v", item.ThreadID, item)
		}
	}
	for i, item := range items {
		wantSeq := int64(i + 1)
		if item.Seq != wantSeq {
			t.Fatalf("persisted seq for item %d = %d, want %d", i, item.Seq, wantSeq)
		}
	}
	if items[0].ID != "durable-thread-1-000001" || items[4].ID != "durable-thread-1-000005" {
		t.Fatalf("persisted IDs start/end = %q, %q; want durable-thread IDs", items[0].ID, items[4].ID)
	}
	for _, item := range s.Snapshot().Items {
		if item.ThreadID != "session" {
			t.Fatalf("in-memory ThreadID = %q, want session for item %#v", item.ThreadID, item)
		}
	}
}

func TestSessionActiveTurnAllowsOnlyOneOwner(t *testing.T) {
	s := NewSession()

	turn, cancel, err := s.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if turn.ID != "turn-1" || turn.Phase != TurnPhaseCreated {
		t.Fatalf("active turn = %#v, want turn-1 in created phase", turn)
	}

	if _, _, err := s.BeginTurn(context.Background(), "turn-2"); err == nil || !strings.Contains(err.Error(), "active turn") {
		t.Fatalf("overlap error = %v, want active turn error", err)
	}

	if err := s.EndTurn("turn-1", TurnEndReasonCompleted); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.ActiveTurnSnapshot(); ok {
		t.Fatal("active turn still present after EndTurn")
	}

	turn, cancel, err = s.BeginTurn(context.Background(), "turn-2")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if turn.ID != "turn-2" {
		t.Fatalf("active turn ID = %q, want turn-2", turn.ID)
	}
}

func TestSessionCancelActiveTurnCancelsContext(t *testing.T) {
	s := NewSession()
	turn, cancel, err := s.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	if err := s.CancelActiveTurn("user cancelled"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-turn.Context.Done():
	case <-time.After(time.Second):
		t.Fatal("active turn context was not cancelled")
	}
	if _, ok := s.ActiveTurnSnapshot(); !ok {
		t.Fatal("CancelActiveTurn should cancel context without ending the active turn")
	}
}

func TestSessionAppendToolResultForTurnRejectsStaleTurn(t *testing.T) {
	s := NewSession()
	s.RecordInput("run tool")
	_, cancel, err := s.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if err := s.EndTurn("turn-1", TurnEndReasonCancelled); err != nil {
		t.Fatal(err)
	}

	err = s.AppendToolResultForTurn("turn-1", protocol.ToolResultItem{ToolCallID: "call-1", Text: "late"})
	if !errors.Is(err, ErrStaleTurn) {
		t.Fatalf("AppendToolResultForTurn error = %v, want ErrStaleTurn", err)
	}

	for _, item := range s.Snapshot().Items {
		if item.Kind == protocol.ItemToolResult {
			t.Fatalf("stale tool result was appended: %#v", item)
		}
	}
}

func TestSessionAppendToolResultForTurnRejectsCancelledActiveTurn(t *testing.T) {
	s := NewSession()
	s.RecordInput("run tool")
	_, cancel, err := s.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if err := s.CancelActiveTurn("user cancelled"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.ActiveTurnSnapshot(); !ok {
		t.Fatal("cancelled turn should remain present until EndTurn")
	}

	err = s.AppendToolResultForTurn("turn-1", protocol.ToolResultItem{ToolCallID: "call-1", Text: "late"})
	if !errors.Is(err, ErrStaleTurn) {
		t.Fatalf("AppendToolResultForTurn error = %v, want ErrStaleTurn", err)
	}

	for _, item := range s.Snapshot().Items {
		if item.Kind == protocol.ItemToolResult {
			t.Fatalf("stale tool result was appended: %#v", item)
		}
	}
}

func TestSessionAppendToolResultForTurnChecksStaleBeforeEmptyToolCallID(t *testing.T) {
	s := NewSession()
	s.RecordInput("run tool")

	err := s.AppendToolResultForTurn("turn-1", protocol.ToolResultItem{Text: "empty call id"})
	if !errors.Is(err, ErrStaleTurn) {
		t.Fatalf("AppendToolResultForTurn error = %v, want ErrStaleTurn", err)
	}

	_, cancel, err := s.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	err = s.AppendToolResultForTurn("turn-1", protocol.ToolResultItem{Text: "empty call id"})
	if err != nil {
		t.Fatalf("AppendToolResultForTurn live empty ToolCallID error = %v, want nil", err)
	}
	for _, item := range s.Snapshot().Items {
		if item.Kind == protocol.ItemToolResult {
			t.Fatalf("empty ToolCallID should be a no-op, appended: %#v", item)
		}
	}
}

func TestSessionAppendFailureForTurnRejectsStaleTurn(t *testing.T) {
	s := NewSession()
	s.RecordInput("run tool")

	err := s.AppendFailureForTurn("turn-1", protocol.FailureItem{Decision: protocol.ClassifyToolArgFailure("bad args")})
	if !errors.Is(err, ErrStaleTurn) {
		t.Fatalf("AppendFailureForTurn error = %v, want ErrStaleTurn", err)
	}

	for _, item := range s.Snapshot().Items {
		if item.Kind == protocol.ItemFailure {
			t.Fatalf("stale failure was appended: %#v", item)
		}
	}
}

func TestSessionAppendFailureAndToolResultForTurnRejectsCancelledActiveTurn(t *testing.T) {
	s := NewSession()
	s.RecordInput("run tool")
	_, cancel, err := s.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if err := s.CancelActiveTurn("user cancelled"); err != nil {
		t.Fatal(err)
	}

	err = s.AppendFailureAndToolResultForTurn(
		"turn-1",
		protocol.FailureItem{Decision: protocol.ClassifyToolArgFailure("bad args")},
		protocol.ToolCallItem{ToolName: "custom_tool", ToolCallID: "call-1"},
		protocol.ToolResultItem{ToolCallID: "call-1", Text: "bad args"},
	)
	if !errors.Is(err, ErrStaleTurn) {
		t.Fatalf("AppendFailureAndToolResultForTurn error = %v, want ErrStaleTurn", err)
	}

	for _, item := range s.Snapshot().Items {
		if item.Kind == protocol.ItemFailure || item.Kind == protocol.ItemToolResult {
			t.Fatalf("stale item was appended: %#v", item)
		}
	}
}

func TestSessionAppendFailureAndToolResultForTurnChecksStaleBeforeEmptyToolCallID(t *testing.T) {
	s := NewSession()
	s.RecordInput("run tool")
	failure := protocol.FailureItem{Decision: protocol.ClassifyToolArgFailure("bad args")}
	toolCall := protocol.ToolCallItem{ToolName: "custom_tool", ToolCallID: "call-1"}

	err := s.AppendFailureAndToolResultForTurn("turn-1", failure, toolCall, protocol.ToolResultItem{Text: "bad args"})
	if !errors.Is(err, ErrStaleTurn) {
		t.Fatalf("AppendFailureAndToolResultForTurn error = %v, want ErrStaleTurn", err)
	}

	_, cancel, err := s.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	err = s.AppendFailureAndToolResultForTurn("turn-1", failure, toolCall, protocol.ToolResultItem{Text: "bad args"})
	if err != nil {
		t.Fatalf("AppendFailureAndToolResultForTurn live empty ToolCallID error = %v, want nil", err)
	}
	for _, item := range s.Snapshot().Items {
		if item.Kind == protocol.ItemFailure || item.Kind == protocol.ItemToolResult {
			t.Fatalf("empty ToolCallID should be a no-op, appended: %#v", item)
		}
	}
}

func TestSessionAppendToolCallForTurnRejectsCancelledActiveTurn(t *testing.T) {
	s := NewSession()
	s.RecordInput("run tool")
	_, cancel, err := s.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if err := s.CancelActiveTurn("user cancelled"); err != nil {
		t.Fatal(err)
	}

	err = s.AppendToolCallForTurn("turn-1", protocol.ToolCallItem{ToolName: "custom_tool", ToolCallID: "call-1"})
	if !errors.Is(err, ErrStaleTurn) {
		t.Fatalf("AppendToolCallForTurn error = %v, want ErrStaleTurn", err)
	}

	for _, item := range s.Snapshot().Items {
		if item.Kind == protocol.ItemToolCall {
			t.Fatalf("stale tool call was appended: %#v", item)
		}
	}
}

func TestSessionIsActiveTurnRequiresLiveMatchingTurn(t *testing.T) {
	s := NewSession()
	_, cancel, err := s.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	if !s.IsActiveTurn("turn-1") {
		t.Fatal("turn-1 should be active")
	}
	if s.IsActiveTurn("turn-2") {
		t.Fatal("turn-2 should not be active")
	}
	if err := s.CancelActiveTurn("user cancelled"); err != nil {
		t.Fatal(err)
	}
	if s.IsActiveTurn("turn-1") {
		t.Fatal("cancelled turn should not be active")
	}
}

func TestSessionStoresTurnEndAndCancellationReasons(t *testing.T) {
	s := NewSession()
	_, cancel, err := s.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	if err := s.CancelActiveTurn("user cancelled"); err != nil {
		t.Fatal(err)
	}
	if err := s.EndTurn("turn-1", TurnEndReasonCancelled); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if snap.LastTurnEndReason != TurnEndReasonCancelled {
		t.Fatalf("last end reason = %q, want %q", snap.LastTurnEndReason, TurnEndReasonCancelled)
	}
	if snap.LastTurnCancelReason != "user cancelled" {
		t.Fatalf("cancel reason = %q, want user cancelled", snap.LastTurnCancelReason)
	}
}

func assertPersistedItemOrder(t *testing.T, items []protocol.Item, kinds []protocol.ItemKind) {
	t.Helper()
	if len(items) != len(kinds) {
		t.Fatalf("persisted item count = %d, want %d", len(items), len(kinds))
	}
	for i, kind := range kinds {
		if items[i].Kind != kind {
			t.Fatalf("persisted item %d kind = %q, want %q; items=%#v", i, items[i].Kind, kind, items)
		}
	}
}

func assertPersistedItemKind(t *testing.T, items []protocol.Item, kind protocol.ItemKind) {
	t.Helper()
	var matches []protocol.Item
	for _, item := range items {
		if item.Kind == kind {
			matches = append(matches, item)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("persisted items = %#v, want exactly one %s item", items, kind)
	}
	if matches[0].Version == 0 || matches[0].At.IsZero() {
		t.Fatalf("persisted item was not normalized: %#v", matches[0])
	}
}

func TestSessionRecordsUserAndAssistantItemsOnce(t *testing.T) {
	s := NewSession()
	turn := s.RecordInput("hello")
	if err := s.AppendAssistantMessage("hi"); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteTurn(turn, "hi", nil, nil); err != nil {
		t.Fatal(err)
	}
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

func TestSessionPersistsQueuedInput(t *testing.T) {
	s := NewSession()
	s.RecordInput("active turn")
	s.QueuePendingInput("steer toward tests")

	snap := s.Snapshot()
	for _, item := range snap.Items {
		if item.Kind == protocol.ItemTurnContext && item.TurnContext != nil && item.TurnContext.Mode == "queued_input" && item.TurnContext.Input == "steer toward tests" {
			return
		}
	}
	t.Fatalf("queued input item not found: %#v", snap.Items)
}

func TestSessionPersistsAssistantToolCalls(t *testing.T) {
	s := NewSession()
	s.RecordInput("inspect")
	s.AppendAssistantToolTurn("checking", []llm.NativeToolCall{{ID: "call-1", Name: "read_file", ArgsJSON: `{"path":"README.md"}`}})

	snap := s.Snapshot()
	var foundAssistant, foundToolCall bool
	for _, item := range snap.Items {
		if item.Kind == protocol.ItemAssistantMessage && item.Message != nil && item.Message.Text == "checking" {
			foundAssistant = true
		}
		if item.Kind == protocol.ItemToolCall && item.ToolCall != nil && item.ToolCall.ToolName == "read_file" && item.ToolCall.ToolCallID == "call-1" && item.ToolCall.Args["path"] == "README.md" {
			foundToolCall = true
		}
	}
	if !foundAssistant || !foundToolCall {
		t.Fatalf("assistant=%v toolCall=%v items=%#v", foundAssistant, foundToolCall, snap.Items)
	}
}

func TestSessionPersistsToolResultName(t *testing.T) {
	s := NewSession()
	s.RecordInput("inspect")
	s.AppendAssistantToolTurn("", []llm.NativeToolCall{{ID: "call-1", Name: "read_file", ArgsJSON: `{"path":"README.md"}`}})
	s.AppendNativeToolResult("call-1", "contents")

	snap := s.Snapshot()
	for _, item := range snap.Items {
		if item.Kind == protocol.ItemToolResult && item.ToolResult != nil && item.ToolResult.ToolCallID == "call-1" && item.ToolResult.ToolName == "read_file" {
			return
		}
	}
	t.Fatalf("tool result with name not found: %#v", snap.Items)
}

func TestSessionPersistsInterruptedTerminal(t *testing.T) {
	s := NewSession()
	s.RecordInput("run long command")
	s.MarkInterrupted()

	snap := s.Snapshot()
	if !snap.Interrupted {
		t.Fatal("snapshot should remain interrupted")
	}
	for _, item := range snap.Items {
		if item.Kind == protocol.ItemTurnComplete && item.TurnComplete != nil && item.TurnComplete.Status == protocol.TurnStatusInterrupted {
			return
		}
	}
	t.Fatalf("interrupted terminal item not found: %#v", snap.Items)
}

func TestSessionDoesNotAppendSecondTerminalAfterInterrupt(t *testing.T) {
	s := NewSession()
	turn := s.RecordInput("cancel me")
	s.MarkInterrupted()
	s.CompleteTurn(turn, "", nil, errors.New("context canceled"))

	terminals := 0
	for _, item := range s.Snapshot().Items {
		if item.TurnID == "turn-1" && item.IsTerminal() {
			terminals++
		}
	}
	if terminals != 1 {
		t.Fatalf("terminal items = %d, want 1; items=%#v", terminals, s.Snapshot().Items)
	}
}

func TestRecoverableFailureDoesNotBlockCompletedTurn(t *testing.T) {
	s := NewSession()
	turn := s.RecordInput("recover from bad args")
	s.AppendItem(protocol.Item{
		Kind:    protocol.ItemFailure,
		TurnID:  "turn-1",
		Failure: &protocol.FailureItem{Decision: protocol.ClassifyToolArgFailure("bad args")},
	})
	s.CompleteTurn(turn, "recovered", nil, nil)

	for _, item := range s.Snapshot().Items {
		if item.Kind == protocol.ItemTurnComplete && item.TurnComplete != nil && item.TurnComplete.Status == protocol.TurnStatusCompleted {
			return
		}
	}
	t.Fatalf("completed terminal missing after recoverable failure: %#v", s.Snapshot().Items)
}

func TestSessionPersistsCompactionItem(t *testing.T) {
	s := NewSession()
	for _, input := range []string{"one", "two", "three"} {
		turn := s.RecordInput(input)
		s.AppendAssistantMessage("answer " + input)
		s.CompleteTurn(turn, "answer "+input, nil, nil)
	}
	if !s.compact(1) {
		t.Fatal("expected compaction to change state")
	}

	snap := s.Snapshot()
	for _, item := range snap.Items {
		if item.Kind == protocol.ItemCompaction && item.Compaction != nil && strings.TrimSpace(item.Compaction.Summary) != "" {
			return
		}
	}
	t.Fatalf("compaction item not found: %#v", snap.Items)
}

func TestNewSessionFromItemsRebuildsState(t *testing.T) {
	items := []protocol.Item{
		{TurnID: "turn-1", Seq: 1, Kind: protocol.ItemUserMessage, Message: &protocol.MessageItem{Role: string(llm.RoleUser), Text: "run tests"}},
		{TurnID: "turn-1", Seq: 2, Kind: protocol.ItemAssistantMessage, Message: &protocol.MessageItem{Role: string(llm.RoleAssistant), Text: "running"}},
		{TurnID: "turn-1", Seq: 3, Kind: protocol.ItemToolCall, ToolCall: &protocol.ToolCallItem{ToolName: "run_command", ToolCallID: "call-1"}},
		{TurnID: "turn-1", Seq: 4, Kind: protocol.ItemToolResult, ToolResult: &protocol.ToolResultItem{ToolName: "run_command", ToolCallID: "call-1", Text: "ok"}},
		{Seq: 5, Kind: protocol.ItemCompaction, Compaction: &protocol.CompactionItem{Summary: "compacted old turns"}},
		{TurnID: "turn-1", Seq: 6, Kind: protocol.ItemTurnComplete, TurnComplete: &protocol.TurnCompleteItem{Status: protocol.TurnStatusInterrupted}},
	}
	s, err := NewSessionFromItems(items)
	if err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if snap.Turn != 1 || len(snap.History) != 3 || len(snap.Turns) != 1 || !snap.Interrupted {
		t.Fatalf("snapshot = %#v", snap)
	}
	if snap.Turns[0].Input != "run tests" || snap.Turns[0].Error != "interrupted" {
		t.Fatalf("turns = %#v", snap.Turns)
	}
	if snap.CompactionSummary != "compacted old turns" || len(snap.RecentInputs) != 1 || snap.RecentInputs[0] != "run tests" {
		t.Fatalf("compaction/recent = %q %#v", snap.CompactionSummary, snap.RecentInputs)
	}
}

func TestNewSessionFromItemsReplaysSessionEmittedToolTurnAsSingleTurn(t *testing.T) {
	s := NewSession()
	turn := s.RecordInput("inspect")
	s.AppendAssistantToolTurn("checking", []llm.NativeToolCall{{ID: "call-1", Name: "read_file", ArgsJSON: `{"path":"README.md"}`}})
	s.AppendNativeToolResult("call-1", "contents")
	s.CompleteTurn(turn, "done", nil, nil)

	replayed, err := NewSessionFromItems(s.Snapshot().Items)
	if err != nil {
		t.Fatal(err)
	}
	snap := replayed.Snapshot()
	if len(snap.Turns) != 1 || snap.Turns[0].Input != "inspect" || len(snap.Turns[0].ToolCalls) != 1 || snap.Turns[0].ToolCalls[0].Name != "read_file" {
		t.Fatalf("turns = %#v", snap.Turns)
	}
}

func TestNewSessionFromItemsRestoresQueuedInputWithoutOverwritingTurnInput(t *testing.T) {
	items := []protocol.Item{
		{TurnID: "turn-1", Seq: 1, Kind: protocol.ItemUserMessage, Message: &protocol.MessageItem{Role: string(llm.RoleUser), Text: "original turn"}},
		{TurnID: "turn-1", Seq: 2, Kind: protocol.ItemTurnContext, TurnContext: &protocol.TurnContextItem{Mode: "queued_input", Input: "queued steering"}},
	}
	s, err := NewSessionFromItems(items)
	if err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if len(snap.Turns) != 1 || snap.Turns[0].Input != "original turn" {
		t.Fatalf("turns = %#v", snap.Turns)
	}
	if len(snap.PendingInput) != 1 || snap.PendingInput[0] != "queued steering" {
		t.Fatalf("pending input = %#v", snap.PendingInput)
	}
}

func TestNewSessionFromItemsDoesNotRestoreConsumedQueuedInput(t *testing.T) {
	s := NewSession()
	s.RecordInput("original turn")
	s.QueuePendingInput("queued steering")
	s.appendQueuedUserInput("queued steering")
	replayed, err := NewSessionFromItems(s.Snapshot().Items)
	if err != nil {
		t.Fatal(err)
	}
	if pending := replayed.Snapshot().PendingInput; len(pending) != 0 {
		t.Fatalf("pending input = %#v", pending)
	}
}

func TestNewSessionFromItemsKeepsOriginalInputAfterAppliedQueuedMessage(t *testing.T) {
	items := []protocol.Item{
		{TurnID: "turn-1", Seq: 1, Kind: protocol.ItemUserMessage, Message: &protocol.MessageItem{Role: string(llm.RoleUser), Text: "original turn"}},
		{TurnID: "turn-1", Seq: 2, Kind: protocol.ItemUserMessage, Message: &protocol.MessageItem{Role: string(llm.RoleUser), Text: "applied queued steering"}},
	}
	s, err := NewSessionFromItems(items)
	if err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if len(snap.Turns) != 1 || snap.Turns[0].Input != "original turn" {
		t.Fatalf("turns = %#v", snap.Turns)
	}
}

func TestAppendAssistantWithToolCalls(t *testing.T) {
	s := NewSession()
	s.RecordInput("check the repo")
	calls := []llm.NativeToolCall{
		{ID: "c1", Name: "git_status", ArgsJSON: `{}`},
		{ID: "c2", Name: "run_command", ArgsJSON: `{"command":"ls"}`},
	}
	s.AppendAssistantWithToolCalls(calls)

	snap := s.Snapshot()
	if len(snap.History) != 2 {
		t.Fatalf("want 2 history entries, got %d", len(snap.History))
	}
	last := snap.History[1]
	if last.Role != llm.RoleAssistant {
		t.Fatalf("role = %q, want assistant", last.Role)
	}
	if len(last.ToolCalls) != 2 {
		t.Fatalf("tool calls = %d, want 2", len(last.ToolCalls))
	}
	if last.ToolCalls[0].ID != "c1" || last.ToolCalls[0].Name != "git_status" {
		t.Fatal("first tool call mismatch")
	}
	if len(snap.Turns) != 1 || len(snap.Turns[0].ToolCalls) != 2 {
		t.Fatalf("turn tool calls = %#v", snap.Turns)
	}
	if snap.Turns[0].ToolCalls[1].Name != "run_command" {
		t.Fatalf("turn tool calls = %#v", snap.Turns[0].ToolCalls)
	}
}

func TestAppendAssistantToolTurnPreservesPreamble(t *testing.T) {
	s := NewSession()
	s.RecordInput("check the repo")
	s.AppendAssistantToolTurn("I'll inspect the README first.", []llm.NativeToolCall{
		{ID: "c1", Name: "read_file", ArgsJSON: `{"path":"README.md"}`},
	})

	snap := s.Snapshot()
	if len(snap.History) != 2 {
		t.Fatalf("want 2 history entries, got %d", len(snap.History))
	}
	last := snap.History[1]
	if got, want := last.Content, "I'll inspect the README first."; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
	if len(last.ToolCalls) != 1 || last.ToolCalls[0].Name != "read_file" {
		t.Fatalf("tool calls = %#v", last.ToolCalls)
	}
}

func TestAppendAssistantToolTurnRedactsSecretArgsInHistory(t *testing.T) {
	s := NewSession()
	s.RecordInput("write the file")
	secret := "TOKEN=" + strings.Repeat("x", 24)
	s.AppendAssistantToolTurn("", []llm.NativeToolCall{{
		ID:       "c1",
		Name:     "write_file",
		ArgsJSON: `{"path":"note.txt","content":"` + secret + `"}`,
	}})

	snap := s.Snapshot()
	if got := snap.History[1].ToolCalls[0].ArgsJSON; strings.Contains(got, secret) {
		t.Fatalf("stored tool args leaked secret: %s", got)
	}
	if got := snap.History[1].ToolCalls[0].ArgsJSON; !strings.Contains(got, "<REDACTED:generic-token>") {
		t.Fatalf("stored tool args missing redaction marker: %s", got)
	}
}

func TestAppendAssistantToolTurnRedactsSecretPreambleInHistory(t *testing.T) {
	s := NewSession()
	s.RecordInput("write the file")
	secret := "TOKEN=" + strings.Repeat("x", 24)
	s.AppendAssistantToolTurn("using "+secret, []llm.NativeToolCall{{
		ID:       "c1",
		Name:     "write_file",
		ArgsJSON: `{"path":"note.txt","content":"ok"}`,
	}})

	snap := s.Snapshot()
	content := snap.History[1].Content
	if strings.Contains(content, secret) {
		t.Fatal("stored assistant preamble leaked secret")
	}
	if !strings.Contains(content, "<REDACTED:generic-token>") {
		t.Fatal("stored assistant preamble missing redaction marker")
	}
}

func TestSetLastAssistantReasoningRedactsSecrets(t *testing.T) {
	s := NewSession()
	s.RecordInput("write the file")
	s.AppendAssistantToolTurn("", []llm.NativeToolCall{{ID: "c1", Name: "git_status", ArgsJSON: `{}`}})
	secret := "TOKEN=" + strings.Repeat("x", 24)
	s.SetLastAssistantReasoning("saw " + secret)

	snap := s.Snapshot()
	reasoning := snap.History[1].ReasoningContent
	if strings.Contains(reasoning, secret) {
		t.Fatal("stored assistant reasoning leaked secret")
	}
	if !strings.Contains(reasoning, "<REDACTED:generic-token>") {
		t.Fatal("stored assistant reasoning missing redaction marker")
	}
}

func TestAppendNativeToolResult(t *testing.T) {
	s := NewSession()
	s.RecordInput("run ls")
	s.AppendAssistantWithToolCalls([]llm.NativeToolCall{
		{ID: "c1", Name: "run_command", ArgsJSON: `{"command":"ls"}`},
	})
	if err := s.AppendNativeToolResult("c1", "file1.go\nfile2.go"); err != nil {
		t.Fatal(err)
	}

	snap := s.Snapshot()
	if len(snap.History) != 3 {
		t.Fatalf("want 3 history entries, got %d", len(snap.History))
	}
	result := snap.History[2]
	if result.Role != llm.RoleTool {
		t.Fatalf("role = %q, want tool", result.Role)
	}
	if result.ToolCallID != "c1" {
		t.Fatalf("tool_call_id = %q, want c1", result.ToolCallID)
	}
	if result.Content != "file1.go\nfile2.go" {
		t.Fatalf("content = %q", result.Content)
	}
}

func TestAppendNativeToolResultGuardsEmptyID(t *testing.T) {
	s := NewSession()
	s.RecordInput("check")
	s.AppendAssistantWithToolCalls([]llm.NativeToolCall{{ID: "c1", Name: "git_status", ArgsJSON: "{}"}})
	if err := s.AppendNativeToolResult("", "result"); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	// Empty toolCallID should be ignored — only user+assistant in history
	if len(snap.History) != 2 {
		t.Fatalf("empty toolCallID should be ignored, got %d history entries", len(snap.History))
	}
}

func TestMessagesIncludesToolRoleMessages(t *testing.T) {
	s := NewSession()
	s.RecordInput("check status")
	s.AppendAssistantWithToolCalls([]llm.NativeToolCall{
		{ID: "c1", Name: "git_status", ArgsJSON: `{}`},
	})
	if err := s.AppendNativeToolResult("c1", "nothing to commit"); err != nil {
		t.Fatal(err)
	}

	msgs := s.Messages("system prompt")
	// system + user + assistant(tool_calls) + tool(result)
	if len(msgs) != 4 {
		t.Fatalf("want 4 messages, got %d\nhistory: %+v", len(msgs), msgs)
	}
	if msgs[2].Role != llm.RoleAssistant || len(msgs[2].ToolCalls) != 1 {
		t.Fatal("message 3 should be assistant with tool calls")
	}
	if msgs[3].Role != llm.RoleTool || msgs[3].ToolCallID != "c1" {
		t.Fatal("message 4 should be tool result with correct ID")
	}
}

func TestSessionTaskStateAppearsInSnapshot(t *testing.T) {
	s := NewSession()
	s.SetTaskState(TaskState{
		Objective:            "merge feature/go-rewrite into main",
		RequiredVerification: "verify main contains the resulting commit",
	})

	snap := s.Snapshot()
	if snap.TaskState == nil {
		t.Fatal("expected task state in snapshot")
	}
	if snap.TaskState.Objective != "merge feature/go-rewrite into main" {
		t.Fatalf("objective = %q", snap.TaskState.Objective)
	}
	if snap.TaskState.RequiredVerification != "verify main contains the resulting commit" {
		t.Fatalf("required verification = %q", snap.TaskState.RequiredVerification)
	}
}

func TestSessionPlanStateAppearsInSnapshot(t *testing.T) {
	s := NewSession()
	s.SetPlanState(PlanState{
		Explanation: "Doing the runtime work in slices",
		Steps: []PlanStep{
			{Step: "Inspect code", Status: "completed"},
			{Step: "Patch runtime", Status: "in_progress"},
		},
	})

	snap := s.Snapshot()
	if snap.PlanState == nil || len(snap.PlanState.Steps) != 2 {
		t.Fatalf("plan state = %#v", snap.PlanState)
	}
}

func TestSessionPendingDelegationActionAppearsInSnapshotAndClears(t *testing.T) {
	s := NewSession()
	s.SetPendingDelegationAction(DelegationActionState{
		Kind:        DelegationActionWriteDoc,
		TargetPath:  "docs/reports/audit.md",
		SourceAgent: "agent-1",
		Description: "write delegated audit report",
	})

	snap := s.Snapshot()
	if snap.PendingDelegationAction == nil {
		t.Fatal("expected pending delegation action")
	}
	if snap.PendingDelegationAction.Kind != DelegationActionWriteDoc || snap.PendingDelegationAction.TargetPath != "docs/reports/audit.md" {
		t.Fatalf("pending delegation action = %#v", snap.PendingDelegationAction)
	}
	snap.PendingDelegationAction.TargetPath = "mutated.md"
	if got := s.Snapshot().PendingDelegationAction.TargetPath; got != "docs/reports/audit.md" {
		t.Fatalf("session action mutated through snapshot: %q", got)
	}

	s.ClearPendingDelegationAction()
	if got := s.Snapshot().PendingDelegationAction; got != nil {
		t.Fatalf("pending delegation action after clear = %#v", got)
	}
}

func TestSessionPendingDelegationActionKinds(t *testing.T) {
	for _, kind := range []DelegationActionKind{
		DelegationActionWriteDoc,
		DelegationActionRunVerification,
		DelegationActionCommit,
		DelegationActionAskUser,
	} {
		t.Run(string(kind), func(t *testing.T) {
			s := NewSession()
			s.SetPendingDelegationAction(DelegationActionState{Kind: kind})
			if got := s.Snapshot().PendingDelegationAction; got == nil || got.Kind != kind {
				t.Fatalf("pending action for %q = %#v", kind, got)
			}
		})
	}
}

func TestSessionAgentTaskStateTracksLifecycle(t *testing.T) {
	s := NewSession()
	turn := s.RecordInput("audit the repo")
	created := time.Date(2026, 5, 8, 20, 30, 0, 0, time.UTC)
	started := created.Add(time.Second)
	progressAt := started.Add(2 * time.Second)
	completed := progressAt.Add(3 * time.Second)

	s.UpsertAgentTask(AgentTaskState{
		ID:          " agent-1 ",
		Role:        " repo-auditor ",
		Description: " Inspect repository ",
		Prompt:      " Read files and report findings ",
		Status:      AgentStatusPending,
		CreatedAt:   created,
		ParentTurn:  turn,
	})
	s.UpsertAgentTask(AgentTaskState{
		ID:             "agent-1",
		Status:         AgentStatusRunning,
		StartedAt:      started,
		LastActivityAt: started,
	})
	s.RecordAgentTaskProgress("agent-1", "read_file", "README.md", progressAt)
	s.UpsertAgentTask(AgentTaskState{
		ID:             "agent-1",
		Status:         AgentStatusCompleted,
		CompletedAt:    completed,
		LastActivityAt: completed,
		Result:         "done",
	})

	snap := s.Snapshot()
	if len(snap.AgentTasks) != 1 {
		t.Fatalf("agent tasks = %#v", snap.AgentTasks)
	}
	task := snap.AgentTasks[0]
	if task.ID != "agent-1" || task.Role != "repo-auditor" || task.Description != "Inspect repository" || task.Prompt != "Read files and report findings" {
		t.Fatalf("normalized task identity = %#v", task)
	}
	if task.Status != AgentStatusCompleted || task.ParentTurn != turn {
		t.Fatalf("task status/turn = %#v", task)
	}
	if !task.CreatedAt.Equal(created) || !task.StartedAt.Equal(started) || !task.CompletedAt.Equal(completed) || !task.LastActivityAt.Equal(completed) {
		t.Fatalf("task timestamps = %#v", task)
	}
	if task.Result != "done" || task.Error != "" {
		t.Fatalf("task terminal data = %#v", task)
	}
	if task.LastToolName != "read_file" || len(task.RecentActivity) != 1 || task.RecentActivity[0].Summary != "README.md" {
		t.Fatalf("task progress = %#v", task)
	}
}

func TestSessionAgentTaskStateCoversTerminalStatuses(t *testing.T) {
	s := NewSession()
	statuses := []AgentStatus{
		AgentStatusFailed,
		AgentStatusKilled,
		AgentStatusTimeout,
		AgentStatusNotFound,
	}
	for _, status := range statuses {
		s.UpsertAgentTask(AgentTaskState{ID: string(status), Status: status, Error: "terminal"})
	}

	snap := s.Snapshot()
	if len(snap.AgentTasks) != len(statuses) {
		t.Fatalf("agent tasks = %#v", snap.AgentTasks)
	}
	for i, status := range statuses {
		if snap.AgentTasks[i].Status != status {
			t.Fatalf("agent task %d status = %q, want %q", i, snap.AgentTasks[i].Status, status)
		}
	}
}

func TestSessionAgentTaskSnapshotIsCloned(t *testing.T) {
	s := NewSession()
	s.UpsertAgentTask(AgentTaskState{ID: "agent-1", Status: AgentStatusRunning})
	s.RecordAgentTaskProgress("agent-1", "list_dir", ".", time.Now())

	snap := s.Snapshot()
	snap.AgentTasks[0].Status = AgentStatusCompleted
	snap.AgentTasks[0].RecentActivity[0].ToolName = "mutated"

	next := s.Snapshot()
	if next.AgentTasks[0].Status != AgentStatusRunning {
		t.Fatalf("session task status mutated through snapshot: %#v", next.AgentTasks[0])
	}
	if next.AgentTasks[0].RecentActivity[0].ToolName != "list_dir" {
		t.Fatalf("session task activity mutated through snapshot: %#v", next.AgentTasks[0])
	}
}

func TestSessionClearRemovesAgentTaskState(t *testing.T) {
	s := NewSession()
	s.UpsertAgentTask(AgentTaskState{ID: "agent-1", Status: AgentStatusRunning})
	s.Clear()

	if got := s.Snapshot().AgentTasks; len(got) != 0 {
		t.Fatalf("agent tasks after clear = %#v", got)
	}
}

func TestPlanStateHelpersExposeActiveAndBlockedSteps(t *testing.T) {
	state := PlanState{
		Steps: []PlanStep{
			{Step: "Wait for approval", Status: "blocked", Blocker: "need user confirmation"},
			{Step: "Patch runtime", Status: "pending"},
		},
	}

	if !state.HasActiveStep() {
		t.Fatal("expected active step")
	}
	active, ok := state.ActiveStep()
	if !ok {
		t.Fatal("expected active step details")
	}
	if active.Step != "Wait for approval" {
		t.Fatalf("active step = %#v", active)
	}
	blocked, ok := state.BlockedStep()
	if !ok {
		t.Fatal("expected blocked step details")
	}
	if blocked.Blocker != "need user confirmation" {
		t.Fatalf("blocked step = %#v", blocked)
	}
}

func TestPlanStateHelpersReportNoActiveStepWhenAllCompleted(t *testing.T) {
	state := PlanState{
		Steps: []PlanStep{
			{Step: "Inspect code", Status: "completed"},
			{Step: "Patch runtime", Status: "completed"},
		},
	}

	if state.HasActiveStep() {
		t.Fatal("did not expect active step")
	}
	if _, ok := state.ActiveStep(); ok {
		t.Fatal("did not expect active step details")
	}
	if _, ok := state.BlockedStep(); ok {
		t.Fatal("did not expect blocked step details")
	}
}

func TestSessionQueuesAndDrainsPendingInput(t *testing.T) {
	s := NewSession()
	s.QueuePendingInput("steer toward tests")
	s.QueuePendingInput("focus on service/main.py")

	if !s.HasPendingInput() {
		t.Fatal("expected pending input")
	}
	got := s.TakePendingInput()
	if len(got) != 2 {
		t.Fatalf("pending input = %#v", got)
	}
	if got[0] != "steer toward tests" || got[1] != "focus on service/main.py" {
		t.Fatalf("pending input order = %#v", got)
	}
	if s.HasPendingInput() {
		t.Fatal("expected pending input to be drained")
	}
}

func TestSessionSetHookOverlayUpsertsByKey(t *testing.T) {
	s := NewSession()
	s.SetHookOverlay(HookOverlay{
		Key:        "suggested_skill",
		Content:    "first",
		Priority:   HookPriorityNormal,
		Provenance: "runtime",
	})
	s.SetHookOverlay(HookOverlay{
		Key:        "suggested_skill",
		Content:    "second",
		Priority:   HookPriorityHigh,
		Provenance: "runtime",
	})

	got := s.Snapshot().HookOverlays
	if len(got) != 1 {
		t.Fatalf("hook overlays = %#v", got)
	}
	if got[0].Content != "second" || got[0].Priority != HookPriorityHigh {
		t.Fatalf("hook overlays = %#v", got)
	}
	if len(s.Snapshot().HookOutput.Overlays) != 1 {
		t.Fatalf("hook output overlays = %#v", s.Snapshot().HookOutput.Overlays)
	}
	if s.Snapshot().HookOutput.Overlays[0].Content != "second" {
		t.Fatalf("hook output overlays = %#v", s.Snapshot().HookOutput.Overlays)
	}
}

func TestSessionClearHookOverlayRemovesMatchingKey(t *testing.T) {
	s := NewSession()
	s.SetHookOverlays([]HookOverlay{
		{Key: "suggested_skill", Content: "first"},
		{Key: "plan_blocker", Content: "second"},
	})

	s.ClearHookOverlay("suggested_skill")

	got := s.Snapshot().HookOverlays
	if len(got) != 1 || got[0].Key != "plan_blocker" {
		t.Fatalf("hook overlays = %#v", got)
	}
	if got := s.Snapshot().HookOutput.Overlays; len(got) != 1 || got[0].Key != "plan_blocker" {
		t.Fatalf("hook output overlays = %#v", got)
	}
}

func TestSessionSetHookOutputStoresNormalizedHookState(t *testing.T) {
	s := NewSession()
	s.SetHookOutput(hooks.ExecutionOutput{
		Overlays: []hooks.OverlayResult{{
			Key:        "suggested_skill",
			Content:    "Use the TDD workflow before editing runtime behavior.",
			Priority:   hooks.PriorityHigh,
			Provenance: "runtime",
		}},
		Note: &hooks.NoteResult{
			Message:    "Runtime note from normalized hook output.",
			Priority:   hooks.PriorityNormal,
			Provenance: "runtime",
		},
	})

	snap := s.Snapshot()
	if len(snap.HookOutput.Overlays) != 1 {
		t.Fatalf("hook output overlays = %#v", snap.HookOutput.Overlays)
	}
	if snap.HookOutput.Note == nil || snap.HookOutput.Note.Message != "Runtime note from normalized hook output." {
		t.Fatalf("hook output note = %#v", snap.HookOutput.Note)
	}
	if len(snap.HookOverlays) != 1 || snap.HookOverlays[0].Key != "suggested_skill" {
		t.Fatalf("hook overlays = %#v", snap.HookOverlays)
	}
	if snap.RuntimeNote != "Runtime note from normalized hook output." {
		t.Fatalf("runtime note = %q", snap.RuntimeNote)
	}
}

func TestRestoreSessionFromItemsMarksUnterminatedTurnResumable(t *testing.T) {
	items := []protocol.Item{
		{Version: 1, ID: "item-1", ThreadID: "thread-1", TurnID: "turn-1", Seq: 1, Kind: protocol.ItemUserMessage, Message: &protocol.MessageItem{Role: "user", Text: "read README"}},
		{Version: 1, ID: "item-2", ThreadID: "thread-1", TurnID: "turn-1", Seq: 2, Kind: protocol.ItemAssistantMessage, Message: &protocol.MessageItem{Role: "assistant", Text: "I'll inspect it."}},
		{Version: 1, ID: "item-3", ThreadID: "thread-1", TurnID: "turn-1", Seq: 3, Kind: protocol.ItemToolCall, ToolCall: &protocol.ToolCallItem{ToolName: "read_file", ToolCallID: "c1"}},
		{Version: 1, ID: "item-4", ThreadID: "thread-1", TurnID: "turn-1", Seq: 4, Kind: protocol.ItemToolResult, ToolResult: &protocol.ToolResultItem{ToolName: "read_file", ToolCallID: "c1", Text: "contents"}},
	}

	s, err := RestoreSessionFromItems(items)
	if err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()

	if snap.Turn != 1 || snap.LastInput != "read README" || snap.InitialInput != "read README" {
		t.Fatalf("restored inputs = turn %d last %q initial %q", snap.Turn, snap.LastInput, snap.InitialInput)
	}
	if len(snap.Items) != len(items) {
		t.Fatalf("items = %#v, want original durable items", snap.Items)
	}
	if len(snap.History) != 3 {
		t.Fatalf("history = %#v, want user, assistant, tool", snap.History)
	}
	if len(snap.Turns) != 1 || len(snap.Turns[0].ToolCalls) != 1 || snap.Turns[0].ToolCalls[0].Name != "read_file" {
		t.Fatalf("turns = %#v, want reconstructed tool call", snap.Turns)
	}
	if !snap.Interrupted || !strings.Contains(strings.ToLower(snap.RuntimeNote), "resumable") {
		t.Fatalf("interrupted/runtime note = %v/%q, want resumable interrupted state", snap.Interrupted, snap.RuntimeNote)
	}
	if _, ok := s.ActiveTurnSnapshot(); ok {
		t.Fatal("restore should not create an active turn or restart tools")
	}
}

func TestRestoreSessionFromItemsGroupsMissingTurnIDAsResumableSessionTurn(t *testing.T) {
	items := []protocol.Item{
		{Version: 1, ID: "item-1", ThreadID: "thread-1", Seq: 1, Kind: protocol.ItemUserMessage, Message: &protocol.MessageItem{Role: "user", Text: "recover this"}},
		{Version: 1, ID: "item-2", ThreadID: "thread-1", TurnID: " ", Seq: 2, Kind: protocol.ItemToolCall, ToolCall: &protocol.ToolCallItem{ToolName: "read_file", ToolCallID: "c1"}},
		{Version: 1, ID: "item-3", ThreadID: "thread-1", Seq: 3, Kind: protocol.ItemToolResult, ToolResult: &protocol.ToolResultItem{ToolName: "read_file", ToolCallID: "c1", Text: "contents"}},
	}

	s, err := RestoreSessionFromItems(items)
	if err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()

	if len(snap.Turns) != 1 {
		t.Fatalf("turns = %#v, want one grouped session turn", snap.Turns)
	}
	if snap.Turns[0].Input != "recover this" || len(snap.Turns[0].ToolCalls) != 1 {
		t.Fatalf("turn = %#v, want input and tool call grouped together", snap.Turns[0])
	}
	if !snap.Interrupted || !strings.Contains(strings.ToLower(snap.RuntimeNote), "resumable") {
		t.Fatalf("interrupted/runtime note = %v/%q, want resumable interrupted state", snap.Interrupted, snap.RuntimeNote)
	}
}

func TestRestoreSessionFromItemsKeepsCompletedTurnComplete(t *testing.T) {
	items := []protocol.Item{
		{Version: 1, ID: "item-1", ThreadID: "thread-1", TurnID: "turn-1", Seq: 1, Kind: protocol.ItemUserMessage, Message: &protocol.MessageItem{Role: "user", Text: "hello"}},
		{Version: 1, ID: "item-2", ThreadID: "thread-1", TurnID: "turn-1", Seq: 2, Kind: protocol.ItemAssistantMessage, Message: &protocol.MessageItem{Role: "assistant", Text: "hi there"}},
		{Version: 1, ID: "item-3", ThreadID: "thread-1", TurnID: "turn-1", Seq: 3, Kind: protocol.ItemTurnComplete, TurnComplete: &protocol.TurnCompleteItem{Status: protocol.TurnStatusCompleted}},
	}

	s, err := RestoreSessionFromItems(items)
	if err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()

	if snap.Turn != 1 || len(snap.Turns) != 1 || snap.Turns[0].FinalResponse != "hi there" {
		t.Fatalf("snapshot = %#v, want completed turn with final response", snap)
	}
	if snap.Interrupted || snap.RuntimeNote != "" {
		t.Fatalf("interrupted/runtime note = %v/%q, want no resumable marker", snap.Interrupted, snap.RuntimeNote)
	}
	if snap.LastTurnEndReason != TurnEndReasonCompleted {
		t.Fatalf("last turn end reason = %q, want completed", snap.LastTurnEndReason)
	}
}

func TestRestoreSessionFromItemsAllowsRecoverableFailureBeforeCompletion(t *testing.T) {
	items := []protocol.Item{
		{Version: 1, ID: "item-1", ThreadID: "thread-1", TurnID: "turn-1", Seq: 1, Kind: protocol.ItemUserMessage, Message: &protocol.MessageItem{Role: "user", Text: "read README"}},
		{Version: 1, ID: "item-2", ThreadID: "thread-1", TurnID: "turn-1", Seq: 2, Kind: protocol.ItemFailure, Failure: &protocol.FailureItem{Decision: protocol.ClassifyToolArgFailure("bad args")}},
		{Version: 1, ID: "item-3", ThreadID: "thread-1", TurnID: "turn-1", Seq: 3, Kind: protocol.ItemToolResult, ToolResult: &protocol.ToolResultItem{ToolCallID: "c1", Text: "bad args"}},
		{Version: 1, ID: "item-4", ThreadID: "thread-1", TurnID: "turn-1", Seq: 4, Kind: protocol.ItemAssistantMessage, Message: &protocol.MessageItem{Role: "assistant", Text: "done"}},
		{Version: 1, ID: "item-5", ThreadID: "thread-1", TurnID: "turn-1", Seq: 5, Kind: protocol.ItemTurnComplete, TurnComplete: &protocol.TurnCompleteItem{Status: protocol.TurnStatusCompleted}},
	}

	s, err := RestoreSessionFromItems(items)
	if err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()

	if len(snap.Turns) != 1 || snap.Turns[0].FinalResponse != "done" {
		t.Fatalf("turns = %#v, want completed turn after recoverable failure", snap.Turns)
	}
	if snap.Interrupted || snap.LastTurnEndReason != TurnEndReasonCompleted {
		t.Fatalf("interrupted/end reason = %v/%q, want completed", snap.Interrupted, snap.LastTurnEndReason)
	}
}

func TestRestoreSessionFromItemsPreservesBlockingAgentHandoff(t *testing.T) {
	items := []protocol.Item{
		{Version: 1, ID: "item-1", ThreadID: "thread-1", Seq: 1, Kind: protocol.ItemAgentHandoff, AgentHandoff: &protocol.AgentHandoffItem{
			AgentID:  "agent-1",
			Blocking: true,
			RemainingActions: []protocol.AgentFollowupActionItem{{
				Kind:        string(AgentActionWriteFile),
				TargetPath:  "docs/audit.md",
				Description: "Save report",
				Blocking:    true,
			}},
			Incidents: []protocol.AgentWorkspaceIncidentItem{{
				Kind:        string(AgentIncidentAccidentalWrite),
				Paths:       []string{"README.md"},
				Description: "Child wrote report into README",
				Blocking:    true,
			}},
		}},
	}

	s, err := RestoreSessionFromItems(items)
	if err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if len(snap.AgentTasks) != 1 {
		t.Fatalf("agent tasks = %#v", snap.AgentTasks)
	}
	task := snap.AgentTasks[0]
	if task.ID != "agent-1" || task.Status != AgentStatusCompleted || task.Handoff == nil || !task.Handoff.Blocking() {
		t.Fatalf("restored task = %#v", task)
	}
	if task.Handoff.RemainingActions[0].TargetPath != "docs/audit.md" || task.Handoff.Incidents[0].Paths[0] != "README.md" {
		t.Fatalf("restored handoff = %#v", task.Handoff)
	}
}

func TestRestoreSessionFromItemsClearsResolvedAgentHandoff(t *testing.T) {
	items := []protocol.Item{
		{Version: 1, ID: "item-1", ThreadID: "thread-1", Seq: 1, Kind: protocol.ItemAgentHandoff, AgentHandoff: &protocol.AgentHandoffItem{
			AgentID:  "agent-1",
			Blocking: true,
			Incidents: []protocol.AgentWorkspaceIncidentItem{{
				Kind:        string(AgentIncidentAccidentalWrite),
				Paths:       []string{"README.md"},
				Description: "Child wrote report into README",
				Blocking:    true,
			}},
		}},
		{Version: 1, ID: "item-2", ThreadID: "thread-1", Seq: 2, Kind: protocol.ItemAgentHandoff, AgentHandoff: &protocol.AgentHandoffItem{AgentID: "agent-1", Blocking: false}},
	}

	s, err := RestoreSessionFromItems(items)
	if err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if len(snap.AgentTasks) != 1 || snap.AgentTasks[0].Handoff != nil {
		t.Fatalf("agent tasks = %#v, want resolved handoff cleared", snap.AgentTasks)
	}
}

func TestClearBlockingAgentHandoffsPersistsResolution(t *testing.T) {
	sink := &fakeDurableSink{}
	s := NewSession()
	s.SetDurableSink(sink)
	s.UpsertAgentTask(AgentTaskState{ID: "agent-1", Status: AgentStatusCompleted, Handoff: &AgentHandoff{Incidents: []AgentWorkspaceIncident{{Kind: AgentIncidentAccidentalWrite, Blocking: true}}}})
	s.ClearBlockingAgentHandoffs()

	items := sink.Items()
	if len(items) != 2 {
		t.Fatalf("durable items = %#v, want set and clear handoff items", items)
	}
	clear := items[1]
	if clear.Kind != protocol.ItemAgentHandoff || clear.AgentHandoff == nil || clear.AgentHandoff.AgentID != "agent-1" || clear.AgentHandoff.Blocking {
		t.Fatalf("clear handoff item = %#v", clear)
	}
}

func TestSessionSetRuntimeNoteReplacesTypedNoteMetadata(t *testing.T) {
	s := NewSession()
	s.SetHookOutput(hooks.ExecutionOutput{
		Note: &hooks.NoteResult{
			Message:    "old typed note",
			Priority:   hooks.PriorityLow,
			Provenance: "typed-handler",
		},
	})

	s.SetRuntimeNote("legacy runtime note")

	snap := s.Snapshot()
	if snap.HookOutput.Note == nil {
		t.Fatal("expected hook output note")
	}
	if snap.HookOutput.Note.Message != "legacy runtime note" {
		t.Fatalf("hook output note = %#v", snap.HookOutput.Note)
	}
	if snap.HookOutput.Note.Priority != hooks.PriorityHigh {
		t.Fatalf("hook output note priority = %v", snap.HookOutput.Note.Priority)
	}
	if snap.HookOutput.Note.Provenance != "runtime" {
		t.Fatalf("hook output note provenance = %q", snap.HookOutput.Note.Provenance)
	}
}

func TestSessionDefaultsToChatModeAndTracksTaskMode(t *testing.T) {
	s := NewSession()
	if got := s.Snapshot().Mode; got != ModeChat {
		t.Fatalf("default mode = %q, want %q", got, ModeChat)
	}

	s.SetTaskState(TaskState{
		Objective:            "plan the runtime work",
		Operation:            "plan",
		RequiredVerification: "produce a plan",
	})
	if got := s.Snapshot().Mode; got != ModePlan {
		t.Fatalf("mode after task state = %q, want %q", got, ModePlan)
	}

	s.SetMode(ModeImplement)
	if got := s.Snapshot().Mode; got != ModeImplement {
		t.Fatalf("mode after explicit set = %q, want %q", got, ModeImplement)
	}
}

func TestSessionMapsOverviewOperationToInspectMode(t *testing.T) {
	s := NewSession()
	s.SetTaskState(TaskState{
		Objective:            "tell me about this repo",
		Operation:            "overview",
		RequiredVerification: "give a brief overview",
	})
	if got := s.Snapshot().Mode; got != ModeInspect {
		t.Fatalf("mode after overview task state = %q, want %q", got, ModeInspect)
	}
}
