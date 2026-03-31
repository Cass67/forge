# Forge Phase 3 Typed Hooks Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn Forge's overlay-only hook plumbing into a typed, runtime-owned hook engine that can inject overlays, emit notes, and block actions deterministically.

**Architecture:** Build the hook engine under `internal/hooks`, keep registration internal/runtime-owned, and adapt existing overlay-producing runtime paths to emit typed hook results instead of mutating session overlays directly. Preserve current prompt behavior by keeping normalized overlay output as the bridge into `internal/react/prompt`.

**Tech Stack:** Go, `internal/hooks`, `internal/react`, `internal/runtime`, existing prompt/session/runtime tests.

---

**Spec:** `docs/superpowers/specs/2026-03-31-forge-phase-3-typed-hooks-design.md`

## File Structure

- Create: `internal/hooks/types.go`
  Responsibility: hook point, event, result, and execution-output types.
- Create: `internal/hooks/runtime.go`
  Responsibility: registry, deterministic dispatch, block short-circuiting, panic containment.
- Create: `internal/hooks/runtime_dispatch_test.go`
  Responsibility: ordering, containment, and block behavior tests.
- Modify: `internal/hooks/overlays.go`
  Responsibility: convert typed overlay results into promptcomposer overlays.
- Modify: `internal/hooks/runtime_test.go`
  Responsibility: result-to-overlay regression coverage.
- Modify: `internal/react/session.go`
  Responsibility: store normalized hook outputs with minimal API churn.
- Modify: `internal/react/session_test.go`
  Responsibility: session hook-output storage regressions.
- Modify: `internal/react/prompt.go`
  Responsibility: consume typed hook output in prompt composition.
- Modify: `internal/react/prompt_test.go`
  Responsibility: ensure hook-generated overlays/notes still land in system messages.
- Modify: `internal/react/loop.go`
  Responsibility: execute runtime-owned hook handlers for prompt-context/runtime guidance.
- Modify: `internal/react/loop_test.go`
  Responsibility: verify blocked-plan/review/search/git guidance now arrives through hooks.
- Modify: `internal/runtime/chat.go`
  Responsibility: route suggested-skill and guardian-warning behavior through hooks.
- Modify: `internal/runtime/chat_test.go`
  Responsibility: runtime hook integration regressions.

## Task 1: Introduce Typed Hook Core

**Files:**
- Create: `internal/hooks/types.go`
- Create: `internal/hooks/runtime.go`
- Create: `internal/hooks/runtime_dispatch_test.go`
- Modify: `internal/hooks/overlays.go`
- Modify: `internal/hooks/runtime_test.go`

- [ ] **Step 1: Write the failing dispatcher tests**

Add tests covering:
- deterministic handler order
- block short-circuiting
- panic containment
- overlay-result conversion into prompt overlays

Run: `go test ./internal/hooks -run 'Test(HookRuntime|ToPromptOverlays)'`
Expected: FAIL because the typed runtime does not exist yet.

- [ ] **Step 2: Implement typed hook primitives**

Add hook types for:
- hook point enum(s)
- event payload
- overlay/note/block results
- execution output

Keep the types small, explicit, and internal-runtime friendly.

- [ ] **Step 3: Implement the dispatcher**

Add registry/dispatcher behavior with:
- runtime-owned registration
- deterministic execution order
- short-circuit on block
- panic recovery into contained output

Do not add config or plugin loading in this phase.

- [ ] **Step 4: Adapt overlay conversion**

Update `internal/hooks/overlays.go` so prompt conversion accepts normalized hook overlay results without regressing current provenance/priority behavior.

- [ ] **Step 5: Verify the hook core slice**

Run: `go test ./internal/hooks`
Expected: PASS

- [ ] **Step 6: Commit the task**

```bash
git add internal/hooks/types.go internal/hooks/runtime.go internal/hooks/runtime_dispatch_test.go internal/hooks/overlays.go internal/hooks/runtime_test.go
git commit -m "hooks: add typed runtime dispatcher"
```

## Task 2: Route Session And Prompt Consumption Through Hook Output

**Files:**
- Modify: `internal/react/session.go`
- Modify: `internal/react/session_test.go`
- Modify: `internal/react/prompt.go`
- Modify: `internal/react/prompt_test.go`

- [ ] **Step 1: Write the failing session/prompt tests**

Add tests covering:
- storing normalized hook output on session state
- preserving existing prompt-visible overlay behavior
- note output reaching prompt/runtime surfaces as designed

