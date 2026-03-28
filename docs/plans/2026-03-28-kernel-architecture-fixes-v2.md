# Forge Kernel Architecture Fixes v2 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate recurring harness failures by enforcing truthful side-effect reporting, deterministic routing, preview-phase boundaries, and protected-branch safety.

**Architecture:** Move critical guarantees from prompt heuristics into host-side policy and runtime contracts. Keep worker output validation strict, add claim/evidence enforcement, and introduce explicit thread phase state (`ideate` vs `apply`) so preview iteration cannot mutate source until requested.

**Tech Stack:** Go (existing harness/runtime/agent packages), existing `go test` suite, existing `gitutil` utilities.

---

## Scope And Baseline

- Supersedes: `docs/plans/2026-03-28-kernel-architecture-fixes.md`
- Root failures to fix:
  - False side-effect claim: branch claimed as created with no tool evidence (`/tmp/forge-debug-grok-theme-cass.jsonl`).
  - Misrouting: branch/apply intent classified as answer-only follow-up.
  - Preview flow drift: selected direction not locked to implementation output.
  - Preview iteration writing source without explicit apply intent.

## Non-Goals

- No permissive parsing of malformed worker JSON.
- No broad prompt simplification that weakens protocol safety.
- No default `git_commit` expansion in worker tool allowlists.

---

## Chunk 1: Truthful Side-Effects And Routing

### Task 1: Route Branch/Apply Follow-Ups To Action Lanes

**Files:**
- Modify: `internal/harness/classifier.go`
- Modify: `internal/harness/thread.go`
- Test: `internal/harness/classifier_test.go`
- Test: `internal/harness/stress_corpus_test.go`

- [ ] **Step 1: Add failing classification tests for branch/apply intent**

```go
func TestClassifyActivePreviewBranchRequestPrefersAction(t *testing.T) {
	session := SessionState{
		Threads: ThreadLedger{
			Active: ThreadState{
				ID:         "thread-1",
				Kind:       ThreadPreviewCollaboration,
				Status:     ThreadAwaitingUserFeedback,
				TopicKey:   "path:/theme",
				TaskText:   "show three theme mockups",
				Deliverable: DeliverablePreviewAvailableAndRenderable,
			},
		},
	}

	got := Classify(UserTurn{Text: "i like obsidian term, lets make a branch and bring it to life"}, session)
	if got.Family != FamilyImplement || !got.WantsAction || got.ThreadIntent != TurnIntentContinueThread {
		t.Fatalf("classification = %#v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/harness -run TestClassifyActivePreviewBranchRequestPrefersAction -count=1`

Expected: FAIL (currently routes through answer path in this scenario).

- [ ] **Step 3: Add explicit branch/apply intent detection**

```go
// classifier.go
var branchWorkflowTokens = tokenSet(
	"branch", "checkout", "switch", "create", "new", "worktree", "feature",
)

func wantsBranchWorkflowAction(lower string, tokens map[string]struct{}) bool {
	if !containsAny(tokens, branchWorkflowTokens) {
		return false
	}
	return strings.Contains(lower, "make a branch") ||
		strings.Contains(lower, "create a branch") ||
		strings.Contains(lower, "checkout -b") ||
		strings.Contains(lower, "switch to a branch")
}
```

```go
// thread.go (inside classifyActiveThreadTurn for preview threads)
if wantsBranchWorkflowAction(lower, tokens) {
	return Classification{
		Family:                  FamilyImplement,
		WantsAction:             true,
		PrefersVisibleExecution: true,
		CanStayLocal:            true,
		IsFollowUp:              true,
		TopicKey:                strings.TrimSpace(active.TopicKey),
		TaskText:                strings.TrimSpace(text),
		ThreadIntent:            TurnIntentContinueThread,
		Reason:                  "active thread branch workflow action",
	}, true
}
```

- [ ] **Step 4: Add stress fixtures for branch/apply follow-ups**

Add corpus entries for:
- `"make a branch for this theme"`
- `"branch this and implement obsidian"`
- `"switch to a feature branch then wire /theme"`

- [ ] **Step 5: Run harness tests**

Run: `go test ./internal/harness -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/harness/classifier.go internal/harness/thread.go internal/harness/classifier_test.go internal/harness/stress_corpus_test.go
git commit -m "fix(harness): route preview follow-up branch/apply intents to action lane"
```

---

### Task 2: Add Host Claim/Evidence Guard For Side-Effects

**Files:**
- Modify: `internal/harness/types.go`
- Modify: `internal/harness/local.go`
- Modify: `internal/harness/strictlocal.go`
- Modify: `internal/harness/workers.go`
- Modify: `internal/harness/outcome.go`
- Test: `internal/harness/outcome_test.go`
- Test: `internal/harness/runner_test.go`

