# Forge Harness Redesign Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Forge's dispatch-centered chat harness with one coherent Codex-like assistant that stays local by default, uses skills first, invokes hidden bounded workers only when policy says they help, and ships with a transcript-first UI plus debug-only advanced trace under `-d`.

**Architecture:** Add a new `internal/harness` runtime kernel that owns request-family classification, session carry-forward, policy decisions, local execution, hidden worker orchestration, and trace emission in code. Reuse `internal/agent.Agent` as the underlying tool-using execution engine for local assistant turns and worker turns, but move orchestration authority out of prompt prose and into deterministic Go code. Keep the old dispatch path only as a short-lived compatibility path during migration, then delete it once the kernel covers the common path and the new worker contracts are stable.

**Tech Stack:** Go, Bubble Tea, Lip Gloss, existing `internal/llm` streaming/event interfaces, existing `internal/skills` loader/state, existing `internal/agent/tools` registry, Go test/build tooling.

---

## File Map

### New package: `internal/harness`

- Create: `internal/harness/types.go`
  Purpose: request families, runtime states, step kinds, worker kinds, observation/result types.
- Create: `internal/harness/session.go`
  Purpose: follow-up detection, turn metadata, carry-forward evidence/worker context.
- Create: `internal/harness/classifier.go`
  Purpose: classify a user turn into `answer`, `inspect`, `implement`, `debug`, `verify`, `research`, `transform`, or `mixed`.
- Create: `internal/harness/planner.go`
  Purpose: pick the next smallest useful action from the request family plus current observations.
- Create: `internal/harness/policy.go`
  Purpose: deterministic transition rules, stop conditions, and worker-admission checks.
- Create: `internal/harness/local.go`
  Purpose: local-first execution wrapper around `agent.Agent`.
- Create: `internal/harness/workers.go`
  Purpose: hidden worker launch policy, worker envelopes, tool scopes, and result handling.
- Create: `internal/harness/contracts.go`
  Purpose: schema validation for `reader`, `editor`, `verifier`, and `researcher` results.
- Create: `internal/harness/trace.go`
  Purpose: structured trace records for `-d`, plus human-readable debug summaries.
- Create: `internal/harness/runner.go`
  Purpose: main kernel loop for one user turn.
- Create: `internal/harness/*_test.go`
  Purpose: unit coverage for classifier, session, policy, contracts, trace, and runner behavior.
- Create: `internal/harness/testdata/debuglogs/`
  Purpose: sanitized real-log regression fixtures.
- Create: `internal/harness/testdata/paraphrases/`
  Purpose: semantically equivalent prompt suites that must route the same way.

### Agent package changes

- Modify: `internal/agent/agent.go`
  Purpose: keep the local assistant loop lean and non-dispatchy by default; expose hooks the harness can use without inheriting old control-plane heuristics.
- Modify: `internal/agent/subagent.go`
  Purpose: turn current role spawning into a hidden worker runner with bounded contracts and validation.
- Modify: `internal/agent/system.go`
  Purpose: keep one primary `forge` system prompt and add worker-specific prompt suffix builders.
- Modify: `internal/agent/roles.go`
  Purpose: short-lived compatibility only during migration; delete or quarantine in Phase 4.
- Modify: `internal/agent/tools/delegate.go`
  Purpose: short-lived compatibility only during migration; remove from default path in Phase 4.
- Modify: `internal/agent/integration_test.go`
  Purpose: keep end-to-end local agent coverage after removing dispatch-default assumptions.

### Runtime / config / event plumbing

- Modify: `internal/runtime/chat.go`
  Purpose: instantiate the new kernel, pass debug state, and stop centering chat mode around `chat.agents.enabled`.
- Modify: `internal/runtime/chat_test.go`
  Purpose: kernel-selection and local-first runtime coverage.
- Modify: `internal/runtime/chat_debug.go`
  Purpose: emit harness trace records into the debug log and create a fresh `/tmp` log per run unless overridden.
- Modify: `internal/runtime/chat_debug_test.go`
  Purpose: trace/debug-log coverage and fresh-file semantics.
- Modify: `internal/config/config.go`
  Purpose: add worker-model config, retire or quarantine old visible-agent config, and keep bare `forge` chat semantics.
- Modify: `internal/config/config_test.go`
  Purpose: config migration and save/load coverage.
