package react

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"forge/internal/hooks"
	"forge/internal/llm"
)

type contextErrorThenSuccessDriver struct {
	calls int
}

func (d *contextErrorThenSuccessDriver) Name() string { return "context-error-then-success" }

func (d *contextErrorThenSuccessDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	d.calls++
	if d.calls == 1 {
		return errors.New("context_length_exceeded")
	}
	out <- llm.Token{Text: "recovered after compaction"}
	return nil
}

type contextErrorAlwaysDriver struct {
	calls int
}

func (d *contextErrorAlwaysDriver) Name() string { return "context-error-always" }

func (d *contextErrorAlwaysDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	d.calls++
	return errors.New("context_length_exceeded")
}

func TestCompactSessionHistoryTrimsOldTurns(t *testing.T) {
	session := NewSession()
	for i := 1; i <= 6; i++ {
		turn := session.RecordInput(fmt.Sprintf("prompt %d", i))
		mustAppendAssistantMessage(t, session, fmt.Sprintf("answer %d", i))
		mustCompleteTurn(t, session, turn, fmt.Sprintf("answer %d", i), nil, nil)
	}

	changed := CompactSessionHistory(session, 3)
	if !changed {
		t.Fatal("expected compaction to trigger")
	}
	snap := session.Snapshot()
	if snap.CompactedTurns != 3 {
		t.Fatalf("CompactedTurns = %d, want 3", snap.CompactedTurns)
	}
	if len(snap.Turns) != 3 {
		t.Fatalf("Turns = %d, want 3", len(snap.Turns))
	}
	if len(snap.RecentInputs) != 3 {
		t.Fatalf("RecentInputs = %d, want 3", len(snap.RecentInputs))
	}
	if !strings.Contains(snap.CompactionSummary, "prompt 1") {
		t.Fatalf("CompactionSummary = %q", snap.CompactionSummary)
	}
	if !strings.Contains(snap.CompactionSummary, "answer 1") {
		t.Fatalf("expected semantic summary to include prior outcome, got %q", snap.CompactionSummary)
	}
}

func TestCompactSessionHistorySummarizesToolsAndErrors(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("inspect repo")
	if err := session.AppendUserMessage("[list_dir] README.md\ninternal"); err != nil {
		t.Fatal(err)
	}
	mustAppendAssistantMessage(t, session, "repo overview")
	mustCompleteTurn(t, session, turn, "repo overview", []TurnToolCall{{Name: "list_dir"}}, fmt.Errorf("tool timeout"))

	next := session.RecordInput("follow up")
	mustAppendAssistantMessage(t, session, "done")
	mustCompleteTurn(t, session, next, "done", nil, nil)

	if !CompactSessionHistory(session, 1) {
		t.Fatal("expected compaction")
	}

	snap := session.Snapshot()
	if !strings.Contains(snap.CompactionSummary, "inspect repo") {
		t.Fatalf("summary = %q", snap.CompactionSummary)
	}
	if !strings.Contains(snap.CompactionSummary, "tools: list_dir") {
		t.Fatalf("expected tool usage in summary, got %q", snap.CompactionSummary)
	}
	if !strings.Contains(snap.CompactionSummary, "outcome: repo overview") {
		t.Fatalf("expected final outcome in summary, got %q", snap.CompactionSummary)
	}
	if !strings.Contains(snap.CompactionSummary, "error: tool timeout") {
		t.Fatalf("expected error in summary, got %q", snap.CompactionSummary)
	}
}

func TestRunnerRunEmitsCompactionProgress(t *testing.T) {
	var progress []string
	r := NewRunner(Config{
		Progress:              func(text string) { progress = append(progress, text) },
		CompactionMaxFailures: 1,
	})

	r.applyCompactionDecision(context.Background(), CompactionDecision{Mode: CompactionSummarize, Reason: "test failure", KeepTurns: 40})

	found := false
	for _, msg := range progress {
		if strings.Contains(strings.ToLower(msg), "compaction circuit breaker") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected compaction progress message, got %#v", progress)
	}
}

