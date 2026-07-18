package react

import (
	"strings"
	"testing"

	"forge/internal/llm"
)

func TestCompactionManagerBelowThresholdNoCompaction(t *testing.T) {
	session := NewSession()
	session.RecordInput("small")
	mgr := NewCompactionManager(CompactionConfig{KeepTurns: 40, HistoryPressureTurns: 40, LargeToolResultBytes: 1024})

	decision := mgr.Decide(session.Snapshot())
	if decision.Mode != CompactionNone {
		t.Fatalf("Mode = %q, want %q", decision.Mode, CompactionNone)
	}
}

func TestCompactionManagerOldToolResultMicrocompacts(t *testing.T) {
	snap := SessionSnapshot{History: []llm.Message{{Role: llm.RoleTool, Content: strings.Repeat("x", 2048)}}}
	mgr := NewCompactionManager(CompactionConfig{KeepTurns: 40, LargeToolResultBytes: 1024})

	decision := mgr.Decide(snap)
	if decision.Mode != CompactionMicro {
		t.Fatalf("Mode = %q, want %q", decision.Mode, CompactionMicro)
	}
}

func TestCompactionManagerMicroCompactionShrinksLargeToolResult(t *testing.T) {
	session := NewSession()
	if err := session.AppendNativeToolResult("call-1", "Tool output stored out-of-band. Handle: output-123. Size: 8192 bytes. SHA256: abc123.\n"+strings.Repeat("large output\n", 1024)); err != nil {
		t.Fatal(err)
	}
	mgr := NewCompactionManager(CompactionConfig{KeepTurns: 40, LargeToolResultBytes: 1024, MaxFailures: 1})

	decision := mgr.Decide(session.Snapshot())
	if decision.Mode != CompactionMicro {
		t.Fatalf("Mode = %q, want %q", decision.Mode, CompactionMicro)
	}
	mgr.RecordFailure()
	if !mgr.CircuitOpen() {
		t.Fatal("test setup expected open circuit before successful micro compaction")
	}
	if !mgr.Apply(session, decision) {
		t.Fatal("expected micro compaction to apply")
	}

	snap := session.Snapshot()
	if len(snap.History) != 1 {
		t.Fatalf("history length = %d, want 1", len(snap.History))
	}
	msg := snap.History[0]
	if msg.Role != llm.RoleTool {
		t.Fatalf("Role = %q, want %q", msg.Role, llm.RoleTool)
	}
	if msg.ToolCallID != "call-1" {
		t.Fatalf("ToolCallID = %q, want call-1", msg.ToolCallID)
	}
	if len(msg.Content) >= 512 {
		t.Fatalf("compacted content length = %d, want small summary", len(msg.Content))
	}
	if !strings.Contains(msg.Content, "tool result compacted") {
		t.Fatalf("compacted content = %q, want summary marker", msg.Content)
	}
	if !strings.Contains(msg.Content, "Handle: output-123") {
		t.Fatalf("compacted content = %q, want preserved handle metadata", msg.Content)
	}
	if strings.Contains(msg.Content, "large output") {
		t.Fatalf("compacted content retained oversized output: %q", msg.Content)
	}
	if mgr.CircuitOpen() {
		t.Fatal("successful micro compaction should reset failure counter")
	}
}

func TestCompactionManagerMicroCompactionBoundsJSONHandleMetadata(t *testing.T) {
	session := NewSession()
	if err := session.AppendNativeToolResult("call-1", `{"handle":"output-json","size":131072,"sha256":"abc123","payload":"`+strings.Repeat("x", 8192)+`"}`); err != nil {
		t.Fatal(err)
	}
	mgr := NewCompactionManager(CompactionConfig{KeepTurns: 40, LargeToolResultBytes: 1024})

	if !mgr.Apply(session, mgr.Decide(session.Snapshot())) {
		t.Fatal("expected micro compaction to apply")
	}
	msg := session.Snapshot().History[0]
	if len(msg.Content) >= 512 {
		t.Fatalf("compacted content length = %d, want bounded metadata", len(msg.Content))
	}
	if !strings.Contains(msg.Content, "output-json") {
		t.Fatalf("compacted content = %q, want preserved handle identifier", msg.Content)
	}
	if strings.Contains(msg.Content, strings.Repeat("x", 128)) {
		t.Fatalf("compacted content retained large payload: %q", msg.Content)
	}
}