- [ ] **Step 1: Add failing test for ungrounded side-effect claim**

```go
func TestNormalizeObservationBlocksUngroundedBranchClaim(t *testing.T) {
	class := Classification{Family: FamilyAnswer, CanStayLocal: true}
	step := Step{Lane: LaneConversational, Kind: StepLocal}
	obs := Observation{
		Status:   ObservationComplete,
		Response: "Branch created: obsidian-term",
		Summary:  "Branch created: obsidian-term",
	}
	got := normalizeObservation(step, class, SessionState{}, obs)
	if got.Status != ObservationBlocked {
		t.Fatalf("status = %q, want blocked", got.Status)
	}
}
```

- [ ] **Step 2: Extend observation with tool-call evidence**

```go
// types.go
type ObservedToolCall struct {
	Name string
	Args map[string]any
}

type Observation struct {
	// existing fields...
	ToolCalls []ObservedToolCall
}
```

- [ ] **Step 3: Capture tool calls in local/strict/worker executors**

```go
// local.go / strictlocal.go / workers.go
calls := e.Agent.LastToolCalls()
obs.ToolCalls = append(obs.ToolCalls, mapObservedToolCalls(calls)...)
```

- [ ] **Step 4: Implement side-effect claim validation in outcome normalization**

```go
// outcome.go
if outcome, ok := ungroundedSideEffectOutcome(step, class, obs); ok {
	obs.Status = ObservationBlocked
	obs.Response = ""
	obs.Summary = outcome.Reason
	obs.Outcome = outcome
	return obs
}
```

Guard at minimum:
- branch created/switch claims require `run_command` git branch/checkout evidence.
- commit created claims require `git_commit` tool evidence.
- preview live claims require verified preview runtime snapshot (already available).

- [ ] **Step 5: Run tests**

Run:
- `go test ./internal/harness -run TestNormalizeObservationBlocksUngroundedBranchClaim -count=1`
- `go test ./internal/harness -run TestRunner.*Claim.* -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/harness/types.go internal/harness/local.go internal/harness/strictlocal.go internal/harness/workers.go internal/harness/outcome.go internal/harness/outcome_test.go internal/harness/runner_test.go
git commit -m "fix(harness): enforce claim-evidence contract for side-effect statements"
```

---

### Task 3: Add Protected-Branch Workspace Policy (Host-Owned)

**Files:**
- Create: `internal/harness/workspace_policy.go`
- Modify: `internal/harness/runner.go`
- Create: `internal/runtime/workspace_policy.go`
- Modify: `internal/runtime/chat.go`
- Modify: `internal/gitutil/gitutil.go`
- Test: `internal/harness/runner_test.go`
- Test: `internal/runtime/chat_test.go`
- Test: `internal/gitutil/gitutil_test.go`

- [ ] **Step 1: Add failing tests for protected-branch behavior**

Coverage:
- Implementation/apply intent on `main` must not execute code edits before branch policy runs.
- Explicit branch request in preview flow must create/switch once, then continue task.

- [ ] **Step 2: Add branch helpers in gitutil**

```go
func CurrentBranch(dir string) (string, error)
func BranchExists(dir, name string) (bool, error)
func CheckoutNewBranch(dir, name string) error
```

- [ ] **Step 3: Add workspace policy interface and hook**

```go
type WorkspacePolicy interface {
	EnsureExecutionContext(ctx context.Context, turn UserTurn, class Classification, session SessionState) (ProgressMilestone, error)
}
```

Invoke in `Runner.Run` between classify and plan for action families.

- [ ] **Step 4: Implement runtime-backed policy**

Policy defaults:
- Protected branches: `main`, `master`.
- For implement/debug/verify action intents on protected branches, create/switch to `forge/<slug>-<turn>`.
- Emit progress milestone with actual branch name.

- [ ] **Step 5: Run tests**

Run:
- `go test ./internal/gitutil -count=1`
- `go test ./internal/runtime -run Test.*WorkspacePolicy.* -count=1`
- `go test ./internal/harness -run TestRunner.*ProtectedBranch.* -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/harness/workspace_policy.go internal/harness/runner.go internal/runtime/workspace_policy.go internal/runtime/chat.go internal/gitutil/gitutil.go internal/harness/runner_test.go internal/runtime/chat_test.go internal/gitutil/gitutil_test.go
git commit -m "feat(harness): enforce protected-branch workspace policy before action execution"
```

---

## Chunk 2: Preview Thread Phase Model (`ideate` vs `apply`)