- Modify: `internal/llm/types.go`
  Purpose: add structured trace/debug event support without overloading free-form text.

### TUI replacement

- Create: `internal/tui/transcript.go`
  Purpose: transcript-first rendering helpers.
- Create: `internal/tui/activity.go`
  Purpose: compact inline activity rows.
- Create: `internal/tui/traceview.go`
  Purpose: debug-only advanced trace rendering shown only when `-d` is active.
- Create: `internal/tui/codeblock.go`
  Purpose: styled code, diff, and verification-output boxes.
- Create: `internal/tui/*_test.go` for the new rendering helpers.
- Modify: `internal/tui/chatmodel.go`
  Purpose: state management for transcript, compact activity, scroll behavior, and optional debug trace.
- Modify: `internal/tui/chatshared.go`
  Purpose: pass debug-enabled/kernel capabilities into the UI.
- Modify: `internal/tui/chatmsg.go`
  Purpose: message styling for one coherent assistant transcript.
- Modify: `internal/tui/chattheme.go`
  Purpose: replace the current panel-heavy default theme with one coherent Codex-like visual direction.
- Modify: `internal/tui/view_test.go`
- Modify: `internal/tui/chatmodel_test.go`
- Modify: `internal/tui/chatmsg_test.go`
  Purpose: regression coverage for rendering, scrolling, input visibility, and debug-only advanced trace.

### CLI / docs / regression assets

- Modify: `cmd/forge/main.go`
  Purpose: keep bare `forge` as chat, ensure `-d` is the only advanced-trace entry point, and remove visible multi-agent wording from help text.
- Modify: `cmd/forge/main_test.go`
  Purpose: CLI/help coverage for default chat behavior and `-d`.
- Modify: `README.md`
- Modify: `ARCHITECTURE.md`
  Purpose: document the new kernel after cutover.
- Modify: `docs/superpowers/specs/2026-03-25-forge-harness-redesign-design.md`
  Purpose: force-add the approved design doc to git.
- Modify: `docs/superpowers/plans/2026-03-25-forge-harness-redesign-implementation.md`
  Purpose: this plan file.

## Chunk 1: Kernel Skeleton And Runtime Hook

### Task 1: Lock the new request-family and trace model with failing tests

**Files:**
- Create: `internal/harness/types.go`
- Create: `internal/harness/classifier.go`
- Create: `internal/harness/session.go`
- Create: `internal/harness/trace.go`
- Create: `internal/harness/classifier_test.go`
- Create: `internal/harness/session_test.go`
- Create: `internal/harness/trace_test.go`

- [ ] **Step 1: Write classifier and session tests before any implementation**

Cover:
- directory-inspection paraphrases such as `describe this directory`, `go over this directory`, `walk through this directory`, `explain this directory`, `review this directory`, `summarize this directory`, `give an overview of this directory`, `take me through this directory`, `show me what’s in this directory`, and `help me understand this directory` all classify as `inspect`
- those same prompts do not request recommendations, repo review, or automatic synthesis
- follow-up turns like `what do you think?` only classify as `interpretive follow-up` when the immediately prior turn gathered compatible evidence
- trace records capture `state`, `family`, `step`, `worker`, `reason`, and `debug_summary`

Example test skeleton:

```go
func TestClassifyDirectoryParaphrasesStayInspect(t *testing.T) {
	cases := []string{
		"describe this directory",
		"go over this directory",
		"walk through this directory",
	}
	for _, input := range cases {
		got := Classify(UserTurn{Text: input}, SessionState{})
		if got.Family != FamilyInspect || got.WantsEvaluation {
			t.Fatalf("%q => %#v", input, got)
		}
	}
}
```

- [ ] **Step 2: Run the focused harness tests to verify they fail**

Run: `go test ./internal/harness -run 'Test(Classify|Session|Trace)'`
Expected: FAIL because the package and types do not exist yet.

- [ ] **Step 3: Implement the minimal type system and classifier**

Implement:
- `RequestFamily`, `RuntimeState`, `StepKind`, `WorkerKind`
- `UserTurn`, `Classification`, `Observation`, and `TraceRecord`
- a classifier that is semantic enough to group paraphrases without keyword-patching to a single English phrase
- session helpers that only permit referential follow-up reuse from the current turn or immediately prior turn

Example structural target:

