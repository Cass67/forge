package react

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agenttools "forge/internal/agent/tools"
	"forge/internal/hooks"
	"forge/internal/llm"
	"forge/internal/protocol"
	"forge/internal/sessionstore"
)

// nativeScriptedDriver is a minimal NativeToolCaller driver for tests.
// It responds with a plain text answer to every StreamWithTools call.
type nativeScriptedDriver struct {
	responses []string
	callCount int
	reset     bool
}

func (d *nativeScriptedDriver) Name() string { return "native-scripted" }

func (d *nativeScriptedDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	if d.callCount >= len(d.responses) {
		return errors.New("no scripted response")
	}
	out <- llm.Token{Text: d.responses[d.callCount]}
	d.callCount++
	return nil
}

func (d *nativeScriptedDriver) StreamWithTools(_ context.Context, _ []llm.Message, _ []llm.ToolDef, out chan<- llm.Token) error {
	defer close(out)
	if d.callCount >= len(d.responses) {
		return errors.New("no scripted response")
	}
	out <- llm.Token{Text: d.responses[d.callCount]}
	d.callCount++
	return nil
}

func (d *nativeScriptedDriver) ResetConversation() { d.reset = true }

type captureMessagesDriver struct {
	lastMessages []llm.Message
	response     string
}

func (d *captureMessagesDriver) Name() string { return "capture-messages" }

func (d *captureMessagesDriver) Stream(_ context.Context, msgs []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	d.lastMessages = append([]llm.Message(nil), msgs...)
	out <- llm.Token{Text: d.response}
	return nil
}

func (d *captureMessagesDriver) StreamWithTools(_ context.Context, msgs []llm.Message, _ []llm.ToolDef, out chan<- llm.Token) error {
	defer close(out)
	d.lastMessages = append([]llm.Message(nil), msgs...)
	out <- llm.Token{Text: d.response}
	return nil
}

// scriptedDriver is a plain Driver (no NativeToolCaller) used to test the
// plain-stream path and the error path when tool calling is required.
type scriptedDriver struct {
	responses []string
	callCount int
}

func (d *scriptedDriver) Name() string { return "scripted" }

func (d *scriptedDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	if d.callCount >= len(d.responses) {
		return errors.New("no scripted response")
	}
	out <- llm.Token{Text: d.responses[d.callCount]}
	d.callCount++
	return nil
}

type silentRenderer struct{}

func (silentRenderer) AgentToken(string)                       {}
func (silentRenderer) AgentText(string)                        {}
func (silentRenderer) ToolCall(string, string)                 {}
func (silentRenderer) ToolResult(string, string, string, bool) {}
func (silentRenderer) Stats(time.Duration, llm.Usage)          {}
func (silentRenderer) Error(string)                            {}
func (silentRenderer) Info(string)                             {}

type recordingRenderer struct {
	mu         sync.Mutex
	tokenTexts []string
	fullTexts  []string
	retryTexts []string
	toolTexts  []string
	events     []string
	statsCalls int
	statsUsage []llm.Usage
	statsCtx   []int
}

func (r *recordingRenderer) AgentToken(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "token")
	r.tokenTexts = append(r.tokenTexts, text)
}

func (r *recordingRenderer) AgentText(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "text")
	r.fullTexts = append(r.fullTexts, text)
}

func (r *recordingRenderer) Retry(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.retryTexts = append(r.retryTexts, text)
}

func (r *recordingRenderer) ToolCall(string, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "tool_call")
}
func (r *recordingRenderer) ToolResult(_ string, text string, _ string, _ bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "tool_result")
	r.toolTexts = append(r.toolTexts, text)
}
func (r *recordingRenderer) Stats(_ time.Duration, usage llm.Usage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "stats")
	r.statsCalls++
	r.statsUsage = append(r.statsUsage, usage)
}
func (r *recordingRenderer) StatsWithContext(_ time.Duration, usage llm.Usage, contextUsed int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "stats")
	r.statsCalls++
	r.statsUsage = append(r.statsUsage, usage)
	r.statsCtx = append(r.statsCtx, contextUsed)
}
func (r *recordingRenderer) Error(string) {}
func (r *recordingRenderer) Info(string)  {}

type errorDriver struct {
	err error
}

func (d *errorDriver) Name() string { return "error-driver" }

func (d *errorDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	close(out)
	return d.err
}

func TestRunnerRunInvokesDriverAndProgress(t *testing.T) {
	driver := &nativeScriptedDriver{responses: []string{"repo overview"}}
	session := NewSession()
	r := NewRunner(Config{
		Driver:  driver,
		Session: session,
	})

	if err := r.Run(context.Background(), "  inspect this file  "); err != nil {
		t.Fatal(err)
	}
	if driver.callCount != 1 {
		t.Fatalf("calls = %d, want 1", driver.callCount)
	}
	snap := session.Snapshot()
	if snap.Turn != 1 {
		t.Fatalf("turn = %d, want 1", snap.Turn)
	}
}

func TestRunnerRecordsAnswerOnlyTurnContractForCasualInput(t *testing.T) {
	session := NewSession()
	r := NewRunner(Config{Session: session, Driver: &scriptedDriver{responses: []string{"hi"}}})

	if err := r.Run(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	contract := session.Snapshot().TurnContract
	if contract == nil {
		t.Fatal("missing turn contract")
	}
	if contract.Intent != TurnIntentAnswerOnly || len(contract.RequiredActions) != 0 {
		t.Fatalf("TurnContract = %#v, want answer_only with no required actions", contract)
	}
}

func TestRunnerFinalFeedbackDoesNotDuplicateMirroredMissingArtifact(t *testing.T) {
	session := NewSession()
	session.SetTurnContract(TurnContract{
		ID:                "contract-1",
		Intent:            TurnIntentWriteArtifact,
		RequiredActions:   []ContractAction{{Kind: ContractActionEdit}},
		RequiredArtifacts: []ArtifactRequirement{{Path: "docs/report.md"}},
		Gates:             []ContractGate{{Name: "artifact", Status: ContractGatePending}},
		Status:            ContractStatusActive,
	})
	session.SetSideEffectIntent(SideEffectIntent{
		ID:              "intent-1",
		ArtifactPaths:   []string{"docs/report.md"},
		AllowedPaths:    []string{"docs/report.md"},
		RequiredActions: []SideEffectAction{SideEffectActionWrite},
		Gates:           []SideEffectGate{{Name: string(SideEffectActionWrite), Status: SideEffectGatePending}},
	})
	r := NewRunner(Config{Session: session})

	feedbackParts, hasArtifactFeedback, err := r.finalSideEffectGateFeedback("Done, wrote the file.")
	if err != nil {
		t.Fatal(err)
	}
	if !hasArtifactFeedback || len(feedbackParts) != 1 {
		t.Fatalf("feedbackParts = %#v, hasArtifactFeedback=%v; want only artifact feedback", feedbackParts, hasArtifactFeedback)
	}
}

func TestRunnerArtifactGateRequiresExactPathEvidenceDespiteSideEffectWriteGate(t *testing.T) {
	workspace := t.TempDir()
	for _, path := range []string{"docs/a.txt", "docs/b.txt"} {
		fullPath := filepath.Join(workspace, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte("content"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	session := NewSession()
	session.SetActiveWorkspaceRoot(workspace)
	session.SetTurnContract(TurnContract{
		ID:                "contract-1",
		Intent:            TurnIntentWriteArtifact,
		RequiredActions:   []ContractAction{{Kind: ContractActionEdit}},
		RequiredArtifacts: []ArtifactRequirement{{Path: "docs/a.txt"}, {Path: "docs/b.txt"}},
		Evidence:          []EvidenceRecord{{Kind: EvidenceWrite, Summary: "write: write_file docs/a.txt"}},
		Gates:             []ContractGate{{Name: "artifact", Status: ContractGatePending}},
		Status:            ContractStatusActive,
	})
	session.SetSideEffectIntent(SideEffectIntent{
		ID:              "intent-1",
		ArtifactPaths:   []string{"docs/a.txt", "docs/b.txt"},
		AllowedPaths:    []string{"docs/a.txt", "docs/b.txt"},
		RequiredActions: []SideEffectAction{SideEffectActionWrite},
		Gates:           []SideEffectGate{{Name: string(SideEffectActionWrite), Status: SideEffectGatePassed}},
	})
	r := NewRunner(Config{Session: session})

	feedbackParts, hasArtifactFeedback, err := r.finalSideEffectGateFeedback("Done, wrote the files.")
	if err != nil {
		t.Fatal(err)
	}
	if !hasArtifactFeedback || len(feedbackParts) == 0 || !strings.Contains(strings.Join(feedbackParts, "\n"), "docs/b.txt") {
		t.Fatalf("feedbackParts = %#v, hasArtifactFeedback=%v; want missing exact evidence for docs/b.txt", feedbackParts, hasArtifactFeedback)
	}
}

func TestRunnerArtifactGateDoesNotPassRootlessMirroredArtifactWithOnlyWriteEvidence(t *testing.T) {
	session := NewSession()
	session.SetTurnContract(TurnContract{
		ID:                "contract-1",
		Intent:            TurnIntentWriteArtifact,
		RequiredActions:   []ContractAction{{Kind: ContractActionEdit}},
		RequiredArtifacts: []ArtifactRequirement{{Path: "docs/rootless.txt", Description: "requested artifact"}},
		Evidence:          []EvidenceRecord{{Kind: EvidenceWrite, Summary: "write: write_file docs/rootless.txt"}},
		Gates:             []ContractGate{{Name: "artifact", Status: ContractGatePending}},
		Status:            ContractStatusActive,
	})
	session.SetSideEffectIntent(SideEffectIntent{
		ID:              "intent-1",
		ArtifactPaths:   []string{"docs/rootless.txt"},
		AllowedPaths:    []string{"docs/rootless.txt"},
		RequiredActions: []SideEffectAction{SideEffectActionWrite},
		Gates:           []SideEffectGate{{Name: string(SideEffectActionWrite), Status: SideEffectGatePassed}},
	})
	r := NewRunner(Config{Session: session})

	feedbackParts, hasArtifactFeedback, err := r.finalSideEffectGateFeedback("Done, wrote the file.")
	if err != nil {
		t.Fatal(err)
	}
	if !hasArtifactFeedback || len(feedbackParts) == 0 {
		t.Fatalf("feedbackParts = %#v, hasArtifactFeedback=%v; want rootless artifact validation to remain unresolved", feedbackParts, hasArtifactFeedback)
	}
}

func TestRunnerArtifactGateFailsWhenWriteToolResultIsTextError(t *testing.T) {
	workspace := t.TempDir()
	artifact := "docs/plans/2026-05-23-term-wrangler-design.md"
	session := NewSession()
	session.SetActiveWorkspaceRoot(workspace)
	driver := &nativeSequenceDriver{steps: append([][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "write-1", Name: "write_file", ArgsJSON: fmt.Sprintf(`{"path":%q,"content":"# Term Wrangler Design\n\n## Approach\n\nThe tool reports an error and must not satisfy the gate."}`, artifact)}}},
	}, repeatedTextSteps("Done, I wrote the plan.", 4)...)}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{Name: "write_file", Parameters: []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}, {Name: "content", Type: "string", Required: true}}, Execute: func(context.Context, map[string]any) (string, error) {
		return "error: disk full", nil
	}})
	r := NewRunner(Config{Session: session, Driver: driver, Tools: reg, MaxSteps: 6})

	if err := r.Run(context.Background(), "write "+artifact); err != nil {
		t.Fatalf("err = %v, want final answer accepted after bounded gate nudges", err)
	}
	if _, statErr := os.Stat(filepath.Join(workspace, artifact)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("artifact stat err = %v, want not exist", statErr)
	}
}

func TestRunnerPreservesSideEffectIntentWhenPromptHasNoSideEffectTarget(t *testing.T) {
	session := NewSession()
	session.SetSideEffectIntent(SideEffectIntent{ID: "intent-1", AllowedPaths: []string{"docs/a.md"}})
	r := NewRunner(Config{Session: session, Driver: &scriptedDriver{responses: []string{"ok"}}})

	if err := r.Run(context.Background(), "what happened?"); err != nil {
		t.Fatal(err)
	}
	intent := session.Snapshot().SideEffectIntent
	if intent == nil || intent.ID != "intent-1" || !containsString(intent.AllowedPaths, "docs/a.md") {
		t.Fatalf("SideEffectIntent = %#v", intent)
	}
}

func TestRunnerPreservesSideEffectIntentForReadOnlyPathPrompt(t *testing.T) {
	session := NewSession()
	session.SetSideEffectIntent(SideEffectIntent{ID: "intent-1", AllowedPaths: []string{"docs/a.md"}, RequiredActions: []SideEffectAction{SideEffectActionWrite}})
	r := NewRunner(Config{Session: session, Driver: &scriptedDriver{responses: []string{"ok"}}})

	if err := r.Run(context.Background(), "read internal/react/loop.go"); err != nil {
		t.Fatal(err)
	}
	intent := session.Snapshot().SideEffectIntent
	if intent == nil || intent.ID != "intent-1" || !containsString(intent.AllowedPaths, "docs/a.md") || containsString(intent.AllowedPaths, "internal/react/loop.go") {
		t.Fatalf("SideEffectIntent = %#v", intent)
	}
}

func TestRunnerRunReturnsErrorWhenDriverMissing(t *testing.T) {
	r := NewRunner(Config{})
	if err := r.Run(context.Background(), "inspect"); err == nil {
		t.Fatal("expected error when driver is nil")
	}
}

func TestRunnerRunReturnsDurableInputAppendError(t *testing.T) {
	sinkErr := errors.New("durable append failed")
	session := NewSession()
	session.SetDurableSink(failingDurableSink{err: sinkErr})
	r := NewRunner(Config{Session: session})

	err := r.Run(context.Background(), "hello")
	if !errors.Is(err, sinkErr) {
		t.Fatalf("Run error = %v, want durable append error %v", err, sinkErr)
	}
}

func TestRunnerRunSkipsEmptyInput(t *testing.T) {
	driver := &nativeScriptedDriver{responses: []string{"ignored"}}
	r := NewRunner(Config{Driver: driver})
	if err := r.Run(context.Background(), "   "); err != nil {
		t.Fatal(err)
	}
	if driver.callCount != 0 {
		t.Fatalf("calls = %d, want 0", driver.callCount)
	}
}

func TestRunnerRunRecordsCompletedTurnDetails(t *testing.T) {
	driver := &nativeScriptedDriver{responses: []string{"repo overview"}}
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Session: session})

	if err := r.Run(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}

	snap := session.Snapshot()
	if len(snap.Turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(snap.Turns))
	}
	if snap.Turns[0].Input != "hello" {
		t.Fatalf("turn input = %q", snap.Turns[0].Input)
	}
	if snap.Turns[0].FinalResponse != "repo overview" {
		t.Fatalf("turn final response = %q", snap.Turns[0].FinalResponse)
	}
	if len(snap.Turns[0].ToolCalls) != 0 {
		t.Fatalf("tool calls = %d, want 0", len(snap.Turns[0].ToolCalls))
	}
}

func TestRunnerRunUsesActiveTurnContextAndEndsTurn(t *testing.T) {
	driver := &nativeToolCallDriver{}
	session := NewSession()
	toolCtx := make(chan context.Context, 1)
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "git_status",
		Description: "git status",
		AutoApprove: true,
		Execute: func(ctx context.Context, _ map[string]any) (string, error) {
			toolCtx <- ctx
			return "nothing to commit", nil
		},
	})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "check the repo"); err != nil {
		t.Fatal(err)
	}
	if ctx := <-toolCtx; ctx == context.Background() {
		t.Fatal("tool received original parent context, want active turn context")
	}
	if _, ok := session.ActiveTurnSnapshot(); ok {
		t.Fatal("active turn still present after Run completed")
	}
}

func TestRunnerRunRejectsOverlapWithoutRecordingSecondTurn(t *testing.T) {
	driver := &nativeToolCallDriver{}
	session := NewSession()
	toolStarted := make(chan struct{})
	releaseTool := make(chan struct{})
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "git_status",
		Description: "git status",
		AutoApprove: true,
		Execute: func(ctx context.Context, _ map[string]any) (string, error) {
			close(toolStarted)
			select {
			case <-releaseTool:
				return "nothing to commit", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
	})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})
	firstErr := make(chan error, 1)
	go func() {
		firstErr <- r.Run(context.Background(), "check the repo")
	}()
	<-toolStarted

	err := r.Run(context.Background(), "second run")
	if err == nil || !strings.Contains(err.Error(), "active turn") {
		t.Fatalf("Run error = %v, want active turn overlap error", err)
	}
	snap := session.Snapshot()
	if snap.Turn != 1 {
		t.Fatalf("turn = %d, want overlap rejection to leave turn at 1", snap.Turn)
	}
	var userMessages int
	for _, msg := range snap.History {
		if msg.Role == llm.RoleUser {
			userMessages++
		}
	}
	if userMessages != 1 {
		t.Fatalf("user messages = %d, want only first run recorded", userMessages)
	}

	close(releaseTool)
	if err := <-firstErr; err != nil {
		t.Fatal(err)
	}
	for _, item := range session.Snapshot().Items {
		if item.TurnID != "turn-1" {
			t.Fatalf("item %s has TurnID %q, want turn-1; item=%#v", item.Kind, item.TurnID, item)
		}
	}
}

func TestRunnerRunRejectsOverlapBeforeRuntimeNoteMutation(t *testing.T) {
	session := NewSession()
	_, cancel, err := session.BeginTurn(context.Background(), "turn-existing")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	r := NewRunner(Config{
		Driver:  &nativeScriptedDriver{responses: []string{"unused"}},
		Session: session,
		ConfigureHooks: func(registry *hooks.Registry) {
			registry.Register(hooks.PointPromptContext, "test:prompt", func(context.Context, hooks.Event) []hooks.Result {
				return []hooks.Result{hooks.OverlayResult{
					Key:        "test_overlap_overlay",
					Content:    "must not be applied before overlap rejection",
					Priority:   hooks.PriorityHigh,
					Provenance: "test",
				}}
			})
		},
	})
	before := session.Snapshot()

	err = r.Run(context.Background(), "inspect repo")
	if err == nil || !strings.Contains(err.Error(), "active turn") {
		t.Fatalf("Run error = %v, want active turn overlap error", err)
	}
	after := session.Snapshot()
	if after.RuntimeNote != before.RuntimeNote {
		t.Fatalf("runtime note changed from %q to %q", before.RuntimeNote, after.RuntimeNote)
	}
	if after.HookOutputSet != before.HookOutputSet || len(after.HookOutput.Overlays) != len(before.HookOutput.Overlays) {
		t.Fatalf("hook output changed from %#v to %#v", before.HookOutput, after.HookOutput)
	}
	if after.CompactedTurns != before.CompactedTurns || after.CompactionSummary != before.CompactionSummary {
		t.Fatalf("compaction state changed from (%d, %q) to (%d, %q)", before.CompactedTurns, before.CompactionSummary, after.CompactedTurns, after.CompactionSummary)
	}
	if len(after.History) != len(before.History) || len(after.Turns) != len(before.Turns) || len(after.Items) != len(before.Items) {
		t.Fatalf("session transcript changed: history %d->%d turns %d->%d items %d->%d", len(before.History), len(after.History), len(before.Turns), len(after.Turns), len(before.Items), len(after.Items))
	}
}

func TestRunnerRunActiveTurnCancellationCancelsBlockedToolContext(t *testing.T) {
	driver := &nativeToolCallDriver{}
	session := NewSession()
	toolStarted := make(chan struct{})
	toolCancelled := make(chan struct{})
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "git_status",
		Description: "git status",
		AutoApprove: true,
		Execute: func(ctx context.Context, _ map[string]any) (string, error) {
			close(toolStarted)
			<-ctx.Done()
			close(toolCancelled)
			return "", ctx.Err()
		},
	})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})
	runErr := make(chan error, 1)
	go func() {
		runErr <- r.Run(context.Background(), "check the repo")
	}()
	<-toolStarted

	if err := session.CancelActiveTurn("user cancelled"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-toolCancelled:
	case <-time.After(time.Second):
		t.Fatal("blocked tool context was not cancelled")
	}
	if err := <-runErr; err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context canceled", err)
	}
	for _, item := range session.Snapshot().Items {
		if item.Kind == protocol.ItemTurnComplete && item.TurnComplete != nil && item.TurnComplete.Status == protocol.TurnStatusCompleted {
			t.Fatalf("cancelled turn recorded successful terminal item: %#v", item)
		}
	}
}

func TestRunnerRunReturnsOverlapErrorWhenTurnAlreadyActive(t *testing.T) {
	session := NewSession()
	_, cancel, err := session.BeginTurn(context.Background(), "turn-existing")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	r := NewRunner(Config{Driver: &nativeScriptedDriver{responses: []string{"unused"}}, Session: session})

	err = r.Run(context.Background(), "inspect repo")
	if err == nil || !strings.Contains(err.Error(), "active turn") {
		t.Fatalf("Run error = %v, want active turn overlap error", err)
	}
	if session.Snapshot().Turn != 0 {
		t.Fatalf("recorded turns = %d, want overlap rejection before recording input", session.Snapshot().Turn)
	}
}

func TestRunnerInvokesTurnCompleteHookAfterSuccessfulTurn(t *testing.T) {
	driver := &nativeScriptedDriver{responses: []string{"repo overview"}}
	session := NewSession()
	var got SessionSnapshot
	called := false
	r := NewRunner(Config{
		Driver:  driver,
		Session: session,
		TurnComplete: func(snapshot SessionSnapshot) {
			called = true
			got = snapshot
		},
	})

	if err := r.Run(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("expected turn-complete hook")
	}
	if got.Turn != 1 {
		t.Fatalf("snapshot turn = %d", got.Turn)
	}
	if len(got.Turns) != 1 || got.Turns[0].FinalResponse != "repo overview" {
		t.Fatalf("snapshot turns = %#v", got.Turns)
	}
}

func TestRunnerTurnCompleteHookCanFeedMemorySummaryIntoNextTurn(t *testing.T) {
	driver := &captureMessagesDriver{response: "second answer"}
	session := NewSession()
	r := NewRunner(Config{
		Driver:  driver,
		Session: session,
		TurnComplete: func(snapshot SessionSnapshot) {
			if len(snapshot.Turns) == 1 {
				session.SetMemorySummary("Remembered: runtime flow was already inspected.")
			}
		},
	})

	firstDriver := &nativeScriptedDriver{responses: []string{"first answer"}}
	r.SetDriver(firstDriver)
	if err := r.Run(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}

	r.SetDriver(driver)
	session.SetMode(ModeInspect)
	if err := r.Run(context.Background(), "keep going"); err != nil {
		t.Fatal(err)
	}

	foundMemory := false
	for _, msg := range driver.lastMessages {
		if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "Memory summary:") {
			foundMemory = true
			break
		}
	}
	if !foundMemory {
		t.Fatalf("expected memory summary in second turn messages: %#v", driver.lastMessages)
	}
}

func TestRunnerRunRecordsTurnError(t *testing.T) {
	driver := &errorDriver{err: context.DeadlineExceeded}
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Session: session})

	if err := r.Run(context.Background(), "inspect repo"); err == nil {
		t.Fatal("expected runner error")
	}

	snap := session.Snapshot()
	if len(snap.Turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(snap.Turns))
	}
	if snap.Turns[0].Error == "" {
		t.Fatal("expected recorded turn error")
	}
}

func TestRunnerIncludesInterruptedGuidanceAfterExecSessionTurn(t *testing.T) {
	driver := &captureMessagesDriver{response: "checked current state before continuing"}
	session := NewSession()

	turn := session.RecordInput("start the dev server")
	session.AppendAssistantWithToolCalls([]llm.NativeToolCall{{
		ID:       "call-1",
		Name:     "exec_session_start",
		ArgsJSON: `{"command":"npm run dev","cols":120,"rows":40}`,
	}})
	if err := session.AppendNativeToolResult("call-1", `{"status":"running","session_id":9,"command":"npm run dev","pty":true,"cols":120,"rows":40}`); err != nil {
		t.Fatal(err)
	}
	if err := session.CompleteTurn(turn, "", []TurnToolCall{{Name: "exec_session_start"}}, nil); err != nil {
		t.Fatal(err)
	}
	session.MarkInterrupted()

	r := NewRunner(Config{
		Driver:  driver,
		Session: session,
	})

	if err := r.Run(context.Background(), "continue from there"); err != nil {
		t.Fatal(err)
	}

	var interruptedMsg string
	for _, msg := range driver.lastMessages {
		if msg.Role == llm.RoleSystem && strings.Contains(strings.ToLower(msg.Content), "previous turn was interrupted") {
			interruptedMsg = msg.Content
			break
		}
	}
	if interruptedMsg == "" {
		t.Fatalf("expected interrupted guidance in messages: %#v", driver.lastMessages)
	}
	if !strings.Contains(interruptedMsg, "verify current state before continuing") {
		t.Fatalf("interrupted guidance = %q", interruptedMsg)
	}
}

func TestRunnerIncludesReviewRuntimeGuidance(t *testing.T) {
	driver := &nativeSequenceDriver{
		steps: [][]llm.Token{
			{{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "list_dir", ArgsJSON: `{"path":"."}`}}},
			{{Text: "- Finding: runtime guidance is present."}},
		},
	}
	session := NewSession()
	session.SetTaskState(TaskState{
		Objective:            "review this repo and tell me what i need to change",
		Operation:            "review",
		RequiredVerification: "produce source-grounded findings first, ordered by severity, and keep the summary secondary to the actual review issues",
	})
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "list_dir",
		Description: "list a directory",
		Parameters:  []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			if got := fmt.Sprint(args["path"]); got != "." {
				t.Fatalf("path = %q, want .", got)
			}
			return "README.md\ninternal/", nil
		},
	})

	r := NewRunner(Config{
		Driver:  driver,
		Tools:   reg,
		Session: session,
	})

	if err := r.Run(context.Background(), "review this repo"); err != nil {
		t.Fatal(err)
	}

	if len(driver.allMsgs) == 0 {
		t.Fatalf("expected driver messages, got none")
	}
	var reviewMsg string
	for _, msg := range driver.allMsgs[len(driver.allMsgs)-1] {
		if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "[hook:runtime]") && strings.Contains(msg.Content, "Review workflow active") {
			reviewMsg = msg.Content
			break
		}
	}
	if reviewMsg == "" {
		t.Fatalf("expected review runtime guidance in messages: %#v", driver.allMsgs)
	}
	if !strings.Contains(reviewMsg, "Lead with findings") {
		t.Fatalf("review guidance = %q", reviewMsg)
	}
}

func TestRunnerIncludesBlockedPlanRuntimeGuidance(t *testing.T) {
	driver := &captureMessagesDriver{response: "Plan is blocked on user input."}
	session := NewSession()
	session.SetTaskState(TaskState{
		Objective:            "write a plan for prompt/runtime work",
		Operation:            "plan",
		RequiredVerification: "produce a concise plan grounded in enough repo evidence",
	})
	session.SetPlanState(PlanState{
		Steps: []PlanStep{
			{Step: "Confirm whether plan mode should be mandatory by default", Status: "blocked", Blocker: "need user decision on default behavior"},
			{Step: "Implement the approved runtime behavior", Status: "pending"},
		},
	})

	r := NewRunner(Config{
		Driver:  driver,
		Session: session,
	})

	if err := r.Run(context.Background(), "keep going"); err != nil {
		t.Fatalf("err = %v, want honest blocked report accepted as final answer", err)
	}

	var blockedMsg string
	for _, msg := range driver.lastMessages {
		if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "[hook:runtime]") && strings.Contains(msg.Content, "Current plan is blocked") {
			blockedMsg = msg.Content
			break
		}
	}
	if blockedMsg == "" {
		t.Fatalf("expected blocked plan runtime guidance in messages: %#v", driver.lastMessages)
	}
	if !strings.Contains(blockedMsg, "ask_user_question") {
		t.Fatalf("blocked guidance = %q", blockedMsg)
	}
}

func TestRunnerBlockedPlanOverlayCoexistsWithSuggestedSkillOverlay(t *testing.T) {
	driver := &captureMessagesDriver{response: "Plan is blocked on user input."}
	session := NewSession()
	session.SetHookOverlay(HookOverlay{
		Key:        "suggested_skill",
		Content:    "suggested skill: /brainstorming (planning work benefits from explicit design before implementation)",
		Priority:   HookPriorityNormal,
		Provenance: "runtime",
	})
	session.SetTaskState(TaskState{
		Objective:            "write a plan for prompt/runtime work",
		Operation:            "plan",
		RequiredVerification: "produce a concise plan grounded in enough repo evidence",
	})
	session.SetPlanState(PlanState{
		Steps: []PlanStep{
			{Step: "Confirm whether plan mode should be mandatory by default", Status: "blocked", Blocker: "need user decision on default behavior"},
			{Step: "Implement the approved runtime behavior", Status: "pending"},
		},
	})

	r := NewRunner(Config{
		Driver:  driver,
		Session: session,
	})

	if err := r.Run(context.Background(), "keep going"); err != nil {
		t.Fatalf("err = %v, want honest blocked report accepted as final answer", err)
	}

	foundSuggested := false
	foundBlocked := false
	for _, msg := range driver.lastMessages {
		if msg.Role != llm.RoleSystem {
			continue
		}
		if strings.Contains(msg.Content, "suggested skill: /brainstorming") {
			foundSuggested = true
		}
		if strings.Contains(msg.Content, "Current plan is blocked") {
			foundBlocked = true
		}
	}
	if !foundSuggested || !foundBlocked {
		t.Fatalf("expected both overlays, got %#v", driver.lastMessages)
	}
}