func TestCompactionManagerMicroCompactionBoundsManyHandleLines(t *testing.T) {
	var content strings.Builder
	for i := range 80 {
		content.WriteString("Tool output stored out-of-band. Handle: output-")
		content.WriteString(strings.Repeat("x", 16))
		content.WriteString("-")
		content.WriteString(string(rune('a' + i%26)))
		content.WriteString(". Size: 8192 bytes. SHA256: abc123.\n")
	}
	session := NewSession()
	if err := session.AppendNativeToolResult("call-1", content.String()); err != nil {
		t.Fatal(err)
	}
	mgr := NewCompactionManager(CompactionConfig{KeepTurns: 40, LargeToolResultBytes: 1024})

	decision := mgr.Decide(session.Snapshot())
	if decision.Mode != CompactionMicro {
		t.Fatalf("Mode = %q, want %q", decision.Mode, CompactionMicro)
	}
	if !mgr.Apply(session, decision) {
		t.Fatal("expected micro compaction to shrink many handle lines")
	}
	msg := session.Snapshot().History[0]
	if len(msg.Content) >= 1024 {
		t.Fatalf("compacted content length = %d, want below threshold", len(msg.Content))
	}
	if !strings.Contains(msg.Content, "Handle: output-") {
		t.Fatalf("compacted content = %q, want preserved handle snippet", msg.Content)
	}
	if !strings.Contains(msg.Content, "metadata lines omitted") {
		t.Fatalf("compacted content = %q, want omitted metadata marker", msg.Content)
	}
}

func TestCompactionManagerOversizedNonToolMessageDoesNotChooseMicro(t *testing.T) {
	mgr := NewCompactionManager(CompactionConfig{KeepTurns: 10, HistoryPressureTurns: 40, LargeToolResultBytes: 1024})

	decision := mgr.Decide(SessionSnapshot{History: []llm.Message{{Role: llm.RoleAssistant, Content: strings.Repeat("x", 2048)}}})
	if decision.Mode == CompactionMicro {
		t.Fatal("oversized non-tool message chose micro compaction")
	}
	if decision.Mode != CompactionNone {
		t.Fatalf("Mode = %q, want %q without history pressure", decision.Mode, CompactionNone)
	}

	decision = mgr.Decide(SessionSnapshot{Turn: 45, History: []llm.Message{{Role: llm.RoleUser, Content: strings.Repeat("x", 2048)}}})
	if decision.Mode == CompactionMicro {
		t.Fatal("oversized pressured non-tool message chose micro compaction")
	}
	if decision.Mode != CompactionSummarize {
		t.Fatalf("Mode = %q, want %q under history pressure", decision.Mode, CompactionSummarize)
	}
}

func TestCompactionManagerHighHistoryPressureSummarizes(t *testing.T) {
	session := NewSession()
	for i := range 45 {
		session.RecordInput("turn")
		mustCompleteTurn(t, session, i+1, "ok", nil, nil)
	}
	mgr := NewCompactionManager(CompactionConfig{KeepTurns: 10, HistoryPressureTurns: 40})

	decision := mgr.Decide(session.Snapshot())
	if decision.Mode != CompactionSummarize {
		t.Fatalf("Mode = %q, want %q", decision.Mode, CompactionSummarize)
	}
	if !mgr.Apply(session, decision) {
		t.Fatal("expected compaction to apply")
	}
	if got := len(session.Snapshot().RecentInputs); got > 10 {
		t.Fatalf("recent inputs = %d, want <= 10", got)
	}
}