```go
type Classification struct {
	Family          RequestFamily
	WantsEvaluation bool
	WantsAction     bool
	CanStayLocal    bool
	TopicKey        string
}
```

- [ ] **Step 4: Run the focused harness tests to verify they pass**

Run: `go test ./internal/harness -run 'Test(Classify|Session|Trace)'`
Expected: PASS

### Task 2: Add the deterministic kernel loop and wire it into runtime behind a temporary selector

**Files:**
- Create: `internal/harness/planner.go`
- Create: `internal/harness/policy.go`
- Create: `internal/harness/local.go`
- Create: `internal/harness/runner.go`
- Create: `internal/harness/planner_test.go`
- Create: `internal/harness/policy_test.go`
- Create: `internal/harness/runner_test.go`
- Modify: `internal/runtime/chat.go`
- Modify: `internal/runtime/chat_test.go`

- [ ] **Step 1: Write failing tests for the new local-first kernel path**

Cover:
- `forge` chat defaults to the new kernel when the temporary selector is set to `kernel`
- simple `answer` and `inspect` turns take the local path and do not register a visible delegation step
- the kernel records trace decisions before acting

- [ ] **Step 2: Run the targeted runtime and harness tests to verify they fail**

Run: `go test ./internal/harness ./internal/runtime -run 'Test(Kernel|Planner|Policy|RunChat)'`
Expected: FAIL on missing kernel wiring.

- [ ] **Step 3: Implement the minimal kernel loop**

Implement:
- `Intake -> Classify -> PlanStep -> Act -> Observe -> Decide -> Respond -> Complete`
- a temporary runtime selector using an env var such as `FORGE_CHAT_RUNTIME=legacy|kernel` so the new kernel can land before cutover
- local execution only for this chunk; worker spawning remains unimplemented and must fail closed

- [ ] **Step 4: Run the targeted runtime and harness tests to verify the kernel path passes**

Run: `go test ./internal/harness ./internal/runtime -run 'Test(Kernel|Planner|Policy|RunChat)'`
Expected: PASS

- [ ] **Step 5: Commit the kernel skeleton**

```bash
git add internal/harness/*.go internal/runtime/chat.go internal/runtime/chat_test.go
git commit -m "feat: add forge harness kernel skeleton"
```

## Chunk 2: Move The Common Path To The New Kernel

### Task 3: Lock down local-first common-path behavior with failing tests

**Files:**
- Modify: `internal/harness/runner_test.go`
- Modify: `internal/runtime/chat_test.go`
- Modify: `internal/agent/integration_test.go`

- [ ] **Step 1: Add failing tests for the majority path**

Cover:
- `answer`, `inspect`, `implement`, and `verify` requests stay local by default
- the kernel does not invent worker handoffs for a simple repo-inspection ask
- implementation turns still end with verification instead of stopping after edits
- the visible transcript gets one coherent `forge` answer, not a worker result relay

- [ ] **Step 2: Run the focused tests to verify they fail**

Run:
- `go test ./internal/harness -run 'TestRunner(Local|Verify|Respond)'`
- `go test ./internal/runtime -run 'TestRunChat'`
- `go test ./internal/agent -run 'TestAgentEndToEnd'`
Expected: FAIL on missing common-path behavior.

### Task 4: Implement the local assistant executor and flip the default chat path

**Files:**
- Modify: `internal/harness/local.go`
- Modify: `internal/harness/runner.go`
- Modify: `internal/agent/agent.go`
- Modify: `internal/agent/system.go`
- Modify: `internal/runtime/chat.go`
- Modify: `internal/runtime/chat_test.go`

- [ ] **Step 1: Implement the local executor as the default action path**

Implement:
- `answer`, `inspect`, `implement`, and `verify` families go through one primary `forge` execution path
- `agent.Agent` runs with the base `forge` system prompt, not a dispatch role
- success/failure observations return to the kernel instead of letting prompt prose decide the next transition

- [ ] **Step 2: Flip the default runtime to the kernel and keep legacy dispatch behind opt-in compatibility only**

Implement:
- bare `forge` uses the kernel by default
- legacy dispatch can still be selected temporarily through the hidden selector for migration only
- `chat.agents.enabled` no longer controls the primary user-facing behavior

- [ ] **Step 3: Run the focused tests to verify the common path passes**

