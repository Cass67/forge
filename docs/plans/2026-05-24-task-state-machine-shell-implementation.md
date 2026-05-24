# Task State Machine Shell Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a runtime-owned task state machine shell that owns task phase, tool budgets, continuation, recovery, and completion while preserving the existing ReAct loop internals.

**Architecture:** Introduce a small pure `TaskStateMachine` in `internal/react` and integrate it incrementally into `Runner`. Existing `TurnContract`, `SideEffectIntent`, `TaskState`, delegation state, and budget counters become compatibility views or inputs to the coordinator rather than independent control planes.

**Tech Stack:** Go, existing `internal/react` session/runner types, native tool registry, current ReAct loop tests, TDD.

---

### Task 1: Add Pure Task State Machine Types

**Files:**
- Create: `internal/react/task_state_machine.go`
- Test: `internal/react/task_state_machine_test.go`

**Step 1: Write the failing test**

Add tests for the minimal lifecycle:

```go
func TestTaskStateMachineStartsEditTask(t *testing.T) {
	m := NewTaskStateMachine()
	decision := m.ResolveInput(TaskInput{Turn: 1, Text: "fix the bug and run tests"})

	if decision.Action != TaskActionStart || decision.Phase != TaskPhaseInspecting {
		t.Fatalf("decision = %#v, want start inspecting", decision)
	}
	if m.ActiveTask() == nil || m.ActiveTask().Goal == "" {
		t.Fatalf("active task = %#v, want task", m.ActiveTask())
	}
}

func TestTaskStateMachineContinueResumesActiveEditTask(t *testing.T) {
	m := NewTaskStateMachine()
	m.ResolveInput(TaskInput{Turn: 1, Text: "fix the bug"})
	decision := m.ResolveInput(TaskInput{Turn: 2, Text: "continue"})

	if decision.Action != TaskActionContinue || decision.Phase != TaskPhaseInspecting {
		t.Fatalf("decision = %#v, want continue inspecting", decision)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -count=1 ./internal/react -run 'TestTaskStateMachine(Start|Continue)'`

Expected: FAIL because `TaskStateMachine` does not exist.

**Step 3: Implement minimal types and transitions**

Create:

```go
type TaskPhase string

const (
	TaskPhaseNoTask      TaskPhase = "no_task"
	TaskPhaseInspecting  TaskPhase = "inspecting"
	TaskPhaseEditing     TaskPhase = "editing"
	TaskPhaseVerifying   TaskPhase = "verifying"
	TaskPhaseSummarizing TaskPhase = "summarizing"
	TaskPhaseDone        TaskPhase = "done"
	TaskPhaseBlocked     TaskPhase = "blocked"
	TaskPhaseFailed      TaskPhase = "failed"
)

type TaskAction string

const (
	TaskActionNone     TaskAction = "none"
	TaskActionStart    TaskAction = "start"
	TaskActionContinue TaskAction = "continue"
	TaskActionClear    TaskAction = "clear"
)

type RuntimeTask struct {
	ID     string
	Turn   int
	Goal   string
	Phase  TaskPhase
	Intent TurnIntent
}

type TaskInput struct {
	Turn int
	Text string
}

type TaskDecision struct {
	Action TaskAction
	Phase  TaskPhase
	TaskID string
}

type TaskStateMachine struct { active *RuntimeTask }
```

Use current intent helpers for first classification, but only at task creation. Bare continuation resumes active non-terminal tasks.

**Step 4: Run test to verify it passes**