func TestRunnerPromptHookOutputIncludesRuntimeGuidance(t *testing.T) {
	t.Run("inspect first action guidance", func(t *testing.T) {
		session := NewSession()
		session.SetTaskState(TaskState{
			Objective:            "inspect the image input flow",
			Operation:            "inspect",
			RequiredVerification: "inspect the repository with read/search tools before answering. For a casual repo overview, usually inspect the repo root and one high-signal file such as README.md, then give a brief overview grounded only in that evidence",
		})
		r := NewRunner(Config{Session: session})

		output := r.promptHookOutput(context.Background())

		got := hookOverlayContent(output, "inspect_first_action")
		if !strings.Contains(got, "Start with a short natural sentence") {
			t.Fatalf("inspect_first_action = %q", got)
		}
		if strings.Contains(got, "instead of prose") {
			t.Fatalf("inspect_first_action = %q", got)
		}
	})

	t.Run("overview first action guidance", func(t *testing.T) {
		session := NewSession()
		session.SetTaskState(TaskState{
			Objective:            "whats this repo all about",
			Operation:            "overview",
			RequiredVerification: "inspect the repository with read/search tools before answering. For a casual repo overview, usually inspect the repo root and one high-signal file such as README.md, then give a brief overview grounded only in that evidence",
		})
		r := NewRunner(Config{Session: session})

		output := r.promptHookOutput(context.Background())

		if got := hookOverlayContent(output, "inspect_first_action"); !strings.Contains(got, "Start with a short natural sentence") {
			t.Fatalf("inspect_first_action = %q", got)
		}
	})

	t.Run("inspect first action guidance clears after read evidence", func(t *testing.T) {
		session := NewSession()
		session.SetTaskState(TaskState{
			Objective:            "inspect the theme setup",
			Operation:            "inspect",
			RequiredVerification: "inspect the repository with read/search tools before answering",
		})
		session.RecordInput("inspect the theme setup")
		session.AppendAssistantWithToolCalls([]llm.NativeToolCall{{ID: "c1", Name: "read_file", ArgsJSON: `{"path":"README.md"}`}})
		if err := session.AppendNativeToolResult("c1", "forge readme"); err != nil {
			t.Fatal(err)
		}
		r := NewRunner(Config{Session: session})

		output := r.promptHookOutput(context.Background())

		if got := hookOverlayContent(output, "inspect_first_action"); got != "" {
			t.Fatalf("inspect_first_action should be cleared after read evidence, got %q", got)
		}
	})

	t.Run("repo overview guidance flips to synthesis after read evidence", func(t *testing.T) {
		session := NewSession()
		session.SetTaskState(TaskState{
			Objective:            "whats this repo all about",
			Operation:            "overview",
			RequiredVerification: "inspect the repository with read/search tools before answering. For a casual repo overview, usually inspect the repo root and one high-signal file such as README.md, then give a brief overview grounded only in that evidence",
		})
		session.RecordInput("whats this repo all about")
		session.AppendAssistantWithToolCalls([]llm.NativeToolCall{{ID: "c1", Name: "read_file", ArgsJSON: `{"path":"README.md"}`}})
		if err := session.AppendNativeToolResult("c1", "forge readme"); err != nil {
			t.Fatal(err)
		}
		r := NewRunner(Config{Session: session})

		output := r.promptHookOutput(context.Background())

		if got := hookOverlayContent(output, "inspect_first_action"); !strings.Contains(got, "stop exploring and answer briefly") {
			t.Fatalf("inspect_first_action = %q", got)
		}
	})

	t.Run("preview first action guidance", func(t *testing.T) {
		session := NewSession()
		session.SetTaskState(TaskState{
			Objective:            "show me 3 theme ideas and start a preview",
			Operation:            "preview",
			RequiredVerification: "use preview or artifact tools, verify the preview URL or visible result, and do not rely on an ad-hoc shell webserver",
		})
		r := NewRunner(Config{Session: session})

		output := r.promptHookOutput(context.Background())

		if got := hookOverlayContent(output, "preview_workflow"); !strings.Contains(got, "most likely directory or named file") {
			t.Fatalf("preview_workflow = %q", got)
		}
	})

	t.Run("preview guidance flips to build after exploration budget", func(t *testing.T) {
		session := NewSession()
		session.SetTaskState(TaskState{
			Objective:            "show me 3 theme ideas and start a preview",
			Operation:            "preview",
			RequiredVerification: "use preview or artifact tools, verify the preview URL or visible result, and do not rely on an ad-hoc shell webserver",
		})
		session.RecordInput("show me 3 theme ideas and start a preview")
		session.AppendAssistantWithToolCalls([]llm.NativeToolCall{{ID: "c1", Name: "read_file", ArgsJSON: `{"path":"internal/tui/chattheme.go"}`}})
		if err := session.AppendNativeToolResult("c1", "theme source"); err != nil {
			t.Fatal(err)
		}
		r := NewRunner(Config{Session: session})
		r.planWorkflow = planWorkflowState{
			mode:              "preview",
			active:            true,
			synthesisRequired: true,
		}

		output := r.promptHookOutput(context.Background())

		if got := hookOverlayContent(output, "preview_workflow"); !strings.Contains(got, "Stop exploring. Write the preview artifact") {
			t.Fatalf("preview_workflow = %q", got)
		}
	})

	t.Run("review guidance", func(t *testing.T) {
		session := NewSession()
		session.SetTaskState(TaskState{
			Objective:            "review this repo",
			Operation:            "review",
			RequiredVerification: "lead with findings",
		})
		r := NewRunner(Config{Session: session})

		output := r.promptHookOutput(context.Background())

		if got := hookOverlayContent(output, "review_guidance"); !strings.Contains(got, "Lead with findings") {
			t.Fatalf("review_guidance = %q", got)
		}
	})

	t.Run("blocked plan guidance", func(t *testing.T) {
		session := NewSession()
		session.SetTaskState(TaskState{
			Objective:            "write a plan",
			Operation:            "plan",
			RequiredVerification: "produce a concise plan",
		})
		session.SetPlanState(PlanState{
			Steps: []PlanStep{
				{Step: "Get a user decision", Status: "blocked", Blocker: "need user decision on default behavior"},
			},
		})
		r := NewRunner(Config{Session: session})

		output := r.promptHookOutput(context.Background())

		if got := hookOverlayContent(output, "plan_blocker"); !strings.Contains(got, "ask_user_question") {
			t.Fatalf("plan_blocker = %q", got)
		}
	})

	t.Run("synthesis validation search git and repeat guidance", func(t *testing.T) {
		session := NewSession()
		r := NewRunner(Config{Session: session})
		r.planWorkflow = planWorkflowState{
			mode:              "analysis",
			active:            true,
			synthesisRequired: true,
		}
		r.validationWorkflow = validationWorkflowState{
			ran:    true,
			passed: false,
			cmd:    "go test ./internal/react",
		}
		r.searchWorkflow = sameFileSearchWorkflowState{
			toolName: "code_search",
			path:     "internal/react/loop.go",
			streak:   sameFileSearchThrashThreshold,
			nudged:   true,
		}
		r.gitWorkflow = gitWorkflowState{
			mergeActive:    true,
			commitBlocker:  commitBlockerEdit,
			blockerSummary: "commit blocked by pre-commit hook failures",
		}
		r.repeatWorkflow = repeatToolCallState{
			lastToolName: "read_file",
			lastTarget:   "internal/react/loop.go",
			streak:       repeatToolCallThreshold,
		}

		output := r.promptHookOutput(context.Background())

		if got := hookOverlayContent(output, "synthesis_guidance"); !strings.Contains(got, "Analysis guidance") {
			t.Fatalf("synthesis_guidance = %q", got)
		}
		if got := hookOverlayContent(output, "validation_failure"); !strings.Contains(got, "Last validation failed") {
			t.Fatalf("validation_failure = %q", got)
		}
		if got := hookOverlayContent(output, "search_thrash"); !strings.Contains(got, "Search thrash guidance") {
			t.Fatalf("search_thrash = %q", got)
		}
		if got := hookOverlayContent(output, "git_workflow"); !strings.Contains(got, "Git merge workflow active") {
			t.Fatalf("git_workflow = %q", got)
		}
		if got := hookOverlayContent(output, "repeat_loop"); !strings.Contains(got, "Loop detection") {
			t.Fatalf("repeat_loop = %q", got)
		}
	})

	t.Run("post delegation write guidance", func(t *testing.T) {
		snap := SessionSnapshot{
			LastInput: "compare repos and write the result as a markdown report to docs/reports/report.md",
			PendingDelegationAction: &DelegationActionState{
				Kind:       DelegationActionWriteDoc,
				TargetPath: "docs/reports/report.md",
			},
			History: []llm.Message{
				{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{{ID: "wait-1", Name: "wait_agent", ArgsJSON: `{"id":"agent-1"}`}}},
				{Role: llm.RoleTool, ToolCallID: "wait-1", Content: `{"id":"agent-1","status":"completed","result":"raw child report"}`},
			},
		}

		output := promptHookOutputForSnapshot(t, snap)
		got := hookOverlayContent(output, "post_delegation_write")
		for _, want := range []string{"synthesize", "Do not paste raw child-agent outputs", "write_file", "docs/reports/report.md"} {
			if !strings.Contains(got, want) {
				t.Fatalf("post_delegation_write = %q, want %q", got, want)
			}
		}
	})
}

func TestRunnerBeforeToolHookAllowsBroadAlternationSearch(t *testing.T) {
	r := NewRunner(Config{})

	output := r.beforeToolHookOutput(context.Background(), "search", map[string]any{
		"pattern": "tui|theming|theme|terminal ui|tailwind|colors|palette|config|styles|css|scss",
		"path":    ".",
	})

	if output.Block != nil {
		t.Fatalf("search should not be hard-blocked: %#v", output.Block)
	}
}

func TestRunnerBeforeToolHookBlocksCommitWorkflow(t *testing.T) {
	t.Run("unmerged conflicts", func(t *testing.T) {
		r := NewRunner(Config{})
		r.gitWorkflow.unmergedFiles = true

		output := r.beforeToolHookOutput(context.Background(), "run_command", map[string]any{
			"command": `git commit -m "merge"`,
		})

		if output.Block == nil || !strings.Contains(output.Block.Message, "unmerged git conflicts remain") {
			t.Fatalf("block = %#v", output.Block)
		}
	})

	t.Run("restage blocker allows git add", func(t *testing.T) {
		r := NewRunner(Config{})
		r.gitWorkflow.commitBlocker = commitBlockerRestage

		output := r.beforeToolHookOutput(context.Background(), "run_command", map[string]any{
			"command": "git add README.md",
		})

		if output.Block != nil {
			t.Fatalf("unexpected block = %#v", output.Block)
		}
	})

	t.Run("edit blocker", func(t *testing.T) {
		r := NewRunner(Config{})
		r.gitWorkflow.commitBlocker = commitBlockerEdit

		output := r.beforeToolHookOutput(context.Background(), "git_commit", map[string]any{
			"message": "merge branch",
		})

		if output.Block == nil || !strings.Contains(output.Block.Message, "previous commit attempt already failed") {
			t.Fatalf("block = %#v", output.Block)
		}
	})
}

func TestRunnerAllowsScopedGitCommitToRecoverRestageWorkflow(t *testing.T) {
	r := NewRunner(Config{})
	r.gitWorkflow.commitBlocker = commitBlockerRestage
	session := NewSession()
	session.SetSideEffectIntent(SideEffectIntent{ID: "intent-1", AllowedPaths: []string{"FORGE_VS_CODEX.md"}, RequiredActions: []SideEffectAction{SideEffectActionCommit}})
	r.session = session

	commitOutput := r.beforeToolHookOutput(context.Background(), "git_commit", map[string]any{"message": "retry scoped commit"})
	if commitOutput.Block != nil {
		t.Fatalf("scoped git_commit should recover restage workflow, block = %#v", commitOutput.Block)
	}
	addOutput := r.beforeToolHookOutput(context.Background(), "run_command", map[string]any{"command": "git add FORGE_VS_CODEX.md"})
	if addOutput.Block == nil || !strings.Contains(addOutput.Block.Message, "scoped git transaction") {
		t.Fatalf("shell git add block = %#v", addOutput.Block)
	}
}

func TestRunnerBlocksShellGitMutationDuringScopedIntent(t *testing.T) {
	r := NewRunner(Config{})
	session := NewSession()
	session.SetSideEffectIntent(SideEffectIntent{ID: "intent-1", AllowedPaths: []string{"FORGE_VS_CODEX.md"}, RequiredActions: []SideEffectAction{SideEffectActionCommit}})
	r.session = session

	for _, command := range []string{
		"git add .",
		"git add -A",
		"git commit -m x",
		"git push origin HEAD:main",
		"git reset --hard HEAD~1",
		"git restore FORGE_VS_CODEX.md",
		"git clean -fd",
		"git checkout -- FORGE_VS_CODEX.md",
		"git checkout src/main",
		"git checkout main",
		"git switch main",
		"git merge feature",
		"git rebase main",
		"git pull --rebase",
		"git cherry-pick abc1234",
		"git revert abc1234",
		"git rm old.txt",
		"git mv old.txt new.txt",
		"git stash push",
		"git branch -D old-branch",
		"git remote add origin https://example.invalid/repo.git",
		"git tag v1.0.0",
		"git config user.name x",
		"git -C repo add .",
		"git -c user.name=x commit -m x",
		"(git add .)",
		"command git commit -m x",
		"env VAR=x git push origin HEAD:main",
		"sh -c 'git add .'",
		"bash -lc 'git commit -m x'",
		"sudo git add .",
		"sudo -n git add .",
		"time git add .",
		"time -p git add .",
		"env -i git add .",
		"eval 'git add .'",
		"exec git add .",
		"echo $(git add .)",
		"echo `git commit -m x`",
		"echo \"$(git add .)\"",
		"echo \"x$(git commit -m x)\"",
		"echo \"`git add .`\"",
		"if git status; then git add .; fi",
		"git status\ngit add .",
		"git status & git add .",
		"git status && git add .",
		"echo ok; git commit -m x",
		`g\it add .`,
		"g''it commit -m x",
		"command g''it add .",
	} {
		t.Run(command, func(t *testing.T) {
			output := r.beforeToolHookOutput(context.Background(), "run_command", map[string]any{"command": command})
			if output.Block == nil || !strings.Contains(output.Block.Message, "scoped git transaction") {
				t.Fatalf("block for %q = %#v", command, output.Block)
			}
		})
	}
}

func TestRunnerBlocksExecSessionGitMutationDuringScopedIntent(t *testing.T) {
	r := NewRunner(Config{})
	session := NewSession()
	session.SetSideEffectIntent(SideEffectIntent{ID: "intent-1", AllowedPaths: []string{"FORGE_VS_CODEX.md"}, RequiredActions: []SideEffectAction{SideEffectActionCommit}})
	r.session = session

	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{tool: "exec_session_start", args: map[string]any{"command": "git add ."}},
		{tool: "exec_session_write", args: map[string]any{"chars": "git add .\n"}},
		{tool: "exec_session_write", args: map[string]any{"chars": "git "}},
		{tool: "exec_session_write", args: map[string]any{"chars": "add .\n"}},
		{tool: "command_write_stdin", args: map[string]any{"chars": "git commit -m x\n"}},
		{tool: "command_write_stdin", args: map[string]any{"chars": "commit -m x\n"}},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			output := r.beforeToolHookOutput(context.Background(), tc.tool, tc.args)
			if output.Block == nil || !strings.Contains(output.Block.Message, "scoped git transaction") {
				t.Fatalf("block for %s = %#v", tc.tool, output.Block)
			}
		})
	}
}

func TestRunnerAllowsShellGitReadOnlyDuringScopedIntent(t *testing.T) {
	r := NewRunner(Config{})
	session := NewSession()
	session.SetSideEffectIntent(SideEffectIntent{ID: "intent-1", AllowedPaths: []string{"FORGE_VS_CODEX.md"}, RequiredActions: []SideEffectAction{SideEffectActionCommit}})
	r.session = session

	for _, command := range []string{"git status", "git diff", "git log --oneline -n 1", "git show --stat", "git branch --show-current"} {
		t.Run(command, func(t *testing.T) {
			output := r.beforeToolHookOutput(context.Background(), "run_command", map[string]any{"command": command})
			if output.Block != nil {
				t.Fatalf("unexpected block for %q = %#v", command, output.Block)
			}
		})
	}
}

func TestRunnerPlanStateBlocksFinalSuccessWhenStepInProgress(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{Text: "Done, implemented the requested change."}},
		{{Text: "I still need to finish the implementation step."}},
		{{Text: "I still need to finish the implementation step."}},
	}}
	session := NewSession()
	session.SetPlanState(PlanState{Steps: []PlanStep{{Step: "Implement the requested change", Status: "in_progress"}}})
	r := NewRunner(Config{Driver: driver, Session: session})

	if err := r.Run(context.Background(), "continue"); err != nil {
		t.Fatalf("err = %v, want final answer accepted after bounded gate nudges", err)
	}
	if strings.Contains(r.LastResponse(), "implemented the requested change") {
		t.Fatalf("final response = %q, should not claim completion while plan is in progress", r.LastResponse())
	}
	if !sessionHistoryContains(session, "plan state inconsistent", "Implement the requested change") {
		t.Fatalf("history missing concrete plan feedback: %#v", session.Snapshot().History)
	}
	if driver.callCount != 3 {
		t.Fatalf("driver calls = %d, want two nudges then acceptance", driver.callCount)
	}
}

func TestRunnerPlanStateAndSideEffectGateFeedbackAreBothReported(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{Text: "Done, committed and pushed."}},
		{{Text: "I still need to finish the implementation and run git_commit."}},
		{{Text: "I still need to finish the implementation and run git_commit."}},
	}}
	session := NewSession()
	session.SetPlanState(PlanState{Steps: []PlanStep{{Step: "Finish implementation", Status: "in_progress"}}})
	session.SetSideEffectIntent(SideEffectIntent{
		ID:              "intent-1",
		AllowedPaths:    []string{"docs/a.md"},
		RequiredActions: []SideEffectAction{SideEffectActionWrite, SideEffectActionCommit},
		Gates:           []SideEffectGate{{Name: "write", Status: SideEffectGatePassed}, {Name: "commit", Status: SideEffectGatePending}},
	})
	r := NewRunner(Config{Driver: driver, Session: session})

	if err := r.Run(context.Background(), "continue"); err != nil {
		t.Fatalf("err = %v, want final answer accepted after bounded gate nudges", err)
	}
	if !sessionHistoryContains(session, "plan state inconsistent", "Finish implementation") {
		t.Fatalf("history missing plan feedback: %#v", session.Snapshot().History)
	}
	if !sessionHistoryContains(session, "unresolved side-effect gates", "commit") {
		t.Fatalf("history missing side-effect feedback: %#v", session.Snapshot().History)
	}
}

func TestRunnerPlanStateBlocksFinalSuccessWhenStepBlocked(t *testing.T) {
	driver := &nativeSequenceDriver{steps: repeatedTextSteps("Complete, all work is done.", 4)}
	session := NewSession()
	session.SetPlanState(PlanState{Steps: []PlanStep{{Step: "Choose deployment target", Status: "blocked", Blocker: "need user decision"}}})
	r := NewRunner(Config{Driver: driver, Session: session, MaxSteps: 5})

	if err := r.Run(context.Background(), "continue"); err != nil {
		t.Fatalf("err = %v, want final answer accepted after bounded gate nudges", err)
	}
	if turns := session.Snapshot().Turns; len(turns) == 0 || strings.TrimSpace(turns[len(turns)-1].FinalResponse) == "" {
		t.Fatalf("turns = %#v, want persisted final response after gate cap", turns)
	}
	if !sessionHistoryContains(session, "plan state inconsistent", "Choose deployment target", "need user decision") {
		t.Fatalf("history missing concrete blocked-plan feedback: %#v", session.Snapshot().History)
	}
}

func TestRunnerPlanStateFailureReportWithBlockedStepIsDeliveredAfterNudges(t *testing.T) {
	driver := &nativeSequenceDriver{steps: repeatedTextSteps("I cannot complete this because the plan is blocked on user approval.", 3)}
	session := NewSession()
	session.SetPlanState(PlanState{Steps: []PlanStep{{Step: "Get approval", Status: "blocked", Blocker: "waiting for user approval"}}})
	r := NewRunner(Config{Driver: driver, Session: session, MaxSteps: 5})

	if err := r.Run(context.Background(), "continue"); err != nil {
		t.Fatalf("err = %v, want final answer accepted after bounded gate nudges", err)
	}
	if driver.callCount != 3 {
		t.Fatalf("driver calls = %d, want two nudges then acceptance", driver.callCount)
	}
	turns := session.Snapshot().Turns
	if len(turns) == 0 {
		t.Fatal("missing turn record")
	}
	last := turns[len(turns)-1]
	if !strings.Contains(last.FinalResponse, "blocked on user approval") {
		t.Fatalf("FinalResponse = %q, want honest blocker report delivered to user", last.FinalResponse)
	}
}

func TestRunnerAllowsFinalSuccessWhenAllPlanStepsComplete(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{{{Text: "Done, implemented the requested change."}}}}
	session := NewSession()
	session.SetPlanState(PlanState{Steps: []PlanStep{{Step: "Implement the requested change", Status: "completed"}}})
	r := NewRunner(Config{Driver: driver, Session: session})

	if err := r.Run(context.Background(), "continue"); err != nil {
		t.Fatal(err)
	}
	if got := r.LastResponse(); got != "Done, implemented the requested change." {
		t.Fatalf("LastResponse = %q", got)
	}
}

func TestRunnerPlanStateBlocksPendingAndUnknownStatuses(t *testing.T) {
	for _, status := range []string{"pending", "waiting_for_review"} {
		t.Run(status, func(t *testing.T) {
			driver := &nativeSequenceDriver{steps: repeatedTextSteps("Done, implemented the requested change.", 4)}
			session := NewSession()
			session.SetPlanState(PlanState{Steps: []PlanStep{{Step: "Implement the requested change", Status: status}}})
			r := NewRunner(Config{Driver: driver, Session: session, MaxSteps: 5})

			if err := r.Run(context.Background(), "continue"); err != nil {
				t.Fatalf("err = %v, want final answer accepted after bounded gate nudges", err)
			}
			if !sessionHistoryContains(session, "plan state inconsistent", "Implement the requested change") {
				t.Fatalf("history missing plan feedback: %#v", session.Snapshot().History)
			}
		})
	}
}

func TestRunnerPlanStateGateNoPlanStateLeavesFinalBehaviorUnchanged(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{{{Text: "Done, implemented the requested change."}}}}
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Session: session})

	if err := r.Run(context.Background(), "continue"); err != nil {
		t.Fatal(err)
	}
	if got := r.LastResponse(); got != "Done, implemented the requested change." {
		t.Fatalf("LastResponse = %q", got)
	}
}

func TestRunnerFinalCompletionValidationBlocksUnresolvedGates(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("continue")
	session.SetPlanState(PlanState{Steps: []PlanStep{{Step: "Write required artifact", Status: "in_progress"}}})
	session.SetSideEffectIntent(SideEffectIntent{
		ID:              "intent-1",
		AllowedPaths:    []string{"docs/a.md"},
		RequiredActions: []SideEffectAction{SideEffectActionWrite, SideEffectActionCommit},
		Gates:           []SideEffectGate{{Name: "write", Status: SideEffectGatePassed}, {Name: "commit", Status: SideEffectGatePending}},
	})
	r := NewRunner(Config{Session: session})
	_, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	ok, err := r.validateFinalCompletion(context.Background(), turn, "Done, I wrote and committed the artifact.", false)

	if ok || err == nil || !strings.Contains(err.Error(), "plan state inconsistent") {
		t.Fatalf("ok, err = %v, %v; want plan-state gate failure", ok, err)
	}
	if !sessionHistoryContains(session, "plan state inconsistent", "Write required artifact") || !sessionHistoryContains(session, "unresolved side-effect gates", "commit") {
		t.Fatalf("history missing combined central validation feedback: %#v", session.Snapshot().History)
	}
}

func TestRunnerFinalCompletionValidationStaleTurnDoesNotMutate(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("continue")
	session.SetPlanState(PlanState{Steps: []PlanStep{{Step: "Write required artifact", Status: "in_progress"}}})
	session.SetSideEffectIntent(SideEffectIntent{
		ID:              "intent-1",
		AllowedPaths:    []string{"docs/a.md"},
		RequiredActions: []SideEffectAction{SideEffectActionWrite},
		Gates:           []SideEffectGate{{Name: "write", Status: SideEffectGatePending}},
	})
	session.SetTurnContract(TurnContract{ID: "contract-1", Status: ContractStatusActive})
	r := NewRunner(Config{Session: session})
	_, cancel, err := session.BeginTurn(context.Background(), "turn-2")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	before := session.Snapshot()

	ok, err := r.validateFinalCompletion(context.Background(), turn, "Done, I wrote the artifact.", false)

	if ok || !errors.Is(err, ErrStaleTurn) {
		t.Fatalf("ok, err = %v, %v; want stale turn", ok, err)
	}
	after := session.Snapshot()
	if len(after.History) != len(before.History) {
		t.Fatalf("stale final validation mutated history: before=%#v after=%#v", before.History, after.History)
	}
	if !reflect.DeepEqual(after.SideEffectIntent, before.SideEffectIntent) {
		t.Fatalf("stale final validation mutated side-effect intent: before=%#v after=%#v", before.SideEffectIntent, after.SideEffectIntent)
	}
	if !reflect.DeepEqual(after.TurnContract, before.TurnContract) {
		t.Fatalf("stale final validation mutated turn contract: before=%#v after=%#v", before.TurnContract, after.TurnContract)
	}
}

func TestRunnerRequiredActionAndVerificationBlockSuccessWithoutEvidence(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("fix this and run tests")
	session.SetTurnContract(TurnContract{
		ID:                   "contract-1",
		SourceTurn:           turn,
		Intent:               TurnIntentEditCode,
		RequiredActions:      []ContractAction{{Kind: ContractActionEdit}},
		RequiredVerification: []VerificationRequirement{{Command: "go test ./internal/react"}},
		Status:               ContractStatusActive,
	})
	r := NewRunner(Config{Session: session})
	_, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	ok, err := r.validateFinalCompletion(context.Background(), turn, "Done, fixed this and tests pass.", false)

	if ok || err != nil {
		t.Fatalf("ok, err = %v, %v; want blocked without hard error", ok, err)
	}
	if !sessionHistoryContains(session, "required turn contract evidence missing", "edit", "verification") {
		t.Fatalf("history missing required evidence feedback: %#v", session.Snapshot().History)
	}
}

func TestRunnerRequiredActionBlocksPlainSuccessfulFinalWithoutEvidence(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("inspect this")
	session.SetTurnContract(TurnContract{
		ID:              "contract-1",
		SourceTurn:      turn,
		Intent:          TurnIntentInspect,
		RequiredActions: []ContractAction{{Kind: ContractActionRead}},
		Status:          ContractStatusActive,
	})
	r := NewRunner(Config{Session: session})
	_, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	ok, err := r.validateFinalCompletion(context.Background(), turn, "The repo contains Go code.", false)

	if ok || err != nil {
		t.Fatalf("ok, err = %v, %v; want blocked without hard error", ok, err)
	}
	if !sessionHistoryContains(session, "required turn contract evidence missing", "read") {
		t.Fatalf("history missing read evidence feedback: %#v", session.Snapshot().History)
	}
}

func TestRunnerRequiredActionAndVerificationPassAfterEvidence(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("fix this and run tests")
	session.SetTurnContract(TurnContract{
		ID:                   "contract-1",
		SourceTurn:           turn,
		Intent:               TurnIntentEditCode,
		RequiredActions:      []ContractAction{{Kind: ContractActionEdit}},
		RequiredVerification: []VerificationRequirement{{Command: "go test ./internal/react"}},
		Evidence: []EvidenceRecord{
			{Kind: EvidenceWrite, Summary: "write edit_file main.go"},
			{Kind: EvidenceVerification, Summary: "verification passed: go test ./internal/react"},
		},
		Status: ContractStatusActive,
	})
	r := NewRunner(Config{Session: session})
	_, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	ok, err := r.validateFinalCompletion(context.Background(), turn, "Done, fixed this and tests pass.", false)

	if !ok || err != nil {
		t.Fatalf("ok, err = %v, %v; want success", ok, err)
	}
}

func TestRunnerSuccessfulFinalMarksTurnContractSatisfied(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("fix this")
	session.SetTurnContract(TurnContract{
		ID:              "contract-1",
		SourceTurn:      turn,
		Intent:          TurnIntentEditCode,
		RequiredActions: []ContractAction{{Kind: ContractActionEdit}},
		Evidence:        []EvidenceRecord{{Kind: EvidenceWrite, Summary: "write edit_file main.go"}},
		Status:          ContractStatusActive,
	})
	r := NewRunner(Config{Session: session})
	_, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	if err := r.appendFinalAssistantMessageAndCompleteTurn(context.Background(), turn, "Done.", nil); err != nil {
		t.Fatal(err)
	}

	contract := session.Snapshot().TurnContract
	if contract == nil || contract.Status != ContractStatusSatisfied {
		t.Fatalf("TurnContract = %#v, want satisfied", contract)
	}
}

func TestRunnerRequiredVerificationCommandMustMatch(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("run focused tests")
	session.SetTurnContract(TurnContract{
		ID:                   "contract-1",
		SourceTurn:           turn,
		Intent:               TurnIntentVerify,
		RequiredVerification: []VerificationRequirement{{Command: "go test ./internal/react"}},
		Evidence:             []EvidenceRecord{{Kind: EvidenceVerification, Summary: "verification passed: go test ./cmd/forge"}},
		Status:               ContractStatusActive,
	})
	r := NewRunner(Config{Session: session})
	_, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	ok, err := r.validateFinalCompletion(context.Background(), turn, "Tests passed.", false)

	if ok || err != nil {
		t.Fatalf("ok, err = %v, %v; want blocked without hard error", ok, err)
	}
	if !sessionHistoryContains(session, "required turn contract evidence missing", "verification") {
		t.Fatalf("history missing verification feedback: %#v", session.Snapshot().History)
	}
}

func TestRunnerRequiredVerificationWithoutCommandAcceptsAnyPassedVerification(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("run tests")
	session.SetTurnContract(TurnContract{
		ID:                   "contract-1",
		SourceTurn:           turn,
		Intent:               TurnIntentVerify,
		RequiredVerification: []VerificationRequirement{{Description: "tests requested"}},
		Evidence:             []EvidenceRecord{{Kind: EvidenceVerification, Summary: "verification passed: go test ./cmd/forge"}},
		Status:               ContractStatusActive,
	})
	r := NewRunner(Config{Session: session})
	_, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	ok, err := r.validateFinalCompletion(context.Background(), turn, "Tests passed.", false)

	if !ok || err != nil {
		t.Fatalf("ok, err = %v, %v; want success", ok, err)
	}
}

func TestRunnerContractRunActionSatisfiedBySuccessfulRunCommandEvidence(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("run ls")
	session.SetTurnContract(TurnContract{
		ID:              "contract-1",
		SourceTurn:      turn,
		Intent:          TurnIntentVerify,
		RequiredActions: []ContractAction{{Kind: ContractActionRun}},
		Status:          ContractStatusActive,
	})
	session.UpdateTurnContract(func(contract *TurnContract) {
		recordToolResultEvidence(contract, "run_command", map[string]any{"command": "ls"}, "file.go\nexit 0", false)
	})
	r := NewRunner(Config{Session: session})
	_, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	ok, err := r.validateFinalCompletion(context.Background(), turn, "Command completed.", false)

	if !ok || err != nil {
		t.Fatalf("ok, err = %v, %v; want success", ok, err)
	}
}

func TestRunnerRequiredArtifactRequiresExactPathEvidence(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("write docs/a.md")
	session.SetTurnContract(TurnContract{
		ID:                "contract-1",
		SourceTurn:        turn,
		Intent:            TurnIntentEditCode,
		RequiredActions:   []ContractAction{{Kind: ContractActionEdit}},
		RequiredArtifacts: []ArtifactRequirement{{Path: "docs/a.md"}},
		Evidence:          []EvidenceRecord{{Kind: EvidenceWrite, Summary: "write write_file docs/b.md"}},
		Status:            ContractStatusActive,
	})
	r := NewRunner(Config{Session: session})
	_, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	ok, err := r.validateFinalCompletion(context.Background(), turn, "Done.", false)

	if ok || err != nil {
		t.Fatalf("ok, err = %v, %v; want blocked without hard error", ok, err)
	}
	if !sessionHistoryContains(session, "required turn contract evidence missing", "artifact docs/a.md") {
		t.Fatalf("history missing exact artifact feedback: %#v", session.Snapshot().History)
	}
}

func TestRunnerRequiredActionHonestFailureReportIsNotSuccess(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("fix this and run tests")
	session.SetTurnContract(TurnContract{
		ID:                   "contract-1",
		SourceTurn:           turn,
		Intent:               TurnIntentEditCode,
		RequiredActions:      []ContractAction{{Kind: ContractActionEdit}},
		RequiredVerification: []VerificationRequirement{{Command: "go test ./internal/react"}},
		Status:               ContractStatusActive,
	})
	r := NewRunner(Config{Session: session})
	_, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	ok, err := r.validateFinalCompletion(context.Background(), turn, "I could not fix this because the edit was blocked.", false)

	if ok || err != nil {
		t.Fatalf("ok, err = %v, %v; want a bounded nudge with feedback", ok, err)
	}
	if !sessionHistoryContains(session, "required turn contract evidence missing") {
		t.Fatalf("history missing contract feedback: %#v", session.Snapshot().History)
	}
}

func TestRunnerDelegationFailureBlocksSuccessfulFinalWithoutParentRecovery(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("use an agent then fix this")
	session.SetTurnContract(TurnContract{
		ID:              "contract-1",
		SourceTurn:      turn,
		Intent:          TurnIntentEditCode,
		RequiredActions: []ContractAction{{Kind: ContractActionEdit}},
		Status:          ContractStatusActive,
	})
	session.UpdateTurnContract(func(contract *TurnContract) {
		recordToolResultEvidence(contract, "wait_agent", map[string]any{"id": "agent-1"}, `{"id":"agent-1","status":"failed","result":"boom"}`, false)
	})
	r := NewRunner(Config{Session: session})
	_, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	ok, err := r.validateFinalCompletion(context.Background(), turn, "Done, fixed this.", false)

	if ok || err != nil {
		t.Fatalf("ok, err = %v, %v; want blocked without hard error", ok, err)
	}
	if !sessionHistoryContains(session, "delegation failed", "parent-owned recovery evidence") {
		t.Fatalf("history missing delegation failure feedback: %#v", session.Snapshot().History)
	}
}

func TestRunnerReadOnlyDelegationFailureBlocksSuccessfulFinal(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("use an agent to inspect this")
	session.SetTurnContract(TurnContract{
		ID:              "contract-1",
		SourceTurn:      turn,
		Intent:          TurnIntentInspect,
		RequiredActions: []ContractAction{{Kind: ContractActionRead}},
		Evidence:        []EvidenceRecord{{Kind: EvidenceDelegationFailure, Summary: "wait_agent agent-1 failed"}},
		Status:          ContractStatusActive,
	})
	r := NewRunner(Config{Session: session})
	_, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	ok, err := r.validateFinalCompletion(context.Background(), turn, "Done, inspected it.", false)

	if ok || err != nil {
		t.Fatalf("ok, err = %v, %v; want blocked without hard error", ok, err)
	}
}

func TestRunnerDelegationFailureAllowsParentRecoveryEvidence(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("use an agent then fix this")
	session.SetTurnContract(TurnContract{
		ID:              "contract-1",
		SourceTurn:      turn,
		Intent:          TurnIntentEditCode,
		RequiredActions: []ContractAction{{Kind: ContractActionEdit}},
		Evidence: []EvidenceRecord{
			{Kind: EvidenceDelegationFailure, Summary: "wait_agent agent-1 failed"},
			{Kind: EvidenceWrite, Summary: "write edit_file main.go"},
		},
		Status: ContractStatusActive,
	})
	r := NewRunner(Config{Session: session})
	_, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	ok, err := r.validateFinalCompletion(context.Background(), turn, "Done, fixed this.", false)

	if !ok || err != nil {
		t.Fatalf("ok, err = %v, %v; want success after parent recovery", ok, err)
	}
}