Run:
- `go test ./internal/harness -run 'TestRunner(Local|Verify|Respond)'`
- `go test ./internal/runtime -run 'TestRunChat'`
- `go test ./internal/agent -run 'TestAgentEndToEnd'`
Expected: PASS

- [ ] **Step 4: Commit the common-path cutover**

```bash
git add internal/harness/*.go internal/agent/agent.go internal/agent/system.go internal/agent/integration_test.go internal/runtime/chat.go internal/runtime/chat_test.go
git commit -m "feat: route forge chat through the new local-first kernel"
```

## Chunk 3: Add Hidden Workers With Typed Contracts

### Task 5: Lock worker contracts and validation with failing tests

**Files:**
- Create: `internal/harness/contracts_test.go`
- Create: `internal/harness/workers_test.go`
- Modify: `internal/agent/subagent.go`

- [ ] **Step 1: Add failing tests for each worker contract**

Cover:
- `reader` returns `status`, `evidence`, `coverage`, `gaps`, and `suggested_next`
- `editor` returns `changes`, `verification_attempts`, and `remaining_issues`
- `verifier` returns `checks`, `failures`, and `confidence`
- `researcher` returns `findings`, `sources`, and `confidence`
- unknown fields, missing fields, and invalid enum values fail closed
- worker output cannot widen scope or request a different worker family

Example contract target:

```go
type ReaderResult struct {
	Status        string     `json:"status"`
	Evidence      []Evidence `json:"evidence"`
	Coverage      string     `json:"coverage"`
	Gaps          []string   `json:"gaps"`
	SuggestedNext string     `json:"suggested_next"`
}
```

- [ ] **Step 2: Run the worker-focused tests to verify they fail**

Run: `go test ./internal/harness -run 'Test(Worker|Contract)'`
Expected: FAIL on missing worker schemas and validation.

### Task 6: Implement hidden worker execution without visible agent personas

**Files:**
- Modify: `internal/harness/workers.go`
- Modify: `internal/harness/contracts.go`
- Modify: `internal/harness/policy.go`
- Modify: `internal/harness/runner.go`
- Modify: `internal/agent/subagent.go`
- Modify: `internal/agent/system.go`

- [ ] **Step 1: Implement bounded worker specs**

Implement:
- worker kinds `reader`, `editor`, `verifier`, `researcher`
- per-worker tool allowlists and prompt suffixes
- no worker-to-worker spawning
- no prompt-authored control-plane authority

- [ ] **Step 2: Validate worker output in code before the kernel accepts it**

Implement:
- strict JSON decoding with unknown-field rejection
- required-field checks
- bounded fallback behavior: retry structured output once or twice, then fail closed to local recovery

- [ ] **Step 3: Update policy so workers are optional and non-default**

Implement:
- local-first stays the default
- workers only launch for clearly isolated, background-safe, or independent-verification cases
- most turns still complete without any worker launch

- [ ] **Step 4: Run the worker-focused tests to verify they pass**

Run: `go test ./internal/harness -run 'Test(Worker|Contract)'`
Expected: PASS

- [ ] **Step 5: Commit hidden worker support**

```bash
git add internal/harness/*.go internal/agent/subagent.go internal/agent/system.go
git commit -m "feat: add hidden worker contracts to the forge kernel"
```

## Chunk 4: Remove The Old Orchestration Control Plane

### Task 7: Lock the legacy-removal behavior with failing tests

**Files:**
- Modify: `internal/runtime/chat_test.go`
- Modify: `internal/config/config_test.go`
- Modify: `cmd/forge/main_test.go`

- [ ] **Step 1: Add failing tests for the cutover rules**

Cover:
- bare `forge` starts chat with no visible agent-mode switch
- `-d` is the only way to surface advanced trace
- old visible-agent wording does not appear in help or default UI
- old `[chat.agents.models]` config does not drive runtime behavior after cutover

- [ ] **Step 2: Run the targeted cutover tests to verify they fail**

Run:
- `go test ./internal/runtime -run 'TestRunChat'`
- `go test ./internal/config -run 'Test(Chat|Agent)'`
- `go test ./cmd/forge -run 'Test(Help|RunChat)'`
Expected: FAIL on old config/help/runtime assumptions.

### Task 8: Delete or quarantine the old dispatch path and migrate config

