# Codex Behavior Alignment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Align Forge’s native chat/runtime behavior with Codex’s written operating contract so the app behaves more like Codex in real work, not just in tool-calling architecture.

**Architecture:** Keep Forge’s native `react` runtime and structured tool-calling path, but strengthen the model-visible contract, runtime-owned task state, and verification/postcondition checks. The aim is not to reintroduce legacy workflow engines; it is to move Forge closer to Codex’s behavior by improving the prompt, state, and completion discipline around the existing runtime.

**Tech Stack:** Go, `internal/agent`, `internal/react`, `internal/runtime`, existing tool registry and native `llm.NativeToolCaller` path.

---

## Source-Grounded Gap Summary

Primary Codex source:
- `/tmp/codex-upstream/codex-rs/protocol/src/prompts/base_instructions/default.md`
- `/tmp/codex-upstream/codex-rs/core/src/codex.rs`

Primary Forge source:
- `/Users/cass/git/forge/internal/agent/system.go`
- `/Users/cass/git/forge/internal/react/loop.go`
- `/Users/cass/git/forge/internal/react/prompt.go`
- `/Users/cass/git/forge/internal/runtime/chat.go`
- `/Users/cass/git/forge/internal/agent/tools/git.go`

What Forge already matches:
- Native/structured tool calling on the default path
- Runtime-owned session history and prompt assembly
- Streaming native tool execution
- No legacy XML tool-calling on the live path

What Forge still lacks relative to Codex:

1. **Prompt contract is too thin**
- Codex’s base instructions cover AGENTS scope, precision vs ambition, root-cause fixes, unrelated-fix avoidance, validation philosophy, progress update discipline, and final-answer structure.
- Forge’s native prompt in `internal/agent/system.go` is much shorter and omits most of that operating contract.

2. **No runtime plan/state primitive like Codex’s `update_plan`**
- Codex exposes explicit plan state and encourages the model to keep progress synchronized.
- Forge has progress strings, but no runtime-visible task plan or completion checklist.

3. **Weak postcondition discipline**
- Codex’s written behavior strongly pushes “do exactly what the user asked” and “validate your work.”
- Forge does not yet give the model a generic way to distinguish “local success” from “task success,” which is why branch-targeted Git work can still be declared done too early.

4. **Git state surface is still too coarse**
- Forge has `git_status`, `git_diff`, `git_log`, `git_merge_status`, and `git_commit`, but no first-class branch-target state tool.
- This leaves the model inferring important end-state facts from ad hoc shell commands.

5. **Completion gating is too permissive**
- Forge accepts a final plain-text answer as soon as the model stops making tool calls.
- There is no generic runtime check for objective postconditions when the user request names a target state that can be verified.

6. **Observability is good for tool traces, but not for task truth**
- Debug logs capture requests, tool calls, and responses.
- They do not yet expose runtime task goals/postconditions well enough to explain “why the model thought it was done.”

## Implementation Principles

- Do not reintroduce the old harness/kernel architecture.
- Do not build a giant Git-specific workflow engine.
- Prefer Codex-style improvements:
  - richer written behavior
  - explicit state
  - stronger verification discipline
  - structured tool/state surfaces
- Add narrow host-side invariants only when the success condition is objective and cheap to verify.

## File Map

- Modify: `internal/agent/system.go`
  Purpose: expand Forge’s native system prompt toward Codex’s behavioral contract.
- Modify: `internal/react/loop.go`
  Purpose: carry task intent/postconditions and enforce objective completion checks before final success.
- Modify: `internal/react/session.go`
  Purpose: persist lightweight task-state metadata across turns.
- Modify: `internal/react/prompt.go`
  Purpose: expose runtime task state and postconditions to the model as system context.
- Modify: `internal/runtime/chat.go`
  Purpose: seed task-state detection from user input and keep runtime wiring thin.
- Modify: `internal/agent/tools/git.go`
  Purpose: extend Git tool surface with branch-target state.
- Create: `internal/agent/tools/git_branch.go`
  Purpose: report current branch, target branch state, containment, and ahead/behind facts.
- Modify: `internal/agent/tools/git_test.go`
  Purpose: test branch-state reporting.
- Modify: `internal/react/loop_test.go`
  Purpose: test objective completion gating and target-state checks.
- Modify: `internal/react/prompt_test.go`
  Purpose: test task-state prompt injection.
- Modify: `internal/runtime/chat_test.go`
  Purpose: test runtime intent detection and tool registration.
- Modify: `internal/runtime/chat_debug.go`
  Purpose: optionally log runtime task-state metadata for debugability.

## Task 1: Expand Forge’s native system prompt to Codex-level operating guidance

**Files:**
- Modify: `internal/agent/system.go`
- Modify: `internal/react/prompt_test.go`

- [x] **Step 1: Write failing prompt tests**

Cover:
- prompt includes precision/root-cause guidance
- prompt includes unrelated-fix avoidance
- prompt includes validation philosophy
- prompt includes progress-update expectations
- prompt includes “don’t commit unless asked” guidance

- [x] **Step 2: Run focused tests to verify failure**

Run: `go test ./internal/agent ./internal/react -run 'TestBuildNativeSystemPrompt|TestBuildMessages'`
Expected: FAIL because current native prompt is too thin.

- [x] **Step 3: Implement the prompt expansion**