func TestRunnerStaleFinalOutputNotAppendedAfterValidation(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("answer")
	r := NewRunner(Config{Session: session})
	_, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}

	ok, err := r.validateFinalCompletion(context.Background(), turn, "Done.", false)
	if !ok || err != nil {
		t.Fatalf("ok, err = %v, %v; want initial validation success", ok, err)
	}
	cancel()
	before := session.Snapshot()
	err = r.appendFinalAssistantMessageAndCompleteTurn(context.Background(), turn, "Done.", nil)

	if !errors.Is(err, ErrStaleTurn) {
		t.Fatalf("err = %v; want stale turn", err)
	}
	after := session.Snapshot()
	if len(after.History) != len(before.History) {
		t.Fatalf("stale final append mutated history: before=%#v after=%#v", before.History, after.History)
	}
}

func TestRunnerFinalValidationRequiresActiveMatchingTurn(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("answer")
	r := NewRunner(Config{Session: session})

	ok, err := r.validateFinalCompletion(context.Background(), turn, "Done.", false)

	if ok || !errors.Is(err, ErrStaleTurn) {
		t.Fatalf("ok, err = %v, %v; want stale turn", ok, err)
	}
}

func TestCompletedAgentFallbackDoesNotBypassSideEffectGate(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("ask agent to inspect docs/a.md")
	session.SetSideEffectIntent(SideEffectIntent{
		ID:              "intent-1",
		AllowedPaths:    []string{"docs/a.md"},
		RequiredActions: []SideEffectAction{SideEffectActionWrite},
		Gates:           []SideEffectGate{{Name: "write", Status: SideEffectGatePending}},
	})
	session.UpsertAgentTask(AgentTaskState{ID: "agent-1", Role: "repo-auditor", Status: AgentStatusCompleted, Result: "wrote docs/a.md", ParentTurn: turn})
	r := NewRunner(Config{Session: session})
	_, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	handled, err := r.tryCompletedAgentResultFallbackAfterError(context.Background(), turn)
	if err != nil {
		t.Fatal(err)
	}
	if handled {
		t.Fatal("fallback should not complete with unresolved side-effect success claim")
	}
	if strings.Contains(r.LastResponse(), "wrote docs/a.md") {
		t.Fatalf("LastResponse leaked fallback claim: %q", r.LastResponse())
	}
	if !sessionHistoryContains(session, "unresolved side-effect gates", "write") {
		t.Fatalf("history missing gate feedback: %#v", session.Snapshot().History)
	}
}

func TestRunnerMarksSideEffectGatesFromToolResults(t *testing.T) {
	session := NewSession()
	session.SetSideEffectIntent(SideEffectIntent{
		ID:              "intent-1",
		AllowedPaths:    []string{"FORGE_VS_CODEX.md"},
		RequiredActions: []SideEffectAction{SideEffectActionWrite, SideEffectActionCommit, SideEffectActionPush},
		Gates: []SideEffectGate{
			{Name: "write", Status: SideEffectGatePending},
			{Name: "commit", Status: SideEffectGatePending},
			{Name: "push", Status: SideEffectGatePending},
		},
	})
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:       "write_file",
		Parameters: []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}, {Name: "content", Type: "string", Required: true}},
		Execute: func(context.Context, map[string]any) (string, error) {
			return "wrote 10 bytes to FORGE_VS_CODEX.md", nil
		},
	})
	reg.Register(agenttools.Tool{Name: "git_commit", Parameters: []agenttools.ParameterDef{{Name: "message", Type: "string", Required: true}}, Execute: func(context.Context, map[string]any) (string, error) {
		return "commit abc1234 created with files:\nFORGE_VS_CODEX.md", nil
	}})
	reg.Register(agenttools.Tool{Name: "git_push", Execute: func(context.Context, map[string]any) (string, error) {
		return "remote contains abc1234 at origin/main", nil
	}})
	r := NewRunner(Config{Tools: reg, Session: session})

	if err := r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{
		{ID: "write-1", Name: "write_file", ArgsJSON: `{"path":"FORGE_VS_CODEX.md","content":"comparison"}`},
		{ID: "commit-1", Name: "git_commit", ArgsJSON: `{"message":"add comparison"}`},
		{ID: "push-1", Name: "git_push", ArgsJSON: `{}`},
	}); err != nil {
		t.Fatal(err)
	}

	for _, gate := range session.Snapshot().SideEffectIntent.Gates {
		if gate.Status != SideEffectGatePassed {
			t.Fatalf("gate %s status = %s, want passed; gates = %#v", gate.Name, gate.Status, session.Snapshot().SideEffectIntent.Gates)
		}
		if gate.Evidence == "" {
			t.Fatalf("gate %s missing evidence", gate.Name)
		}
	}
}

func TestRunnerMarksWriteGateFromAlternateWriteTools(t *testing.T) {
	workspace := t.TempDir()
	for _, tc := range []struct {
		name   string
		tool   string
		args   string
		result string
	}{
		{name: "absolute write_file", tool: "write_file", args: fmt.Sprintf(`{"path":%q,"content":"comparison"}`, filepath.Join(workspace, "docs/a.md")), result: "wrote 10 bytes to /workspace/docs/a.md"},
		{name: "edit_file", tool: "edit_file", args: `{"path":"docs/a.md","old_text":"old","new_text":"new"}`, result: "edited docs/a.md"},
		{name: "apply_patch", tool: "apply_patch", args: `{"patch":"*** Begin Patch\n*** Update File: docs/a.md\n@@\n-old\n+new\n*** End Patch"}`, result: "applied patch from patch"},
		{name: "artifact_write", tool: "artifact_write", args: `{"path":"docs/a.md","content":"comparison"}`, result: `{"path":"docs/a.md","bytes":10}`},
		{name: "run_command", tool: "run_command", args: `{"command":"cat > docs/a.md <<'EOF'\ncomparison\nEOF"}`, result: "exit 0"},
		{name: "absolute run_command", tool: "run_command", args: fmt.Sprintf(`{"command":%q}`, "printf hi > "+filepath.Join(workspace, "docs/a.md")), result: "exit 0"},
		{name: "dot-relative run_command", tool: "run_command", args: `{"command":"printf hi > ./docs/a.md"}`, result: "exit 0"},
		{name: "cp run_command", tool: "run_command", args: `{"command":"cp /tmp/report.md docs/a.md"}`, result: "exit 0"},
		{name: "touch option run_command", tool: "run_command", args: `{"command":"touch -m docs/a.md"}`, result: "exit 0"},
		{name: "touch run_command", tool: "run_command", args: `{"command":"touch docs/a.md"}`, result: "exit 0"},
		{name: "tee run_command", tool: "run_command", args: `{"command":"printf hi | tee docs/a.md"}`, result: "exit 0"},
		{name: "tee run_command with failure text", tool: "run_command", args: `{"command":"printf 'failed cases' | tee docs/a.md"}`, result: "failed cases\nexit 0"},
		{name: "tee run_command with git text", tool: "run_command", args: `{"command":"printf 'nothing to commit' | tee docs/a.md"}`, result: "nothing to commit\nexit 0"},
		{name: "tee run_command with error text", tool: "run_command", args: `{"command":"printf 'error summary' | tee docs/a.md"}`, result: "error summary\nexit 0"},
		{name: "nested shell run_command", tool: "run_command", args: `{"command":"sh -c 'printf hi > docs/a.md'"}`, result: "exit 0"},
		{name: "cp directory run_command", tool: "run_command", args: `{"command":"cp /tmp/a.md docs/"}`, result: "exit 0"},
		{name: "absolute cp directory run_command", tool: "run_command", args: fmt.Sprintf(`{"command":%q}`, "cp /tmp/a.md "+filepath.Join(workspace, "docs")+string(filepath.Separator)), result: "exit 0"},
		{name: "multi-source cp directory run_command", tool: "run_command", args: `{"command":"cp /tmp/a.md /tmp/other.md docs/"}`, result: "exit 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := NewSession()
			session.SetSideEffectIntent(SideEffectIntent{
				ID:              "intent-1",
				ArtifactPaths:   []string{"docs/a.md"},
				AllowedPaths:    []string{"docs/a.md"},
				WorkspaceRoot:   workspace,
				RequiredActions: []SideEffectAction{SideEffectActionWrite},
				Gates:           []SideEffectGate{{Name: "write", Status: SideEffectGatePending}},
			})
			active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
			if err != nil {
				t.Fatal(err)
			}
			defer cancel()
			reg := agenttools.NewRegistry()
			reg.Register(agenttools.Tool{Name: tc.tool, Execute: func(context.Context, map[string]any) (string, error) { return tc.result, nil }})
			r := NewRunner(Config{Tools: reg, Session: session})

			if err := r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{{ID: "write-1", Name: tc.tool, ArgsJSON: tc.args}}); err != nil {
				t.Fatal(err)
			}
			gate := session.Snapshot().SideEffectIntent.Gates[0]
			if gate.Status != SideEffectGatePassed {
				t.Fatalf("gate status = %s, want passed; gates = %#v", gate.Status, session.Snapshot().SideEffectIntent.Gates)
			}
		})
	}
}

func TestRunnerDoesNotMarkWriteGateFromReadOnlyRunCommand(t *testing.T) {
	session := NewSession()
	session.SetSideEffectIntent(SideEffectIntent{
		ID:              "intent-1",
		ArtifactPaths:   []string{"docs/a.md"},
		AllowedPaths:    []string{"docs/a.md"},
		RequiredActions: []SideEffectAction{SideEffectActionWrite},
		Gates:           []SideEffectGate{{Name: "write", Status: SideEffectGatePending}},
	})
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{Name: "run_command", Execute: func(context.Context, map[string]any) (string, error) { return "docs/a.md\nexit 0", nil }})
	r := NewRunner(Config{Tools: reg, Session: session})

	if err := r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{{ID: "run-1", Name: "run_command", ArgsJSON: `{"command":"ls docs/a.md"}`}}); err != nil {
		t.Fatal(err)
	}
	if gate := session.Snapshot().SideEffectIntent.Gates[0]; gate.Status != SideEffectGatePending {
		t.Fatalf("gate status = %s, want pending", gate.Status)
	}
}

func TestRunnerDoesNotMarkWriteGateWhenRunCommandCopiesFromArtifact(t *testing.T) {
	session := NewSession()
	session.SetSideEffectIntent(SideEffectIntent{
		ID:              "intent-1",
		ArtifactPaths:   []string{"docs/a.md"},
		AllowedPaths:    []string{"docs/a.md"},
		RequiredActions: []SideEffectAction{SideEffectActionWrite},
		Gates:           []SideEffectGate{{Name: "write", Status: SideEffectGatePending}},
	})
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{Name: "run_command", Execute: func(context.Context, map[string]any) (string, error) { return "exit 0", nil }})
	r := NewRunner(Config{Tools: reg, Session: session})

	if err := r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{{ID: "run-1", Name: "run_command", ArgsJSON: `{"command":"cp docs/a.md /tmp/backup"}`}}); err != nil {
		t.Fatal(err)
	}
	if gate := session.Snapshot().SideEffectIntent.Gates[0]; gate.Status != SideEffectGatePending {
		t.Fatalf("gate status = %s, want pending", gate.Status)
	}
}

func TestRunnerDoesNotMarkWriteGateWhenRunCommandCopiesArtifactToOwnDirectory(t *testing.T) {
	session := NewSession()
	session.SetSideEffectIntent(SideEffectIntent{
		ID:              "intent-1",
		ArtifactPaths:   []string{"docs/a.md"},
		AllowedPaths:    []string{"docs/a.md"},
		RequiredActions: []SideEffectAction{SideEffectActionWrite},
		Gates:           []SideEffectGate{{Name: "write", Status: SideEffectGatePending}},
	})
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{Name: "run_command", Execute: func(context.Context, map[string]any) (string, error) { return "exit 0", nil }})
	r := NewRunner(Config{Tools: reg, Session: session})

	if err := r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{{ID: "run-1", Name: "run_command", ArgsJSON: `{"command":"cp docs/./a.md docs/"}`}}); err != nil {
		t.Fatal(err)
	}
	if gate := session.Snapshot().SideEffectIntent.Gates[0]; gate.Status != SideEffectGatePending {
		t.Fatalf("gate status = %s, want pending", gate.Status)
	}
}

func TestRunnerMarksWriteGateFromNestedShellPositionalPath(t *testing.T) {
	session := NewSession()
	session.SetSideEffectIntent(SideEffectIntent{
		ID:              "intent-1",
		ArtifactPaths:   []string{"docs/a.md"},
		AllowedPaths:    []string{"docs/a.md"},
		RequiredActions: []SideEffectAction{SideEffectActionWrite},
		Gates:           []SideEffectGate{{Name: "write", Status: SideEffectGatePending}},
	})
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{Name: "run_command", Execute: func(context.Context, map[string]any) (string, error) { return "exit 0", nil }})
	r := NewRunner(Config{Tools: reg, Session: session})

	if err := r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{{ID: "run-1", Name: "run_command", ArgsJSON: `{"command":"sh -c 'printf hi > \"$1\"' sh docs/a.md"}`}}); err != nil {
		t.Fatal(err)
	}
	if gate := session.Snapshot().SideEffectIntent.Gates[0]; gate.Status != SideEffectGatePassed {
		t.Fatalf("gate status = %s, want passed", gate.Status)
	}
}

func TestRunnerNormalizesAtPrefixedSideEffectArtifactPath(t *testing.T) {
	session := NewSession()
	session.SetSideEffectIntent(SideEffectIntent{
		ID:              "intent-1",
		ArtifactPaths:   []string{"@testing-anti-patterns.md"},
		AllowedPaths:    []string{"@testing-anti-patterns.md"},
		RequiredActions: []SideEffectAction{SideEffectActionWrite},
		Gates:           []SideEffectGate{{Name: "write", Status: SideEffectGatePending}},
	})
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "write_file",
		Description: "write file",
		Parameters: []agenttools.ParameterDef{
			{Name: "path", Type: "string", Required: true},
			{Name: "content", Type: "string", Required: true},
		},
		Execute: func(context.Context, map[string]any) (string, error) { return "wrote testing-anti-patterns.md", nil },
	})
	r := NewRunner(Config{Tools: reg, Session: session})

	if err := r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{{ID: "write-1", Name: "write_file", ArgsJSON: `{"path":"testing-anti-patterns.md","content":"ok"}`}}); err != nil {
		t.Fatal(err)
	}
	if gate := session.Snapshot().SideEffectIntent.Gates[0]; gate.Status != SideEffectGatePassed {
		t.Fatalf("gate status = %s, want passed; gates = %#v", gate.Status, session.Snapshot().SideEffectIntent.Gates)
	}
}

func TestRunnerDoesNotMarkWriteGateFromInputRedirection(t *testing.T) {
	session := NewSession()
	session.SetSideEffectIntent(SideEffectIntent{
		ID:              "intent-1",
		ArtifactPaths:   []string{"docs/a.md"},
		AllowedPaths:    []string{"docs/a.md"},
		RequiredActions: []SideEffectAction{SideEffectActionWrite},
		Gates:           []SideEffectGate{{Name: "write", Status: SideEffectGatePending}},
	})
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{Name: "run_command", Execute: func(context.Context, map[string]any) (string, error) { return "exit 0", nil }})
	r := NewRunner(Config{Tools: reg, Session: session})

	if err := r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{{ID: "run-1", Name: "run_command", ArgsJSON: `{"command":"tee < docs/a.md"}`}}); err != nil {
		t.Fatal(err)
	}
	if gate := session.Snapshot().SideEffectIntent.Gates[0]; gate.Status != SideEffectGatePending {
		t.Fatalf("gate status = %s, want pending", gate.Status)
	}
}

func TestRunnerDoesNotMarkWriteGateFromQuotedShellExample(t *testing.T) {
	session := NewSession()
	session.SetSideEffectIntent(SideEffectIntent{
		ID:              "intent-1",
		ArtifactPaths:   []string{"docs/a.md"},
		AllowedPaths:    []string{"docs/a.md"},
		RequiredActions: []SideEffectAction{SideEffectActionWrite},
		Gates:           []SideEffectGate{{Name: "write", Status: SideEffectGatePending}},
	})
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{Name: "run_command", Execute: func(context.Context, map[string]any) (string, error) { return "exit 0", nil }})
	r := NewRunner(Config{Tools: reg, Session: session})

	if err := r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{{ID: "run-1", Name: "run_command", ArgsJSON: `{"command":"printf 'example > docs/a.md'"}`}}); err != nil {
		t.Fatal(err)
	}
	if gate := session.Snapshot().SideEffectIntent.Gates[0]; gate.Status != SideEffectGatePending {
		t.Fatalf("gate status = %s, want pending", gate.Status)
	}
}

func TestRunnerDoesNotMarkWriteGateWhenRunCommandFinalExitFails(t *testing.T) {
	session := NewSession()
	session.SetSideEffectIntent(SideEffectIntent{
		ID:              "intent-1",
		ArtifactPaths:   []string{"docs/a.md"},
		AllowedPaths:    []string{"docs/a.md"},
		RequiredActions: []SideEffectAction{SideEffectActionWrite},
		Gates:           []SideEffectGate{{Name: "write", Status: SideEffectGatePending}},
	})
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{Name: "run_command", Execute: func(context.Context, map[string]any) (string, error) { return "exit 0\nexit 1", nil }})
	r := NewRunner(Config{Tools: reg, Session: session})

	if err := r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{{ID: "run-1", Name: "run_command", ArgsJSON: `{"command":"printf data > docs/a.md; false"}`}}); err != nil {
		t.Fatal(err)
	}
	if gate := session.Snapshot().SideEffectIntent.Gates[0]; gate.Status != SideEffectGateFailed {
		t.Fatalf("gate status = %s, want failed", gate.Status)
	}
}

func TestRunnerDoesNotMarkWriteGateFromNestedQuotedShellExample(t *testing.T) {
	session := NewSession()
	session.SetSideEffectIntent(SideEffectIntent{
		ID:              "intent-1",
		ArtifactPaths:   []string{"docs/a.md"},
		AllowedPaths:    []string{"docs/a.md"},
		RequiredActions: []SideEffectAction{SideEffectActionWrite},
		Gates:           []SideEffectGate{{Name: "write", Status: SideEffectGatePending}},
	})
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{Name: "run_command", Execute: func(context.Context, map[string]any) (string, error) { return "exit 0", nil }})
	r := NewRunner(Config{Tools: reg, Session: session})

	if err := r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{{ID: "run-1", Name: "run_command", ArgsJSON: `{"command":"sh -c 'printf \"example > docs/a.md\"'"}`}}); err != nil {
		t.Fatal(err)
	}
	if gate := session.Snapshot().SideEffectIntent.Gates[0]; gate.Status != SideEffectGatePending {
		t.Fatalf("gate status = %s, want pending", gate.Status)
	}
}

func TestRunnerDoesNotMarkWriteGateFromRsyncDryRun(t *testing.T) {
	session := NewSession()
	session.SetSideEffectIntent(SideEffectIntent{
		ID:              "intent-1",
		ArtifactPaths:   []string{"docs/a.md"},
		AllowedPaths:    []string{"docs/a.md"},
		RequiredActions: []SideEffectAction{SideEffectActionWrite},
		Gates:           []SideEffectGate{{Name: "write", Status: SideEffectGatePending}},
	})
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{Name: "run_command", Execute: func(context.Context, map[string]any) (string, error) { return "exit 0", nil }})
	r := NewRunner(Config{Tools: reg, Session: session})

	if err := r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{{ID: "run-1", Name: "run_command", ArgsJSON: `{"command":"rsync --dry-run /tmp/report.md docs/a.md"}`}}); err != nil {
		t.Fatal(err)
	}
	if gate := session.Snapshot().SideEffectIntent.Gates[0]; gate.Status != SideEffectGatePending {
		t.Fatalf("gate status = %s, want pending", gate.Status)
	}
}

func TestRunnerAllowsQuotedGitSnippetInShellWriteDuringScopedIntent(t *testing.T) {
	r := NewRunner(Config{})
	session := NewSession()
	session.SetSideEffectIntent(SideEffectIntent{ID: "intent-1", AllowedPaths: []string{"docs/a.md"}, RequiredActions: []SideEffectAction{SideEffectActionWrite}})
	r.session = session

	output := r.beforeToolHookOutput(context.Background(), "run_command", map[string]any{"command": "printf 'steps: edit; git add .' > docs/a.md"})
	if output.Block != nil {
		t.Fatalf("unexpected block = %#v", output.Block)
	}
}

func hookOverlayContent(output hooks.ExecutionOutput, key string) string {
	for _, overlay := range output.Overlays {
		if overlay.Key == key {
			return overlay.Content
		}
	}
	return ""
}

func promptHookOutputForSnapshot(t *testing.T, snap SessionSnapshot) hooks.ExecutionOutput {
	t.Helper()
	return newLoopHookRegistry().Dispatch(context.Background(), hooks.Event{
		Point:    hooks.PointPromptContext,
		Snapshot: snap,
	})
}

func TestRunnerAllowsNonNativeDriverWithTaskState(t *testing.T) {
	// A plain scriptedDriver does NOT implement NativeToolCaller.
	// With no forced tool requirements, it falls back to plain text mode.
	driver := &scriptedDriver{responses: []string{"some response"}}
	session := NewSession()
	session.SetTaskState(TaskState{
		Objective:            "whats this repo all about",
		Operation:            "overview",
		RequiredVerification: "inspect the repository with read/search tools before answering",
	})
	r := NewRunner(Config{Driver: driver, Session: session})
	if err := r.Run(context.Background(), "whats this repo all about"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := r.LastResponse(); got != "some response" {
		t.Fatalf("last response = %q, want some response", got)
	}
}

func TestRunnerAllowsPlainChatWithoutNativeToolCaller(t *testing.T) {
	driver := &scriptedDriver{responses: []string{"plain answer"}}
	r := NewRunner(Config{Driver: driver})

	if err := r.Run(context.Background(), "hello there"); err != nil {
		t.Fatal(err)
	}
	if got := r.LastResponse(); got != "plain answer" {
		t.Fatalf("last response = %q, want plain answer", got)
	}
}

func TestRunnerDoesNotBypassModelForAmbiguousReportCreation(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("what's broken?")
	mustCompleteTurn(t, session, turn, "prior answer", nil, nil)
	driver := &nativeScriptedDriver{responses: []string{"model handled it"}}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "write_file",
		Description: "write file",
		AutoApprove: true,
		Execute: func(context.Context, map[string]any) (string, error) {
			t.Fatal("write_file should not be called directly")
			return "", nil
		},
	})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "create a report for that bug"); err != nil {
		t.Fatal(err)
	}
	if driver.callCount != 1 {
		t.Fatalf("driver calls = %d, want 1", driver.callCount)
	}
	if got := r.LastResponse(); got != "model handled it" {
		t.Fatalf("last response = %q, want model handled it", got)
	}
}

func TestRunnerDoesNotBypassModelWhenPriorAnswerReferenceHasNoSaveTarget(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("summarize report risks")
	mustCompleteTurn(t, session, turn, "prior answer", nil, nil)
	driver := &nativeScriptedDriver{responses: []string{"model handled it"}}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "write_file",
		Description: "write file",
		AutoApprove: true,
		Execute: func(context.Context, map[string]any) (string, error) {
			t.Fatal("write_file should not be called directly")
			return "", nil
		},
	})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "write the answer about the report"); err != nil {
		t.Fatal(err)
	}
	if driver.callCount != 1 {
		t.Fatalf("driver calls = %d, want 1", driver.callCount)
	}
}

func TestRunnerDoesNotBypassModelWhenPriorAnswerReferenceIsNotWriteObject(t *testing.T) {
	for _, input := range []string{
		"write about it in prose",
		"write an answer in chat",
	} {
		t.Run(input, func(t *testing.T) {
			session := NewSession()
			turn := session.RecordInput("summarize report risks")
			mustCompleteTurn(t, session, turn, "prior answer", nil, nil)
			driver := &nativeScriptedDriver{responses: []string{"model handled it"}}
			reg := agenttools.NewRegistry()
			reg.Register(agenttools.Tool{
				Name:        "write_file",
				Description: "write file",
				AutoApprove: true,
				Execute: func(context.Context, map[string]any) (string, error) {
					t.Fatal("write_file should not be called directly")
					return "", nil
				},
			})
			r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

			if err := r.Run(context.Background(), input); err != nil {
				t.Fatal(err)
			}
			if driver.callCount != 1 {
				t.Fatalf("driver calls = %d, want 1", driver.callCount)
			}
		})
	}
}

func TestRunnerDoesNotBypassModelForMarkdownContentRequests(t *testing.T) {
	for _, input := range []string{
		"write markdown",
		"write the markdown",
		"write it to me in markdown",
	} {
		t.Run(input, func(t *testing.T) {
			session := NewSession()
			turn := session.RecordInput("summarize report risks")
			mustCompleteTurn(t, session, turn, "prior answer", nil, nil)
			driver := &nativeScriptedDriver{responses: []string{"model handled it"}}
			reg := agenttools.NewRegistry()
			reg.Register(agenttools.Tool{
				Name:        "write_file",
				Description: "write file",
				AutoApprove: true,
				Execute: func(context.Context, map[string]any) (string, error) {
					t.Fatal("write_file should not be called directly")
					return "", nil
				},
			})
			r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

			if err := r.Run(context.Background(), input); err != nil {
				t.Fatal(err)
			}
			if driver.callCount != 1 {
				t.Fatalf("driver calls = %d, want 1", driver.callCount)
			}
		})
	}
}

func TestReadOutputMissingHandleErrorIsModelCorrectable(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("read delegated output")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "read_output",
		Description: "read output",
		Parameters:  []agenttools.ParameterDef{{Name: "handle", Type: "string", Required: true}},
		Execute: func(context.Context, map[string]any) (string, error) {
			return "", errors.New(`read output handle "session/missing": lstat /tmp/session/missing: no such file or directory`)
		},
	})
	r := NewRunner(Config{Tools: reg, Session: session})

	if err := r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{{ID: "read-1", Name: "read_output", ArgsJSON: `{"handle":"session/missing"}`}}); err != nil {
		t.Fatal(err)
	}
	if !sessionHistoryContains(session, "read output handle", "no such file or directory") {
		t.Fatalf("history missing correctable read_output error: %#v", session.Snapshot().History)
	}
}

func TestMalformedToolArgSchemaAliasFeedsRepeatLoopDetector(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("read files")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "read_file",
		Description: "read file",
		Parameters:  []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}},
		Execute: func(context.Context, map[string]any) (string, error) {
			t.Fatal("malformed read_file args should not execute")
			return "", nil
		},
	})
	r := NewRunner(Config{Tools: reg, Session: session})
	call := llm.NativeToolCall{ID: "read-1", Name: "read_file", ArgsJSON: `{"filePath":"README.md","summary":true}`}

	for i := 0; i < repeatToolCallThreshold; i++ {
		call.ID = fmt.Sprintf("read-%d", i)
		if err := r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{call}); err != nil {
			t.Fatal(err)
		}
	}

	if r.repeatWorkflow.streak < repeatToolCallThreshold || r.repeatWorkflow.lastTarget != "README.md" {
		t.Fatalf("repeat workflow = %#v, want malformed schema alias tracked", r.repeatWorkflow)
	}
	if got := r.repeatWorkflow.overlayContent(repeatToolCallThreshold); !strings.Contains(got, "Loop detection") {
		t.Fatalf("repeat overlay = %q, want loop detection", got)
	}
}

func TestRunnerPreservesFailedChildCauseWhenParentResponseEmpty(t *testing.T) {
	session := NewSession()
	session.UpsertAgentTask(AgentTaskState{
		ID:         "agent-1",
		Role:       "explorer",
		Status:     AgentStatusFailed,
		Result:     "opencode-go stream failed: unexpected end of JSON input",
		ParentTurn: 1,
	})
	driver := &nativeSequenceDriver{steps: repeatedTokenSteps(nil, maxCompletionRetriesPerTurn+1)}
	r := NewRunner(Config{Driver: driver, Session: session})

	err := r.Run(context.Background(), "continue")
	if err == nil || !strings.Contains(err.Error(), "opencode-go stream failed") {
		t.Fatalf("err = %v, want failed child cause", err)
	}
}

func TestRunnerDelegatedCompletedAgentWaitThenWritesReport(t *testing.T) {
	session := NewSession()
	session.SetPendingDelegationAction(DelegationActionState{
		Kind:       DelegationActionWriteDoc,
		TargetPath: "docs/reports/gaps.md",
	})
	session.UpsertAgentTask(AgentTaskState{
		ID:         "agent-1",
		Role:       "synthesizer",
		Status:     AgentStatusCompleted,
		Result:     "# Forge Gap Report\n\nCompleted child-agent synthesis.",
		ParentTurn: 1,
	})
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "wait-1", Name: "wait_agent", ArgsJSON: `{"id":"agent-1"}`}}},
		{{ToolCall: &llm.NativeToolCall{ID: "write-1", Name: "write_file", ArgsJSON: `{"path":"docs/reports/gaps.md","content":"# Forge Gap Report\n\nCompleted child-agent synthesis."}`}}},
		{{Text: "wrote synthesized report"}},
	}}
	var writtenPath, writtenContent string
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "wait_agent",
		Description: "wait for agent",
		AutoApprove: true,
		Execute: func(context.Context, map[string]any) (string, error) {
			return `{"id":"agent-1","status":"completed","result":"# Forge Gap Report\n\nCompleted child-agent synthesis."}`, nil
		},
	})
	reg.Register(agenttools.Tool{
		Name:        "write_file",
		Description: "write file",
		AutoApprove: true,
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			writtenPath, _ = args["path"].(string)
			writtenContent, _ = args["content"].(string)
			return "wrote " + writtenPath, nil
		},
	})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "continue"); err != nil {
		t.Fatal(err)
	}
	if writtenPath != "docs/reports/gaps.md" {
		t.Fatalf("path = %q, want docs/reports/gaps.md", writtenPath)
	}
	if writtenContent != "# Forge Gap Report\n\nCompleted child-agent synthesis." {
		t.Fatalf("content = %q", writtenContent)
	}
	if !historyIncludesCompletedToolCall(session.Snapshot(), "wait_agent") {
		t.Fatalf("history missing completed wait_agent: %#v", session.Snapshot().History)
	}
}

func TestRunnerFailedWaitAgentRecordsDelegationFailureEvidence(t *testing.T) {
	session := NewSession()
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "wait-1", Name: "wait_agent", ArgsJSON: `{"id":"agent-1"}`}}},
	}}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "wait_agent",
		Description: "wait for agent",
		AutoApprove: true,
		Execute: func(context.Context, map[string]any) (string, error) {
			return "", errors.New("child provider failed")
		},
	})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session, MaxSteps: 3})

	err := r.Run(context.Background(), "inspect repo")
	if err == nil || !strings.Contains(err.Error(), "child provider failed") {
		t.Fatalf("err = %v, want child provider failed", err)
	}
	contract := session.Snapshot().TurnContract
	if contract == nil || !contractHasEvidence(contract, EvidenceDelegationFailure, "wait_agent", "agent-1") {
		t.Fatalf("TurnContract evidence = %#v, want failed delegation evidence", contract)
	}
	if contractHasEvidenceKind(contract, EvidenceRead) || contractHasEvidenceKind(contract, EvidenceWrite) {
		t.Fatalf("TurnContract evidence = %#v, failed delegation must not satisfy read/write", contract.Evidence)
	}
}

func TestRunnerCompletedAgentFallbackWithoutArtifactWriteFailsVisibly(t *testing.T) {
	workspace := t.TempDir()
	artifact := "docs/plans/2026-05-23-fallback.md"
	session := NewSession()
	session.SetActiveWorkspaceRoot(workspace)
	turn := session.RecordInput("write " + artifact)
	session.SetTurnContract(TurnContract{
		ID:                "contract-1",
		SourceTurn:        turn,
		Intent:            TurnIntentWriteArtifact,
		RequiredActions:   []ContractAction{{Kind: ContractActionEdit, Description: "write requested artifact"}},
		RequiredArtifacts: []ArtifactRequirement{{Path: artifact, Description: "requested plan artifact"}},
		Gates:             []ContractGate{{Name: "artifact", Status: ContractGatePending}},
		Status:            ContractStatusActive,
	})
	session.UpsertAgentTask(AgentTaskState{
		ID:         "agent-1",
		Role:       "planner",
		Status:     AgentStatusCompleted,
		Result:     "# Delegated Plan\n\n## Approach\n\nChild-only artifact content.",
		ParentTurn: turn,
	})
	r := NewRunner(Config{Session: session})
	_, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	handled, err := r.tryCompletedAgentResultFallbackAfterError(context.Background(), turn)
	if handled || err == nil || !strings.Contains(err.Error(), "artifact gate") {
		t.Fatalf("handled, err = %v, %v; want visible artifact gate failure", handled, err)
	}
	if got := r.LastResponse(); strings.Contains(got, "Showing completed child-agent result") || strings.Contains(got, "Child-only") {
		t.Fatalf("LastResponse = %q, should not append success fallback", got)
	}
	if !contractHasEvidence(session.Snapshot().TurnContract, EvidenceModelViolation, "completed-agent fallback", "artifact gate") {
		t.Fatalf("TurnContract evidence = %#v, want fallback failure evidence", session.Snapshot().TurnContract)
	}
}