### Task 4: Add Thread Phase State And Deterministic Transitions

**Files:**
- Modify: `internal/harness/types.go`
- Modify: `internal/harness/thread.go`
- Modify: `internal/harness/session.go`
- Test: `internal/harness/session_test.go`
- Test: `internal/harness/thread.go`-adjacent tests in `classifier_test.go`

- [ ] **Step 1: Add failing lifecycle tests**

Coverage:
- New preview thread starts in `ideate`.
- Explicit apply instruction transitions to `apply`.
- Replay/status/meta questions do not mutate phase.

- [ ] **Step 2: Add phase type and thread fields**

```go
type ThreadPhase string
const (
	ThreadPhaseNone   ThreadPhase = ""
	ThreadPhaseIdeate ThreadPhase = "ideate"
	ThreadPhaseApply  ThreadPhase = "apply"
)
```

Add `Phase ThreadPhase` to `ThreadState`.

- [ ] **Step 3: Implement phase transitions in thread/session ledger**

Rules:
- `preview_collaboration` thread create => `ideate`.
- `explicitSourceApplyRequest(...)` => `apply`.
- Superseding non-preview threads reset phase (`none`).

- [ ] **Step 4: Run tests**

Run: `go test ./internal/harness -run TestSession.*PreviewPhase.* -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/harness/types.go internal/harness/thread.go internal/harness/session.go internal/harness/session_test.go internal/harness/classifier_test.go
git commit -m "feat(harness): add preview thread phase state and transitions"
```

---

### Task 5: Enforce Phase-Gated Tool Access

**Files:**
- Modify: `internal/harness/strictlocal.go`
- Modify: `internal/harness/local.go`
- Test: `internal/harness/strictlocal_test.go`
- Test: `internal/harness/local_test.go`

- [ ] **Step 1: Add failing gating tests**

Coverage:
- `ideate` phase: source-edit tools blocked; artifact/preview tools allowed.
- `apply` phase: source-edit tools allowed.

- [ ] **Step 2: Replace heuristic-only gating with phase-driven gating**

```go
func shouldConstrainPreviewThreadToArtifacts(...) bool {
	// true when active preview thread phase == ideate
}
```

Keep `explicitSourceApplyRequest` for transition trigger, not as sole gating mechanism.

- [ ] **Step 3: Update visible-collaboration prompt text**

Prompt must explicitly reflect phase:
- `ideate`: “preview artifacts only”
- `apply`: “apply chosen direction into source”

- [ ] **Step 4: Run tests**

Run:
- `go test ./internal/harness -run TestStrictLocal.*Preview.*Phase.* -count=1`
- `go test ./internal/harness -run TestVisible.*Preview.*Phase.* -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/harness/strictlocal.go internal/harness/local.go internal/harness/strictlocal_test.go internal/harness/local_test.go
git commit -m "fix(harness): enforce preview phase-gated tool constraints"
```

---

### Task 6: Add Objective Lock For Selected Direction

**Files:**
- Modify: `internal/harness/types.go`
- Modify: `internal/harness/thread.go`
- Modify: `internal/harness/outcome.go`
- Test: `internal/harness/outcome_test.go`
- Test: `internal/harness/runner_test.go`

- [ ] **Step 1: Add failing objective-lock tests**

Coverage:
- User selects one direction (e.g. “obsidian”).
- Apply turn returning mismatched direction (e.g. “nexus”) fails deliverable with explicit reason.

- [ ] **Step 2: Add selected-direction constraint to thread state**

```go
type ThreadState struct {
	// existing fields...
	SelectedDirection string
}
```

Set/update during preview follow-up classification when user makes a concrete selection.

- [ ] **Step 3: Validate apply-phase completion against selected direction**