**Files:**
- Modify: `internal/runtime/chat.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `cmd/forge/main.go`
- Modify: `cmd/forge/main_test.go`
- Delete or quarantine: `internal/agent/roles.go`
- Delete or quarantine: `internal/agent/tools/delegate.go`

- [ ] **Step 1: Remove old dispatch as the default orchestration authority**

Implement:
- remove runtime registration of the `delegate` tool from the default chat path
- stop labeling the visible assistant as `dispatch`
- stop centering configuration around visible agent-role models

- [ ] **Step 2: Migrate config to worker models or default-chat-model fallback**

Implement:
- add a worker-model map that is internal/runtime-facing rather than UI-facing
- keep a minimal compatibility read path if necessary, but do not surface old agent config in the product model

- [ ] **Step 3: Run the targeted cutover tests to verify they pass**

Run:
- `go test ./internal/runtime -run 'TestRunChat'`
- `go test ./internal/config -run 'Test(Chat|Agent)'`
- `go test ./cmd/forge -run 'Test(Help|RunChat)'`
Expected: PASS

- [ ] **Step 4: Commit the control-plane removal**

```bash
git add internal/runtime/chat.go internal/config/config.go internal/config/config_test.go cmd/forge/main.go cmd/forge/main_test.go
git rm internal/agent/tools/delegate.go internal/agent/roles.go || true
git commit -m "refactor: remove legacy dispatch-centered orchestration"
```

## Chunk 5: Add Real-Log Regressions And Paraphrase Evals

### Task 9: Turn the known failures into failing regression tests

**Files:**
- Create: `internal/harness/regression_test.go`
- Create: `internal/harness/testdata/debuglogs/repo-inspect-stall.jsonl`
- Create: `internal/harness/testdata/debuglogs/follow-up-misroute.jsonl`
- Create: `internal/harness/testdata/paraphrases/directory-inspect.txt`
- Create: `internal/harness/testdata/paraphrases/follow-up-interpret.txt`

- [ ] **Step 1: Add sanitized fixtures derived from real debug failures**

Include:
- over-escalation from simple prompts
- no-response/looping after the first scout-like inspection
- malformed or empty worker outputs
- follow-up turns that should interpret prior evidence without repo-wide reinspection

- [ ] **Step 2: Add failing regression tests that replay the fixtures**

Cover:
- the same failure does not recur under the new kernel
- paraphrases route identically regardless of wording
- no test depends on English fallback phrases like `i don't understand`

- [ ] **Step 3: Run the regression tests to verify they fail**

Run: `go test ./internal/harness -run 'Test(Regression|Paraphrase)'`
Expected: FAIL until the new fixtures are wired into the kernel tests.

### Task 10: Implement the regression harness and keep it green

**Files:**
- Modify: `internal/harness/regression_test.go`
- Modify: `internal/harness/classifier.go`
- Modify: `internal/harness/session.go`
- Modify: `internal/harness/policy.go`

- [ ] **Step 1: Implement the replay helpers and paraphrase table tests**

Implement:
- sanitized log replay helpers that assert family, step selection, and stop conditions
- paraphrase suite loading from text fixtures
- explicit negative assertions for over-escalation and unwanted synthesis

- [ ] **Step 2: Run the regression tests to verify they pass**

Run: `go test ./internal/harness -run 'Test(Regression|Paraphrase)'`
Expected: PASS

- [ ] **Step 3: Commit the regression suite**

```bash
git add internal/harness/*.go internal/harness/testdata/
git commit -m "test: add forge harness regression and paraphrase coverage"
```

## Chunk 6: Replace The Default UI And Keep Advanced Trace Under `-d`

### Task 11: Lock the new transcript-first UI with failing tests

**Files:**
- Create: `internal/tui/transcript.go`
- Create: `internal/tui/activity.go`
- Create: `internal/tui/traceview.go`
- Create: `internal/tui/codeblock.go`
- Create: `internal/tui/transcript_test.go`
- Create: `internal/tui/activity_test.go`
- Create: `internal/tui/traceview_test.go`
- Create: `internal/tui/codeblock_test.go`
- Modify: `internal/tui/chatmodel_test.go`
- Modify: `internal/tui/view_test.go`
- Modify: `internal/tui/chatmsg_test.go`

- [ ] **Step 1: Add failing UI tests for the approved transcript-first behavior**