func TestRunnerCompactionProgressIncludesContextDelta(t *testing.T) {
	session := NewSession()
	for i := 1; i <= 45; i++ {
		turn := session.RecordInput(fmt.Sprintf("prompt %d %s", i, strings.Repeat("x", 256)))
		mustAppendAssistantMessage(t, session, fmt.Sprintf("answer %d %s", i, strings.Repeat("y", 256)))
		mustCompleteTurn(t, session, turn, fmt.Sprintf("answer %d", i), nil, nil)
	}
	var progress []string
	r := NewRunner(Config{
		Session:  session,
		Progress: func(text string) { progress = append(progress, text) },
	})

	if !r.applyCompactionDecision(context.Background(), CompactionDecision{Mode: CompactionSummarize, Reason: "history pressure", KeepTurns: 40}) {
		t.Fatal("expected compaction to change session")
	}

	found := false
	for _, msg := range progress {
		lower := strings.ToLower(msg)
		if strings.Contains(lower, "compacted context") && strings.Contains(msg, "->") && strings.Contains(lower, "history pressure") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected compaction progress with context delta, got %#v", progress)
	}
}

func TestRunnerDispatchesCompactionHookPayloads(t *testing.T) {
	driver := &nativeScriptedDriver{responses: []string{"done"}}
	session := NewSession()
	for i := 1; i <= 45; i++ {
		turn := session.RecordInput(fmt.Sprintf("prompt %d", i))
		mustAppendAssistantMessage(t, session, fmt.Sprintf("answer %d", i))
		mustCompleteTurn(t, session, turn, fmt.Sprintf("answer %d", i), nil, nil)
	}
	before := session.Snapshot()

	var prePayloads []CompactionHookPayload
	var postPayloads []CompactionHookPayload
	var preSnapshots []any
	var postSnapshots []any
	r := NewRunner(Config{
		Driver:                driver,
		Session:               session,
		CompactionMaxFailures: 1,
		ConfigureHooks: func(registry *hooks.Registry) {
			registry.Register(hooks.PointPreCompact, "capture:pre", func(_ context.Context, event hooks.Event) []hooks.Result {
				preSnapshots = append(preSnapshots, event.Snapshot)
				payload, ok := event.Transient.(CompactionHookPayload)
				if !ok {
					t.Fatalf("pre_compact payload type = %T, want CompactionHookPayload", event.Transient)
				}
				prePayloads = append(prePayloads, payload)
				panic("pre hook failure should be non-fatal")
			})
			registry.Register(hooks.PointPostCompact, "capture:post", func(_ context.Context, event hooks.Event) []hooks.Result {
				postSnapshots = append(postSnapshots, event.Snapshot)
				payload, ok := event.Transient.(CompactionHookPayload)
				if !ok {
					t.Fatalf("post_compact payload type = %T, want CompactionHookPayload", event.Transient)
				}
				postPayloads = append(postPayloads, payload)
				return nil
			})
		},
	})

	if err := r.Run(context.Background(), "trigger compaction"); err != nil {
		t.Fatal(err)
	}
	after := session.Snapshot()

	if len(prePayloads) != 1 {
		t.Fatalf("pre payload count = %d, want 1", len(prePayloads))
	}
	if len(preSnapshots) != 1 || preSnapshots[0] != nil {
		t.Fatalf("pre snapshot = %#v, want nil", preSnapshots)
	}
	pre := prePayloads[0]
	if pre.Mode != CompactionSummarize {
		t.Fatalf("pre Mode = %q, want %q", pre.Mode, CompactionSummarize)
	}
	if pre.Reason != "history pressure" {
		t.Fatalf("pre Reason = %q, want history pressure", pre.Reason)
	}
	if pre.KeepTurns != 40 {
		t.Fatalf("pre KeepTurns = %d, want 40", pre.KeepTurns)
	}
	if pre.SummaryLength != len(before.CompactionSummary) {
		t.Fatalf("pre SummaryLength = %d, want %d", pre.SummaryLength, len(before.CompactionSummary))
	}
	if pre.Changed {
		t.Fatal("pre Changed = true, want false before compaction")
	}
	if pre.CircuitOpen {
		t.Fatal("pre CircuitOpen = true, want false")
	}

	if len(postPayloads) != 1 {
		t.Fatalf("post payload count = %d, want 1", len(postPayloads))
	}
	if len(postSnapshots) != 1 || postSnapshots[0] != nil {
		t.Fatalf("post snapshot = %#v, want nil", postSnapshots)
	}
	post := postPayloads[0]
	if post.Mode != CompactionSummarize {
		t.Fatalf("post Mode = %q, want %q", post.Mode, CompactionSummarize)
	}
	if post.Reason != "history pressure" {
		t.Fatalf("post Reason = %q, want history pressure", post.Reason)
	}
	if post.KeepTurns != 40 {
		t.Fatalf("post KeepTurns = %d, want 40", post.KeepTurns)
	}
	if post.DroppedTurns != after.CompactedTurns-before.CompactedTurns {
		t.Fatalf("post DroppedTurns = %d, want compacted turn delta %d", post.DroppedTurns, after.CompactedTurns-before.CompactedTurns)
	}
	if post.DroppedTurns <= 0 {
		t.Fatalf("post DroppedTurns = %d, want meaningful positive count", post.DroppedTurns)
	}
	if post.SummaryLength != len(after.CompactionSummary) {
		t.Fatalf("post SummaryLength = %d, want %d", post.SummaryLength, len(after.CompactionSummary))
	}
	if !post.Changed {
		t.Fatal("post Changed = false, want true")
	}
	if post.CircuitOpen {
		t.Fatal("post CircuitOpen = true, want false after successful compaction")
	}
}