func TestRunnerReadOnlyDelegationSummaryStillAllowed(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("ask agent to inspect the repo")
	session.UpsertAgentTask(AgentTaskState{
		ID:         "agent-1",
		Role:       "repo-auditor",
		Status:     AgentStatusCompleted,
		Result:     "Repo inspection found no required writes.",
		ParentTurn: turn,
	})
	r := NewRunner(Config{Session: session})
	_, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	handled, err := r.tryCompletedAgentResultFallbackAfterError(context.Background(), turn)
	if !handled || err != nil {
		t.Fatalf("handled, err = %v, %v", handled, err)
	}
	if got := r.LastResponse(); !strings.Contains(got, "Repo inspection found no required writes") {
		t.Fatalf("LastResponse = %q, want read-only child summary", got)
	}
}

func TestRunnerBlocksOutOfScopeWriteDuringSideEffectIntent(t *testing.T) {
	var executed atomic.Bool
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{Name: "write_file", Description: "write", Parameters: []agenttools.ParameterDef{
		{Name: "path", Type: "string", Required: true},
		{Name: "content", Type: "string", Required: true},
	}, AutoApprove: true, Execute: func(context.Context, map[string]any) (string, error) {
		executed.Store(true)
		return "wrote", nil
	}})
	session := NewSession()
	session.SetSideEffectIntent(SideEffectIntent{ID: "intent-1", ArtifactPaths: []string{"docs/a.md"}, AllowedPaths: []string{"docs/a.md"}})
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	r := NewRunner(Config{Tools: reg, Session: session})

	err = r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{{
		ID: "write-1", Name: "write_file", ArgsJSON: `{"path":"docs/other.md","content":"outside"}`,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if executed.Load() {
		t.Fatal("write_file executed for out-of-scope side-effect path")
	}
	if !sessionHistoryContains(session, "blocked", "outside active side-effect intent") {
		t.Fatalf("history missing out-of-scope block feedback: %#v", session.Snapshot().History)
	}
}

func TestRunnerBlocksOutOfScopeWriteDuringArtifactOnlySideEffectIntent(t *testing.T) {
	var executed atomic.Bool
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{Name: "write_file", Description: "write", Parameters: []agenttools.ParameterDef{
		{Name: "path", Type: "string", Required: true},
		{Name: "content", Type: "string", Required: true},
	}, AutoApprove: true, Execute: func(context.Context, map[string]any) (string, error) {
		executed.Store(true)
		return "wrote", nil
	}})
	session := NewSession()
	session.SetSideEffectIntent(SideEffectIntent{ID: "intent-1", ArtifactPaths: []string{"docs/a.md"}})
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	r := NewRunner(Config{Tools: reg, Session: session})

	err = r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{{
		ID: "write-1", Name: "write_file", ArgsJSON: `{"path":"docs/other.md","content":"outside"}`,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if executed.Load() {
		t.Fatal("write_file executed for out-of-scope artifact-only side-effect path")
	}
}

func TestRunnerBlocksMixedScopePatchDuringSideEffectIntent(t *testing.T) {
	var executed atomic.Bool
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{Name: "apply_patch", Description: "patch", Parameters: []agenttools.ParameterDef{{Name: "patch", Type: "string", Required: true}}, AutoApprove: true, Execute: func(context.Context, map[string]any) (string, error) {
		executed.Store(true)
		return "applied", nil
	}})
	session := NewSession()
	session.SetSideEffectIntent(SideEffectIntent{ID: "intent-1", ArtifactPaths: []string{"docs/a.md"}, AllowedPaths: []string{"docs/a.md"}})
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	r := NewRunner(Config{Tools: reg, Session: session})

	err = r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{{
		ID: "patch-1", Name: "apply_patch", ArgsJSON: `{"patch":"diff --git a/docs/a.md b/docs/a.md\n--- a/docs/a.md\n+++ b/docs/a.md\n@@ -1 +1 @@\n-old\n+new\ndiff --git a/docs/other.md b/docs/other.md\n--- a/docs/other.md\n+++ b/docs/other.md\n@@ -1 +1 @@\n-old\n+new"}`,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if executed.Load() {
		t.Fatal("apply_patch executed for mixed in-scope/out-of-scope side-effect paths")
	}
	if !sessionHistoryContains(session, "blocked", "docs/other.md") {
		t.Fatalf("history missing mixed-scope patch block feedback: %#v", session.Snapshot().History)
	}
}

func TestRunnerBoundsBulkPostDelegationReadOutputCalls(t *testing.T) {
	session := NewSession()
	session.SetPendingDelegationAction(DelegationActionState{
		Kind:       DelegationActionWriteDoc,
		TargetPath: "docs/reports/gaps.md",
	})

	var readLimits []int
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "read_output",
		Description: "read output",
		Concurrency: agenttools.ToolConcurrencySerial,
		AutoApprove: true,
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			limit, ok := numericArgForTest(args["limit"])
			if !ok {
				limit = 0
			}
			readLimits = append(readLimits, int(limit))
			return fmt.Sprintf(`{"handle":%q,"offset":0,"limit":%d,"bytes_read":5,"content":"chunk"}`, args["handle"], limit), nil
		},
	})
	reg.Register(agenttools.Tool{
		Name:        "write_file",
		Description: "write file",
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "wrote docs/reports/gaps.md", nil
		},
	})
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{
			{ToolCall: &llm.NativeToolCall{ID: "read-1", Name: "read_output", ArgsJSON: `{"handle":"session/out-1"}`}},
			{ToolCall: &llm.NativeToolCall{ID: "read-2", Name: "read_output", ArgsJSON: `{"handle":"session/out-2"}`}},
			{ToolCall: &llm.NativeToolCall{ID: "read-3", Name: "read_output", ArgsJSON: `{"handle":"session/out-3"}`}},
			{ToolCall: &llm.NativeToolCall{ID: "read-4", Name: "read_output", ArgsJSON: `{"handle":"session/out-4"}`}},
			{ToolCall: &llm.NativeToolCall{ID: "read-5", Name: "read_output", ArgsJSON: `{"handle":"session/out-5"}`}},
		},
		{{ToolCall: &llm.NativeToolCall{ID: "write-1", Name: "write_file", ArgsJSON: `{"path":"docs/reports/gaps.md","content":"# Report\n\nok"}`}}},
		{{Text: "wrote synthesized report"}},
	}}
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "continue"); err != nil {
		t.Fatal(err)
	}
	if len(readLimits) != 5 {
		t.Fatalf("read_output calls = %d, want 5", len(readLimits))
	}
	for _, limit := range readLimits {
		if limit <= 0 || limit > 8*1024 {
			t.Fatalf("read_output limit = %d, want bounded bulk read limit", limit)
		}
	}
}

func numericArgForTest(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), true
	}
	return 0, false
}

func TestRunnerRetriesTransientPostDelegationModelErrorBeforeFallback(t *testing.T) {
	session := NewSession()
	session.SetPendingDelegationAction(DelegationActionState{
		Kind:       DelegationActionWriteDoc,
		TargetPath: "docs/reports/gaps.md",
	})
	session.UpsertAgentTask(AgentTaskState{
		ID:         "agent-1",
		Role:       "synthesizer",
		Status:     AgentStatusCompleted,
		Result:     "# Child Agent Findings\n\nEvidence for the parent report.",
		ParentTurn: 1,
	})
	var writtenPath, writtenContent string
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "write_file",
		Description: "write file",
		Parameters: []agenttools.ParameterDef{
			{Name: "path", Type: "string", Required: true},
			{Name: "content", Type: "string", Required: true},
		},
		AutoApprove: true,
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			writtenPath, _ = args["path"].(string)
			writtenContent, _ = args["content"].(string)
			return "wrote " + writtenPath, nil
		},
	})
	driver := &nativeSequenceDriver{
		steps: [][]llm.Token{
			nil,
			{{ToolCall: &llm.NativeToolCall{ID: "write-1", Name: "write_file", ArgsJSON: `{"path":"docs/reports/gaps.md","content":"# Parent Synthesized Report\n\nPolished comparison."}`}}},
			{{Text: "wrote synthesized report"}},
		},
		errs: []error{errors.New("stream idle timeout after 30s")},
	}
	rec := &recordingRenderer{}
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session, Renderer: rec})

	if err := r.Run(context.Background(), "continue"); err != nil {
		t.Fatal(err)
	}
	if driver.callCount != 3 {
		t.Fatalf("driver calls = %d, want transient error, retry write_file, final text", driver.callCount)
	}
	if len(rec.retryTexts) == 0 {
		t.Fatal("expected retry notice before fallback")
	}
	if writtenPath != "docs/reports/gaps.md" {
		t.Fatalf("path = %q, want docs/reports/gaps.md", writtenPath)
	}
	if writtenContent != "# Parent Synthesized Report\n\nPolished comparison." {
		t.Fatalf("content = %q, want parent-authored report", writtenContent)
	}
	if strings.Contains(writtenContent, "Parent synthesis failed") || strings.Contains(writtenContent, "Child Agent Findings") {
		t.Fatalf("content used fallback instead of parent synthesis: %q", writtenContent)
	}
}

func TestRunnerResetsTransientRetryBudgetAfterPostDelegationToolCall(t *testing.T) {
	session := NewSession()
	session.SetPendingDelegationAction(DelegationActionState{
		Kind:       DelegationActionWriteDoc,
		TargetPath: "docs/reports/gaps.md",
	})
	session.UpsertAgentTask(AgentTaskState{
		ID:         "agent-1",
		Role:       "explorer",
		Status:     AgentStatusCompleted,
		Result:     "# Child Agent Findings\n\nEvidence for the parent report.",
		ParentTurn: 1,
	})
	var writtenContent string
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "read_output",
		Description: "read output",
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return `{"content":"delegated findings"}`, nil
		},
	})
	reg.Register(agenttools.Tool{
		Name:        "write_file",
		Description: "write file",
		Parameters: []agenttools.ParameterDef{
			{Name: "path", Type: "string", Required: true},
			{Name: "content", Type: "string", Required: true},
		},
		AutoApprove: true,
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			writtenContent, _ = args["content"].(string)
			return "wrote docs/reports/gaps.md", nil
		},
	})
	driver := &nativeSequenceDriver{
		steps: [][]llm.Token{
			nil,
			nil,
			{{ToolCall: &llm.NativeToolCall{ID: "read-1", Name: "read_output", ArgsJSON: `{"handle":"session/agent-1"}`}}},
			nil,
			nil,
			nil,
			{{ToolCall: &llm.NativeToolCall{ID: "write-1", Name: "write_file", ArgsJSON: `{"path":"docs/reports/gaps.md","content":"# Parent Synthesized Report\n\nPolished comparison."}`}}},
			{{Text: "wrote synthesized report"}},
		},
		errs: []error{
			errors.New("stream idle timeout after 30s"),
			errors.New("stream idle timeout after 30s"),
			nil,
			errors.New("stream idle timeout after 30s"),
			errors.New("stream idle timeout after 30s"),
			errors.New("stream idle timeout after 30s"),
		},
	}
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "continue"); err != nil {
		t.Fatal(err)
	}
	if writtenContent != "# Parent Synthesized Report\n\nPolished comparison." {
		t.Fatalf("content = %q, want parent-authored report", writtenContent)
	}
	if strings.Contains(writtenContent, "Parent synthesis failed") || strings.Contains(writtenContent, "Child Agent Findings") {
		t.Fatalf("content used fallback instead of parent synthesis: %q", writtenContent)
	}
}

// nativeToolCallDriver simulates a provider that returns a native tool call
// on the first invocation and a plain text response on subsequent invocations.
type nativeToolCallDriver struct {
	callCount int
	lastTools []llm.ToolDef
	lastMsgs  []llm.Message
	lastOpts  []llm.NativeToolOptions
	usage     llm.Usage
}

func (d *nativeToolCallDriver) Name() string { return "native-tool-driver" }

func (d *nativeToolCallDriver) LastUsage() llm.Usage { return d.usage }

func (d *nativeToolCallDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	close(out)
	return errors.New("Stream should not be called on a NativeToolCaller driver")
}

func (d *nativeToolCallDriver) StreamWithTools(_ context.Context, msgs []llm.Message, tools []llm.ToolDef, out chan<- llm.Token) error {
	return d.StreamWithToolsOptions(context.Background(), msgs, tools, llm.NativeToolOptions{}, out)
}

func (d *nativeToolCallDriver) StreamWithToolsOptions(_ context.Context, msgs []llm.Message, tools []llm.ToolDef, opts llm.NativeToolOptions, out chan<- llm.Token) error {
	defer close(out)
	d.callCount++
	d.lastTools = tools
	d.lastMsgs = msgs
	d.lastOpts = append(d.lastOpts, opts)
	switch d.callCount {
	case 1:
		d.usage = llm.Usage{InputTokens: 100, OutputTokens: 20}
		out <- llm.Token{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "git_status", ArgsJSON: `{}`}}
	default:
		d.usage = llm.Usage{InputTokens: 120, OutputTokens: 30}
		out <- llm.Token{Text: "No changes detected."}
	}
	return nil
}

type promptPressureDriver struct {
	maxToolContentBytes int
	sawCompactedTool    bool
	callCount           int
	toolCallsBeforeDone int
}

func (d *promptPressureDriver) Name() string { return "prompt-pressure-driver" }

func (d *promptPressureDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	close(out)
	return errors.New("Stream should not be called on a NativeToolCaller driver")
}

func (d *promptPressureDriver) StreamWithTools(_ context.Context, msgs []llm.Message, tools []llm.ToolDef, out chan<- llm.Token) error {
	return d.StreamWithToolsOptions(context.Background(), msgs, tools, llm.NativeToolOptions{}, out)
}

func (d *promptPressureDriver) StreamWithToolsOptions(_ context.Context, msgs []llm.Message, _ []llm.ToolDef, _ llm.NativeToolOptions, out chan<- llm.Token) error {
	defer close(out)
	d.callCount++
	toolBytes := 0
	for _, msg := range msgs {
		if msg.Role != llm.RoleTool {
			continue
		}
		toolBytes += len(msg.Content)
		if strings.Contains(msg.Content, "tool result compacted") {
			d.sawCompactedTool = true
		}
	}
	if toolBytes > d.maxToolContentBytes {
		d.maxToolContentBytes = toolBytes
	}
	if d.callCount <= d.toolCallsBeforeDone {
		out <- llm.Token{ToolCall: &llm.NativeToolCall{ID: fmt.Sprintf("status-%d", d.callCount), Name: "git_status", ArgsJSON: `{}`}}
		return nil
	}
	out <- llm.Token{Text: "Finished after bounded tool context."}
	return nil
}

func TestRunnerRendersToolCallAndStatsBeforeToolExecution(t *testing.T) {
	driver := &nativeToolCallDriver{}
	renderer := &recordingRenderer{}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "git_status",
		Description: "git status",
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			renderer.mu.Lock()
			defer renderer.mu.Unlock()
			if !slices.Contains(renderer.events, "tool_call") {
				t.Fatalf("tool execution started before tool_call was rendered; events=%v", renderer.events)
			}
			if !slices.Contains(renderer.events, "stats") {
				t.Fatalf("tool execution started before stats were emitted; events=%v", renderer.events)
			}
			return "nothing to commit", nil
		},
	})
	r := NewRunner(Config{
		Driver:   driver,
		Tools:    reg,
		Session:  NewSession(),
		Renderer: renderer,
	})

	if err := r.Run(context.Background(), "check the repo"); err != nil {
		t.Fatal(err)
	}
}

func TestRunnerPersistsStatsItem(t *testing.T) {
	driver := &nativeToolCallDriver{}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{Name: "git_status", Description: "git status", AutoApprove: true, Execute: func(context.Context, map[string]any) (string, error) {
		return "nothing to commit", nil
	}})
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session, Renderer: &recordingRenderer{}})
	if err := r.Run(context.Background(), "check the repo"); err != nil {
		t.Fatal(err)
	}
	toolCalls := 0
	for _, item := range session.Snapshot().Items {
		if item.Kind == protocol.ItemToolCall {
			toolCalls++
		}
		if item.Kind == protocol.ItemStats && item.Stats != nil && item.Stats.Usage.InputTokens > 0 {
			if toolCalls != 1 {
				t.Fatalf("tool call items = %d, want 1; items=%#v", toolCalls, session.Snapshot().Items)
			}
			return
		}
	}
	t.Fatalf("stats item not found: %#v", session.Snapshot().Items)
}

type nativeToolCallWithPreambleDriver struct {
	callCount int
}

func (d *nativeToolCallWithPreambleDriver) Name() string { return "native-tool-driver-with-preamble" }

func (d *nativeToolCallWithPreambleDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	close(out)
	return errors.New("Stream should not be called on a NativeToolCaller driver")
}

func (d *nativeToolCallWithPreambleDriver) StreamWithTools(_ context.Context, _ []llm.Message, _ []llm.ToolDef, out chan<- llm.Token) error {
	defer close(out)
	d.callCount++
	switch d.callCount {
	case 1:
		out <- llm.Token{Text: "I'll inspect the README first."}
		out <- llm.Token{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "read_file", ArgsJSON: `{"path":"README.md"}`}}
	default:
		out <- llm.Token{Text: "The repo is a terminal-first coding agent."}
	}
	return nil
}

type nativeToolCallSecretPreambleDriver struct {
	callCount int
	secret    string
}

func (d *nativeToolCallSecretPreambleDriver) Name() string {
	return "native-tool-driver-secret-preamble"
}

func (d *nativeToolCallSecretPreambleDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	close(out)
	return errors.New("Stream should not be called on a NativeToolCaller driver")
}

func (d *nativeToolCallSecretPreambleDriver) StreamWithTools(_ context.Context, _ []llm.Message, _ []llm.ToolDef, out chan<- llm.Token) error {
	defer close(out)
	d.callCount++
	switch d.callCount {
	case 1:
		out <- llm.Token{Text: "using " + d.secret}
		out <- llm.Token{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "read_file", ArgsJSON: `{"path":"README.md"}`}}
	default:
		out <- llm.Token{Text: "done"}
	}
	return nil
}

func TestRunnerNativeToolCallingPath(t *testing.T) {
	driver := &nativeToolCallDriver{}
	reg := agenttools.NewRegistry()
	called := false
	reg.Register(agenttools.Tool{
		Name:        "git_status",
		Description: "git status",
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			called = true
			return "nothing to commit", nil
		},
	})
	session := NewSession()
	r := NewRunner(Config{
		Driver:  driver,
		Tools:   reg,
		Session: session,
	})

	if err := r.Run(context.Background(), "check the repo"); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("git_status tool should have been called")
	}
	if driver.callCount != 2 {
		t.Fatalf("driver calls = %d, want 2 (tool call turn + final answer turn)", driver.callCount)
	}

	// Session history: user + assistant(tool_calls) + tool(result) + assistant(final)
	snap := session.Snapshot()
	roles := make([]llm.Role, 0, len(snap.History))
	for _, m := range snap.History {
		roles = append(roles, m.Role)
	}
	want := []llm.Role{llm.RoleUser, llm.RoleAssistant, llm.RoleTool, llm.RoleAssistant}
	if len(roles) != len(want) {
		t.Fatalf("history roles = %v, want %v", roles, want)
	}
	for i, r := range want {
		if roles[i] != r {
			t.Fatalf("history[%d] role = %q, want %q", i, roles[i], r)
		}
	}
}

func TestRunnerStatsIncludePromptContextEstimate(t *testing.T) {
	driver := &nativeToolCallDriver{}
	renderer := &recordingRenderer{}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "git_status",
		Description: "git status",
		AutoApprove: true,
		Execute: func(context.Context, map[string]any) (string, error) {
			return "clean", nil
		},
	})
	r := NewRunner(Config{
		Driver:   driver,
		Tools:    reg,
		Renderer: renderer,
		SystemPrompt: func() string {
			return "system prompt with enough content to estimate"
		},
	})

	if err := r.Run(context.Background(), "inspect repository state"); err != nil {
		t.Fatal(err)
	}
	if len(renderer.statsCtx) == 0 {
		t.Fatal("expected stats events to include prompt context estimates")
	}
	for i, got := range renderer.statsCtx {
		if got <= 0 {
			t.Fatalf("stats context estimate %d = %d, want positive", i, got)
		}
	}
}

func TestRunnerProactivelyCompactsSameTurnToolResultsBeforeProviderCall(t *testing.T) {
	driver := &promptPressureDriver{toolCallsBeforeDone: 12}
	reg := agenttools.NewRegistry()
	largeButBelowSingleResultThreshold := strings.Repeat("x", 40*1024)
	reg.Register(agenttools.Tool{
		Name:        "git_status",
		Description: "git status",
		AutoApprove: true,
		Execute: func(context.Context, map[string]any) (string, error) {
			return largeButBelowSingleResultThreshold, nil
		},
	})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: NewSession()})

	if err := r.Run(context.Background(), "check the repo repeatedly"); err != nil {
		t.Fatal(err)
	}
	if !driver.sawCompactedTool {
		t.Fatal("provider never saw compacted tool-result context during long same-turn loop")
	}
	if driver.maxToolContentBytes > 256*1024 {
		t.Fatalf("max tool content sent to provider = %d bytes, want bounded below 256KiB", driver.maxToolContentBytes)
	}
}

func TestRunnerCreatesOneCheckpointBeforeMutatingTools(t *testing.T) {
	root := initReactTestGitRepo(t)
	t.Chdir(root)
	writeReactTestFile(t, filepath.Join(root, "README.md"), "original\n")
	runReactGit(t, root, "add", "README.md")
	runReactGit(t, root, "commit", "-m", "initial")

	reg := agenttools.NewRegistry()
	var runCount atomic.Int32
	reg.Register(agenttools.Tool{
		Name:        "write_file",
		Description: "write file",
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			runCount.Add(1)
			return "ok", nil
		},
	})
	session := NewSession()
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	r := NewRunner(Config{Tools: reg, Session: session})

	err = r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{
		{ID: "write-1", Name: "write_file", ArgsJSON: `{"path":"README.md","content":"one"}`},
		{ID: "write-2", Name: "write_file", ArgsJSON: `{"path":"README.md","content":"two"}`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := runCount.Load(); got != 2 {
		t.Fatalf("write_file executions = %d, want 2", got)
	}

	items := session.Snapshot().Items
	checkpointCount := 0
	firstToolCall := -1
	firstCheckpoint := -1
	for i, item := range items {
		if item.Kind == protocol.ItemToolCall && firstToolCall < 0 {
			firstToolCall = i
		}
		if item.Kind == protocol.ItemCheckpoint {
			checkpointCount++
			if firstCheckpoint < 0 {
				firstCheckpoint = i
			}
			if item.Checkpoint == nil || item.Checkpoint.ID == "" || item.Checkpoint.Phase != "created" {
				t.Fatalf("checkpoint item = %#v", item.Checkpoint)
			}
		}
	}
	if checkpointCount != 1 {
		t.Fatalf("checkpoint items = %d, want 1; items=%#v", checkpointCount, items)
	}
	if firstCheckpoint < 0 || firstToolCall < 0 || firstCheckpoint > firstToolCall {
		t.Fatalf("checkpoint index = %d, first tool call index = %d; want checkpoint before tool call", firstCheckpoint, firstToolCall)
	}
}

func TestRunnerReportsCheckpointDurableAppendError(t *testing.T) {
	root := initReactTestGitRepo(t)
	t.Chdir(root)
	writeReactTestFile(t, filepath.Join(root, "README.md"), "original\n")
	runReactGit(t, root, "add", "README.md")
	runReactGit(t, root, "commit", "-m", "initial")

	session := NewSession()
	session.SetDurableSink(failingDurableSink{err: errors.New("durable append failed")})
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	var progress []string
	r := NewRunner(Config{
		Session: session,
		Progress: func(message string) {
			progress = append(progress, message)
		},
	})

	r.ensurePreMutationCheckpoint(context.Background(), active.Number)

	if len(progress) != 1 || !strings.Contains(progress[0], "checkpoint item was not persisted") {
		t.Fatalf("progress = %#v, want checkpoint persistence warning", progress)
	}
}

func TestRunnerRecordsCheckpointScopeForWriteFile(t *testing.T) {
	root := initReactTestGitRepo(t)
	t.Chdir(root)
	writeReactTestFile(t, filepath.Join(root, "a.txt"), "a original\n")
	writeReactTestFile(t, filepath.Join(root, "b.txt"), "b original\n")
	runReactGit(t, root, "add", "a.txt", "b.txt")
	runReactGit(t, root, "commit", "-m", "initial")

	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "write_file",
		Description: "write file",
		AutoApprove: true,
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			path, _ := args["path"].(string)
			content, _ := args["content"].(string)
			writeReactTestFile(t, filepath.Join(root, path), content)
			return "ok", nil
		},
	})
	session := NewSession()
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	r := NewRunner(Config{Tools: reg, Session: session})

	if err := r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{{ID: "write-1", Name: "write_file", ArgsJSON: `{"path":"a.txt","content":"turn mutation\n"}`}}); err != nil {
		t.Fatal(err)
	}
	writeReactTestFile(t, filepath.Join(root, "b.txt"), "unrelated user mutation\n")

	var checkpointID string
	for _, item := range session.Snapshot().Items {
		if item.Checkpoint != nil {
			checkpointID = item.Checkpoint.ID
		}
	}
	if checkpointID == "" {
		t.Fatal("missing checkpoint item")
	}
	if err := r.checkpointManager.Restore(context.Background(), checkpointID); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if got := readReactTestFile(t, filepath.Join(root, "a.txt")); got != "a original\n" {
		t.Fatalf("a.txt = %q, want checkpoint content", got)
	}
	if got := readReactTestFile(t, filepath.Join(root, "b.txt")); got != "unrelated user mutation\n" {
		t.Fatalf("b.txt = %q, want unrelated post-checkpoint content", got)
	}
}

func TestRunnerAppendsPostEditDiagnosticsAfterSuccessfulMutatingTool(t *testing.T) {
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:             "write_file",
		Description:      "write file",
		AutoApprove:      true,
		MutatesWorkspace: true,
		LastDiff: func() string {
			return "diff --git a/a.txt b/a.txt"
		},
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "ok", nil
		},
	})
	session := NewSession()
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	r := NewRunner(Config{
		Tools:   reg,
		Session: session,
		PostEditValidator: &PostEditValidator{
			Command:        []string{"/bin/sh", "-c", "printf 'go test failed' >&2; exit 1"},
			Timeout:        time.Second,
			MaxOutputBytes: 1024,
		},
	})

	if err := r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{{ID: "write-1", Name: "write_file", ArgsJSON: `{"path":"a.txt","content":"x"}`}}); err != nil {
		t.Fatal(err)
	}

	var found bool
	for _, msg := range session.Snapshot().History {
		if msg.Role == llm.RoleUser && strings.Contains(msg.Content, "Runtime diagnostic feedback") && strings.Contains(msg.Content, "Post-edit validation failed") && strings.Contains(msg.Content, "go test failed") {
			found = true
		}
	}
	if !found {
		t.Fatalf("session history missing post-edit validation feedback: %#v", session.Snapshot().History)
	}
}

func TestRunnerSkipsPostEditValidationWithoutMutationSignal(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "validator-ran")
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:             "edit_file",
		Description:      "edit file",
		AutoApprove:      true,
		MutatesWorkspace: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "edit_file failed: old_text not found in a.txt", nil
		},
	})
	session := NewSession()
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	r := NewRunner(Config{
		Tools:   reg,
		Session: session,
		PostEditValidator: &PostEditValidator{
			Command:        []string{"/bin/sh", "-c", fmt.Sprintf("printf ran > %q; exit 1", marker)},
			Timeout:        time.Second,
			MaxOutputBytes: 1024,
		},
	})

	if err := r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{{ID: "edit-1", Name: "edit_file", ArgsJSON: `{"path":"a.txt","old_text":"old","new_text":"new"}`}}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("validator marker stat error = %v, want not exist", err)
	}
}

func TestRunnerRunsPostEditValidationWithMutationDiff(t *testing.T) {
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:             "edit_file",
		Description:      "edit file",
		AutoApprove:      true,
		MutatesWorkspace: true,
		LastDiff: func() string {
			return "diff --git a/a.txt b/a.txt"
		},
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "edited a.txt", nil
		},
	})
	session := NewSession()
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	r := NewRunner(Config{
		Tools:   reg,
		Session: session,
		PostEditValidator: &PostEditValidator{
			Command:        []string{"/bin/sh", "-c", "printf validator-failed >&2; exit 1"},
			Timeout:        time.Second,
			MaxOutputBytes: 1024,
		},
	})

	if err := r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{{ID: "edit-1", Name: "edit_file", ArgsJSON: `{"path":"a.txt","old_text":"old","new_text":"new"}`}}); err != nil {
		t.Fatal(err)
	}

	var found bool
	for _, msg := range session.Snapshot().History {
		if msg.Role == llm.RoleUser && strings.Contains(msg.Content, "Runtime diagnostic feedback") && strings.Contains(msg.Content, "validator-failed") {
			found = true
		}
	}
	if !found {
		t.Fatalf("session history missing post-edit diagnostics: %#v", session.Snapshot().History)
	}
}

func TestRunnerRunsPostEditValidationAfterScratchpadWriteSuccess(t *testing.T) {
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.NewScratchpadWrite(t.TempDir()))
	session := NewSession()
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	r := NewRunner(Config{
		Tools:   reg,
		Session: session,
		PostEditValidator: &PostEditValidator{
			Command:        []string{"/bin/sh", "-c", "printf scratchpad-validator >&2; exit 1"},
			Timeout:        time.Second,
			MaxOutputBytes: 1024,
		},
	})

	if err := r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{{ID: "scratch-1", Name: "scratchpad_write", ArgsJSON: `{"topic":"notes","content":"content"}`}}); err != nil {
		t.Fatal(err)
	}

	if !sessionHistoryContains(session, "Runtime diagnostic feedback", "scratchpad-validator") {
		t.Fatalf("session history missing scratchpad validation diagnostics: %#v", session.Snapshot().History)
	}
}

func TestRunnerSkipsPostEditValidationAfterScratchpadWriteArgError(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "validator-ran")
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.NewScratchpadWrite(root))
	session := NewSession()
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	r := NewRunner(Config{
		Tools:   reg,
		Session: session,
		PostEditValidator: &PostEditValidator{
			Command:        []string{"/bin/sh", "-c", fmt.Sprintf("printf ran > %q; exit 1", marker)},
			Timeout:        time.Second,
			MaxOutputBytes: 1024,
		},
	})

	if err := r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{{ID: "scratch-1", Name: "scratchpad_write", ArgsJSON: `{"topic":"notes"}`}}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("validator marker stat error = %v, want not exist", err)
	}
}

func TestRunnerRunsPostEditValidationAfterGitCommitSuccess(t *testing.T) {
	root := initReactTestGitRepo(t)
	writeReactTestFile(t, filepath.Join(root, "a.txt"), "a\n")
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.NewGitCommitScoped(root, func(agenttools.Action) (bool, error) { return true, nil }, func() agenttools.GitScope {
		return agenttools.GitScope{AllowedPaths: []string{"a.txt"}}
	}))
	session := NewSession()
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	r := NewRunner(Config{
		Tools:   reg,
		Session: session,
		PostEditValidator: &PostEditValidator{
			Command:        []string{"/bin/sh", "-c", "printf git-validator >&2; exit 1"},
			Timeout:        time.Second,
			MaxOutputBytes: 1024,
		},
	})

	if err := r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{{ID: "commit-1", Name: "git_commit", ArgsJSON: `{"message":"initial"}`}}); err != nil {
		t.Fatal(err)
	}

	if !sessionHistoryContains(session, "Runtime diagnostic feedback", "git-validator") {
		t.Fatalf("session history missing git commit validation diagnostics: %#v", session.Snapshot().History)
	}
}