func TestCompactionManagerRepeatedFailuresOpenCircuitBreaker(t *testing.T) {
	mgr := NewCompactionManager(CompactionConfig{MaxFailures: 2})
	mgr.RecordFailure()
	if mgr.CircuitOpen() {
		t.Fatal("circuit opened too early")
	}
	mgr.RecordFailure()
	if !mgr.CircuitOpen() {
		t.Fatal("expected circuit to open")
	}
	decision := mgr.Decide(SessionSnapshot{Turn: 100})
	if decision.Mode != CompactionNone {
		t.Fatalf("Mode = %q, want none while circuit open", decision.Mode)
	}
}

func TestCompactionManagerUserPartialCompactsSelectedRange(t *testing.T) {
	session := NewSession()
	for i := range 12 {
		session.RecordInput("turn")
		mustCompleteTurn(t, session, i+1, "ok", nil, nil)
	}
	mgr := NewCompactionManager(CompactionConfig{KeepTurns: 40})
	decision := mgr.UserPartial(5)

	if decision.Mode != CompactionUserPartial {
		t.Fatalf("Mode = %q, want %q", decision.Mode, CompactionUserPartial)
	}
	if !mgr.Apply(session, decision) {
		t.Fatal("expected user partial compaction to apply")
	}
	if got := len(session.Snapshot().RecentInputs); got != 5 {
		t.Fatalf("recent inputs = %d, want 5", got)
	}
}

func TestCompactionManagerPromptBudgetScalesToContextWindow(t *testing.T) {
	// ~500KB prompt: over the 256KB default, well under a 1M-token window budget.
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "go"},
		{Role: llm.RoleUser, Content: strings.Repeat("x", 500*1024)},
	}

	mgr := NewCompactionManager(CompactionConfig{})
	if d := mgr.DecidePromptPressure(messages); d.Mode == CompactionNone {
		t.Fatalf("default budget: Mode = %q, want compaction", d.Mode)
	}

	mgr = NewCompactionManager(CompactionConfig{PromptBudgetFn: promptBudgetFromWindow(func() int { return 1_000_000 })})
	if d := mgr.DecidePromptPressure(messages); d.Mode != CompactionNone {
		t.Fatalf("1M window: Mode = %q, want %q", d.Mode, CompactionNone)
	}

	// Unknown window falls back to the 256KB default.
	mgr = NewCompactionManager(CompactionConfig{PromptBudgetFn: promptBudgetFromWindow(func() int { return 0 })})
	if d := mgr.DecidePromptPressure(messages); d.Mode == CompactionNone {
		t.Fatalf("unknown window: Mode = %q, want compaction", d.Mode)
	}
}

func TestCompactionManagerLeanBudgetCapAndEarlyCrush(t *testing.T) {
	if b := promptBudgetFromWindow(func() int { return 1_000_000 })(); b != maxPromptBudgetBytes {
		t.Fatalf("budget = %d, want cap %d", b, maxPromptBudgetBytes)
	}

	// Past half budget with a stale big tool result: crush it, don't summarize.
	budget := 100 * 1024
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "go"},
		{Role: llm.RoleTool, Content: strings.Repeat("x", 60*1024)},
		{Role: llm.RoleUser, Content: "next"},
		{Role: llm.RoleAssistant, Content: "ok"},
		{Role: llm.RoleUser, Content: "next2"},
		{Role: llm.RoleAssistant, Content: "ok"},
		{Role: llm.RoleUser, Content: "next3"},
		{Role: llm.RoleAssistant, Content: "ok"},
		{Role: llm.RoleUser, Content: "next4"},
		{Role: llm.RoleAssistant, Content: "ok"},
	}
	mgr := NewCompactionManager(CompactionConfig{PromptBudgetFn: func() int { return budget }})
	d := mgr.DecidePromptPressure(messages)
	if d.Mode != CompactionMicro || d.Reason != "half prompt budget" {
		t.Fatalf("Mode = %q reason %q, want micro / half prompt budget", d.Mode, d.Reason)
	}
}