func TestRunnerSuccessfulCompactionResetsFailureCircuit(t *testing.T) {
	session := NewSession()
	r := NewRunner(Config{Session: session, CompactionMaxFailures: 2})

	r.applyCompactionDecision(context.Background(), CompactionDecision{Mode: CompactionMicro, Reason: "failed once", KeepTurns: 40})
	if r.compactionCircuitOpen() {
		t.Fatal("circuit open after one failed compaction, want closed")
	}

	for i := 1; i <= 45; i++ {
		turn := session.RecordInput(fmt.Sprintf("prompt %d", i))
		mustAppendAssistantMessage(t, session, fmt.Sprintf("answer %d", i))
		mustCompleteTurn(t, session, turn, fmt.Sprintf("answer %d", i), nil, nil)
	}
	if !r.applyCompactionDecision(context.Background(), CompactionDecision{Mode: CompactionSummarize, Reason: "success", KeepTurns: 40}) {
		t.Fatal("expected successful compaction")
	}

	r.applyCompactionDecision(context.Background(), CompactionDecision{Mode: CompactionMicro, Reason: "failed after reset", KeepTurns: 40})
	if r.compactionCircuitOpen() {
		t.Fatal("circuit open after failure-success-failure, want success to reset failure count")
	}
}

func TestRunnerMicroCompactionDoesNotTripFailureCircuit(t *testing.T) {
	var progress []string
	r := NewRunner(Config{
		Progress:              func(text string) { progress = append(progress, text) },
		CompactionMaxFailures: 1,
	})

	r.applyCompactionDecision(context.Background(), CompactionDecision{Mode: CompactionMicro, Reason: "large tool result", KeepTurns: 40})

	if r.compactionCircuitOpen() {
		t.Fatal("micro compaction should not trip the failure circuit")
	}
	for _, msg := range progress {
		if strings.Contains(strings.ToLower(msg), "compaction circuit breaker") {
			t.Fatalf("unexpected circuit breaker progress for micro compaction: %#v", progress)
		}
	}
}

func TestRunnerCompactsBeforeRecordingTriggeringPrompt(t *testing.T) {
	driver := &nativeScriptedDriver{responses: []string{"continued"}}
	session := NewSession()
	for i := 1; i <= 45; i++ {
		turn := session.RecordInput(fmt.Sprintf("prompt %d", i))
		mustAppendAssistantMessage(t, session, fmt.Sprintf("answer %d", i))
		mustCompleteTurn(t, session, turn, fmt.Sprintf("answer %d", i), nil, nil)
	}
	var preCompactRecentInputs []string
	r := NewRunner(Config{
		Driver:  driver,
		Session: session,
		ConfigureHooks: func(registry *hooks.Registry) {
			registry.Register(hooks.PointPreCompact, "capture:recent-inputs", func(context.Context, hooks.Event) []hooks.Result {
				preCompactRecentInputs = session.Snapshot().RecentInputs
				return nil
			})
		},
	})

	if err := r.Run(context.Background(), "continue"); err != nil {
		t.Fatal(err)
	}
	snap := session.Snapshot()
	if strings.Contains(snap.CompactionSummary, "continue") {
		t.Fatalf("triggering prompt was compacted into summary: %q", snap.CompactionSummary)
	}
	for _, input := range preCompactRecentInputs {
		if input == "continue" {
			t.Fatalf("triggering prompt was recorded before compaction started: %#v", preCompactRecentInputs)
		}
	}
	if len(snap.RecentInputs) == 0 || snap.RecentInputs[len(snap.RecentInputs)-1] != "continue" {
		t.Fatalf("recent inputs should keep triggering prompt active, got %#v", snap.RecentInputs)
	}
}