func TestRunnerSkipsPostEditValidationAfterGitCommitNoopAndDenied(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(t *testing.T, root string)
		approve bool
		paths   []string
	}{
		{
			name: "nothing to commit",
			prepare: func(t *testing.T, root string) {
				writeReactTestFile(t, filepath.Join(root, "a.txt"), "a\n")
				runReactGit(t, root, "add", "a.txt")
				runReactGit(t, root, "commit", "-m", "initial")
			},
			approve: true,
			paths:   []string{"a.txt"},
		},
		{
			name: "denied",
			prepare: func(t *testing.T, root string) {
				writeReactTestFile(t, filepath.Join(root, "a.txt"), "a\n")
			},
			approve: false,
			paths:   []string{"a.txt"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := initReactTestGitRepo(t)
			marker := filepath.Join(root, "validator-ran")
			tc.prepare(t, root)
			reg := agenttools.NewRegistry()
			reg.Register(agenttools.NewGitCommitScoped(root, func(agenttools.Action) (bool, error) { return tc.approve, nil }, func() agenttools.GitScope {
				return agenttools.GitScope{AllowedPaths: tc.paths}
			}))
			session := NewSession()
			active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
			if err != nil {
				t.Fatal(err)
			}
			defer cancel()
			r := NewRunner(Config{
				Tools:   reg,
				Session: session,
				PostEditValidator: &PostEditValidator{
					Command:        []string{"/bin/sh", "-c", fmt.Sprintf("printf ran > %q; exit 1", marker)},
					Timeout:        time.Second,
					MaxOutputBytes: 1024,
				},
			})

			if err := r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{{ID: "commit-1", Name: "git_commit", ArgsJSON: `{"message":"initial"}`}}); err != nil {
				t.Fatal(err)
			}

			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("validator marker stat error = %v, want not exist", err)
			}
		})
	}
}

func initReactTestGitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runReactGit(t, root, "init")
	runReactGit(t, root, "config", "user.email", "test@example.com")
	runReactGit(t, root, "config", "user.name", "Test User")
	return root
}

func runReactGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func writeReactTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readReactTestFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func sessionHistoryContains(session *Session, parts ...string) bool {
	for _, msg := range session.Snapshot().History {
		content := msg.Content
		matched := true
		for _, part := range parts {
			if !strings.Contains(content, part) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func TestRunnerDoesNotRetryAlreadyExhaustedStreamErrorAfterSuccessfulTool(t *testing.T) {
	driver := &nativeSequenceDriver{
		steps: [][]llm.Token{
			{{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "run_command", ArgsJSON: `{"command":"mkdir -p ~/git/panamanian_hats && git init"}`}}},
		},
		errs: []error{
			nil,
			&llm.RetryAttemptsExhaustedError{Attempts: 3, Err: errors.New("stream idle timeout after 30s")},
			&llm.RetryAttemptsExhaustedError{Attempts: 3, Err: errors.New("stream idle timeout after 30s")},
			&llm.RetryAttemptsExhaustedError{Attempts: 3, Err: errors.New("stream idle timeout after 30s")},
			&llm.RetryAttemptsExhaustedError{Attempts: 3, Err: errors.New("stream idle timeout after 30s")},
		},
	}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "run_command",
		Description: "run shell command",
		Parameters:  []agenttools.ParameterDef{{Name: "command", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(context.Context, map[string]any) (string, error) {
			return "Initialized empty Git repository\nexit 0", nil
		},
	})
	rec := &recordingRenderer{}
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: NewSession(), Renderer: rec})

	err := r.Run(context.Background(), "run command to create a new git repo")
	if err == nil {
		t.Fatal("expected exhausted stream error")
	}
	if driver.callCount != 2 {
		t.Fatalf("driver calls = %d, want one tool call and one exhausted post-tool completion", driver.callCount)
	}
	if len(rec.retryTexts) != 0 {
		t.Fatalf("retry notices = %#v, want none after retry driver already exhausted attempts", rec.retryTexts)
	}
	snap := r.session.Snapshot()
	foundToolResult := false
	for _, msg := range snap.History {
		if msg.Role == llm.RoleTool && strings.Contains(msg.Content, "Initialized empty Git repository") {
			foundToolResult = true
		}
	}
	if !foundToolResult {
		t.Fatalf("history missing successful tool result: %#v", snap.History)
	}
}

func TestRunnerStillRetriesExhaustedServerErrors(t *testing.T) {
	driver := &nativeSequenceDriver{
		steps: [][]llm.Token{
			{},
			{{Text: "recovered after server retry"}},
		},
		errs: []error{
			&llm.RetryAttemptsExhaustedError{Attempts: 3, Err: errors.New("500 server overloaded")},
		},
	}
	rec := &recordingRenderer{}
	r := NewRunner(Config{Driver: driver, Session: NewSession(), Renderer: rec})

	if err := r.Run(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if driver.callCount != 2 {
		t.Fatalf("driver calls = %d, want exhausted server error plus retry", driver.callCount)
	}
	if got := r.LastResponse(); got != "recovered after server retry" {
		t.Fatalf("last response = %q", got)
	}
	if len(rec.retryTexts) == 0 {
		t.Fatal("expected retry notice for exhausted server error")
	}
}

func TestRunnerPreservesAssistantPreambleOnToolTurn(t *testing.T) {
	driver := &nativeToolCallWithPreambleDriver{}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "read_file",
		Description: "read file",
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "Forge is a terminal-first coding agent.", nil
		},
	})
	session := NewSession()
	rec := &recordingRenderer{}
	r := NewRunner(Config{
		Driver:   driver,
		Tools:    reg,
		Session:  session,
		Renderer: rec,
	})

	if err := r.Run(context.Background(), "tell me about this repo"); err != nil {
		t.Fatal(err)
	}

	snap := session.Snapshot()
	if len(snap.History) < 2 {
		t.Fatalf("history = %#v", snap.History)
	}
	firstAssistant := snap.History[1]
	if got, want := firstAssistant.Content, "I'll inspect the README first."; got != want {
		t.Fatalf("assistant preamble = %q, want %q", got, want)
	}
	if len(firstAssistant.ToolCalls) != 1 || firstAssistant.ToolCalls[0].Name != "read_file" {
		t.Fatalf("assistant tool calls = %#v", firstAssistant.ToolCalls)
	}
	if len(rec.tokenTexts) != 0 {
		t.Fatalf("renderer tokenTexts = %#v, expected empty (buffered when tools present)", rec.tokenTexts)
	}
	if len(rec.fullTexts) == 0 || rec.fullTexts[0] != "I'll inspect the README first." {
		t.Fatalf("renderer fullTexts = %#v", rec.fullTexts)
	}
}

func TestRunnerRedactsSecretPreambleOnToolTurn(t *testing.T) {
	secret := "TOKEN=" + strings.Repeat("x", 24)
	driver := &nativeToolCallSecretPreambleDriver{secret: secret}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "read_file",
		Description: "read file",
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "Forge is a terminal-first coding agent.", nil
		},
	})
	session := NewSession()
	rec := &recordingRenderer{}
	r := NewRunner(Config{
		Driver:   driver,
		Tools:    reg,
		Session:  session,
		Renderer: rec,
	})

	if err := r.Run(context.Background(), "tell me about this repo"); err != nil {
		t.Fatal(err)
	}

	snap := session.Snapshot()
	if strings.Contains(snap.History[1].Content, secret) {
		t.Fatal("stored assistant preamble leaked secret")
	}
	if !strings.Contains(snap.History[1].Content, "<REDACTED:generic-token>") {
		t.Fatal("stored assistant preamble missing redaction marker")
	}
	if len(rec.fullTexts) == 0 {
		t.Fatal("renderer did not receive assistant preamble")
	}
	rendered := strings.Join(rec.fullTexts, "\n")
	if strings.Contains(rendered, secret) {
		t.Fatal("rendered assistant preamble leaked secret")
	}
	if !strings.Contains(rendered, "<REDACTED:generic-token>") {
		t.Fatal("rendered assistant preamble missing redaction marker")
	}
}

type pendingInputDriver struct {
	callCount int
	lastMsgs  []llm.Message
}

func (d *pendingInputDriver) Name() string { return "pending-input-driver" }

func (d *pendingInputDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	close(out)
	return errors.New("Stream should not be called on a NativeToolCaller driver")
}

func (d *pendingInputDriver) StreamWithTools(_ context.Context, msgs []llm.Message, _ []llm.ToolDef, out chan<- llm.Token) error {
	defer close(out)
	d.callCount++
	d.lastMsgs = append([]llm.Message(nil), msgs...)
	switch d.callCount {
	case 1:
		out <- llm.Token{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "git_status", ArgsJSON: `{}`}}
	default:
		lastUser := ""
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Role == llm.RoleUser {
				lastUser = msgs[i].Content
				break
			}
		}
		if lastUser != "steer toward tests" {
			out <- llm.Token{Text: "missing pending steer"}
			return nil
		}
		out <- llm.Token{Text: "handled queued steer after tool result"}
	}
	return nil
}

func TestRunnerConsumesQueuedPendingInputWithinActiveLoop(t *testing.T) {
	driver := &pendingInputDriver{}
	reg := agenttools.NewRegistry()
	session := NewSession()
	reg.Register(agenttools.Tool{
		Name:        "git_status",
		Description: "git status",
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			session.QueuePendingInput("steer toward tests")
			return "working tree clean", nil
		},
	})
	reg.Register(agenttools.Tool{
		Name:        "spawn_agent",
		Description: "spawn agent",
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "ok", nil
		},
	})
	r := NewRunner(Config{
		Driver:  driver,
		Tools:   reg,
		Session: session,
	})

	if err := r.Run(context.Background(), "inspect repo"); err != nil {
		t.Fatal(err)
	}
	if driver.callCount != 2 {
		t.Fatalf("driver calls = %d, want 2", driver.callCount)
	}
	if got := r.LastResponse(); got != "handled queued steer after tool result" {
		t.Fatalf("last response = %q", got)
	}
	snap := session.Snapshot()
	if len(snap.History) < 4 {
		t.Fatalf("history = %#v", snap.History)
	}
	foundQueuedUser := false
	for _, msg := range snap.History {
		if msg.Role == llm.RoleUser && msg.Content == "steer toward tests" {
			foundQueuedUser = true
			break
		}
	}
	if !foundQueuedUser {
		t.Fatalf("expected queued input in history, got %#v", snap.History)
	}
}

func TestRunnerNativePathUsesSystemPrompt(t *testing.T) {
	driver := &nativeToolCallDriver{}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name: "git_status", AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) { return "ok", nil },
	})
	promptCalled := false
	session := NewSession()
	session.SetTaskState(TaskState{
		Objective:            "whats this repo all about",
		Operation:            "overview",
		RequiredVerification: "inspect the repository with read/search tools before answering",
	})
	r := NewRunner(Config{
		Driver:  driver,
		Tools:   reg,
		Session: session,
		SystemPrompt: func() string {
			promptCalled = true
			return "native-prompt"
		},
	})
	_ = r.Run(context.Background(), "whats this repo all about")
	if !promptCalled {
		t.Fatal("system prompt should be called")
	}
	// Verify the prompt was sent to the driver
	foundPrompt := false
	for _, msg := range driver.lastMsgs {
		if msg.Role == llm.RoleSystem && msg.Content == "native-prompt" {
			foundPrompt = true
		}
	}
	if !foundPrompt {
		t.Fatalf("expected system prompt in messages, got %#v", driver.lastMsgs)
	}
}

func TestRunnerNativePathPassesToolDefs(t *testing.T) {
	driver := &nativeToolCallDriver{}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "read_file",
		Description: "read a file",
		Parameters:  []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}},
		AutoApprove: true,
		Execute:     func(_ context.Context, _ map[string]any) (string, error) { return "content", nil },
	})
	r := NewRunner(Config{Driver: driver, Tools: reg})
	_ = r.Run(context.Background(), "read something")
	if len(driver.lastTools) == 0 {
		t.Fatal("tool defs should be passed to StreamWithTools")
	}
	if driver.lastTools[0].Name != "read_file" {
		t.Fatalf("first tool def name = %q, want read_file", driver.lastTools[0].Name)
	}
}

func TestRunnerStreamsPlainTextTokensIncrementally(t *testing.T) {
	driver := &nativeChunkedDriver{chunks: []string{"repo ", "overview"}}
	renderer := &recordingRenderer{}
	session := NewSession()
	r := NewRunner(Config{
		Driver:       driver,
		Renderer:     renderer,
		SystemPrompt: func() string { return "system prompt" },
		Session:      session,
	})

	if err := r.Run(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}

	renderer.mu.Lock()
	defer renderer.mu.Unlock()
	if len(renderer.tokenTexts) == 0 {
		t.Fatal("expected incremental token rendering")
	}
	if got := renderer.tokenTexts[0]; got != "repo " {
		t.Fatalf("first streamed token = %q", got)
	}
	if len(renderer.fullTexts) != 0 {
		t.Fatalf("expected no duplicate full-text render, got %#v", renderer.fullTexts)
	}
	if got := r.LastResponse(); got != "repo overview" {
		t.Fatalf("last response = %q", got)
	}
}

// nativeChunkedDriver sends multiple text tokens in one StreamWithTools call.
type nativeChunkedDriver struct {
	chunks    []string
	callCount int
}

func (d *nativeChunkedDriver) Name() string { return "native-chunked" }

func (d *nativeChunkedDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	d.callCount++
	for _, chunk := range d.chunks {
		out <- llm.Token{Text: chunk}
	}
	return nil
}

func (d *nativeChunkedDriver) StreamWithTools(_ context.Context, _ []llm.Message, _ []llm.ToolDef, out chan<- llm.Token) error {
	defer close(out)
	d.callCount++
	for _, chunk := range d.chunks {
		out <- llm.Token{Text: chunk}
	}
	return nil
}

func TestRunnerRejectsLegacyXMLToolCallMarkupFromNativeProvider(t *testing.T) {
	responses := make([]string, maxCompletionRetriesPerTurn+1)
	for i := range responses {
		responses[i] = "<tool_call>\n{\"name\":\"list_dir\",\"args\":{\"path\":\".\"}}\n</tool_call>"
	}
	driver := &nativeScriptedDriver{responses: responses}
	reg := agenttools.NewRegistry()
	called := false
	reg.Register(agenttools.Tool{
		Name:        "list_dir",
		Description: "list a directory",
		Parameters:  []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			called = true
			return "README.md\ninternal/", nil
		},
	})
	session := NewSession()
	session.SetTaskState(TaskState{
		Objective:            "whats this repo all about",
		Operation:            "overview",
		RequiredVerification: "inspect the repository with read/search tools before answering. For a casual repo overview, usually inspect the repo root and one high-signal file such as README.md, then give a brief overview grounded only in that evidence",
	})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	err := r.Run(context.Background(), "whats this repo all about")
	if err == nil {
		t.Fatal("expected runner error when provider emits legacy XML markup")
	}
	if !strings.Contains(err.Error(), "deprecated XML tool-call markup") {
		t.Fatalf("err = %v", err)
	}
	if called {
		t.Fatal("legacy XML markup should not execute a tool")
	}
}

func TestRunnerRecoversFromLegacyXMLToolCallMarkupFromNativeProvider(t *testing.T) {
	driver := &nativeScriptedDriver{responses: []string{
		"<tool_call>\n{\"name\":\"list_dir\",\"args\":{\"path\":\".\"}}\n</tool_call>",
		"recovered with native response",
	}}
	reg := agenttools.NewRegistry()
	called := false
	reg.Register(agenttools.Tool{
		Name:        "list_dir",
		Description: "list a directory",
		Parameters:  []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			called = true
			return "README.md\ninternal/", nil
		},
	})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: NewSession()})

	if err := r.Run(context.Background(), "whats this repo all about"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if called {
		t.Fatal("legacy XML markup should not execute a tool")
	}
	if got := r.LastResponse(); got != "recovered with native response" {
		t.Fatalf("LastResponse = %q", got)
	}
}

func TestRunnerRejectsSelfClosingXMLToolCallMarkupFromNativeProvider(t *testing.T) {
	responses := make([]string, maxCompletionRetriesPerTurn+1)
	for i := range responses {
		responses[i] = `<tool_call name="shell.exec" arguments='{"cmd":"ls"}' />`
	}
	driver := &nativeScriptedDriver{responses: responses}
	reg := agenttools.NewRegistry()
	called := false
	reg.Register(agenttools.Tool{
		Name:        "shell.exec",
		Description: "run a shell command",
		Parameters:  []agenttools.ParameterDef{{Name: "cmd", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			called = true
			return "README.md", nil
		},
	})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: NewSession()})

	err := r.Run(context.Background(), "list files")
	if err == nil {
		t.Fatal("expected runner error when provider emits self-closing XML tool markup")
	}
	if !strings.Contains(err.Error(), "deprecated XML tool-call markup") {
		t.Fatalf("err = %v", err)
	}
	if called {
		t.Fatal("self-closing XML tool markup should not execute a tool")
	}
}

func TestRunnerRejectsMalformedXMLToolCallMarkupFromNativeProvider(t *testing.T) {
	responses := make([]string, maxCompletionRetriesPerTurn+1)
	for i := range responses {
		responses[i] = `<tool_call>{"cmd":"ls"}</tool_call>`
	}
	driver := &nativeScriptedDriver{responses: responses}
	r := NewRunner(Config{Driver: driver, Tools: agenttools.NewRegistry(), Session: NewSession()})

	err := r.Run(context.Background(), "testing")
	if err == nil {
		t.Fatal("expected runner error when provider emits malformed XML tool markup")
	}
	if !strings.Contains(err.Error(), "deprecated XML tool-call markup") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunnerRejectsRawDSMLToolMarkupFinalText(t *testing.T) {
	driver := &nativeScriptedDriver{responses: []string{
		`<｜｜DSML｜｜tool_calls>[{"name":"bash","arguments":{"command":"ls"}}]`,
		"recovered without raw tool markup",
	}}
	session := NewSession()
	turnCompleteCalled := false
	r := NewRunner(Config{
		Driver:  driver,
		Session: session,
		TurnComplete: func(SessionSnapshot) {
			turnCompleteCalled = true
		},
	})

	if err := r.Run(context.Background(), "list files"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := r.LastResponse(); got != "recovered without raw tool markup" {
		t.Fatalf("LastResponse = %q", got)
	}
	if !turnCompleteCalled {
		t.Fatal("expected turn_complete after recovered final text")
	}
	for _, msg := range session.Snapshot().History {
		if msg.Role == llm.RoleAssistant && strings.Contains(msg.Content, "DSML") {
			t.Fatalf("raw DSML markup persisted as assistant text: %#v", msg)
		}
	}
	assertContractEvidence(t, session.Snapshot().TurnContract, EvidenceModelViolation, "raw_tool_markup", "DSML")
}

func TestRunnerRejectsRawToolMarkupXMLToolCallFinalText(t *testing.T) {
	driver := &nativeScriptedDriver{responses: []string{
		`Please run <tool_calls><tool_call>{"name":"bash","arguments":{"command":"ls"}}</tool_call></tool_calls>`,
		"recovered without xml",
	}}
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Session: session})

	if err := r.Run(context.Background(), "list files"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := r.LastResponse(); got != "recovered without xml" {
		t.Fatalf("LastResponse = %q", got)
	}
	for _, msg := range session.Snapshot().History {
		if msg.Role == llm.RoleAssistant && strings.Contains(msg.Content, "<tool_call") {
			t.Fatalf("raw XML markup persisted as assistant text: %#v", msg)
		}
	}
	assertContractEvidence(t, session.Snapshot().TurnContract, EvidenceModelViolation, "raw_tool_markup", "tool_call")
}

func TestRunnerRejectsRawToolMarkupJSONIshStandaloneToolCallFinalText(t *testing.T) {
	driver := &nativeScriptedDriver{responses: []string{
		`{"name":"bash","arguments":{"command":"ls"}}`,
		"recovered without json tool call",
	}}
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Session: session})

	if err := r.Run(context.Background(), "list files"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := r.LastResponse(); got != "recovered without json tool call" {
		t.Fatalf("LastResponse = %q", got)
	}
	for _, msg := range session.Snapshot().History {
		if msg.Role == llm.RoleAssistant && strings.Contains(msg.Content, `"name":"bash"`) {
			t.Fatalf("standalone JSON tool call persisted as assistant text: %#v", msg)
		}
	}
	assertContractEvidence(t, session.Snapshot().TurnContract, EvidenceModelViolation, "raw_tool_markup", "bash")
}

func TestRunnerRejectsJSONToolCallWrapperFinalText(t *testing.T) {
	tests := []struct {
		name     string
		response string
		detail   string
	}{
		{name: "tool calls wrapper", response: `{"tool_calls":[{"name":"bash","arguments":{"command":"ls"}}]}`, detail: "bash"},
		{name: "tool call array", response: `[{"tool_name":"bash","arguments":{"command":"ls"}}]`, detail: "bash"},
		{name: "function object", response: `{"function":{"name":"bash","arguments":"{\"command\":\"ls\"}"}}`, detail: "bash"},
		{name: "nested function object", response: `{"tool_calls":[{"function":{"name":"bash","arguments":"{\"command\":\"ls\"}"}}]}`, detail: "bash"},
		{name: "embedded prose object", response: `I'll call this: {"name":"write_file","arguments":{"path":"x.txt","content":"x"}}`, detail: "write_file"},
		{name: "embedded prose array", response: `I should run this: [{"name":"bash","arguments":{}}]`, detail: "bash"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			driver := &nativeScriptedDriver{responses: []string{tt.response, "recovered"}}
			session := NewSession()
			r := NewRunner(Config{Driver: driver, Session: session})

			if err := r.Run(context.Background(), "list files"); err != nil {
				t.Fatalf("Run returned error: %v", err)
			}
			if got := r.LastResponse(); got != "recovered" {
				t.Fatalf("LastResponse = %q", got)
			}
			assertContractEvidence(t, session.Snapshot().TurnContract, EvidenceModelViolation, "raw_tool_markup", tt.detail)
		})
	}
}

func TestRunnerRawToolMarkupDoesNotStreamSuspiciousPrefixesBeforeRejection(t *testing.T) {
	tests := []struct {
		name   string
		chunks []string
	}{
		{name: "DSML", chunks: []string{"<", "｜｜DSML｜｜tool_calls>", `[{"name":"bash","arguments":{"command":"ls"}}]`}},
		{name: "JSON", chunks: []string{"{", `"name":"bash",`, `"arguments":{"command":"ls"}}`}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			steps := make([][]llm.Token, maxCompletionRetriesPerTurn+1)
			for i := range steps {
				for _, chunk := range tt.chunks {
					steps[i] = append(steps[i], llm.Token{Text: chunk})
				}
			}
			driver := &nativeSequenceDriver{steps: steps}
			renderer := &recordingRenderer{}
			r := NewRunner(Config{Driver: driver, Renderer: renderer, Session: NewSession()})

			if err := r.Run(context.Background(), "list files"); err == nil {
				t.Fatal("expected raw markup to fail")
			}
			renderer.mu.Lock()
			defer renderer.mu.Unlock()
			if len(renderer.tokenTexts) != 0 {
				t.Fatalf("AgentToken chunks = %#v, want none for rejected raw markup", renderer.tokenTexts)
			}
			if len(renderer.fullTexts) != 0 {
				t.Fatalf("AgentText chunks = %#v, want none for rejected raw markup", renderer.fullTexts)
			}
		})
	}
}

func TestRunnerRawToolMarkupDoesNotStreamEmbeddedMarkupBeforeRejection(t *testing.T) {
	tests := []struct {
		name   string
		chunks []string
	}{
		{name: "XML", chunks: []string{"Please run ", "<tool_calls>", `<tool_call>{"name":"bash","arguments":{"command":"ls"}}</tool_call></tool_calls>`}},
		{name: "DSML", chunks: []string{"Please run ", "<｜｜DSML｜｜", `tool_calls>[{"name":"bash","arguments":{"command":"ls"}}]`}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			steps := make([][]llm.Token, maxCompletionRetriesPerTurn+1)
			for i := range steps {
				for _, chunk := range tt.chunks {
					steps[i] = append(steps[i], llm.Token{Text: chunk})
				}
			}
			driver := &nativeSequenceDriver{steps: steps}
			renderer := &recordingRenderer{}
			r := NewRunner(Config{Driver: driver, Renderer: renderer, Session: NewSession()})

			if err := r.Run(context.Background(), "list files"); err == nil {
				t.Fatal("expected raw markup to fail")
			}
			renderer.mu.Lock()
			defer renderer.mu.Unlock()
			streamed := strings.Join(renderer.tokenTexts, "")
			if strings.Contains(streamed, "<tool_call") || strings.Contains(streamed, "DSML") {
				t.Fatalf("AgentToken leaked raw markup: %#v", renderer.tokenTexts)
			}
			if len(renderer.fullTexts) != 0 {
				t.Fatalf("AgentText chunks = %#v, want none for rejected raw markup", renderer.fullTexts)
			}
		})
	}
}

func TestRunnerStaleRawToolMarkupDoesNotRecordContractEvidence(t *testing.T) {
	driver := &nativeScriptedDriver{responses: []string{`<｜｜DSML｜｜tool_calls>[{"name":"bash","arguments":{"command":"ls"}}]`}}
	session := NewSession()
	session.SetTurnContract(TurnContract{ID: "contract-1", Status: ContractStatusActive})
	r := NewRunner(Config{Driver: driver, Session: session})
	turn := session.RecordInput("list files")
	_, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	cancel()

	_, err = r.streamPlainTurn(context.Background(), turn, session.Messages(""))
	if err == nil {
		t.Fatal("expected stale raw markup to fail")
	}
	for _, evidence := range session.Snapshot().TurnContract.Evidence {
		if evidence.Kind == EvidenceModelViolation && strings.Contains(evidence.Summary, "raw_tool_markup") {
			t.Fatalf("stale raw markup recorded contract evidence: %#v", evidence)
		}
	}
}

func TestRunnerAllowsDSMLInvokeExplanationWithoutRawMarkupDelimiters(t *testing.T) {
	final := "The words DSML invoke can appear in documentation without being raw markup."
	driver := &nativeScriptedDriver{responses: []string{final}}
	r := NewRunner(Config{Driver: driver, Session: NewSession()})

	if err := r.Run(context.Background(), "explain DSML"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := r.LastResponse(); got != final {
		t.Fatalf("LastResponse = %q", got)
	}
}

func TestRunnerAllowsRawToolMarkupLookingJSONCodeBlockFinalText(t *testing.T) {
	final := "Use this config:\n\n```json\n{\n  \"name\": \"bash\",\n  \"arguments\": {\"command\": \"ls\"}\n}\n```"
	driver := &nativeScriptedDriver{responses: []string{final}}
	r := NewRunner(Config{Driver: driver, Session: NewSession()})

	if err := r.Run(context.Background(), "show config"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := r.LastResponse(); got != final {
		t.Fatalf("LastResponse = %q", got)
	}
}

func TestRunnerAllowsGenericFencedJSONFinalText(t *testing.T) {
	final := "Use this example:\n\n```json\n{\n  \"name\": \"example\",\n  \"enabled\": true\n}\n```"
	driver := &nativeScriptedDriver{responses: []string{final}}
	r := NewRunner(Config{Driver: driver, Session: NewSession()})

	if err := r.Run(context.Background(), "show json example"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := r.LastResponse(); got != final {
		t.Fatalf("LastResponse = %q", got)
	}
}

func TestRunnerRepeatedRawToolMarkupEventuallyFailsVisibly(t *testing.T) {
	responses := make([]string, maxCompletionRetriesPerTurn+1)
	for i := range responses {
		responses[i] = `<｜｜DSML｜｜invoke name="bash" arguments='{"command":"ls"}' />`
	}
	driver := &nativeScriptedDriver{responses: responses}
	session := NewSession()
	turnCompleteCalled := false
	r := NewRunner(Config{
		Driver:  driver,
		Session: session,
		TurnComplete: func(SessionSnapshot) {
			turnCompleteCalled = true
		},
	})

	err := r.Run(context.Background(), "list files")
	if err == nil {
		t.Fatal("expected repeated raw tool markup to fail")
	}
	assertContainsAll(t, err.Error(), "raw tool-call markup")
	if driver.callCount != maxCompletionRetriesPerTurn+1 {
		t.Fatalf("driver calls = %d, want finite retry budget", driver.callCount)
	}
	if got := r.LastResponse(); got != "" {
		t.Fatalf("LastResponse = %q, want no completed success", got)
	}
	if turnCompleteCalled {
		t.Fatal("turn_complete called for raw markup final output")
	}
	assertContractEvidence(t, session.Snapshot().TurnContract, EvidenceModelViolation, "raw_tool_markup", "DSML")
}

func TestRunnerSetDriverSwitchesSubsequentTurns(t *testing.T) {
	first := &nativeScriptedDriver{responses: []string{"first answer"}}
	second := &nativeScriptedDriver{responses: []string{"second answer"}}
	r := NewRunner(Config{
		Driver:       first,
		Renderer:     silentRenderer{},
		SystemPrompt: func() string { return "system prompt" },
		Session:      NewSession(),
	})

	if err := r.Run(context.Background(), "first"); err != nil {
		t.Fatal(err)
	}
	r.SetDriver(second)
	if err := r.Run(context.Background(), "second"); err != nil {
		t.Fatal(err)
	}
	if got := r.LastResponse(); got != "second answer" {
		t.Fatalf("last response = %q", got)
	}
	if first.callCount != 1 || second.callCount != 1 {
		t.Fatalf("driver calls = (%d, %d), want (1, 1)", first.callCount, second.callCount)
	}
}

func TestRunnerSystemPromptIsPassedToDriver(t *testing.T) {
	driver := &nativeToolCallDriver{}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name: "git_status", AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) { return "ok", nil },
	})
	session := NewSession()
	session.SetTaskState(TaskState{
		Objective:            "whats this repo all about",
		Operation:            "overview",
		RequiredVerification: "inspect the repository with read/search tools before answering",
	})
	r := NewRunner(Config{
		Driver:       driver,
		Tools:        reg,
		Session:      session,
		SystemPrompt: func() string { return "  my system prompt  " },
	})
	_ = r.Run(context.Background(), "whats this repo all about")
	foundPrompt := false
	for _, msg := range driver.lastMsgs {
		if msg.Role == llm.RoleSystem && msg.Content == "my system prompt" {
			foundPrompt = true
		}
	}
	if !foundPrompt {
		t.Fatalf("system prompt not found in messages: %#v", driver.lastMsgs)
	}
}

func TestRunnerNativePathHandlesMalformedArgsJSONAsToolFeedback(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "bad-json", Name: "read_file", ArgsJSON: `{"path":`}}},
		{{Text: "recovered after malformed args feedback"}},
	}}
	reg := agenttools.NewRegistry()
	executed := false
	reg.Register(agenttools.Tool{
		Name:        "read_file",
		Description: "read file",
		Parameters:  []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			executed = true
			return "should not execute", nil
		},
	})
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "read README"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if executed {
		t.Fatal("tool executed despite malformed JSON args")
	}
	if got := r.LastResponse(); got != "recovered after malformed args feedback" {
		t.Fatalf("LastResponse = %q", got)
	}
	found := false
	for _, msg := range session.Snapshot().History {
		if msg.Role == llm.RoleTool && msg.ToolCallID == "bad-json" && strings.Contains(msg.Content, "malformed tool call arguments") {
			found = true
		}
	}
	if !found {
		t.Fatalf("history missing malformed-args feedback: %#v", session.Snapshot().History)
	}
}

func TestRunnerRetriesUnknownToolInsteadOfTreatingAsNormalToolResult(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "bad-1", Name: "bash", ArgsJSON: `{"command":"ls"}`}}},
		{{ToolCall: &llm.NativeToolCall{ID: "read-1", Name: "read_file", ArgsJSON: `{"path":"README.md"}`}}},
		{{Text: "recovered after native read"}},
	}}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "read_file",
		Description: "read file",
		Parameters:  []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(context.Context, map[string]any) (string, error) {
			return "forge readme", nil
		},
	})
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "inspect the repo"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if driver.callCount != 3 {
		t.Fatalf("driver calls = %d, want recovery turn", driver.callCount)
	}
	if got := r.LastResponse(); got != "recovered after native read" {
		t.Fatalf("LastResponse = %q", got)
	}
	snap := session.Snapshot()
	for _, msg := range snap.History {
		if msg.Role == llm.RoleTool && msg.ToolCallID == "bad-1" {
			t.Fatalf("unknown tool was treated as normal tool feedback: %#v", msg)
		}
	}
	if len(driver.allMsgs) < 2 {
		t.Fatalf("driver messages = %d, want corrective retry", len(driver.allMsgs))
	}
	correction := messagesText(driver.allMsgs[1])
	assertContainsAll(t, correction, "bash", "not available", "Available tools", "read_file")
	assertContractEvidence(t, snap.TurnContract, EvidenceModelViolation, "unknown_tool", "bash")
}