In `evaluateDeliverableStatus` / `normalizeObservation`, add mismatch checks:
- Selected direction exists.
- Completion summary/changes reflect selected direction.
- Otherwise block with `deliverable missing` + reason `selected direction mismatch`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/harness -run Test.*SelectedDirection.* -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/harness/types.go internal/harness/thread.go internal/harness/outcome.go internal/harness/outcome_test.go internal/harness/runner_test.go
git commit -m "fix(harness): enforce selected-direction objective lock in apply phase"
```

---

## Chunk 3: Worker Contract Robustness And Diagnostics

### Task 7: Keep Strict JSON Contract, Improve Retry Diagnostics

**Files:**
- Modify: `internal/harness/workers.go`
- Test: `internal/harness/workers_test.go`
- Test: `internal/harness/contracts_test.go`

- [ ] **Step 1: Add failing retry test for control-character JSON failure**

Coverage:
- First worker output fails decode with `invalid character '\t' in string literal`.
- Retry prompt includes explicit escaping guidance.
- Second valid JSON succeeds.

- [ ] **Step 2: Add targeted retry hinting (no permissive fallback)**

Retry prompt branch:
- If decode error indicates raw tabs/newlines in JSON string, instruct escaping (`\\t`, `\\n`) and compact JSON object output.
- Keep `decodeStrictJSON` fail-closed.

- [ ] **Step 3: Run tests**

Run:
- `go test ./internal/harness -run TestManagerExecuteRetriesInvalidStructuredOutput -count=1`
- `go test ./internal/harness -run Test.*Retry.*StringLiteral.* -count=1`

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/harness/workers.go internal/harness/workers_test.go internal/harness/contracts_test.go
git commit -m "fix(workers): improve retry diagnostics for strict JSON contract violations"
```

---

### Task 8: Add Runtime Audit Fields For Root-Cause Debugging

**Files:**
- Modify: `internal/harness/trace.go`
- Modify: `internal/harness/runner.go`
- Modify: `internal/harness/types.go`
- Test: `internal/harness/trace_test.go`

- [ ] **Step 1: Add trace fields**

Add structured fields for:
- `thread_phase`
- `claim_guard_status`
- `workspace_policy_action`
- `tool_call_count`

- [ ] **Step 2: Populate fields in runner flow**

Record in classify/act/observe transitions so log replay can explain failures without manual inference.

- [ ] **Step 3: Run tests**

Run: `go test ./internal/harness -run TestTrace.* -count=1`

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/harness/trace.go internal/harness/runner.go internal/harness/types.go internal/harness/trace_test.go
git commit -m "feat(trace): add phase, claim-guard, policy, and tool-count audit fields"
```

---

## Chunk 4: Regression Coverage And Release Gates

### Task 9: Add Reproduction Fixtures For Recent Failures

**Files:**
- Modify: `internal/harness/regression_test.go`
- Modify: `internal/harness/stress_corpus_test.go`
- Create: `internal/harness/testdata/preview_branch_claim_regression.json`

- [ ] **Step 1: Add regression fixture from observed failure shape**

Scenarios:
- Preview thread + branch request misrouted to answer lane.
- Branch claimed without tool evidence.
- Selected direction mismatch on apply.

- [ ] **Step 2: Add corpus tests**

Run deterministic corpus validation across classification + outcome normalization.

- [ ] **Step 3: Run tests**

Run: `go test ./internal/harness -run TestRegression.* -count=1`

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/harness/regression_test.go internal/harness/stress_corpus_test.go internal/harness/testdata/preview_branch_claim_regression.json
git commit -m "test(harness): add regression fixtures for preview-branch claim and routing failures"
```

---

### Task 10: Final Verification Gate

**Files:**
- Modify: `docs/plans/2026-03-28-kernel-architecture-fixes-v2.md` (checklist completion status only)

- [ ] **Step 1: Run full suite**

Run: `go test ./... -count=1`

Expected: PASS.

- [ ] **Step 2: Build binary**

Run: `go build -o ./forge ./cmd/forge`

Expected: exit 0.

- [ ] **Step 3: Manual trace replay checks**

Run live flows in `forge -d`:
- Preview ideation request.
- Direction selection.
- Branch/apply request.
- `/theme` wiring request.

Confirm:
- No side-effect claims without evidence.
- Protected branch policy triggers correctly.
- Thread phase transitions `ideate -> apply` only on explicit apply intent.
- Objective lock preserves selected direction.

- [ ] **Step 4: Commit (if only checklist status changed)**

```bash
git add docs/plans/2026-03-28-kernel-architecture-fixes-v2.md
git commit -m "docs(plan): mark v2 architecture fix plan verification checkpoints"
```

---

## Acceptance Criteria (Release Blockers)

- No false branch/commit/server claims can reach user output without matching runtime evidence.
- Preview follow-up phraseology like “make a branch and bring that to life” routes to action, not answer-only.
- Source edits are blocked in preview `ideate` phase and allowed in `apply` phase.
- Selected direction is enforced during apply work; mismatches block completion.
- Protected branches are never directly edited by action turns.
- Full test suite green with new regression coverage.

## Rollout Strategy

1. Ship Chunk 1 first (routing + claim guard + branch safety).
2. Ship Chunk 2 second (phase model + objective lock).
3. Ship Chunk 3 third (worker diagnostics/trace improvements).
4. Ship Chunk 4 last (regression fixtures + full verification).

If any chunk fails acceptance, stop rollout and fix before moving on.