Add Codex-like guidance, adapted to Forge:
- precision over overreach
- root-cause fixes
- minimal focused changes
- validation from specific to broad
- concise progress updates
- no commits/branches unless requested
- respect repo instructions

- [x] **Step 4: Re-run focused tests to verify pass**

Run: `go test ./internal/agent ./internal/react -run 'TestBuildNativeSystemPrompt|TestBuildMessages'`
Expected: PASS

## Task 2: Add lightweight task-state and postcondition tracking

**Files:**
- Modify: `internal/react/session.go`
- Modify: `internal/react/prompt.go`
- Modify: `internal/react/loop.go`
- Modify: `internal/react/loop_test.go`

- [x] **Step 1: Write failing tests for task-state persistence**

Cover:
- user requests that name objective target state can be recorded
- task state is surfaced back to the model
- final plain-text completion is rejected when objective postconditions are not met

- [x] **Step 2: Run focused tests to verify failure**

Run: `go test ./internal/react -run 'TestRunner.*Postcondition|TestSession.*TaskState'`
Expected: FAIL because Forge currently has no generic task postcondition state.

- [x] **Step 3: Implement minimal runtime task-state**

Implement:
- lightweight task metadata in session state
- optional runtime note for target state
- generic completion gate hook in `Runner`

- [x] **Step 4: Re-run focused tests to verify pass**

Run: `go test ./internal/react -run 'TestRunner.*Postcondition|TestSession.*TaskState'`
Expected: PASS

## Task 3: Add first-class Git branch-state inspection

**Files:**
- Create: `internal/agent/tools/git_branch.go`
- Modify: `internal/agent/tools/git_test.go`
- Modify: `internal/runtime/chat.go`
- Modify: `internal/runtime/chat_test.go`

- [x] **Step 1: Write failing tool and registration tests**

Cover:
- current branch detection
- target branch containment checks
- ahead/behind summary where available
- tool registered on the default chat path

- [x] **Step 2: Run focused tests to verify failure**

Run: `go test ./internal/agent/tools ./internal/runtime -run 'TestGitBranchState|TestRegisterToolsAddsGitBranchState'`
Expected: FAIL because tool does not yet exist.

- [x] **Step 3: Implement `git_branch_state`**

Return structured plain text with:
- current branch
- `HEAD` commit
- whether named target branch exists
- whether target contains `HEAD`
- whether merge/rebase/cherry-pick is active

- [x] **Step 4: Re-run focused tests to verify pass**

Run: `go test ./internal/agent/tools ./internal/runtime -run 'TestGitBranchState|TestRegisterToolsAddsGitBranchState'`
Expected: PASS

## Task 4: Add narrow objective completion gates for branch-targeted Git tasks

**Files:**
- Modify: `internal/react/loop.go`
- Modify: `internal/react/loop_test.go`
- Modify: `internal/runtime/chat.go`

- [x] **Step 1: Write failing tests for the exact debug-file failure mode**

Cover:
- request like “merge X into main” is not complete if the resulting commit is not reachable from `main`
- side-branch-only success cannot produce a final answer

- [x] **Step 2: Run focused tests to verify failure**

Run: `go test ./internal/react ./internal/runtime -run 'TestRunnerRejectsBranchTargetMismatch|TestRunChatTurnSeedsGitTargetState'`
Expected: FAIL because completion is currently too permissive.

- [x] **Step 3: Implement narrow completion gate**

Implement:
- detect explicit branch-target tasks from user input
- require a branch-state verification before allowing final completion
- keep this generic and small; do not build a large Git workflow engine

- [x] **Step 4: Re-run focused tests to verify pass**

Run: `go test ./internal/react ./internal/runtime -run 'TestRunnerRejectsBranchTargetMismatch|TestRunChatTurnSeedsGitTargetState'`
Expected: PASS

## Task 5: Improve observability for task truth

**Files:**
- Modify: `internal/runtime/chat_debug.go`
- Modify: `internal/runtime/chat_debug_test.go`

- [x] **Step 1: Write failing debug-log tests**

Cover:
- debug logs include runtime task-state metadata when present
- logs expose completion-gate failures clearly

- [x] **Step 2: Run focused tests to verify failure**

Run: `go test ./internal/runtime -run 'TestEnableChatDebug.*TaskState'`
Expected: FAIL because current logs do not include task-state metadata.

- [x] **Step 3: Implement minimal task-state logging**

Add:
- runtime task goal / target metadata
- completion-gate reason when finalization is blocked

- [x] **Step 4: Re-run focused tests to verify pass**

Run: `go test ./internal/runtime -run 'TestEnableChatDebug.*TaskState'`
Expected: PASS

## Verification

- `go test ./internal/agent ./internal/agent/tools ./internal/react ./internal/runtime`
- `go test ./...`

## Success Criteria

Forge is materially closer to Codex when all of the following are true:
- the native system prompt teaches Codex-like precision and validation behavior
- runtime task state exists for objective end states
- Git branch-target truth is a first-class tool/state concept
- final answers are blocked when objective named targets are not yet satisfied
- debug logs explain why the runtime accepted or rejected completion

## Non-Goals

- Reintroducing any legacy harness/kernel fallback
- Reverting to prompt-level XML tool calling
- Building a giant workflow-specific controller for every task family
- Copying OpenCode-specific bash workflow conventions that are not needed for Codex alignment