func TestRunnerRepeatedUnknownToolEventuallyFailsVisibly(t *testing.T) {
	steps := make([][]llm.Token, maxCompletionRetriesPerTurn+1)
	for i := range steps {
		steps[i] = []llm.Token{{ToolCall: &llm.NativeToolCall{ID: fmt.Sprintf("bad-%d", i), Name: "bash", ArgsJSON: `{"command":"ls"}`}}}
	}
	driver := &nativeSequenceDriver{steps: steps}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{Name: "read_file", Description: "read file"})
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	err := r.Run(context.Background(), "inspect the repo")
	if err == nil {
		t.Fatal("expected repeated unknown tool to fail")
	}
	assertContainsAll(t, err.Error(), "unknown tool", "bash")
	if driver.callCount != maxCompletionRetriesPerTurn+1 {
		t.Fatalf("driver calls = %d, want finite retry budget", driver.callCount)
	}
	if got := r.LastResponse(); got != "" {
		t.Fatalf("LastResponse = %q, want no completed success", got)
	}
	assertContractEvidence(t, session.Snapshot().TurnContract, EvidenceModelViolation, "unknown_tool", "bash")
}

func TestRunnerRepeatedUnknownToolFailureClassIsModelOutputInvalid(t *testing.T) {
	steps := make([][]llm.Token, maxCompletionRetriesPerTurn+1)
	for i := range steps {
		steps[i] = []llm.Token{{ToolCall: &llm.NativeToolCall{ID: fmt.Sprintf("bad-%d", i), Name: "bash", ArgsJSON: `{"command":"ls"}`}}}
	}
	driver := &nativeSequenceDriver{steps: steps}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{Name: "read_file", Description: "read file"})
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	err := r.Run(context.Background(), "inspect the repo")
	if err == nil {
		t.Fatal("expected repeated unknown tool to fail")
	}
	for _, item := range session.Snapshot().Items {
		if item.Kind == protocol.ItemFailure {
			if item.Failure == nil || item.Failure.Decision.Class != protocol.FailureModelOutputInvalid {
				t.Fatalf("failure item = %#v, want model_output_invalid", item.Failure)
			}
			return
		}
	}
	t.Fatalf("missing failure item: %#v", session.Snapshot().Items)
}

func TestRunnerUnknownToolRecordsContractViolation(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "bad-1", Name: "bogus_tool", ArgsJSON: `{}`}}},
		{{ToolCall: &llm.NativeToolCall{ID: "read-1", Name: "read_file", ArgsJSON: `{"path":"README.md"}`}}},
		{{Text: "recovered"}},
	}}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{Name: "read_file", Description: "read file", Parameters: []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}}, AutoApprove: true, Execute: func(context.Context, map[string]any) (string, error) { return "readme", nil }})
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "look at the repo"); err != nil {
		t.Fatal(err)
	}

	assertContractEvidence(t, session.Snapshot().TurnContract, EvidenceModelViolation, "unknown_tool", "bogus_tool")
}

func TestRunnerMalformedArgsEmitsDurableFailureItem(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "bad-json", Name: "read_file", ArgsJSON: `{"path":`}}},
		{{ToolCall: &llm.NativeToolCall{ID: "read-1", Name: "read_file", ArgsJSON: `{"path":"README.md"}`}}},
		{{ToolCall: &llm.NativeToolCall{ID: "read-2", Name: "read_file", ArgsJSON: `{"path":"README.md"}`}}},
		{{Text: "recovered"}},
	}}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{Name: "read_file", Description: "read file", Parameters: []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}}, AutoApprove: true, Execute: func(context.Context, map[string]any) (string, error) { return "readme", nil }})
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})
	if err := r.Run(context.Background(), "read README"); err != nil {
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

func TestRunnerMalformedArgumentsRecordsContractViolation(t *testing.T) {
	session := NewSession()
	session.SetTurnContract(TurnContract{ID: "contract-1", Status: ContractStatusActive})
	r := NewRunner(Config{Session: session})

	r.recordModelViolation("malformed_arguments", "read_file")

	assertContractEvidence(t, session.Snapshot().TurnContract, EvidenceModelViolation, "malformed_arguments", "read_file")
}

func TestRunnerUnavailableToolRecordsContractViolation(t *testing.T) {
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{Name: "read_file", Description: "read file", Execute: func(context.Context, map[string]any) (string, error) { return "", nil }})
	session := NewSession()
	session.SetTurnContract(TurnContract{ID: "contract-1", Status: ContractStatusActive})
	r := NewRunner(Config{Tools: reg, Session: session})
	turn := session.RecordInput("read file")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	err = r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{{ID: "read-1", Name: "read_file", ArgsJSON: `{}`}}, []string{"tool_help"})
	if err == nil {
		t.Fatal("expected unavailable tool error")
	}
	for _, msg := range session.Snapshot().History {
		if msg.Role == llm.RoleTool && msg.ToolCallID == "read-1" {
			t.Fatalf("unavailable tool appended normal tool result: %#v", msg)
		}
	}

	assertContractEvidence(t, session.Snapshot().TurnContract, EvidenceModelViolation, "tool_unavailable_for_turn", "read_file")
}

func TestRunnerDirectUnknownToolDoesNotAppendSuccessfulToolResult(t *testing.T) {
	session := NewSession()
	session.SetTurnContract(TurnContract{ID: "contract-1", Status: ContractStatusActive})
	r := NewRunner(Config{Tools: agenttools.NewRegistry(), Session: session})
	turn := session.RecordInput("run missing tool")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	err = r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{{ID: "missing-1", Name: "missing_tool", ArgsJSON: `{}`}})
	if err == nil {
		t.Fatal("expected unknown tool error")
	}
	for _, msg := range session.Snapshot().History {
		if msg.Role == llm.RoleTool && msg.ToolCallID == "missing-1" {
			t.Fatalf("unknown tool appended normal tool result: %#v", msg)
		}
	}
	assertContractEvidence(t, session.Snapshot().TurnContract, EvidenceModelViolation, "unknown_tool", "missing_tool")
}

func TestRunnerFailedReadDoesNotRecordSuccessfulReadEvidence(t *testing.T) {
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "read_file",
		Description: "read file",
		Parameters:  []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}},
		Execute: func(context.Context, map[string]any) (string, error) {
			return "error: file not found", nil
		},
	})
	session := NewSession()
	session.SetTurnContract(TurnContract{ID: "contract-1", Status: ContractStatusActive})
	r := NewRunner(Config{Tools: reg, Session: session})
	turn := session.RecordInput("read missing file")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	err = r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{{ID: "read-1", Name: "read_file", ArgsJSON: `{"path":"missing.md"}`}})
	if err != nil {
		t.Fatal(err)
	}

	contract := session.Snapshot().TurnContract
	assertNoContractEvidence(t, contract, EvidenceRead)
	assertContractEvidence(t, contract, EvidenceTool, "failed read", "read_file", "missing.md")
}

func TestMalformedModelOutputAssertionsUseDurableItems(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "bad-json", Name: "read_file", ArgsJSON: `{"path":`}}},
		{{Text: "recovered"}},
	}}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{Name: "read_file", Description: "read file", Parameters: []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}}, AutoApprove: true, Execute: func(context.Context, map[string]any) (string, error) { return "", nil }})
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})
	if err := r.Run(context.Background(), "read README"); err != nil {
		t.Fatal(err)
	}
	for _, item := range session.Snapshot().Items {
		if item.Kind == protocol.ItemFailure && item.Failure.Decision.Class == protocol.FailureToolArgsInvalid {
			return
		}
	}
	t.Fatalf("missing structured malformed-output failure item: %#v", session.Snapshot().Items)
}

func TestRunnerToolValidationFailureContinuesLoop(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "read-1", Name: "read_file", ArgsJSON: `{}`}}},
		{{Text: "recovered after path feedback"}},
	}}
	reg := agenttools.NewRegistry()
	executed := false
	reg.Register(agenttools.Tool{
		Name:        "read_file",
		Description: "read file",
		Parameters:  []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			executed = true
			return "should not execute", nil
		},
	})
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "read the file"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if executed {
		t.Fatal("tool executed despite missing required path")
	}
	if got := r.LastResponse(); got != "recovered after path feedback" {
		t.Fatalf("LastResponse = %q", got)
	}
	if driver.callCount != 2 {
		t.Fatalf("driver calls = %d, want recovery turn", driver.callCount)
	}
	found := false
	for _, msg := range session.Snapshot().History {
		if msg.Role == llm.RoleTool && strings.Contains(msg.Content, "read_file.path is required") {
			found = true
		}
	}
	if !found {
		t.Fatalf("history missing validation feedback: %#v", session.Snapshot().History)
	}
}

func TestRunnerUpdatePlanMissingStepsContinuesLoop(t *testing.T) {
	additional := false
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "plan-1", Name: "update_plan", ArgsJSON: `{"explanation":"start"}`}}},
		{{Text: "recovered after plan feedback"}},
	}}
	reg := agenttools.NewRegistry()
	executed := false
	reg.Register(agenttools.Tool{
		Name:        "update_plan",
		Description: "update plan",
		Schema: &llm.ToolSchema{
			Type: "object",
			Properties: map[string]*llm.ToolSchema{
				"steps":       {Type: "array", Items: &llm.ToolSchema{Type: "object"}},
				"explanation": {Type: "string"},
			},
			Required:             []string{"steps"},
			AdditionalProperties: &additional,
		},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			executed = true
			return "should not execute", nil
		},
	})
	session := NewSession()
	session.SetTaskState(TaskState{Objective: "make a plan", Operation: "plan"})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "make a plan"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if executed {
		t.Fatal("tool executed despite missing required steps")
	}
	if got := r.LastResponse(); got != "recovered after plan feedback" {
		t.Fatalf("LastResponse = %q", got)
	}
	found := false
	for _, msg := range session.Snapshot().History {
		if msg.Role == llm.RoleTool && strings.Contains(msg.Content, "update_plan.steps is required") {
			found = true
		}
	}
	if !found {
		t.Fatalf("history missing validation feedback: %#v", session.Snapshot().History)
	}
}

func TestRunnerStructuredSchemaValidationFailureContinuesLoop(t *testing.T) {
	additional := false
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "plan-1", Name: "update_plan", ArgsJSON: `{"steps":[{"step":"Inspect","status":"doing"}]}`}}},
		{{Text: "recovered after enum feedback"}},
	}}
	reg := agenttools.NewRegistry()
	executed := false
	reg.Register(agenttools.Tool{
		Name: "update_plan",
		Schema: &llm.ToolSchema{
			Type: "object",
			Properties: map[string]*llm.ToolSchema{
				"steps": {
					Type: "array",
					Items: &llm.ToolSchema{
						Type: "object",
						Properties: map[string]*llm.ToolSchema{
							"step":   {Type: "string"},
							"status": {Type: "string", Enum: []string{"pending", "in_progress", "blocked", "completed"}},
						},
						Required:             []string{"step", "status"},
						AdditionalProperties: &additional,
					},
				},
			},
			Required:             []string{"steps"},
			AdditionalProperties: &additional,
		},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			executed = true
			return "should not execute", nil
		},
	})
	session := NewSession()
	session.SetTaskState(TaskState{Objective: "make a plan", Operation: "plan"})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "make a plan"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if executed {
		t.Fatal("tool executed despite invalid nested enum")
	}
	found := false
	for _, msg := range session.Snapshot().History {
		if msg.Role == llm.RoleTool && strings.Contains(msg.Content, "update_plan.steps[0].status must be one of") {
			found = true
		}
	}
	if !found {
		t.Fatalf("history missing schema validation feedback: %#v", session.Snapshot().History)
	}
}

func TestRunnerAskUserQuestionExecutionErrorContinuesLoop(t *testing.T) {
	additional := false
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "ask-1", Name: "ask_user_question", ArgsJSON: `{"question":"Pick one","options":[{"label":"Only one"}]}`}}},
		{{Text: "recovered after ask feedback"}},
	}}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "ask_user_question",
		Description: "ask user",
		Schema: &llm.ToolSchema{
			Type: "object",
			Properties: map[string]*llm.ToolSchema{
				"question": {Type: "string"},
				"options": {
					Type: "array",
					Items: &llm.ToolSchema{
						Type: "object",
						Properties: map[string]*llm.ToolSchema{
							"label": {Type: "string"},
						},
						Required:             []string{"label"},
						AdditionalProperties: &additional,
					},
				},
			},
			Required:             []string{"question", "options"},
			AdditionalProperties: &additional,
		},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "", fmt.Errorf("at least two options are required")
		},
	})
	session := NewSession()
	session.SetTaskState(TaskState{Objective: "ask", Operation: "plan"})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "ask"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := r.LastResponse(); got != "recovered after ask feedback" {
		t.Fatalf("LastResponse = %q", got)
	}
	found := false
	for _, msg := range session.Snapshot().History {
		if msg.Role == llm.RoleTool && strings.Contains(msg.Content, "at least two options are required") {
			found = true
		}
	}
	if !found {
		t.Fatalf("history missing ask_user_question feedback: %#v", session.Snapshot().History)
	}
}

func TestRunnerFatalToolExecutionErrorStopsLoop(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "write-1", Name: "write_file", ArgsJSON: `{"path":"README.md","content":"x"}`}}},
	}}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "write_file",
		Description: "write file",
		Parameters: []agenttools.ParameterDef{
			{Name: "path", Type: "string", Required: true},
			{Name: "content", Type: "string", Required: true},
		},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "", fmt.Errorf("disk unavailable")
		},
	})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: NewSession()})

	if err := r.Run(context.Background(), "write file"); err == nil || !strings.Contains(err.Error(), "disk unavailable") {
		t.Fatalf("err = %v, want fatal disk error", err)
	}
}

type nativeSequenceDriver struct {
	steps     [][]llm.Token
	errs      []error
	callCount int
	allMsgs   [][]llm.Message
	lastOpts  []llm.NativeToolOptions
}

func (d *nativeSequenceDriver) Name() string { return "native-sequence" }

func (d *nativeSequenceDriver) Stream(_ context.Context, msgs []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	d.allMsgs = append(d.allMsgs, append([]llm.Message(nil), msgs...))
	if d.callCount < len(d.errs) && d.errs[d.callCount] != nil {
		err := d.errs[d.callCount]
		d.callCount++
		return err
	}
	if d.callCount >= len(d.steps) {
		return errors.New("no scripted step")
	}
	for _, tok := range d.steps[d.callCount] {
		if tok.ToolCall != nil {
			return errors.New("Stream cannot emit tool calls")
		}
		out <- tok
	}
	d.callCount++
	return nil
}

func (d *nativeSequenceDriver) StreamWithTools(_ context.Context, msgs []llm.Message, tools []llm.ToolDef, out chan<- llm.Token) error {
	return d.StreamWithToolsOptions(context.Background(), msgs, tools, llm.NativeToolOptions{}, out)
}

func (d *nativeSequenceDriver) StreamWithToolsOptions(_ context.Context, msgs []llm.Message, _ []llm.ToolDef, opts llm.NativeToolOptions, out chan<- llm.Token) error {
	defer close(out)
	d.allMsgs = append(d.allMsgs, append([]llm.Message(nil), msgs...))
	d.lastOpts = append(d.lastOpts, opts)
	if d.callCount < len(d.errs) && d.errs[d.callCount] != nil {
		err := d.errs[d.callCount]
		d.callCount++
		return err
	}
	if d.callCount >= len(d.steps) {
		return errors.New("no scripted step")
	}
	for _, tok := range d.steps[d.callCount] {
		out <- tok
	}
	d.callCount++
	return nil
}

func TestRunnerBlocksCommitWhileMergeConflictsRemain(t *testing.T) {
	driver := &nativeSequenceDriver{
		steps: [][]llm.Token{
			{{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "run_command", ArgsJSON: `{"command":"git merge feature/go-rewrite"}`}}},
			{{ToolCall: &llm.NativeToolCall{ID: "c2", Name: "run_command", ArgsJSON: `{"command":"git commit -m \"merge\" "}`}}},
			{{Text: "stopped after conflict"}},
		},
	}
	reg := agenttools.NewRegistry()
	executedCommands := []string{}
	reg.Register(agenttools.Tool{
		Name:        "run_command",
		Description: "run shell command",
		Parameters:  []agenttools.ParameterDef{{Name: "command", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			command, _ := args["command"].(string)
			executedCommands = append(executedCommands, command)
			if strings.Contains(command, "git merge feature/go-rewrite") {
				return "Auto-merging .gitignore\nCONFLICT (content): Merge conflict in .gitignore\nAutomatic merge failed; fix conflicts and then commit the result.\n\nexit 1", nil
			}
			return "unexpected execution", nil
		},
	})
	session := NewSession()
	session.SetTaskState(TaskState{
		Objective:            "merge feature/go-rewrite into main",
		Operation:            "merge",
		RequiredVerification: "verify branch main contains the resulting HEAD commit",
		SourceRef:            "feature/go-rewrite",
		TargetBranch:         "main",
	})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "merge the branch"); err != nil {
		t.Fatal(err)
	}
	if len(executedCommands) != 1 {
		t.Fatalf("executed commands = %#v, want only merge command", executedCommands)
	}
	if len(driver.allMsgs) < 2 {
		t.Fatalf("driver messages = %#v", driver.allMsgs)
	}
	foundRuntimeNote := false
	for _, msg := range driver.allMsgs[1] {
		if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "Call git_merge_status to inspect unresolved files, conflict previews, and next steps") {
			foundRuntimeNote = true
		}
	}
	if !foundRuntimeNote {
		t.Fatalf("expected runtime note in second request, got %#v", driver.allMsgs[1])
	}
	snap := session.Snapshot()
	foundBlockedResult := false
	for _, msg := range snap.History {
		if msg.Role == llm.RoleTool && strings.Contains(msg.Content, "blocked: unmerged git conflicts remain") {
			foundBlockedResult = true
		}
	}
	if !foundBlockedResult {
		t.Fatalf("expected blocked commit tool result, history=%#v", snap.History)
	}
}

func TestRunnerRequiresMutationBeforeRetryingFailedCommit(t *testing.T) {
	driver := &nativeSequenceDriver{
		steps: [][]llm.Token{
			{{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "git_commit", ArgsJSON: `{"message":"merge branch"}`}}},
			{{ToolCall: &llm.NativeToolCall{ID: "c2", Name: "git_commit", ArgsJSON: `{"message":"merge branch"}`}}},
			{{Text: "waiting for a real fix"}},
		},
	}
	reg := agenttools.NewRegistry()
	gitCommitCalls := 0
	reg.Register(agenttools.Tool{
		Name:        "git_commit",
		Description: "commit changes",
		Parameters:  []agenttools.ParameterDef{{Name: "message", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			gitCommitCalls++
			return "[INFO] Checking merge-conflict files only.\nprettier.................................................................Passed\nyamllint.................................................................Failed\n.pre-commit-config.yaml:139\nLine too long (304 > 160 characters)\n\nexit 1", nil
		},
	})
	session := NewSession()
	session.SetTaskState(TaskState{
		Objective:            "merge feature/go-rewrite into main",
		Operation:            "merge",
		RequiredVerification: "verify branch main contains the resulting HEAD commit",
		SourceRef:            "feature/go-rewrite",
		TargetBranch:         "main",
	})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "finish the merge"); err != nil {
		t.Fatal(err)
	}
	if gitCommitCalls != 1 {
		t.Fatalf("git commit calls = %d, want 1", gitCommitCalls)
	}
	snap := session.Snapshot()
	foundBlockedResult := false
	for _, msg := range snap.History {
		if msg.Role == llm.RoleTool && strings.Contains(msg.Content, "blocked: the previous commit attempt already failed") {
			foundBlockedResult = true
		}
	}
	if !foundBlockedResult {
		t.Fatalf("expected blocked repeated commit result, history=%#v", snap.History)
	}
}

func TestRunnerNudgesOnExcessivePlanExploration(t *testing.T) {
	// Build planExplorationBudget+1 exploration steps to cross the synthesis threshold,
	// then a final text response. The model should never be blocked — only nudged via
	// a runtime hook overlay injected into the system prompt.
	const explorations = planExplorationBudget + 1
	steps := make([][]llm.Token, explorations+1)
	for i := range explorations {
		steps[i] = []llm.Token{{ToolCall: &llm.NativeToolCall{
			ID:       fmt.Sprintf("c%d", i+1),
			Name:     "search",
			ArgsJSON: fmt.Sprintf(`{"pattern":"pattern%d"}`, i),
		}}}
	}
	steps[explorations] = []llm.Token{{Text: "Plan: all done."}}

	driver := &nativeSequenceDriver{steps: steps}
	reg := agenttools.NewRegistry()
	searchCalls := 0
	reg.Register(agenttools.Tool{
		Name:        "search",
		Description: "search text",
		Parameters:  []agenttools.ParameterDef{{Name: "pattern", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			searchCalls++
			return "match", nil
		},
	})

	session := NewSession()
	session.SetTaskState(TaskState{
		Objective:            "write a plan for removing dead xml code",
		Operation:            "plan",
		RequiredVerification: "produce a concise plan grounded in enough repo evidence",
	})

	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})
	if err := r.Run(context.Background(), "write a cleanup plan"); err != nil {
		t.Fatal(err)
	}
	// All exploration calls must execute — no blocking
	if searchCalls != explorations {
		t.Fatalf("search calls = %d, want %d (no blocking)", searchCalls, explorations)
	}
	// Synthesis note must appear in a request sent after the budget is exceeded
	foundSynthesisNote := false
	for _, msgs := range driver.allMsgs[planExplorationBudget:] {
		for _, msg := range msgs {
			if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "[hook:runtime]") && strings.Contains(msg.Content, "Planning task guidance") {
				foundSynthesisNote = true
			}
		}
	}
	if !foundSynthesisNote {
		t.Fatalf("expected planning synthesis note after budget exceeded, allMsgs=%d", len(driver.allMsgs))
	}
	// No blocking should have occurred
	snap := session.Snapshot()
	for _, msg := range snap.History {
		if msg.Role == llm.RoleTool && strings.Contains(msg.Content, "blocked:") {
			t.Fatalf("expected no blocking, got blocked tool result: %s", msg.Content)
		}
	}
}

func TestRunnerNudgesOnExcessiveAnalysisExploration(t *testing.T) {
	// Build analysisExplorationBudget+1 exploration steps to cross the threshold,
	// then a final text response. Tools must never be blocked — only nudged.
	const explorations = analysisExplorationBudget + 1
	steps := make([][]llm.Token, explorations+1)
	for i := range explorations {
		steps[i] = []llm.Token{{ToolCall: &llm.NativeToolCall{
			ID:       fmt.Sprintf("c%d", i+1),
			Name:     "search",
			ArgsJSON: fmt.Sprintf(`{"pattern":"pattern%d"}`, i),
		}}}
	}
	steps[explorations] = []llm.Token{{Text: "Findings: all done."}}

	driver := &nativeSequenceDriver{steps: steps}
	reg := agenttools.NewRegistry()
	searchCalls := 0
	reg.Register(agenttools.Tool{
		Name:        "search",
		Description: "search text",
		Parameters:  []agenttools.ParameterDef{{Name: "pattern", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			searchCalls++
			return "match", nil
		},
	})

	session := NewSession()
	session.SetTaskState(TaskState{
		Objective:            "audit the repo and explain cleanup targets",
		Operation:            "analysis",
		RequiredVerification: "produce source-grounded findings and stop when the answer can be written",
	})

	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})
	if err := r.Run(context.Background(), "audit the repo"); err != nil {
		t.Fatal(err)
	}
	// All exploration calls must execute — no blocking
	if searchCalls != explorations {
		t.Fatalf("search calls = %d, want %d (no blocking)", searchCalls, explorations)
	}
	// Analysis synthesis note must appear after budget is exceeded
	foundSynthesisNote := false
	for _, msgs := range driver.allMsgs[analysisExplorationBudget:] {
		for _, msg := range msgs {
			if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "[hook:runtime]") && strings.Contains(msg.Content, "Analysis guidance") {
				foundSynthesisNote = true
			}
		}
	}
	if !foundSynthesisNote {
		t.Fatalf("expected analysis synthesis note after budget exceeded, allMsgs=%d", len(driver.allMsgs))
	}
	// No blocking should have occurred
	snap := session.Snapshot()
	for _, msg := range snap.History {
		if msg.Role == llm.RoleTool && strings.Contains(msg.Content, "blocked:") {
			t.Fatalf("expected no blocking, got blocked tool result: %s", msg.Content)
		}
	}
}

func TestRunnerNudgesOnRepeatedSameFileCodeSearch(t *testing.T) {
	const repeatedSearches = sameFileSearchThrashThreshold + 1
	steps := make([][]llm.Token, repeatedSearches+1)
	for i := range repeatedSearches {
		steps[i] = []llm.Token{{ToolCall: &llm.NativeToolCall{
			ID:       fmt.Sprintf("c%d", i+1),
			Name:     "code_search",
			ArgsJSON: fmt.Sprintf(`{"path":"internal/tui/chatmodel.go","query":"pattern%d"}`, i),
		}}}
	}
	steps[repeatedSearches] = []llm.Token{{Text: "Done."}}

	driver := &nativeSequenceDriver{steps: steps}
	reg := agenttools.NewRegistry()
	codeSearchCalls := 0
	reg.Register(agenttools.Tool{
		Name:        "code_search",
		Description: "search code",
		Parameters: []agenttools.ParameterDef{
			{Name: "path", Type: "string", Required: true},
			{Name: "query", Type: "string", Required: true},
		},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			codeSearchCalls++
			return "no matches", nil
		},
	})

	session := NewSession()
	session.SetTaskState(TaskState{
		Objective:            "add a file tree panel to the tui",
		Operation:            "implement",
		RequiredVerification: "inspect the relevant code, make the change with edit tools, and run the relevant verification before claiming completion",
	})

	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})
	if err := r.Run(context.Background(), "add the panel"); err != nil {
		t.Fatal(err)
	}
	if codeSearchCalls != repeatedSearches {
		t.Fatalf("code_search calls = %d, want %d (no blocking)", codeSearchCalls, repeatedSearches)
	}
	foundOverlay := false
	for _, msgs := range driver.allMsgs[sameFileSearchThrashThreshold:] {
		for _, msg := range msgs {
			if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "[hook:runtime]") && strings.Contains(msg.Content, "Search thrash guidance") {
				foundOverlay = true
			}
		}
	}
	if !foundOverlay {
		t.Fatalf("expected search thrash overlay after threshold, allMsgs=%d", len(driver.allMsgs))
	}
	snap := session.Snapshot()
	for _, msg := range snap.History {
		if msg.Role == llm.RoleTool && strings.Contains(msg.Content, "blocked:") {
			t.Fatalf("expected no blocking, got blocked tool result: %s", msg.Content)
		}
	}
}

func TestRunnerBlocksRepeatedSameFileReadsAfterThreshold(t *testing.T) {
	steps := make([][]llm.Token, repeatToolCallThreshold+2)
	for i := 0; i < repeatToolCallThreshold+1; i++ {
		steps[i] = []llm.Token{{ToolCall: &llm.NativeToolCall{
			ID:       fmt.Sprintf("c%d", i+1),
			Name:     "read_file",
			ArgsJSON: `{"path":"src/main.rs"}`,
		}}}
	}
	steps[len(steps)-1] = []llm.Token{{Text: "Done."}}
	driver := &nativeSequenceDriver{steps: steps}
	reg := agenttools.NewRegistry()
	readCalls := 0
	reg.Register(agenttools.Tool{
		Name:        "read_file",
		Description: "read file",
		Parameters:  []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			readCalls++
			return "contents", nil
		},
	})
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "inspect src/main.rs"); err != nil {
		t.Fatal(err)
	}
	if readCalls != repeatToolCallThreshold {
		t.Fatalf("read calls = %d, want repeated read blocked after threshold %d", readCalls, repeatToolCallThreshold)
	}
	if !sessionHistoryContains(session, "blocked: identical read_file", "src/main.rs") {
		t.Fatalf("history missing repeated read block: %#v", session.Snapshot().History)
	}
}

func TestRunnerUsesConfiguredToolThrashThreshold(t *testing.T) {
	steps := [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "code_search", ArgsJSON: `{"path":"internal/tui/chatmodel.go","query":"one"}`}}},
		{{ToolCall: &llm.NativeToolCall{ID: "c2", Name: "code_search", ArgsJSON: `{"path":"internal/tui/chatmodel.go","query":"two"}`}}},
		{{Text: "Done."}},
	}
	driver := &nativeSequenceDriver{steps: steps}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "code_search",
		Description: "search code",
		Parameters:  []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}, {Name: "query", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "no matches", nil
		},
	})

	session := NewSession()
	session.SetTaskState(TaskState{
		Objective:            "add a file tree panel to the tui",
		Operation:            "implement",
		RequiredVerification: "inspect the relevant code, make the change with edit tools, and run the relevant verification before claiming completion",
	})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session, ToolThrashCircuitBreaker: 2})
	if err := r.Run(context.Background(), "add the panel"); err != nil {
		t.Fatal(err)
	}
	if len(driver.allMsgs) < 3 {
		t.Fatalf("driver message batches = %d, want at least 3", len(driver.allMsgs))
	}
	foundOverlay := false
	for _, msg := range driver.allMsgs[2] {
		if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "Search thrash guidance") {
			foundOverlay = true
		}
	}
	if !foundOverlay {
		t.Fatalf("expected configured threshold to emit search thrash overlay on third step")
	}
}

func TestRepeatToolCallTargetUsesSearchPattern(t *testing.T) {
	if got := repeatToolCallTarget("search", map[string]any{"pattern": "theme"}); got != "theme" {
		t.Fatalf("search target = %q", got)
	}
}

func TestRepeatToolCallTargetCoversNoOpEditAndFailedWait(t *testing.T) {
	editArgs := map[string]any{"path": "main.go", "old_text": "old", "new_text": "old"}
	if got := repeatToolCallTarget("edit_file", editArgs); got != "main.go:old->old" {
		t.Fatalf("edit target = %q", got)
	}
	if got := repeatToolCallTarget("wait_agent", map[string]any{"id": "agent-1"}); got != "agent-1" {
		t.Fatalf("wait target = %q", got)
	}
}

func TestReactToolSummaryRedactsSecretArguments(t *testing.T) {
	secret := "TOKEN=" + strings.Repeat("x", 24)
	summary := reactToolSummary(map[string]any{"command": "printf '%s' '" + secret + "'"})
	if strings.Contains(summary, secret) {
		t.Fatalf("tool summary leaked secret: %q", summary)
	}
	if !strings.Contains(summary, "<REDACTED:generic-token>") {
		t.Fatalf("tool summary missing redaction marker: %q", summary)
	}
}

func TestValidateToolArgsRejectsOmittedPlaceholderStrings(t *testing.T) {
	tool := agenttools.Tool{
		Name: "run_command",
		Parameters: []agenttools.ParameterDef{
			{Name: "command", Type: "string", Required: true},
		},
	}
	err := validateToolArgs(tool, map[string]any{"command": "<omitted 658 chars>"})
	if !strings.Contains(err, "placeholder") {
		t.Fatalf("validateToolArgs error = %q, want placeholder rejection", err)
	}
}

func TestWorkspaceEscapeToolErrorsAreModelCorrectable(t *testing.T) {
	err := errors.New(`path "/tmp/test_ghostty.scpt" escapes working directory`)
	for _, name := range []string{"write_file", "edit_file", "apply_patch"} {
		t.Run(name, func(t *testing.T) {
			if !isModelCorrectableToolExecutionError(name, err) {
				t.Fatalf("%s workspace escape should be model-correctable", name)
			}
		})
	}
}

func TestRunCommandOutOfScopeSideEffectMutationBlockedBeforeExecution(t *testing.T) {
	var executed bool
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:       "run_command",
		Parameters: []agenttools.ParameterDef{{Name: "command", Type: "string", Required: true}},
		Execute: func(context.Context, map[string]any) (string, error) {
			executed = true
			return "exit 0", nil
		},
	})
	session := NewSession()
	session.SetSideEffectIntent(SideEffectIntent{
		ID:            "intent-1",
		AllowedPaths:  []string{"docs/a.md"},
		ArtifactPaths: []string{"docs/a.md"},
	})
	turn := session.RecordInput("write docs/a.md")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	r := NewRunner(Config{Tools: reg, Session: session})

	err = r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{{
		ID:       "cmd-1",
		Name:     "run_command",
		ArgsJSON: `{"command":"printf hi > docs/b.md"}`,
	}})

	if err != nil {
		t.Fatal(err)
	}
	if executed {
		t.Fatal("out-of-scope run_command mutation executed")
	}
	if !sessionHistoryContains(session, "blocked: refusing workspace mutation", "docs/b.md") {
		t.Fatalf("history missing policy block: %#v", session.Snapshot().History)
	}
}

func containsString(values []string, want string) bool {
	return slices.Contains(values, want)
}

func repeatedTextSteps(text string, count int) [][]llm.Token {
	steps := make([][]llm.Token, 0, count)
	for range count {
		steps = append(steps, []llm.Token{{Text: text}})
	}
	return steps
}

func repeatedTokenSteps(tokens []llm.Token, count int) [][]llm.Token {
	steps := make([][]llm.Token, 0, count)
	for range count {
		steps = append(steps, append([]llm.Token(nil), tokens...))
	}
	return steps
}

func contractHasGateStatus(contract *TurnContract, name string, status ContractGateStatus) bool {
	if contract == nil {
		return false
	}
	for _, gate := range contract.Gates {
		if gate.Name == name && gate.Status == status {
			return true
		}
	}
	return false
}