Run: `go test ./internal/react -run 'Test(Session|BuildMessages).*Hook'`
Expected: FAIL because session/prompt still only understand raw overlay plumbing.

- [ ] **Step 2: Implement minimal session-state support**

Update session state so typed hook execution output can be stored and snapshotted without broad unrelated churn.

Prefer additive state changes over removing every overlay helper immediately.

- [ ] **Step 3: Update prompt composition**

Modify prompt building so it consumes normalized hook output and still emits the same prompt overlay content for existing runtime guidance.

- [ ] **Step 4: Verify the session/prompt slice**

Run: `go test ./internal/react -run 'Test(Session|BuildMessages).*Hook'`
Expected: PASS

- [ ] **Step 5: Commit the task**

```bash
git add internal/react/session.go internal/react/session_test.go internal/react/prompt.go internal/react/prompt_test.go
git commit -m "react: consume typed hook output"
```

## Task 3: Replace Runtime Overlay Mutation In The ReAct Loop

**Files:**
- Modify: `internal/react/loop.go`
- Modify: `internal/react/loop_test.go`

- [ ] **Step 1: Write the failing runtime-guidance tests**

Add or refactor tests so they prove runtime guidance comes from hook execution rather than direct overlay mutation for:
- review guidance
- blocked plan guidance
- synthesis/validation/search/git/repeat guidance

Run: `go test ./internal/react -run 'TestRunner.*(Hook|Overlay|BlockedPlan)'`
Expected: FAIL because loop code still mutates overlays directly.

- [ ] **Step 2: Implement runtime-owned loop handlers**

Register internal handlers for the relevant hook point(s) and replace direct `SetHookOverlay` / `ClearHookOverlay` calls with hook execution + normalized output updates.

Keep behavior deterministic and preserve existing visible guidance text where practical.

- [ ] **Step 3: Verify the loop slice**

Run: `go test ./internal/react -run 'TestRunner.*(Hook|Overlay|BlockedPlan)'`
Expected: PASS

- [ ] **Step 4: Commit the task**

```bash
git add internal/react/loop.go internal/react/loop_test.go
git commit -m "react: route runtime guidance through hooks"
```

## Task 4: Route Chat-Level Suggestions And Guardian Feedback Through Hooks

**Files:**
- Modify: `internal/runtime/chat.go`
- Modify: `internal/runtime/chat_test.go`

- [ ] **Step 1: Write the failing chat runtime tests**

Add tests covering:
- suggested skill guidance emitted through hooks
- guardian warnings emitted through hooks
- clearing behavior when no hook output is produced

Run: `go test ./internal/runtime -run 'Test(ApplySuggestedSkillOverlay|ApplyGuardianOverlay|Chat).*Hook'`
Expected: FAIL because chat runtime still writes overlays directly.

- [ ] **Step 2: Implement chat/runtime hook integration**

Replace direct session overlay mutation in chat runtime code with typed hook execution/output updates while keeping current visible behavior aligned.

- [ ] **Step 3: Verify the chat slice**

Run: `go test ./internal/runtime -run 'Test(ApplySuggestedSkillOverlay|ApplyGuardianOverlay|Chat).*Hook'`
Expected: PASS

- [ ] **Step 4: Commit the task**

```bash
git add internal/runtime/chat.go internal/runtime/chat_test.go
git commit -m "runtime: move chat guidance onto hook runtime"
```

## Task 5: Final Verification And Scope Check

**Files:**
- Modify any of the files above only if verification exposes a real issue

- [ ] **Step 1: Run focused hook/runtime verification**

Run:
- `go test ./internal/hooks ./internal/react ./internal/runtime`

Expected: PASS

- [ ] **Step 2: Run full repo verification**

Run:
- `go test ./...`
- `just check`

Expected: PASS

- [ ] **Step 3: Review the phase diff for accidental scope creep**

Run: `git diff --stat $(git merge-base HEAD main)..HEAD`
Expected: only the files listed above are included.

- [ ] **Step 4: Commit any final polish**

```bash
git add internal/hooks internal/react internal/runtime
git commit -m "chore: polish phase-3 typed hooks"
```

## Notes For The Implementer

- Keep registration internal/runtime-owned in phase 3.
- Do not add a user-configured or remote hook surface yet.
- Preserve current prompt-visible guidance behavior where feasible.
- Prefer normalized hook output over direct session mutation from many call sites.
- Keep hook result types narrow: overlay, note, block.
- Failures must degrade safely and be test-covered.