func TestRunnerReactiveCompactsAndRetriesOnceOnContextError(t *testing.T) {
	driver := &contextErrorThenSuccessDriver{}
	session := NewSession()
	for i := 1; i <= 45; i++ {
		turn := session.RecordInput(fmt.Sprintf("prompt %d", i))
		mustAppendAssistantMessage(t, session, fmt.Sprintf("answer %d", i))
		mustCompleteTurn(t, session, turn, fmt.Sprintf("answer %d", i), nil, nil)
	}
	r := NewRunner(Config{Driver: driver, Session: session})

	if err := r.Run(context.Background(), "continue"); err != nil {
		t.Fatal(err)
	}
	if driver.calls != 2 {
		t.Fatalf("driver calls = %d, want 2", driver.calls)
	}
	snap := session.Snapshot()
	if !strings.Contains(snap.CompactionSummary, "prompt 1") {
		t.Fatalf("expected reactive compaction summary, got %q", snap.CompactionSummary)
	}
	if got := r.LastResponse(); got != "recovered after compaction" {
		t.Fatalf("last response = %q", got)
	}
}

// A session that overflows no matter how much it sheds must still terminate.
// Recovery retries while compaction keeps making progress, then stops and
// surfaces the provider error rather than spinning.
func TestRunnerReactiveCompactionTerminatesWhenOverflowPersists(t *testing.T) {
	driver := &contextErrorAlwaysDriver{}
	session := NewSession()
	for i := 1; i <= 45; i++ {
		turn := session.RecordInput(fmt.Sprintf("prompt %d", i))
		mustAppendAssistantMessage(t, session, fmt.Sprintf("answer %d", i))
		mustCompleteTurn(t, session, turn, fmt.Sprintf("answer %d", i), nil, nil)
	}
	r := NewRunner(Config{Driver: driver, Session: session})

	if err := r.Run(context.Background(), "continue"); err == nil {
		t.Fatal("expected the context error to surface once compaction stopped helping")
	}
	// A latched implementation recovers exactly once and so calls the driver
	// exactly twice; more than that is the property being guarded.
	if driver.calls <= 2 {
		t.Fatalf("driver calls = %d, want recovery to be attempted more than once", driver.calls)
	}
	if driver.calls > maxContextCompactionsPerTurn+1 {
		t.Fatalf("driver calls = %d, want recovery bounded by %d attempts", driver.calls, maxContextCompactionsPerTurn)
	}
}

func TestRunnerContextErrorMicroCompactsLargeActiveToolResultAndRetriesOnce(t *testing.T) {
	driver := &contextErrorThenSuccessDriver{}
	session := NewSession()
	active, _, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.EndTurn("turn-1", TurnEndReasonCompleted) }()
	turn := session.RecordInput("continue after large tool result")
	if err := session.AppendNativeToolResult("call-1", "Tool output stored out-of-band. Handle: output-ctx. Size: 131072 bytes. SHA256: def456.\n"+strings.Repeat("large output\n", 7000)); err != nil {
		t.Fatal(err)
	}
	r := NewRunner(Config{Driver: driver, Session: session})

	if err := r.runLoop(active.Context, turn); err != nil {
		t.Fatal(err)
	}
	if driver.calls != 2 {
		t.Fatalf("driver calls = %d, want one context error and one retry", driver.calls)
	}
	if got := r.LastResponse(); got != "recovered after compaction" {
		t.Fatalf("last response = %q", got)
	}
	for _, msg := range session.Snapshot().History {
		if msg.Role != llm.RoleTool || msg.ToolCallID != "call-1" {
			continue
		}
		if len(msg.Content) >= 512 {
			t.Fatalf("tool result length = %d, want compacted summary", len(msg.Content))
		}
		if !strings.Contains(msg.Content, "Handle: output-ctx") {
			t.Fatalf("tool result = %q, want preserved handle metadata", msg.Content)
		}
		return
	}
	t.Fatal("compacted tool result not found")
}