func sideEffectHasGateStatus(intent *SideEffectIntent, name string, status SideEffectGateStatus) bool {
	if intent == nil {
		return false
	}
	for _, gate := range intent.Gates {
		if gate.Name == name && gate.Status == status {
			return true
		}
	}
	return false
}

func contractHasEvidence(contract *TurnContract, kind EvidenceKind, parts ...string) bool {
	if contract == nil {
		return false
	}
	for _, evidence := range contract.Evidence {
		if evidence.Kind != kind {
			continue
		}
		matched := true
		for _, part := range parts {
			if !strings.Contains(evidence.Summary, part) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func contractHasEvidenceKind(contract *TurnContract, kind EvidenceKind) bool {
	return contractHasEvidence(contract, kind)
}

func toolDefNames(defs []llm.ToolDef) []string {
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.Name)
	}
	return names
}

func TestRunnerClearHistoryResetsSessionState(t *testing.T) {
	driver := &nativeScriptedDriver{responses: []string{"done"}}
	r := NewRunner(Config{
		Driver:       driver,
		Renderer:     silentRenderer{},
		SystemPrompt: func() string { return "system prompt" },
		Session:      NewSession(),
	})

	if err := r.Run(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if got := r.LastResponse(); got != "done" {
		t.Fatalf("last response = %q", got)
	}

	r.ClearHistory()
	if got := r.LastResponse(); got != "" {
		t.Fatalf("last response after clear = %q", got)
	}
	if snap := r.session.Snapshot(); snap.Turn != 0 || len(snap.History) != 0 || len(snap.Turns) != 0 {
		t.Fatalf("snapshot after clear = %#v", snap)
	}
	if !driver.reset {
		t.Fatal("expected driver conversation state to reset")
	}
}

type toolResultCapture struct {
	silentRenderer
	calls []struct {
		name    string
		text    string
		isError bool
	}
}

func (r *toolResultCapture) ToolResult(name, text, diff string, isError bool) {
	r.calls = append(r.calls, struct {
		name    string
		text    string
		isError bool
	}{name, text, isError})
}

func TestRunnerTracksValidationPassOnGoTest(t *testing.T) {
	driver := &nativeSequenceDriver{
		steps: [][]llm.Token{
			{{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "run_command", ArgsJSON: `{"command":"go test ./..."}`}}},
			{{Text: "all tests pass"}},
		},
	}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "run_command",
		Description: "run shell command",
		Parameters:  []agenttools.ParameterDef{{Name: "command", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "ok  forge/internal/react\nexit 0", nil
		},
	})
	rec := &toolResultCapture{}
	r := NewRunner(Config{Driver: driver, Tools: reg, Renderer: rec})
	if err := r.Run(context.Background(), "verify tests pass"); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range rec.calls {
		if c.name == "__validation" && strings.Contains(c.text, "validation passed") && !c.isError {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected __validation passed call, got %#v", rec.calls)
	}
	if note := r.session.Snapshot().RuntimeNote; note != "" {
		t.Fatalf("runtime note should be empty on pass, got %q", note)
	}
}

func TestTurnContractRecordsValidationCommandEvidence(t *testing.T) {
	driver := &nativeSequenceDriver{
		steps: [][]llm.Token{
			{{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "run_command", ArgsJSON: `{"command":"go test ./internal/react"}`}}},
			{{Text: "all tests pass"}},
		},
	}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "run_command",
		Description: "run shell command",
		Parameters:  []agenttools.ParameterDef{{Name: "command", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "ok  forge/internal/react\nexit 0", nil
		},
	})
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "run the tests"); err != nil {
		t.Fatal(err)
	}

	assertContractEvidence(t, session.Snapshot().TurnContract, EvidenceVerification, "go test ./internal/react", "passed")
}

func TestRunnerTracksValidationFailOnGoTest(t *testing.T) {
	driver := &nativeSequenceDriver{
		steps: [][]llm.Token{
			{{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "run_command", ArgsJSON: `{"command":"go test ./..."}`}}},
			{{Text: "tests failed"}},
		},
	}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "run_command",
		Description: "run shell command",
		Parameters:  []agenttools.ParameterDef{{Name: "command", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "--- FAIL: TestFoo\nFAIL\tforge/internal/react\nexit 1", nil
		},
	})
	rec := &toolResultCapture{}
	r := NewRunner(Config{Driver: driver, Tools: reg, Renderer: rec})
	if err := r.Run(context.Background(), "run the tests"); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range rec.calls {
		if c.name == "__validation" && strings.Contains(c.text, "validation failed") && c.isError {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected __validation failed call, got %#v", rec.calls)
	}
	foundOverlay := false
	for _, msgs := range driver.allMsgs[1:] {
		for _, msg := range msgs {
			if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "[hook:runtime]") && strings.Contains(msg.Content, "Last validation failed") {
				foundOverlay = true
			}
		}
	}
	if !foundOverlay {
		t.Fatalf("expected failure validation overlay, allMsgs=%#v", driver.allMsgs)
	}
	if note := r.session.Snapshot().RuntimeNote; note != "" {
		t.Fatalf("runtime note should be empty after overlay migration, got %q", note)
	}
}

func TestRunnerEmitsTaskContextOnSetTaskState(t *testing.T) {
	rec := &toolResultCapture{}
	r := NewRunner(Config{
		Driver:   &nativeScriptedDriver{responses: []string{"done"}},
		Renderer: rec,
	})
	r.SetTaskState(TaskState{
		Objective:            "merge the feature branch",
		RequiredVerification: "verify branch main contains HEAD",
	})
	var found bool
	for _, c := range rec.calls {
		if c.name == "__task_context" && strings.Contains(c.text, "Objective:") && strings.Contains(c.text, "Verify:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected __task_context call, got %#v", rec.calls)
	}
}

func TestRunnerNonValidationCommandSkipsValidationTracking(t *testing.T) {
	driver := &nativeSequenceDriver{
		steps: [][]llm.Token{
			{{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "run_command", ArgsJSON: `{"command":"ls -la"}`}}},
			{{Text: "done"}},
		},
	}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "run_command",
		Description: "run shell command",
		Parameters:  []agenttools.ParameterDef{{Name: "command", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "file.go\nexit 0", nil
		},
	})
	rec := &toolResultCapture{}
	r := NewRunner(Config{Driver: driver, Tools: reg, Renderer: rec})
	if err := r.Run(context.Background(), "run ls -la"); err != nil {
		t.Fatal(err)
	}
	for _, c := range rec.calls {
		if c.name == "__validation" {
			t.Fatalf("expected no __validation call for ls, got %#v", rec.calls)
		}
	}
}

func TestHasPorcelainConflictsOnlyMatchesConflictCodes(t *testing.T) {
	// Normal staged changes must NOT be treated as conflicts.
	normal := "M  README.md\nA  newfile.go\nD  old.go\n?? untracked.txt"
	if hasPorcelainConflicts(normal) {
		t.Fatalf("staged/unstaged changes falsely detected as conflicts: %q", normal)
	}

	// Empty output = no conflicts.
	if hasPorcelainConflicts("") {
		t.Fatal("empty output falsely detected as conflicts")
	}

	// Lines with conflict XY codes must be detected.
	for _, code := range []string{"UU", "AA", "DD", "AU", "UA", "DU", "UD"} {
		line := code + " somefile.go"
		if !hasPorcelainConflicts(line) {
			t.Errorf("conflict code %q not detected", code)
		}
	}
}

func TestBlockedToolResultClearsAfterConflictResolution(t *testing.T) {
	r := NewRunner(Config{})
	// Conflict detected via git status --porcelain.
	r.updateGitWorkflowForCommand("git status --porcelain", "UU README.md")
	if !r.gitWorkflow.unmergedFiles {
		t.Fatal("setup: expected unmergedFiles=true")
	}
	// Agent resolves and re-checks: no more conflict lines.
	r.updateGitWorkflowForCommand("git status --porcelain", "M  README.md")
	if r.gitWorkflow.unmergedFiles {
		t.Fatal("unmergedFiles should be cleared after clean porcelain output")
	}
}

func TestLsFilesUnmergedRecognizedAsConflictCheck(t *testing.T) {
	if !isGitUnmergedListCommand("git ls-files -u") {
		t.Fatal("git ls-files -u not recognized as unmerged-list command")
	}
	if !isGitUnmergedListCommand("git ls-files --unmerged") {
		t.Fatal("git ls-files --unmerged not recognized as unmerged-list command")
	}
	r := NewRunner(Config{})
	r.updateGitWorkflowForCommand("git ls-files -u", "")
	if r.gitWorkflow.unmergedFiles {
		t.Fatal("empty ls-files -u output should not set unmergedFiles")
	}
	r.updateGitWorkflowForCommand("git ls-files -u", "100644 abc123 1\tREADME.md")
	if !r.gitWorkflow.unmergedFiles {
		t.Fatal("non-empty ls-files -u output should set unmergedFiles")
	}
}

func TestRunnerGitWorkflowOverlayAppearsOnUnmergedFiles(t *testing.T) {
	driver := &captureMessagesDriver{response: "I see unmerged files."}
	r := NewRunner(Config{Driver: driver})

	r.updateGitWorkflowForCommand("git status --porcelain", "UU README.md")
	r.syncRuntimeNote()

	if err := r.Run(context.Background(), "help me resolve conflicts"); err != nil {
		t.Fatal(err)
	}

	var gitMsg string
	for _, msg := range driver.lastMessages {
		if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "[hook:runtime]") && strings.Contains(msg.Content, "Git merge workflow active") {
			gitMsg = msg.Content
			break
		}
	}
	if gitMsg == "" {
		t.Fatalf("expected git_workflow hook overlay in messages: %#v", driver.lastMessages)
	}
	if !strings.Contains(gitMsg, "git_merge_status") {
		t.Fatalf("git workflow overlay = %q", gitMsg)
	}
}

func TestRunnerGitWorkflowOverlayShiftsAfterConflictResolution(t *testing.T) {
	driver := &captureMessagesDriver{response: "Conflicts resolved, ready to commit."}
	r := NewRunner(Config{Driver: driver})

	// Conflict appears.
	r.updateGitWorkflowForCommand("git status --porcelain", "UU README.md")
	r.syncRuntimeNote()

	// Conflicts are staged/resolved — porcelain no longer shows UU lines, but merge is still active.
	r.updateGitWorkflowForCommand("git status --porcelain", "M  README.md")
	r.syncRuntimeNote()

	if err := r.Run(context.Background(), "commit the resolution"); err != nil {
		t.Fatal(err)
	}

	// Merge is still active so the overlay should still be present, but with the
	// shorter "inspect current merge state" guidance rather than the conflict-resolution message.
	var gitMsg string
	for _, msg := range driver.lastMessages {
		if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "Git merge workflow active") {
			gitMsg = msg.Content
			break
		}
	}
	if gitMsg == "" {
		t.Fatal("expected git_workflow overlay to remain while merge is still active after conflict resolution")
	}
	if strings.Contains(gitMsg, "Resolve each conflicted file") {
		t.Fatalf("expected shorter merge-active message, got: %q", gitMsg)
	}
}

func TestRunnerGitWorkflowOverlayClearsOnSuccessfulCommit(t *testing.T) {
	driver := &captureMessagesDriver{response: "Committed."}
	r := NewRunner(Config{Driver: driver})

	// Start a merge workflow.
	r.updateGitWorkflowForCommand("git status --porcelain", "UU README.md")
	r.syncRuntimeNote()

	// Successful commit clears all git workflow state.
	r.updateGitWorkflowForCommitResult("[main abc1234] merge: resolved README conflict\n 2 files changed, 3 insertions(+), 1 deletion(-)")
	r.syncRuntimeNote()

	if err := r.Run(context.Background(), "good, what next"); err != nil {
		t.Fatal(err)
	}

	for _, msg := range driver.lastMessages {
		if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "Git merge workflow active") {
			t.Fatalf("git_workflow overlay should be gone after successful commit, but found: %q", msg.Content)
		}
	}
}

func TestRunnerReportsToolExposureDecision(t *testing.T) {
	reg := agenttools.NewRegistry()
	for _, name := range []string{"read_file", "spawn_agent", "wait_agent"} {
		toolName := name
		reg.Register(agenttools.Tool{
			Name:        toolName,
			Description: toolName,
			AutoApprove: true,
			Execute: func(_ context.Context, _ map[string]any) (string, error) {
				return "ok", nil
			},
		})
	}
	var decisions []ToolExposureDecision
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "spawn-1", Name: "spawn_agent", ArgsJSON: `{}`}}},
		{{Text: "delegating"}},
	}}
	r := NewRunner(Config{
		Driver: driver,
		Tools:  reg,
		ToolExposureObserver: func(decision ToolExposureDecision) {
			decisions = append(decisions, decision)
		},
	})

	if err := r.Run(context.Background(), "ask three agents to review the codebase TOKEN="+strings.Repeat("x", 24)); err != nil {
		t.Fatal(err)
	}
	if len(decisions) == 0 {
		t.Fatal("expected tool exposure decision")
	}
	decision := decisions[0]
	if decision.Reason != "all" {
		t.Fatalf("reason = %q, want all", decision.Reason)
	}
	if decision.RequireToolCall {
		t.Fatalf("RequireToolCall = true, want false (tool calls are never forced)")
	}
	for _, want := range []string{"spawn_agent", "wait_agent"} {
		if !containsString(decision.ToolNames, want) {
			t.Fatalf("tool names = %#v, want %s", decision.ToolNames, want)
		}
	}
}

func TestRunnerAllowsTextAfterRequiredToolCallInSameTurn(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "status-1", Name: "agent_status", ArgsJSON: `{}`}}},
		{{Text: "agent-1 is running"}},
	}}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "agent_status",
		Description: "status",
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return `{"agents":[{"id":"agent-1","role":"explorer","status":"running"}]}`, nil
		},
	})
	session := NewSession()
	session.UpsertAgentTask(AgentTaskState{ID: "agent-1", Role: "explorer", Status: AgentStatusRunning})
	r := NewRunner(Config{
		Driver:  driver,
		Tools:   reg,
		Session: session,
	})

	if err := r.Run(context.Background(), "check active agents"); err != nil {
		t.Fatal(err)
	}
	snap := session.Snapshot()
	if got := snap.History[len(snap.History)-1].Content; got != "agent-1 is running" {
		t.Fatalf("final content = %q", got)
	}
}

func TestRunnerRetriesTransientStreamErrorWhileAgentRuns(t *testing.T) {
	driver := &nativeSequenceDriver{
		errs: []error{errors.New("request timeout")},
		steps: [][]llm.Token{
			{},
			{{ToolCall: &llm.NativeToolCall{ID: "status-1", Name: "agent_status", ArgsJSON: `{}`}}},
			{{Text: "agent-1 is still running"}},
		},
	}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "agent_status",
		Description: "status",
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return `{"agents":[{"id":"agent-1","role":"cci","status":"running"}]}`, nil
		},
	})
	session := NewSession()
	session.UpsertAgentTask(AgentTaskState{ID: "agent-1", Role: "cci", Status: AgentStatusRunning})
	rec := &recordingRenderer{}
	r := NewRunner(Config{
		Driver:   driver,
		Tools:    reg,
		Session:  session,
		Renderer: rec,
	})

	if err := r.Run(context.Background(), "ask agents to compare the repo and gather the cci report"); err != nil {
		t.Fatal(err)
	}
	if got := driver.callCount; got != 3 {
		t.Fatalf("driver calls = %d, want timeout retry, tool call, final text", got)
	}
	if len(rec.retryTexts) == 0 {
		t.Fatal("expected retry notice after transient timeout")
	}
	snap := session.Snapshot()
	if got := snap.Turns[len(snap.Turns)-1].Error; got != "" {
		t.Fatalf("turn error = %q, want none", got)
	}
	if got := snap.History[len(snap.History)-1].Content; got != "agent-1 is still running" {
		t.Fatalf("final content = %q", got)
	}
}

func TestRunnerFallsBackToCompletedAgentResultWhenParentStreamTimesOut(t *testing.T) {
	driver := &nativeSequenceDriver{errs: []error{&llm.RetryAttemptsExhaustedError{Attempts: 3, Err: errors.New("stream idle timeout after 30s")}}}
	session := NewSession()
	rec := &recordingRenderer{}
	r := NewRunner(Config{Driver: driver, Session: session, Renderer: rec})
	turn := session.RecordInput("compare forge with codex and opencode")
	session.UpsertAgentTask(AgentTaskState{
		ID:          "agent-1",
		Role:        "explorer",
		Status:      AgentStatusCompleted,
		Result:      "complete comparison from child agent",
		ParentTurn:  turn,
		CompletedAt: time.Now(),
	})
	_, cancel, err := session.BeginTurn(context.Background(), fmt.Sprintf("turn-%d", turn))
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	if err := r.runLoop(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	if driver.callCount != 1 {
		t.Fatalf("driver calls = %d, want one exhausted synthesis attempt", driver.callCount)
	}
	if len(rec.retryTexts) != 0 {
		t.Fatalf("retry notices = %#v, want none", rec.retryTexts)
	}
	if got := r.LastResponse(); !strings.Contains(got, "complete comparison from child agent") {
		t.Fatalf("last response = %q, want completed child result", got)
	}
	snap := session.Snapshot()
	if got := snap.Turns[len(snap.Turns)-1].Error; got != "" {
		t.Fatalf("turn error = %q, want none", got)
	}
}

func TestRunnerDoesNotFallbackWriteStaleDelegationResultForUnrelatedFollowUp(t *testing.T) {
	driver := &nativeSequenceDriver{errs: []error{
		errors.New("request timeout"),
		errors.New("request timeout"),
		errors.New("request timeout"),
		errors.New("request timeout"),
	}}
	session := NewSession()
	delegationTurn := session.RecordInput("ask agents to audit the repo and write docs/reports/audit.md")
	_ = session.AppendAssistantToolTurn("", []llm.NativeToolCall{{ID: "wait-1", Name: "wait_agent", ArgsJSON: `{}`}})
	if err := session.AppendNativeToolResult("wait-1", `{"status":"completed"}`); err != nil {
		t.Fatal(err)
	}
	if err := session.AppendAssistantMessage("Parent model connection failed while composing the final response. Showing completed child-agent result instead.\n\nold fallback"); err != nil {
		t.Fatal(err)
	}
	session.UpsertAgentTask(AgentTaskState{
		ID:          "agent-1",
		Role:        "synthesizer",
		Status:      AgentStatusCompleted,
		Result:      "# Old Audit\n\nDo not write this for unrelated input.",
		ParentTurn:  delegationTurn,
		CompletedAt: time.Now(),
	})
	session.SetPendingDelegationAction(DelegationActionState{
		Kind:       DelegationActionWriteDoc,
		TargetPath: "docs/reports/audit.md",
	})
	turn := session.RecordInput("what did we do so far?")

	reg := agenttools.NewRegistry()
	writeCalls := 0
	reg.Register(agenttools.Tool{
		Name:        "write_file",
		Description: "write file",
		AutoApprove: true,
		Execute: func(context.Context, map[string]any) (string, error) {
			writeCalls++
			return "wrote stale audit", nil
		},
	})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.runLoop(context.Background(), turn); err == nil {
		t.Fatal("expected parent timeout to surface instead of stale fallback write")
	}
	if writeCalls != 0 {
		t.Fatalf("write_file calls = %d, want none for unrelated follow-up", writeCalls)
	}
}

func TestRunnerDoesNotUseCompletedAgentFallbackWhileSameTurnAgentStillRuns(t *testing.T) {
	for _, status := range []AgentStatus{AgentStatusRunning, AgentStatusPending} {
		t.Run(string(status), func(t *testing.T) {
			driver := &nativeSequenceDriver{
				errs: []error{errors.New("request timeout")},
				steps: [][]llm.Token{
					{},
					{{Text: "agent-2 is still active"}},
				},
			}
			session := NewSession()
			rec := &recordingRenderer{}
			r := NewRunner(Config{Driver: driver, Session: session, Renderer: rec})
			turn := session.RecordInput("compare forge with codex and opencode")
			session.UpsertAgentTask(AgentTaskState{
				ID:          "agent-1",
				Role:        "explorer",
				Status:      AgentStatusCompleted,
				Result:      "complete comparison from child agent",
				ParentTurn:  turn,
				CompletedAt: time.Now(),
			})
			session.UpsertAgentTask(AgentTaskState{
				ID:         "agent-2",
				Role:       "cci",
				Status:     status,
				ParentTurn: turn,
			})

			_, cancel, err := session.BeginTurn(context.Background(), fmt.Sprintf("turn-%d", turn))
			if err != nil {
				t.Fatal(err)
			}
			defer cancel()

			if err := r.runLoop(context.Background(), turn); err != nil {
				t.Fatal(err)
			}
			if driver.callCount != 2 {
				t.Fatalf("driver calls = %d, want retry then final status", driver.callCount)
			}
			if len(rec.retryTexts) == 0 {
				t.Fatal("expected retry notice while same-turn agent remains active")
			}
			if got := r.LastResponse(); got != "agent-2 is still active" {
				t.Fatalf("last response = %q, want active-agent status", got)
			}
		})
	}
}

func TestRunnerDoesNotFailEmptyResponseWhileChildAgentRuns(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "spawn-1", Name: "spawn_agent", ArgsJSON: `{"role":"explorer","task_description":"inspect repo"}`}}},
		{},
		{},
		{},
		{},
	}}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "spawn_agent",
		Description: "spawn child",
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return `{"id":"agent-1","role":"explorer","status":"running"}`, nil
		},
	})
	for _, name := range []string{"wait_agent", "agent_status", "kill_agent"} {
		toolName := name
		reg.Register(agenttools.Tool{
			Name:        toolName,
			Description: toolName,
			AutoApprove: true,
			Execute: func(_ context.Context, _ map[string]any) (string, error) {
				return `{}`, nil
			},
		})
	}
	session := NewSession()
	r := NewRunner(Config{
		Driver:  driver,
		Tools:   reg,
		Session: session,
	})

	if err := r.Run(context.Background(), "ask an agent to inspect the repo"); err != nil {
		t.Fatal(err)
	}

	snap := session.Snapshot()
	if len(snap.History) == 0 {
		t.Fatal("expected history entries")
	}
	last := snap.History[len(snap.History)-1]
	for _, want := range []string{"agent-1", "running"} {
		if !strings.Contains(last.Content, want) {
			t.Fatalf("fallback content = %q, want %q", last.Content, want)
		}
	}
	if len(outstandingSpawnedAgents(snap)) == 0 {
		t.Fatal("expected child agent to remain outstanding")
	}
}

func TestAgentTaskPromptLineRedactsRecentActivitySummary(t *testing.T) {
	secret := "TOKEN=" + strings.Repeat("x", 24)
	line := formatAgentTaskPromptLine(AgentTaskState{
		ID:           "agent-1",
		Role:         "repo-auditor",
		Status:       AgentStatusRunning,
		LastToolName: "run_command",
		RecentActivity: []AgentTaskActivity{{
			ToolName: "run_command",
			Summary:  "printed " + secret,
		}},
	})
	if strings.Contains(line, secret) {
		t.Fatalf("agent task prompt leaked secret: %q", line)
	}
	if !strings.Contains(line, "<REDACTED:generic-token>") {
		t.Fatalf("agent task prompt missing redaction marker: %q", line)
	}
}

func TestRunnerKilledAgentStateOverridesStaleTranscript(t *testing.T) {
	reg := agenttools.NewRegistry()
	for _, name := range []string{"spawn_agent", "wait_agent", "agent_status", "kill_agent"} {
		reg.Register(agenttools.Tool{Name: name, Description: name})
	}
	r := NewRunner(Config{Tools: reg})
	snap := SessionSnapshot{
		LastInput: "is the child agent still running?",
		AgentTasks: []AgentTaskState{{
			ID:     "agent-1",
			Role:   "repo-auditor",
			Status: AgentStatusKilled,
			Error:  context.Canceled.Error(),
		}},
		History: []llm.Message{
			{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{{ID: "spawn-1", Name: "spawn_agent", ArgsJSON: `{}`}}},
			{Role: llm.RoleTool, ToolCallID: "spawn-1", Content: `{"id":"agent-1","role":"repo-auditor","status":"running"}`},
		},
	}

	defs, decision := r.selectToolDefsWithDecision(snap)
	if decision.Reason != "all" {
		t.Fatalf("tool exposure reason = %q, want all", decision.Reason)
	}
	if len(toolDefNames(defs)) != 4 {
		t.Fatalf("tools = %#v, want the full registry exposed", toolDefNames(defs))
	}
	if got := hookOverlayContent(promptHookOutputForSnapshot(t, snap), "agent_status"); got != "" {
		t.Fatalf("agent_status overlay = %q, want empty for killed agent", got)
	}
}

func TestRunnerPromptIncludesOutstandingAgentStatus(t *testing.T) {
	session := NewSession()
	session.RecordInput("ask three agents to review the codebase")
	if err := session.AppendAssistantWithToolCalls([]llm.NativeToolCall{{ID: "spawn-1", Name: "spawn_agent", ArgsJSON: `{}`}}); err != nil {
		t.Fatal(err)
	}
	if err := session.AppendNativeToolResult("spawn-1", `{"id":"agent-1","role":"code-reviewer","status":"running"}`); err != nil {
		t.Fatal(err)
	}
	r := NewRunner(Config{Session: session})

	output := r.promptHookOutput(context.Background())
	got := hookOverlayContent(output, "agent_status")
	for _, want := range []string{"Outstanding child agents", "agent-1", "code-reviewer", "Do not say no agents are running"} {
		if !strings.Contains(got, want) {
			t.Fatalf("agent_status overlay = %q, want %q", got, want)
		}
	}
}

func TestRunnerPromptIncludesAgentTaskProgress(t *testing.T) {
	snap := SessionSnapshot{
		AgentTasks: []AgentTaskState{{
			ID:           "agent-1",
			Role:         "repo-auditor",
			Status:       AgentStatusRunning,
			LastToolName: "read_file",
			RecentActivity: []AgentTaskActivity{{
				ToolName: "read_file",
				Summary:  "README.md",
			}},
		}},
	}

	got := hookOverlayContent(promptHookOutputForSnapshot(t, snap), "agent_status")
	for _, want := range []string{"last: read_file", "README.md"} {
		if !strings.Contains(got, want) {
			t.Fatalf("agent_status overlay = %q, want %q", got, want)
		}
	}
}

func TestRunnerClearsBlockingHandoffAfterParentWrite(t *testing.T) {
	session := NewSession()
	session.UpsertAgentTask(AgentTaskState{
		ID:     "agent-1",
		Role:   "repo-auditor",
		Status: AgentStatusCompleted,
		Result: "audit report",
		Handoff: &AgentHandoff{RemainingActions: []AgentFollowupAction{{
			Kind:        AgentActionWriteFile,
			TargetPath:  "docs/audit.md",
			Description: "Save audit report",
			Blocking:    true,
		}}},
	})
	r := NewRunner(Config{Session: session})

	r.updatePostDelegationWorkflow("write_file", map[string]any{"path": "docs/audit.md"}, "wrote docs/audit.md", false)

	tasks := session.Snapshot().AgentTasks
	if len(tasks) != 1 || tasks[0].Handoff != nil {
		t.Fatalf("agent tasks after parent write = %#v, want handoff cleared", tasks)
	}
}

func TestRunnerSuccessfulSideEffectToolResultUpdatesTurnContractGate(t *testing.T) {
	session := NewSession()
	session.SetTurnContract(TurnContract{
		ID:              "contract-1",
		Intent:          TurnIntentEditCode,
		RequiredActions: []ContractAction{{Kind: ContractActionCommit}},
		Gates:           []ContractGate{{Name: "commit", Status: ContractGatePending}},
		Status:          ContractStatusActive,
	})
	session.SetSideEffectIntent(SideEffectIntent{
		ID:              "intent-1",
		RequiredActions: []SideEffectAction{SideEffectActionCommit},
		Gates:           []SideEffectGate{{Name: string(SideEffectActionCommit), Status: SideEffectGatePending}},
	})
	r := NewRunner(Config{Session: session})

	r.recordToolResultEvidence("git_commit", map[string]any{"message": "task 10"}, "commit abc123 created with files: a.go", false)
	r.updateSideEffectGatesAfterToolResult("git_commit", map[string]any{"message": "task 10"}, "commit abc123 created with files: a.go", false)

	snap := session.Snapshot()
	if snap.SideEffectIntent == nil || !sideEffectHasGateStatus(snap.SideEffectIntent, string(SideEffectActionCommit), SideEffectGatePassed) {
		t.Fatalf("SideEffectIntent = %#v, want passed commit gate", snap.SideEffectIntent)
	}
	if snap.TurnContract == nil || !contractHasGateStatus(snap.TurnContract, "commit", ContractGatePassed) || !contractHasEvidence(snap.TurnContract, EvidenceTool, "git_commit", "abc123") {
		t.Fatalf("TurnContract = %#v, want passed commit gate and evidence", snap.TurnContract)
	}
}

func TestRunnerClearsPendingDelegationActionAfterParentWrite(t *testing.T) {
	session := NewSession()
	session.SetPendingDelegationAction(DelegationActionState{Kind: DelegationActionWriteDoc, TargetPath: "docs/reports/audit.md"})
	r := NewRunner(Config{Session: session})

	r.updatePostDelegationWorkflow("write_file", map[string]any{"path": "docs/reports/audit.md"}, "wrote docs/reports/audit.md", false)

	if action := session.Snapshot().PendingDelegationAction; action != nil {
		t.Fatalf("pending delegation action after write = %#v", action)
	}
}

func TestRunnerKeepsPendingDelegationActionAfterWrongParentWritePath(t *testing.T) {
	session := NewSession()
	session.SetPendingDelegationAction(DelegationActionState{Kind: DelegationActionWriteDoc, TargetPath: "docs/reports/audit.md"})
	r := NewRunner(Config{Session: session})

	r.updatePostDelegationWorkflow("write_file", map[string]any{"path": "docs/reports/other.md"}, "wrote docs/reports/other.md", false)

	if action := session.Snapshot().PendingDelegationAction; action == nil || action.TargetPath != "docs/reports/audit.md" {
		t.Fatalf("pending delegation action after wrong write = %#v", action)
	}
}

func TestRunnerPathlessDelegationWriteUsesRequiredArtifactTarget(t *testing.T) {
	session := NewSession()
	session.SetPendingDelegationAction(DelegationActionState{Kind: DelegationActionWriteDoc})
	session.SetTurnContract(TurnContract{
		ID:                "contract-1",
		Intent:            TurnIntentWriteArtifact,
		RequiredArtifacts: []ArtifactRequirement{{Path: "docs/a.md"}},
		Gates:             []ContractGate{{Name: "artifact", Status: ContractGatePending}},
		Status:            ContractStatusActive,
	})
	r := NewRunner(Config{Session: session})

	r.updatePostDelegationWorkflow("write_file", map[string]any{"path": "docs/other.md"}, "wrote docs/other.md", false)
	if action := session.Snapshot().PendingDelegationAction; action == nil {
		t.Fatal("pending delegation action cleared by wrong path")
	}

	r.updatePostDelegationWorkflow("write_file", map[string]any{"path": "docs/a.md"}, "wrote docs/a.md", false)
	if action := session.Snapshot().PendingDelegationAction; action != nil {
		t.Fatalf("pending delegation action after exact artifact write = %#v", action)
	}
}

func TestRunnerDelegationWriteRejectsBackupPathAndResultMention(t *testing.T) {
	session := NewSession()
	session.SetPendingDelegationAction(DelegationActionState{Kind: DelegationActionWriteDoc, TargetPath: "docs/a.md"})
	r := NewRunner(Config{Session: session})

	r.updatePostDelegationWorkflow("write_file", map[string]any{"path": "docs/a.md.bak"}, "wrote backup while mentioning docs/a.md", false)

	if action := session.Snapshot().PendingDelegationAction; action == nil || action.TargetPath != "docs/a.md" {
		t.Fatalf("pending delegation action after backup write = %#v", action)
	}
}

func TestRunnerStaleCompletedAgentFallbackDoesNotMutateContractOrHistory(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("write docs/a.md")
	session.SetTurnContract(TurnContract{
		ID:                "contract-1",
		Intent:            TurnIntentWriteArtifact,
		RequiredArtifacts: []ArtifactRequirement{{Path: "docs/a.md"}},
		Gates:             []ContractGate{{Name: "artifact", Status: ContractGatePending}},
		Status:            ContractStatusActive,
	})
	session.UpsertAgentTask(AgentTaskState{ID: "agent-1", Role: "planner", Status: AgentStatusCompleted, Result: "# A\n\n## Plan\n\nchild result", ParentTurn: turn})
	r := NewRunner(Config{Session: session})
	_, cancel, err := session.BeginTurn(context.Background(), "turn-2")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	before := session.Snapshot()

	_, _ = r.tryCompletedAgentResultFallbackAfterError(context.Background(), turn)

	after := session.Snapshot()
	if contractHasEvidenceKind(after.TurnContract, EvidenceModelViolation) {
		t.Fatalf("stale fallback mutated turn contract evidence: %#v", after.TurnContract.Evidence)
	}
	if !contractHasGateStatus(after.TurnContract, "artifact", ContractGatePending) || len(after.TurnContract.Gates) != len(before.TurnContract.Gates) || after.TurnContract.Gates[0].Evidence != "" {
		t.Fatalf("stale fallback mutated gates: before=%#v after=%#v", before.TurnContract.Gates, after.TurnContract.Gates)
	}
	if len(after.History) != len(before.History) {
		t.Fatalf("stale fallback mutated history: before=%#v after=%#v", before.History, after.History)
	}
}