Cover:
- no default side tools panel
- one coherent transcript speaker
- compact activity rows instead of visible worker conversations
- styled code, diff, and verification-output blocks
- debug trace only when `DebugEnabled` is true
- pasted input remains visible and the screen does not clear/repaint destructively
- long transcripts keep the current prompt/input visible by scrolling correctly

- [ ] **Step 2: Run the focused TUI tests to verify they fail**

Run: `go test ./internal/tui -run 'Test(ChatModel|Transcript|Activity|TraceView|CodeBlock|View)'`
Expected: FAIL on the current panel-heavy layout and scroll/input behavior.

### Task 12: Implement the new default UI and debug-only advanced trace

**Files:**
- Modify: `internal/tui/chatmodel.go`
- Modify: `internal/tui/chatshared.go`
- Modify: `internal/tui/chatmsg.go`
- Modify: `internal/tui/chattheme.go`
- Modify: `internal/tui/styles.go`
- Modify: `internal/runtime/chat.go`
- Modify: `internal/runtime/chat_debug.go`
- Modify: `internal/llm/types.go`

- [ ] **Step 1: Implement transcript-first rendering and compact activity**

Implement:
- full-width transcript default
- compact inline activity/status rows
- one final `forge` answer per turn
- intentional code/diff/output boxes with success/failure/info styling

- [ ] **Step 2: Implement debug-only advanced trace**

Implement:
- pass `DebugEnabled` from `cmd/forge/main.go` through runtime into the TUI
- show advanced trace UI only under `-d`
- keep non-debug runs clean and transcript-first

- [ ] **Step 3: Fix the known interaction bugs while replacing the UI**

Implement:
- input remains visible while pasting/submitting
- transcript scroll keeps the prompt visible when the pane is full
- no destructive clear-and-repaint that makes pasted prompts disappear temporarily

- [ ] **Step 4: Run the full TUI and runtime verification suite**

Run:
- `go test ./internal/tui`
- `go test ./internal/runtime`
- `go test ./cmd/forge`
Expected: PASS

- [ ] **Step 5: Run the full repo verification and build the binary**

Run:
- `go test ./...`
- `go build -o ./forge ./cmd/forge`
Expected:
- both commands exit 0

- [ ] **Step 6: Run a fresh manual repro with `-d` and inspect the new `/tmp` log**

Verify:
- a fresh debug file is created for the run
- default UI stays transcript-first
- advanced trace appears only under `-d`
- simple inspect/paraphrase prompts stay local or use bounded hidden workers only when warranted
- no prompt/keyword patching appears in the new control path

- [ ] **Step 7: Commit the UI replacement and final docs**

```bash
git add internal/tui/*.go internal/runtime/chat.go internal/runtime/chat_debug.go internal/llm/types.go cmd/forge/main.go README.md ARCHITECTURE.md
git commit -m "feat: ship transcript-first forge chat ui"
```

## Final Verification And Handoff

### Task 13: Verify the redesign end to end and commit the docs

**Files:**
- Modify: `docs/superpowers/specs/2026-03-25-forge-harness-redesign-design.md`
- Modify: `docs/superpowers/plans/2026-03-25-forge-harness-redesign-implementation.md`

- [ ] **Step 1: Force-add the ignored docs and verify they are tracked**

Run:
- `git add -f docs/superpowers/specs/2026-03-25-forge-harness-redesign-design.md`
- `git add -f docs/superpowers/plans/2026-03-25-forge-harness-redesign-implementation.md`
- `git status --short`
Expected:
- both docs appear staged despite `/docs/` being ignored

- [ ] **Step 2: Commit the design and plan docs if they are not already included by earlier commits**

```bash
git add -f docs/superpowers/specs/2026-03-25-forge-harness-redesign-design.md docs/superpowers/plans/2026-03-25-forge-harness-redesign-implementation.md
git commit -m "docs: add forge harness redesign spec and plan"
```

- [ ] **Step 3: Confirm the production-readiness bar before calling this done**

Checklist:
- runtime owns classification, transitions, and stop conditions
- prompt-authored orchestration metadata is non-authoritative
- workers are hidden, bounded, and schema-validated
- simple asks stay simple under paraphrases
- default UI is transcript-first and usable
- advanced trace is only visible under `-d`
- fresh debug logs in `/tmp` support real failure replay