Run: `go test -count=1 ./internal/react -run 'TestTaskStateMachine(Start|Continue)'`

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/react/task_state_machine.go internal/react/task_state_machine_test.go
git commit -m "feat: add runtime task state machine shell"
```

### Task 2: Mirror Task State Into Current Session Views

**Files:**
- Modify: `internal/react/session.go`
- Modify: `internal/react/task_state_machine.go`
- Test: `internal/react/task_state_machine_test.go`

**Step 1: Write the failing test**

Add a test that a runtime edit task can produce the existing compatibility views:

```go
func TestRuntimeTaskBuildsCompatibilityViews(t *testing.T) {
	m := NewTaskStateMachine()
	m.ResolveInput(TaskInput{Turn: 1, Text: "fix the bug and run tests"})
	views := m.Views()

	if views.TurnContract == nil || views.TurnContract.Intent != TurnIntentEditCode {
		t.Fatalf("TurnContract = %#v, want edit", views.TurnContract)
	}
	if views.TaskState == nil || views.TaskState.Operation != "implement" {
		t.Fatalf("TaskState = %#v, want implement", views.TaskState)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -count=1 ./internal/react -run TestRuntimeTaskBuildsCompatibilityViews`

Expected: FAIL because views do not exist.

**Step 3: Implement `TaskViews`**

Add:

```go
type TaskViews struct {
	TurnContract *TurnContract
	TaskState    *TaskState
}
```

Implement `Views()` to build:

- `TurnContract` from active task intent and phase.
- `TaskState.Operation` from phase/intent: `inspect`, `implement`, `validate`, `review`, or `chat`.

Do not persist a new protocol item yet.

**Step 4: Run test to verify it passes**

Run: `go test -count=1 ./internal/react -run TestRuntimeTaskBuildsCompatibilityViews`

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/react/task_state_machine.go internal/react/task_state_machine_test.go
git commit -m "feat: derive runtime task compatibility views"
```

### Task 3: Route `Runner.Run` Task Creation Through Coordinator

**Files:**
- Modify: `internal/react/loop.go:297-313`
- Modify: `internal/react/session.go`
- Test: `internal/react/loop_test.go`

**Step 1: Write the failing test**

Add a runner-level test proving `continue` uses the active runtime task and does not replace it with answer-only:

```go
func TestRunnerTaskMachinePreservesContinueAcrossTurns(t *testing.T) {
	session := NewSession()
	driver := &nativeSequenceDriver{steps: repeatedTextSteps("still working", maxCompletionRetriesPerTurn+1)}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{Name: "write_file", Description: "write file"})
	r := NewRunner(Config{Session: session, Driver: driver, Tools: reg})

	_ = r.taskMachine.ResolveInput(TaskInput{Turn: 1, Text: "fix the bug"})
	err := r.Run(context.Background(), "continue")

	if err == nil {
		t.Fatal("expected unfinished task to require tool progress")
	}
	if session.Snapshot().TurnContract == nil || session.Snapshot().TurnContract.Intent != TurnIntentEditCode {
		t.Fatalf("TurnContract = %#v, want edit", session.Snapshot().TurnContract)
	}
}
```

Adjust field names to match Task 1 implementation.

**Step 2: Run test to verify it fails**

Run: `go test -count=1 ./internal/react -run TestRunnerTaskMachinePreservesContinueAcrossTurns`

Expected: FAIL because runner still derives directly from input.

**Step 3: Add coordinator field to `Runner`**

Add `taskMachine *TaskStateMachine` to `Runner`. Initialize in `NewRunner`.

**Step 4: Replace input-time direct contract setup**

In `Runner.Run`, call `taskMachine.ResolveInput` after `RecordInputWithParts`. Apply `Views()` to `Session.SetTurnContract` and `Session.SetTaskState` unless the decision is plain `NoTask`.

Keep old `deriveSideEffectIntentFromText` for now, but only after task view setup.

**Step 5: Run test to verify it passes**

Run: `go test -count=1 ./internal/react -run TestRunnerTaskMachinePreservesContinueAcrossTurns`

Expected: PASS.

**Step 6: Run existing continuation regression**

Run: `go test -count=1 ./internal/react -run TestRunnerContinuePreservesUnfinishedEditContract`

Expected: PASS.

**Step 7: Commit**

```bash
git add internal/react/loop.go internal/react/session.go internal/react/task_state_machine.go internal/react/task_state_machine_test.go internal/react/loop_test.go
git commit -m "feat: route runner input through task state machine"
```

### Task 4: Move Tool Allowlist Decisions Behind Task Phase

**Files:**
- Modify: `internal/react/loop.go` around `allowedToolNamesForSnapshot` / `selectToolDefs`
- Modify: `internal/react/task_state_machine.go`
- Test: `internal/react/loop_test.go`

**Step 1: Write failing tests**

Add tests:

```go
func TestTaskPhaseControlsToolExposureForContinue(t *testing.T) {
	session := NewSession()
	m := NewTaskStateMachine()
	m.ResolveInput(TaskInput{Turn: 1, Text: "fix the bug"})
	applyTaskViews(session, m.Views())

	tools := allowedToolNamesForSnapshot(session.Snapshot())
	if !containsString(tools, "write_file") || !containsString(tools, "edit_file") {
		t.Fatalf("tools = %#v, want edit tools from task phase", tools)
	}
}

func TestPlainChatNoTaskExposesNoTools(t *testing.T) {
	tools := allowedToolNamesForSnapshot(SessionSnapshot{LastInput: "hello"})
	if len(tools) != 0 {
		t.Fatalf("tools = %#v, want none", tools)
	}
}
```

**Step 2: Run tests to verify failure**

Run: `go test -count=1 ./internal/react -run 'TestTaskPhaseControlsToolExposureForContinue|TestPlainChatNoTaskExposesNoTools'`

Expected: at least the phase exposure test fails or relies on old heuristics.

**Step 3: Add task phase tool groups**

Implement a helper:

```go
func toolGroupsForTaskPhase(task *RuntimeTask) []string
```

Map:

- `Inspecting`: read/search/list/git-read/delegate read-only.
- `Editing`: read/search/write/edit/apply_patch/run_command scoped mutation.
- `Verifying`: run_command/git status/diff/read.
- `Summarizing`: no tools except read_output/artifact_read when handles exist.

**Step 4: Route allowlist through phase first**

In `allowedToolNamesForSnapshot`, if snapshot has an active task view/contract from the task machine, use phase tool groups first. Keep legacy heuristics only when there is no active task.

**Step 5: Run tests**

Run: `go test -count=1 ./internal/react -run 'TestTaskPhaseControlsToolExposureForContinue|TestPlainChatNoTaskExposesNoTools|TestIntentContractToolRoutingMatrix'`

Expected: PASS. If old matrix fails, update only cases where active task state is now authoritative.

**Step 6: Commit**

```bash
git add internal/react/loop.go internal/react/loop_test.go internal/react/task_state_machine.go
git commit -m "feat: make task phase drive tool exposure"
```

### Task 5: Move Exploration Budgets Into Task Phase State

**Files:**
- Modify: `internal/react/task_state_machine.go`
- Modify: `internal/react/loop.go`
- Test: `internal/react/task_state_machine_test.go`
- Test: `internal/react/loop_test.go`

**Step 1: Write pure budget tests**

```go
func TestTaskStateMachineBlocksRepeatedSameFileReads(t *testing.T) {
	m := NewTaskStateMachine()
	m.ResolveInput(TaskInput{Turn: 1, Text: "inspect src/main.rs"})
	var decision ToolPolicyDecision
	for i := 0; i < defaultRepeatedReadBudget+1; i++ {
		decision = m.ApplyToolCall(ToolEvent{Name: "read_file", Target: "src/main.rs"})
	}
	if !decision.Blocked {
		t.Fatalf("decision = %#v, want blocked", decision)
	}
}
```

**Step 2: Run pure test to verify it fails**

Run: `go test -count=1 ./internal/react -run TestTaskStateMachineBlocksRepeatedSameFileReads`

Expected: FAIL.

**Step 3: Add budget state**

Add to `RuntimeTask`:

```go
Budgets TaskBudgets
```

With counters for:

- repeated same-target reads;
- `think` calls;
- `tool_help` calls;
- total tools per phase.

**Step 4: Integrate with `executeNativeToolCalls`**

Before execution, ask the task machine whether the tool call is allowed under budget. Convert budget blocks to policy-blocked tool results. Remove or delegate existing scattered repeated-read block to the task machine.

**Step 5: Run loop regression**

Run: `go test -count=1 ./internal/react -run TestRunnerBlocksRepeatedSameFileReadsAfterThreshold`

Expected: PASS.

**Step 6: Commit**

```bash
git add internal/react/task_state_machine.go internal/react/task_state_machine_test.go internal/react/loop.go internal/react/loop_test.go
git commit -m "feat: enforce task phase tool budgets"
```

### Task 6: Preserve Task State Across Provider and Tool Failures

**Files:**
- Modify: `internal/react/task_state_machine.go`
- Modify: `internal/react/loop.go`
- Test: `internal/react/loop_test.go`

**Step 1: Write failing tests**

Add tests for provider failure and child failure:

```go
func TestProviderFailurePreservesActiveTaskPhase(t *testing.T) {
	session := NewSession()
	driver := &failingNativeDriver{err: errors.New("provider unavailable")}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{Name: "write_file", Description: "write file"})
	r := NewRunner(Config{Session: session, Driver: driver, Tools: reg})

	err := r.Run(context.Background(), "fix the bug")
	if err == nil {
		t.Fatal("expected provider error")
	}
	if r.taskMachine.ActiveTask() == nil || r.taskMachine.ActiveTask().Phase != TaskPhaseInspecting {
		t.Fatalf("active task = %#v, want preserved inspecting task", r.taskMachine.ActiveTask())
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -count=1 ./internal/react -run TestProviderFailurePreservesActiveTaskPhase`

Expected: FAIL if failures are not recorded/preserved through coordinator.

**Step 3: Add failure handling**

Implement:

```go
func (m *TaskStateMachine) ApplyFailure(TaskFailure)
```

Provider errors increment failure counters but do not clear active task. Repeated failure budget transitions to `Blocked` or `Failed`.

**Step 4: Call failure handler from runner**

At provider completion error paths and child-agent failure fallback paths, call `ApplyFailure` before completing the turn.

**Step 5: Run tests**

Run: `go test -count=1 ./internal/react -run 'TestProviderFailurePreservesActiveTaskPhase|TestRunnerPreservesFailedChildCauseWhenParentResponseEmpty'`

Expected: PASS.

**Step 6: Commit**

```bash
git add internal/react/task_state_machine.go internal/react/loop.go internal/react/loop_test.go
git commit -m "feat: preserve task state across runtime failures"
```

### Task 7: Move Final Completion Gate To Task Machine

**Files:**
- Modify: `internal/react/task_state_machine.go`
- Modify: `internal/react/loop.go`
- Test: `internal/react/task_state_machine_test.go`
- Test: `internal/react/loop_test.go`

**Step 1: Write pure completion tests**

```go
func TestTaskCannotCompleteEditWithoutWriteEvidence(t *testing.T) {
	m := NewTaskStateMachine()
	m.ResolveInput(TaskInput{Turn: 1, Text: "fix bug"})
	decision := m.CanComplete("Done")
	if decision.OK || !strings.Contains(decision.Feedback, "edit evidence") {
		t.Fatalf("decision = %#v, want missing edit evidence", decision)
	}
}
```

**Step 2: Run pure test to verify it fails**

Run: `go test -count=1 ./internal/react -run TestTaskCannotCompleteEditWithoutWriteEvidence`

Expected: FAIL.

**Step 3: Implement completion decision**

Add:

```go
func (m *TaskStateMachine) CanComplete(finalText string) CompletionDecision
```

Check phase and evidence. Return feedback for missing read/write/verification evidence.

**Step 4: Integrate in `validateFinalCompletion`**

Call task machine first. Existing `TurnContract` final validation remains as compatibility fallback while migrating.

**Step 5: Run final validation regressions**

Run: `go test -count=1 ./internal/react -run 'TestRunnerRequiredActionAndVerificationBlockSuccessWithoutEvidence|TestRunnerSuccessfulFinalMarksTurnContractSatisfied'`

Expected: PASS.

**Step 6: Commit**

```bash
git add internal/react/task_state_machine.go internal/react/task_state_machine_test.go internal/react/loop.go internal/react/loop_test.go
git commit -m "feat: gate completion through task state machine"
```

### Task 8: Full Verification And Cleanup

**Files:**
- Modify only if needed: `internal/react/*`
- Docs: `docs/plans/2026-05-24-task-state-machine-shell-design.md`

**Step 1: Run focused suite**

Run: `go test -count=1 ./internal/react`

Expected: PASS.

**Step 2: Run driver/runtime related suites**

Run: `go test -count=1 ./internal/react ./internal/runtime ./internal/llm/drivers`

Expected: PASS.

**Step 3: Run build**

Run: `just build`

Expected: PASS.

**Step 4: Run full suite if plugin host is healthy**

Run: `go test -count=1 ./...`

Expected: PASS, or document pre-existing `internal/plugins` OpenCode host EOF if it still reproduces independently.

**Step 5: Check diff hygiene**

Run: `git diff --check`

Expected: no output.

**Step 6: Commit final cleanup**

```bash
git add internal/react docs/plans/2026-05-24-task-state-machine-shell-design.md
git commit -m "test: verify runtime task state machine shell"
```

## Execution Notes

- Use TDD for every behavior change.
- Do not delete old `TurnContract` or `SideEffectIntent` paths in the first implementation pass.
- Do not add prompt-only fixes.
- Do not widen tool exposure to make tests pass.
- Prefer pure state-machine tests before runner integration tests.
- Keep each commit small enough to revert independently.