func TestRunnerNoActiveTurnCompletedAgentFallbackDoesNotMutateContractOrHistory(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("write docs/a.md")
	session.SetTurnContract(TurnContract{
		ID:                "contract-1",
		Intent:            TurnIntentWriteArtifact,
		RequiredArtifacts: []ArtifactRequirement{{Path: "docs/a.md"}},
		Gates:             []ContractGate{{Name: "artifact", Status: ContractGatePending}},
		Status:            ContractStatusActive,
	})
	session.UpsertAgentTask(AgentTaskState{ID: "agent-1", Role: "planner", Status: AgentStatusCompleted, Result: "# A\n\n## Plan\n\nchild result", ParentTurn: turn})
	r := NewRunner(Config{Session: session})
	before := session.Snapshot()

	_, _ = r.tryCompletedAgentResultFallbackAfterError(context.Background(), turn)

	after := session.Snapshot()
	if contractHasEvidenceKind(after.TurnContract, EvidenceModelViolation) {
		t.Fatalf("no-active fallback mutated turn contract evidence: %#v", after.TurnContract.Evidence)
	}
	if !contractHasGateStatus(after.TurnContract, "artifact", ContractGatePending) || len(after.TurnContract.Gates) != len(before.TurnContract.Gates) || after.TurnContract.Gates[0].Evidence != "" {
		t.Fatalf("no-active fallback mutated gates: before=%#v after=%#v", before.TurnContract.Gates, after.TurnContract.Gates)
	}
	if len(after.History) != len(before.History) {
		t.Fatalf("no-active fallback mutated history: before=%#v after=%#v", before.History, after.History)
	}
	if r.LastResponse() != "" {
		t.Fatalf("no-active fallback appended assistant text: %q", r.LastResponse())
	}
}

func TestRunnerPluginPromptOverlayDoesNotDuplicate(t *testing.T) {
	session := NewSession()
	r := NewRunner(Config{
		Session: session,
		ConfigureHooks: func(registry *hooks.Registry) {
			registry.Register(hooks.PointPromptContext, "plugin:demo", func(context.Context, hooks.Event) []hooks.Result {
				return []hooks.Result{hooks.OverlayResult{
					Key:        "plugin_demo_context",
					Content:    "plugin prompt",
					Priority:   hooks.PriorityHigh,
					Provenance: "plugin:demo",
				}}
			})
		},
	})
	r.syncRuntimeNote()
	r.syncRuntimeNote()

	count := 0
	for _, overlay := range session.Snapshot().HookOutput.Overlays {
		if overlay.Key == "plugin_demo_context" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("plugin overlay count = %d, overlays=%#v", count, session.Snapshot().HookOutput.Overlays)
	}
}

func TestRunnerPreservesNonBlockingBeforeToolHookOutput(t *testing.T) {
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "plugin__demo__echo",
		Description: "echo",
		Parameters:  []agenttools.ParameterDef{{Name: "message", Type: "string", Required: true}},
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			message, _ := args["message"].(string)
			return message, nil
		},
	})
	session := NewSession()
	r := NewRunner(Config{
		Tools:   reg,
		Session: session,
		ConfigureHooks: func(registry *hooks.Registry) {
			registry.Register(hooks.PointBeforeTool, "plugin:demo:before", func(context.Context, hooks.Event) []hooks.Result {
				return []hooks.Result{hooks.OverlayResult{
					Key:        "plugin_demo_before",
					Content:    "before hook note",
					Priority:   hooks.PriorityHigh,
					Provenance: "plugin:demo",
				}}
			})
		},
	})
	turn := session.RecordInput("use the demo plugin")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if err := r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{{
		ID:       "c1",
		Name:     "plugin__demo__echo",
		ArgsJSON: `{"message":"hello"}`,
	}}); err != nil {
		t.Fatal(err)
	}

	found := false
	for _, overlay := range session.Snapshot().HookOutput.Overlays {
		if overlay.Key == "plugin_demo_before" && overlay.Content == "before hook note" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected before_tool overlay to persist, overlays=%#v", session.Snapshot().HookOutput.Overlays)
	}
}

func TestRunnerExecuteNativeToolCallsRejectsResultAfterTurnEnd(t *testing.T) {
	reg := agenttools.NewRegistry()
	session := NewSession()
	reg.Register(agenttools.Tool{
		Name:        "late_result",
		Description: "ends the active turn before returning",
		Execute: func(context.Context, map[string]any) (string, error) {
			if err := session.EndTurn("turn-1", TurnEndReasonCancelled); err != nil {
				return "", err
			}
			return "late success", nil
		},
	})
	r := NewRunner(Config{Tools: reg, Session: session})
	turn := session.RecordInput("run late tool")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	err = r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{{ID: "c1", Name: "late_result", ArgsJSON: `{}`}})
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("executeNativeToolCalls error = %v, want nil or context canceled", err)
	}

	for _, item := range session.Snapshot().Items {
		if item.Kind == protocol.ItemToolResult {
			t.Fatalf("stale tool result was appended: %#v", item)
		}
	}
}

func TestRunnerExecuteNativeToolCallsRejectsResultWithoutActiveTurn(t *testing.T) {
	reg := agenttools.NewRegistry()
	session := NewSession()
	reg.Register(agenttools.Tool{
		Name:        "late_result",
		Description: "returns after turn is no longer active",
		Execute: func(context.Context, map[string]any) (string, error) {
			return "late success", nil
		},
	})
	r := NewRunner(Config{Tools: reg, Session: session})
	turn := session.RecordInput("run late tool")

	err := r.executeNativeToolCalls(context.Background(), turn, []llm.NativeToolCall{{ID: "c1", Name: "late_result", ArgsJSON: `{}`}})
	if !errors.Is(err, ErrStaleTurn) {
		t.Fatalf("executeNativeToolCalls error = %v, want ErrStaleTurn", err)
	}

	for _, item := range session.Snapshot().Items {
		if item.Kind == protocol.ItemToolResult {
			t.Fatalf("stale tool result was appended: %#v", item)
		}
	}
}

func TestRunnerExecuteNativeToolCallsSkipsStaleResultSideEffectsAfterCancellation(t *testing.T) {
	reg := agenttools.NewRegistry()
	session := NewSession()
	renderer := &recordingRenderer{}
	reg.Register(agenttools.Tool{
		Name:        "late_result",
		Description: "cancels the active turn before returning",
		Execute: func(context.Context, map[string]any) (string, error) {
			if err := session.CancelActiveTurn("user cancelled"); err != nil {
				return "", err
			}
			return "late success", nil
		},
	})
	r := NewRunner(Config{
		Tools:    reg,
		Session:  session,
		Renderer: renderer,
		ConfigureHooks: func(registry *hooks.Registry) {
			registry.Register(hooks.PointAfterTool, "test:after", func(context.Context, hooks.Event) []hooks.Result {
				return []hooks.Result{hooks.OverlayResult{Key: "late_after", Content: "should not apply", Provenance: "test"}}
			})
		},
	})
	turn := session.RecordInput("run late tool")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	err = r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{{ID: "c1", Name: "late_result", ArgsJSON: `{}`}})
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("executeNativeToolCalls error = %v, want nil or context canceled", err)
	}

	for _, event := range renderer.events {
		if event == "tool_result" {
			t.Fatalf("stale tool result rendered: events=%#v", renderer.events)
		}
	}
	snap := session.Snapshot()
	if snap.HookOutputSet || len(snap.HookOutput.Overlays) != 0 {
		t.Fatalf("stale hook output applied: %#v", snap.HookOutput)
	}
	for _, item := range snap.Items {
		if item.Kind == protocol.ItemToolResult {
			t.Fatalf("stale tool result was appended: %#v", item)
		}
	}
}

func TestRunnerExecuteNativeToolCallsSkipsPreExecutionBranchesAfterCancellation(t *testing.T) {
	for _, tc := range []struct {
		name string
		call llm.NativeToolCall
		reg  func(*agenttools.Registry, *hooks.Registry)
	}{
		{
			name: "malformed args",
			call: llm.NativeToolCall{ID: "c1", Name: "custom_tool", ArgsJSON: `{`},
			reg: func(reg *agenttools.Registry, _ *hooks.Registry) {
				reg.Register(agenttools.Tool{Name: "custom_tool", Description: "custom"})
			},
		},
		{
			name: "validation failure",
			call: llm.NativeToolCall{ID: "c1", Name: "custom_tool", ArgsJSON: `{}`},
			reg: func(reg *agenttools.Registry, _ *hooks.Registry) {
				reg.Register(agenttools.Tool{Name: "custom_tool", Description: "custom", Parameters: []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}}})
			},
		},
		{
			name: "blocked before hook",
			call: llm.NativeToolCall{ID: "c1", Name: "custom_tool", ArgsJSON: `{}`},
			reg: func(reg *agenttools.Registry, hooksReg *hooks.Registry) {
				reg.Register(agenttools.Tool{Name: "custom_tool", Description: "custom"})
				hooksReg.Register(hooks.PointBeforeTool, "test:block", func(context.Context, hooks.Event) []hooks.Result {
					return []hooks.Result{hooks.BlockResult{Message: "blocked"}}
				})
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := agenttools.NewRegistry()
			session := NewSession()
			session.SetTurnContract(TurnContract{ID: "contract-1", Status: ContractStatusActive})
			r := NewRunner(Config{
				Tools:   reg,
				Session: session,
				ConfigureHooks: func(hooksReg *hooks.Registry) {
					tc.reg(reg, hooksReg)
				},
			})
			turn := session.RecordInput("run tool")
			active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
			if err != nil {
				t.Fatal(err)
			}
			defer cancel()
			if err := session.CancelActiveTurn("user cancelled"); err != nil {
				t.Fatal(err)
			}

			err = r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{tc.call})
			if err != nil && !errors.Is(err, ErrStaleTurn) && !errors.Is(err, context.Canceled) {
				t.Fatalf("executeNativeToolCalls error = %v, want stale/cancelled", err)
			}

			for _, item := range session.Snapshot().Items {
				if item.Kind == protocol.ItemToolResult || item.Kind == protocol.ItemFailure {
					t.Fatalf("stale pre-execution item was appended: %#v", item)
				}
			}
			if contract := session.Snapshot().TurnContract; contract == nil || len(contract.Evidence) != 0 {
				t.Fatalf("stale pre-execution contract evidence = %#v", contract)
			}
		})
	}
}

func TestRunnerStaleUnknownToolDoesNotMutateTurnContract(t *testing.T) {
	session := NewSession()
	session.SetTurnContract(TurnContract{ID: "contract-1", Status: ContractStatusActive})
	r := NewRunner(Config{Tools: agenttools.NewRegistry(), Session: session})
	turn := session.RecordInput("run missing tool")

	err := r.executeNativeToolCalls(context.Background(), turn, []llm.NativeToolCall{{ID: "missing-1", Name: "missing_tool", ArgsJSON: `{}`}})
	if !errors.Is(err, ErrStaleTurn) {
		t.Fatalf("executeNativeToolCalls error = %v, want ErrStaleTurn", err)
	}
	if contract := session.Snapshot().TurnContract; contract == nil || len(contract.Evidence) != 0 {
		t.Fatalf("stale unknown-tool contract evidence = %#v", contract)
	}
}

func TestRunnerStaleToolErrorDoesNotMutateTurnContract(t *testing.T) {
	reg := agenttools.NewRegistry()
	session := NewSession()
	session.SetTurnContract(TurnContract{ID: "contract-1", Status: ContractStatusActive})
	reg.Register(agenttools.Tool{
		Name:        "write_file",
		Description: "fails after cancelling the active turn",
		Parameters:  []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}},
		Execute: func(context.Context, map[string]any) (string, error) {
			if err := session.CancelActiveTurn("user cancelled"); err != nil {
				return "", err
			}
			return "", errors.New("late write failure")
		},
	})
	r := NewRunner(Config{Tools: reg, Session: session})
	turn := session.RecordInput("write file")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	err = r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{{ID: "write-1", Name: "write_file", ArgsJSON: `{"path":"a.txt"}`}})
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("executeNativeToolCalls error = %v, want nil or context canceled", err)
	}
	if contract := session.Snapshot().TurnContract; contract == nil || len(contract.Evidence) != 0 {
		t.Fatalf("stale tool-error contract evidence = %#v", contract)
	}
}

func TestRunnerStaleBeforeHookBlockDoesNotMutateTurnContract(t *testing.T) {
	reg := agenttools.NewRegistry()
	session := NewSession()
	session.SetTurnContract(TurnContract{ID: "contract-1", Status: ContractStatusActive})
	reg.Register(agenttools.Tool{
		Name:        "write_file",
		Description: "custom",
		Parameters:  []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}},
		Execute: func(context.Context, map[string]any) (string, error) {
			return "should not execute", nil
		},
	})
	r := NewRunner(Config{
		Tools:   reg,
		Session: session,
		ConfigureHooks: func(hooksReg *hooks.Registry) {
			hooksReg.Register(hooks.PointBeforeTool, "test:cancel-and-block", func(context.Context, hooks.Event) []hooks.Result {
				if err := session.CancelActiveTurn("user cancelled"); err != nil {
					t.Fatalf("cancel active turn: %v", err)
				}
				return []hooks.Result{hooks.BlockResult{Message: "blocked after cancellation"}}
			})
		},
	})
	turn := session.RecordInput("write file")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	err = r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{{ID: "write-1", Name: "write_file", ArgsJSON: `{"path":"a.txt"}`}})
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("executeNativeToolCalls error = %v, want nil or context canceled", err)
	}
	if contract := session.Snapshot().TurnContract; contract == nil || len(contract.Evidence) != 0 {
		t.Fatalf("stale before-hook block contract evidence = %#v", contract)
	}
}

func TestRunnerExecuteNativeToolCallsSkipsToolCallRegistrationAfterBeforeHookCancellation(t *testing.T) {
	reg := agenttools.NewRegistry()
	session := NewSession()
	renderer := &recordingRenderer{}
	reg.Register(agenttools.Tool{
		Name:        "custom_tool",
		Description: "custom",
		Execute: func(context.Context, map[string]any) (string, error) {
			return "should not execute", nil
		},
	})
	r := NewRunner(Config{
		Tools:    reg,
		Session:  session,
		Renderer: renderer,
		ConfigureHooks: func(hooksReg *hooks.Registry) {
			hooksReg.Register(hooks.PointBeforeTool, "test:cancel", func(context.Context, hooks.Event) []hooks.Result {
				if err := session.CancelActiveTurn("user cancelled"); err != nil {
					t.Fatalf("cancel active turn: %v", err)
				}
				return nil
			})
		},
	})
	turn := session.RecordInput("run tool")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	err = r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{{ID: "c1", Name: "custom_tool", ArgsJSON: `{}`}})
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("executeNativeToolCalls error = %v, want nil or context canceled", err)
	}

	for _, event := range renderer.events {
		if event == "tool_call" || event == "tool_result" {
			t.Fatalf("stale tool event rendered: events=%#v", renderer.events)
		}
	}
	for _, item := range session.Snapshot().Items {
		if item.Kind == protocol.ItemToolCall || item.Kind == protocol.ItemToolResult {
			t.Fatalf("stale tool item was appended: %#v", item)
		}
	}
}

func TestRunnerExecuteNativeToolCallsSkipsBeforeHookDispatchWhenTurnAlreadyCancelled(t *testing.T) {
	reg := agenttools.NewRegistry()
	session := NewSession()
	hookCalls := 0
	reg.Register(agenttools.Tool{
		Name:        "custom_tool",
		Description: "custom",
		Execute: func(context.Context, map[string]any) (string, error) {
			return "should not execute", nil
		},
	})
	r := NewRunner(Config{
		Tools:   reg,
		Session: session,
		ConfigureHooks: func(hooksReg *hooks.Registry) {
			hooksReg.Register(hooks.PointBeforeTool, "test:count", func(context.Context, hooks.Event) []hooks.Result {
				hookCalls++
				return nil
			})
		},
	})
	turn := session.RecordInput("run tool")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if err := session.CancelActiveTurn("user cancelled"); err != nil {
		t.Fatal(err)
	}

	err = r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{{ID: "c1", Name: "custom_tool", ArgsJSON: `{}`}})
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("executeNativeToolCalls error = %v, want nil or context canceled", err)
	}
	if hookCalls != 0 {
		t.Fatalf("before_tool hook calls = %d, want 0", hookCalls)
	}
	for _, item := range session.Snapshot().Items {
		if item.Kind == protocol.ItemToolCall || item.Kind == protocol.ItemToolResult {
			t.Fatalf("stale tool item was appended: %#v", item)
		}
	}
}

func TestRunnerExecutesSerialMetadataToolsSequentially(t *testing.T) {
	reg := agenttools.NewRegistry()
	var mu sync.Mutex
	events := []string{}
	record := func(event string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	}

	reg.Register(agenttools.Tool{
		Name:        "custom_serial",
		Description: "serial test tool",
		Concurrency: agenttools.ToolConcurrencySerial,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			record("serial:start")
			time.Sleep(50 * time.Millisecond)
			record("serial:end")
			return "serial", nil
		},
	})
	reg.Register(agenttools.Tool{
		Name:        "custom_parallel",
		Description: "parallel test tool",
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			record("parallel:start")
			record("parallel:end")
			return "parallel", nil
		},
	})
	session := NewSession()
	r := NewRunner(Config{Tools: reg, Session: session})
	turn := session.RecordInput("run serial metadata tools")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	if err := r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{
		{ID: "c1", Name: "custom_serial", ArgsJSON: `{}`},
		{ID: "c2", Name: "custom_parallel", ArgsJSON: `{}`},
	}); err != nil {
		t.Fatal(err)
	}

	want := []string{"serial:start", "serial:end", "parallel:start", "parallel:end"}
	if !slices.Equal(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestRunnerOutputHandleE2EStoresFullPayloadOutOfBand(t *testing.T) {
	largePayload := "large-output-sentinel:" + strings.Repeat("abcdef", 64)
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "read_file", ArgsJSON: `{"path":"large.txt"}`}}},
		{{Text: "handled summarized tool output"}},
	}}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "read_file",
		Description: "large output test tool",
		Parameters:  []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return largePayload, nil
		},
	})
	session := NewSession()
	session.SetTaskState(TaskState{Objective: "inspect large output", Operation: "inspect", RequiredVerification: "inspect with read tools before answering"})
	store := sessionstore.NewFileOutputStore(t.TempDir())
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session, OutputStore: store, OutputStoreThresholdBytes: 16})

	if err := r.Run(context.Background(), "run large output tool"); err != nil {
		t.Fatal(err)
	}

	result := lastToolResult(t, session)
	if result.Handle == "" || result.OriginalBytes != len(largePayload) || result.SHA256 == "" {
		t.Fatalf("tool result metadata = %#v", result)
	}
	if strings.Contains(result.Text, largePayload) || !strings.Contains(result.Text, result.Handle) {
		t.Fatalf("tool result summary = %q", result.Text)
	}
	stored, err := store.Read(context.Background(), sessionstore.OutputHandle{ID: result.Handle, Bytes: result.OriginalBytes, SHA256: result.SHA256}, 0, int64(len(largePayload)))
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != largePayload {
		t.Fatalf("stored output = %q, want full payload", stored)
	}
	if len(driver.allMsgs) < 2 {
		t.Fatalf("driver messages = %#v", driver.allMsgs)
	}
	foundModelSafeToolResult := false
	for _, msg := range driver.allMsgs[1] {
		if msg.Role != llm.RoleTool {
			continue
		}
		foundModelSafeToolResult = true
		if strings.Contains(msg.Content, largePayload) {
			t.Fatalf("model-visible tool result contained full payload: %q", msg.Content)
		}
		if !strings.Contains(msg.Content, result.Handle) {
			t.Fatalf("model-visible tool result missing handle summary: %q", msg.Content)
		}
	}
	if !foundModelSafeToolResult {
		t.Fatalf("second model step missing tool result: %#v", driver.allMsgs[1])
	}
	for _, msg := range session.Snapshot().History {
		if strings.Contains(msg.Content, largePayload) {
			t.Fatalf("session history contained full payload: %#v", msg)
		}
	}
}

func TestRunnerPostEditDiagnosticsE2EFeedsNextModelStep(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "edit-1", Name: "edit_file", ArgsJSON: `{"path":"a.txt","old_text":"old","new_text":"new"}`}}},
		{{Text: "fixed after diagnostics"}},
	}}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:             "edit_file",
		Description:      "edit file",
		AutoApprove:      true,
		MutatesWorkspace: true,
		LastDiff: func() string {
			return "diff --git a/a.txt b/a.txt"
		},
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "edited a.txt", nil
		},
	})
	session := NewSession()
	session.SetTaskState(TaskState{Objective: "edit a.txt", Operation: "implement", RequiredVerification: "edit and validate"})
	r := NewRunner(Config{
		Driver:  driver,
		Tools:   reg,
		Session: session,
		PostEditValidator: &PostEditValidator{
			Command:        []string{"/bin/sh", "-c", "printf post-edit-validator-failed >&2; exit 1"},
			Timeout:        time.Second,
			MaxOutputBytes: 1024,
		},
	})

	if err := r.Run(context.Background(), "edit a.txt"); err != nil {
		t.Fatal(err)
	}

	if len(driver.allMsgs) < 2 {
		t.Fatalf("driver messages = %#v", driver.allMsgs)
	}
	foundNextStepFeedback := false
	for _, msg := range driver.allMsgs[1] {
		if msg.Role == llm.RoleUser && strings.Contains(msg.Content, "Runtime diagnostic feedback") && strings.Contains(msg.Content, "post-edit-validator-failed") {
			foundNextStepFeedback = true
		}
	}
	if !foundNextStepFeedback {
		t.Fatalf("next model step missing diagnostic feedback: %#v", driver.allMsgs[1])
	}
	if !sessionHistoryContains(session, "Runtime diagnostic feedback", "post-edit-validator-failed") {
		t.Fatalf("session history missing diagnostics: %#v", session.Snapshot().History)
	}
}

func TestRunnerCheckpointE2ERestoresOnlyPathMutatedByTool(t *testing.T) {
	root := initReactTestGitRepo(t)
	t.Chdir(root)
	writeReactTestFile(t, filepath.Join(root, "a.txt"), "a original\n")
	writeReactTestFile(t, filepath.Join(root, "b.txt"), "b original\n")
	runReactGit(t, root, "add", "a.txt", "b.txt")
	runReactGit(t, root, "commit", "-m", "initial")
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "write-1", Name: "write_file", ArgsJSON: `{"path":"a.txt","content":"tool mutation\n"}`}}},
		{{Text: "wrote file"}},
	}}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "write_file",
		Description: "write file",
		AutoApprove: true,
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			path, _ := args["path"].(string)
			content, _ := args["content"].(string)
			writeReactTestFile(t, filepath.Join(root, path), content)
			return "ok", nil
		},
	})
	session := NewSession()
	session.SetTaskState(TaskState{Objective: "write a.txt", Operation: "implement", RequiredVerification: "write the requested file"})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "write a.txt"); err != nil {
		t.Fatal(err)
	}
	writeReactTestFile(t, filepath.Join(root, "b.txt"), "unrelated user mutation\n")

	items := session.Snapshot().Items
	checkpointCount := 0
	toolCallCount := 0
	firstCheckpoint := -1
	firstToolCall := -1
	checkpointID := ""
	for i, item := range items {
		if item.Kind == protocol.ItemCheckpoint {
			checkpointCount++
			if firstCheckpoint < 0 {
				firstCheckpoint = i
				checkpointID = item.Checkpoint.ID
			}
		}
		if item.Kind == protocol.ItemToolCall && firstToolCall < 0 {
			toolCallCount++
			firstToolCall = i
		} else if item.Kind == protocol.ItemToolCall {
			toolCallCount++
		}
	}
	if checkpointCount != 1 {
		t.Fatalf("checkpoint items = %d, want 1; items=%#v", checkpointCount, items)
	}
	if toolCallCount != 1 {
		t.Fatalf("tool call items = %d, want 1; items=%#v", toolCallCount, items)
	}
	if firstCheckpoint < 0 || firstToolCall < 0 || firstCheckpoint > firstToolCall {
		t.Fatalf("checkpoint index = %d, first tool call index = %d; want checkpoint before tool call", firstCheckpoint, firstToolCall)
	}
	if err := r.checkpointManager.Restore(context.Background(), checkpointID); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if got := readReactTestFile(t, filepath.Join(root, "a.txt")); got != "a original\n" {
		t.Fatalf("a.txt = %q, want restored checkpoint content", got)
	}
	if got := readReactTestFile(t, filepath.Join(root, "b.txt")); got != "unrelated user mutation\n" {
		t.Fatalf("b.txt = %q, want unrelated mutation preserved", got)
	}
}

func TestExecuteNativeToolCallsStoresLargeOutputOutOfBand(t *testing.T) {
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "large_output",
		Description: "large output test tool",
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "0123456789", nil
		},
	})
	session := NewSession()
	store := sessionstore.NewFileOutputStore(t.TempDir())
	r := NewRunner(Config{Tools: reg, Session: session, OutputStore: store, OutputStoreThresholdBytes: 5})
	turn := session.RecordInput("run large output tool")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	if err := r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{{ID: "c1", Name: "large_output", ArgsJSON: `{}`}}); err != nil {
		t.Fatal(err)
	}

	snap := session.Snapshot()
	var result *protocol.ToolResultItem
	for i := range snap.Items {
		if snap.Items[i].ToolResult != nil {
			result = snap.Items[i].ToolResult
		}
	}
	if result == nil {
		t.Fatal("missing tool result item")
	}
	if result.Handle == "" || result.OriginalBytes != 10 || result.SHA256 == "" {
		t.Fatalf("stored metadata = %#v", result)
	}
	if result.Text == "0123456789" || !strings.Contains(result.Text, result.Handle) || !strings.Contains(result.Text, "10 bytes") {
		t.Fatalf("summary text = %q", result.Text)
	}
	got, err := store.Read(context.Background(), sessionstore.OutputHandle{ID: result.Handle, Bytes: result.OriginalBytes, SHA256: result.SHA256}, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "2345" {
		t.Fatalf("stored output = %q, want 2345", got)
	}
}

func TestExecuteNativeToolCallsHidesOutputStoreHandleFromRenderer(t *testing.T) {
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "large_output",
		Description: "large output test tool",
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return strings.Repeat("x", 32), nil
		},
	})
	session := NewSession()
	store := sessionstore.NewFileOutputStore(t.TempDir())
	renderer := &recordingRenderer{}
	r := NewRunner(Config{Tools: reg, Session: session, Renderer: renderer, OutputStore: store, OutputStoreThresholdBytes: 5})
	turn := session.RecordInput("run large output tool")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	if err := r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{{ID: "c1", Name: "large_output", ArgsJSON: `{}`}}); err != nil {
		t.Fatal(err)
	}

	result := lastToolResult(t, session)
	if result.Handle == "" || !strings.Contains(result.Text, result.Handle) || !strings.Contains(result.Text, "SHA256") {
		t.Fatalf("session result = %#v, want handle-bearing model-visible result", result)
	}
	if len(renderer.toolTexts) != 1 {
		t.Fatalf("renderer tool texts = %#v, want one", renderer.toolTexts)
	}
	display := renderer.toolTexts[0]
	if strings.Contains(display, result.Handle) || strings.Contains(display, "SHA256") || strings.Contains(display, "Tool output stored out-of-band") {
		t.Fatalf("renderer display leaked output-store internals: %q", display)
	}
	if !strings.Contains(display, "output stored") || !strings.Contains(display, "32 bytes") {
		t.Fatalf("renderer display = %q, want concise stored-output summary", display)
	}
}

func TestExecuteNativeToolCallsKeepsReadOutputInlineWhenLarge(t *testing.T) {
	store := sessionstore.NewFileOutputStore(t.TempDir())
	largePayload := "read-output-sentinel:" + strings.Repeat("abcdef", 64)
	handle, err := store.Put(context.Background(), "session", []byte(largePayload))
	if err != nil {
		t.Fatalf("store output: %v", err)
	}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.NewReadOutput(store))
	session := NewSession()
	r := NewRunner(Config{Tools: reg, Session: session, OutputStore: store, OutputStoreThresholdBytes: 5})
	turn := session.RecordInput("read stored output")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	args := fmt.Sprintf(`{"handle":%q,"limit":%d,"offset":0}`, handle.ID, len(largePayload))
	if err := r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{{ID: "c1", Name: "read_output", ArgsJSON: args}}); err != nil {
		t.Fatal(err)
	}

	result := lastToolResult(t, session)
	if result.Handle != "" || result.OriginalBytes != 0 || result.SHA256 != "" {
		t.Fatalf("read_output result was re-stored out of band: %#v", result)
	}
	if !strings.Contains(result.Text, "read-output-sentinel:") || !strings.Contains(result.Text, `"content":`) {
		t.Fatalf("read_output result = %q, want inline JSON content", result.Text)
	}
}

func TestExecuteNativeToolCallsKeepsLargeOutputInlineWhenOutputStoreNil(t *testing.T) {
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "large_output",
		Description: "large output test tool",
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "0123456789", nil
		},
	})
	session := NewSession()
	r := NewRunner(Config{Tools: reg, Session: session, OutputStoreThresholdBytes: 5})
	turn := session.RecordInput("run large output tool")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	if err := r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{{ID: "c1", Name: "large_output", ArgsJSON: `{}`}}); err != nil {
		t.Fatal(err)
	}

	result := lastToolResult(t, session)
	if result.Text != "0123456789" || result.Handle != "" || result.OriginalBytes != 0 || result.SHA256 != "" {
		t.Fatalf("tool result = %#v, want inline without handle metadata", result)
	}
}

func TestExecuteNativeToolCallsKeepsSmallOutputInlineWhenOutputStoreConfigured(t *testing.T) {
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "small_output",
		Description: "small output test tool",
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "0123", nil
		},
	})
	session := NewSession()
	r := NewRunner(Config{Tools: reg, Session: session, OutputStore: sessionstore.NewFileOutputStore(t.TempDir()), OutputStoreThresholdBytes: 5})
	turn := session.RecordInput("run small output tool")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	if err := r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{{ID: "c1", Name: "small_output", ArgsJSON: `{}`}}); err != nil {
		t.Fatal(err)
	}

	result := lastToolResult(t, session)
	if result.Text != "0123" || result.Handle != "" || result.OriginalBytes != 0 || result.SHA256 != "" {
		t.Fatalf("tool result = %#v, want inline without handle metadata", result)
	}
}

func lastToolResult(t *testing.T, session *Session) *protocol.ToolResultItem {
	t.Helper()
	var result *protocol.ToolResultItem
	snap := session.Snapshot()
	for i := range snap.Items {
		if snap.Items[i].ToolResult != nil {
			result = snap.Items[i].ToolResult
		}
	}
	if result == nil {
		t.Fatal("missing tool result item")
	}
	return result
}

func messagesText(messages []llm.Message) string {
	var b strings.Builder
	for _, msg := range messages {
		b.WriteString(msg.Content)
		b.WriteByte('\n')
	}
	return b.String()
}

func assertContainsAll(t *testing.T, text string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if !strings.Contains(text, part) {
			t.Fatalf("text %q missing %q", text, part)
		}
	}
}

func TestRepeatOverlayCatchesVariedRangeRereads(t *testing.T) {
	s := repeatToolCallState{
		lastToolName: "read_file",
		lastTarget:   "game.js#500-600",
		recent: []string{
			"read_file:game.js#1-100",
			"read_file:game.js#100-200",
			"read_file:game.js",
			"read_file:other.js#1-50",
			"read_file:game.js#200-300",
			"read_file:game.js#300-400",
			"read_file:game.js#400-500",
			"read_file:game.js#500-600",
		},
		streak: 1,
	}
	got := s.overlayContent(repeatToolCallThreshold)
	if !strings.Contains(got, "Loop detection") || !strings.Contains(got, `"game.js"`) {
		t.Fatalf("overlay = %q, want same-file reread nudge", got)
	}

	// Linear paging under the threshold stays silent.
	s.recent = s.recent[:4]
	if got := s.overlayContent(repeatToolCallThreshold); got != "" {
		t.Fatalf("overlay = %q, want empty for light paging", got)
	}
}
