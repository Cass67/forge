# Forge Rich Agent Architecture Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn Forge into a richer, more capable coding agent platform by adding a composable prompt architecture, explicit workflow modes, stronger task/approval/memory systems, and better skill and UX orchestration.

**Architecture:** Build on Forge’s existing local-first runtime and native tool-calling path. Introduce a prompt composition layer, runtime policy engine, richer task state, explicit plan mode, approval guardian, bounded memory pipeline, and skill/hook/nudge subsystems without reintroducing a second legacy harness.

**Tech Stack:** Go, `internal/agent`, `internal/react`, `internal/runtime`, `internal/tui`, existing tool registry, skills/runtime plumbing, config and approval systems.

---

**Spec:** `docs/superpowers/specs/2026-03-31-forge-rich-agent-architecture-design.md`

## Implementation Log

### 2026-03-31 Checkpoint 1

- Added the prompt composition platform in `internal/agent/promptcomposer` and routed both the native system prompt and dynamic React overlays through it.
- Added first-class workflow tools for `enter_plan_mode`, `exit_plan_mode`, and `ask_user_question`, and registered them on the default chat path.
- Added explicit session mode state in `internal/react/session.go` and surfaced current mode into prompt assembly.
- Added mode-aware completion enforcement so:
  - `validate` mode now requires real verification evidence before completion.
  - `plan` mode now requires an actionable plan artifact or step list before completion.
  - explicit repo review requests now seed `review` mode instead of falling back to generic analysis.
- Added review-specific prompt guidance so review turns are findings-first rather than summary-first.
- Focused verification run after each slice with:
  - `go test ./internal/runtime ./internal/react -run 'Test(DetectTaskStateFromInput|RunChatTurn|ValidateTaskCompletion|BuildMessages_)'`

### Next Slice

- Tighten `review` mode so completion requires actual review findings, not just general repo narration.
- Add runtime nudges for explicit review work the same way merge, plan, and validation work already get active steering.

### 2026-03-31 Checkpoint 2

- Tightened `review` mode completion so generic summary-only answers are rejected; review turns must now deliver findings-first output before they can complete.
- Added a review runtime note in `internal/react/loop.go` so explicit review work gets active execution-time steering, not just static task guidance.
- Added focused tests covering:
  - review task-state detection
  - review prompt guidance
  - review completion enforcement
  - review runtime-note injection

### Next Slice

- Start upgrading task state from “plan steps only” to richer active/blocked semantics.
- Keep the implementation log in this file current as each slice lands.

### 2026-03-31 Checkpoint 3

- Extended `PlanStep` with blocker metadata and taught `update_plan` to accept `blocked` as an explicit active state.
- Added validation so blocked steps must include blocker text, preventing vague blocked plans from entering session state.
- Updated plan formatting and prompt rendering so blockers appear in the rendered plan instead of being hidden in implementation-only state.
- Preserved the one-active-step rule by treating `in_progress` and `blocked` as the two valid active-step states.
- Added focused tests for:
  - blocked-step acceptance
  - blocked-step rejection without blocker text
  - blocked-step rendering in prompt/system context

### Next Slice

- Extend runner/runtime guidance to notice blocked plan state and nudge the model toward resolving the blocker or asking the user a focused question.
- Keep the implementation log in this file current as each slice lands.

### 2026-03-31 Checkpoint 4

- Added a blocked-plan runtime note in `internal/react/loop.go`.
- When the active mode is `plan` and the current plan contains a blocked step, Forge now injects a runtime reminder that names the blocker and points the model toward resolving it or using `ask_user_question`.
- Added focused runner coverage so blocked plan state is visible in the system messages sent to the model.

### Next Slice

- Decide whether to keep deepening task-state semantics in the existing plan structure or split them into a dedicated richer task-state type.
- Keep the implementation log in this file current as each slice lands.

### 2026-03-31 Checkpoint 5

- Linked task tracking to completion enforcement for implementation turns.
- When Forge is in `implement` mode and the tracked plan still has an `in_progress` or `blocked` step, it can no longer finish with a generic completion claim like “done” or “finished”.
- Allowed honest blocked-status updates when the current plan is explicitly blocked, so plan blockers do not get misclassified as fake tool failures.
- Added focused completion tests for:
  - rejecting premature implementation completion while work remains active
  - allowing explicit blocked-status updates when the blocker is real task state

### Next Slice

- Start separating reusable task-state helpers from ad hoc completion/prompt checks so this richer behavior does not turn into another monolith.
- Keep the implementation log in this file current as each slice lands.

### 2026-03-31 Checkpoint 6

- Extracted reusable `PlanState` helpers in `internal/react/session.go` for:
  - `ActiveStep`
  - `HasActiveStep`
  - `BlockedStep`
- Switched loop/runtime logic to use those helpers instead of scanning plan steps ad hoc.
- Added helper-level tests in `internal/react/session_test.go` so plan-state semantics are verified once and reused across the stack.

### Next Slice

- Keep pulling duplicated workflow semantics inward so prompt, loop, and completion enforcement share the same task-state model instead of drifting.
- Keep the implementation log in this file current as each slice lands.

### 2026-03-31 Checkpoint 7

- Added a first guardian-review foundation in `internal/agent/tools/guardian_review.go`.
- The guardian currently performs a compact deterministic review:
  - blocks obviously destructive actions
  - warns on high-impact or mutating commands that lack task context
  - warns on file mutations that lack diff/content detail
- Extended `ApprovalGate` to support:
  - a guardian reviewer hook
  - a guardian context provider
  - forcing a prompt when the guardian warns
  - denying before prompt when the guardian blocks
- Wired the guardian into both live and console chat paths, and fixed the console path to share one session between tools, runner, and approval context.
- Added tests for:
  - guardian review decisions
  - approval-gate guardian escalation
  - compact guardian context generation from session state

### Next Slice

- Decide whether to deepen the guardian from deterministic heuristics into a structured review artifact with richer reason codes and UI/event surfacing.
- Keep the implementation log in this file current as each slice lands.

### 2026-03-31 Checkpoint 8

- Added the first bounded local memory package under `internal/memory`:
  - `extract.go`
  - `redact.go`
  - `consolidate.go`
  - `pipeline.go`
- The initial memory pipeline now supports:
  - deterministic extraction from session snapshots
  - secret redaction for obvious token patterns
  - bounded consolidation into a summary plus retained records
- Added prompt plumbing for memory summaries:
  - `Session` / `SessionSnapshot` now carry `MemorySummary`
  - `BuildMessages` injects `Memory summary:` as a normal-priority system overlay
- Added tests for:
  - redacted extraction
  - bounded consolidation
  - pipeline processing
  - prompt inclusion of memory summaries

### Next Slice

- Decide whether to wire the new memory pipeline into session compaction/turn finalization now, or keep it staged until the memory schema is richer.
- Keep the implementation log in this file current as each slice lands.

### 2026-03-31 Checkpoint 9

- Wired the new bounded memory pipeline into both live and console chat bootstrapping in `internal/runtime/chat.go`.
- Added a generic `TurnComplete` hook to the React runner so runtime-owned services can react to completed turns without hard-coding those services into the runner.
- Verified that a turn-complete callback can feed `Session.SetMemorySummary` and that the next turn sees the memory summary in its system overlays.

### Next Slice

- Decide whether memory updates should happen only on successful turns, or whether blocked/error turns should also emit bounded memory records.
- Keep the implementation log in this file current as each slice lands.

### 2026-03-31 Checkpoint 10

- Added an initial skills policy layer in `internal/skills/policy.go`.
- The policy can now suggest a loaded skill from:
  - explicit inferred mode
  - input heuristics
  - current active-skill state
- Added runtime nudge plumbing in `internal/runtime/chat.go` so chat surfaces info-only suggested-skill hints before a turn starts when a matching skill is loaded but not yet active.
- Added tests for:
  - mode-aware skill suggestion
  - heuristic fallback suggestion
  - skipping already-active skills
  - runtime suggested-skill nudge behavior

### Next Slice

- Decide whether to escalate suggested skills into stronger overlays or keep them as UI/runtime nudges only until hooks are in place.
- Keep the implementation log in this file current as each slice lands.

### 2026-03-31 Checkpoint 11

- Added an initial typed hooks overlay path:
  - `internal/hooks/overlays.go`
  - prompt-overlay conversion with provenance labels
- Extended `Session` / `SessionSnapshot` to carry hook overlays, with a setter for runtime-owned overlay state.
- Updated `BuildMessages` so hook overlays are injected as normal system overlays with explicit `[hook:<provenance>]` labeling.
- Added tests for:
  - hook overlay conversion into prompt overlays
  - hook overlay inclusion in prompt assembly

### Next Slice

- Decide whether to begin populating hook overlays from runtime services now, or keep the path staged until there is a clearer external hook/config model.
- Keep the implementation log in this file current as each slice lands.

### 2026-03-31 Checkpoint 12

- Began populating hook overlays from a real runtime service instead of leaving the path dormant.
- Suggested-skill nudges now:
  - surface as info messages for the user
  - populate a session hook overlay before the turn starts
- That means the same runtime-owned suggestion can now be visible to both the user and the model through the shared overlay path.
- Added focused tests for:
  - applying a suggested-skill hook overlay
  - clearing the overlay when no suggestion applies

### Next Slice

- Decide whether other runtime services such as guardian warnings or blocked-plan reminders should migrate from ad hoc notes into the same hook-overlay path.
- Keep the implementation log in this file current as each slice lands.

### 2026-03-31 Checkpoint 13

- Added keyed hook-overlay management on `Session`:
  - `SetHookOverlay`
  - `ClearHookOverlay`
- This avoids the earlier problem where one runtime-owned overlay could replace the entire hook-overlay list.
- Switched suggested-skill overlay handling to the keyed API so future runtime services can add overlays without stomping each other.
- Added tests for overlay upsert and keyed removal semantics.

### Next Slice

- Migrate at least one more runtime-owned reminder from ad hoc runtime notes into the hook-overlay path so the new overlay API carries more than suggested-skill nudges.
- Keep the implementation log in this file current as each slice lands.

### 2026-03-31 Checkpoint 14

- Migrated blocked-plan guidance from an ad hoc mode runtime note into the keyed hook-overlay path.
- The runner now:
  - publishes `plan_blocker` as a runtime-owned hook overlay in plan mode when the current plan is blocked
  - clears that overlay automatically when the blocker no longer applies
- Suggested-skill overlays and blocked-plan overlays now coexist cleanly because both use keyed overlay upsert/clear instead of replacing the entire hook set.
- Added focused tests for:
  - blocked-plan hook-overlay rendering
  - coexistence between blocked-plan and suggested-skill overlays

### Next Slice

- Decide whether guardian warnings should also become hook overlays so approval risk cues are visible to both the model and the user before approval prompts fire.
- Keep the implementation log in this file current as each slice lands.

### 2026-03-31 Checkpoint 15

- Routed guardian review outcomes into the keyed hook-overlay path instead of keeping them implicit inside approval prompting alone.
- Approval-gate guardian reviews now emit structured observer events for:
  - allow
  - warn
  - block
- Runtime chat consumes those events and publishes a high-priority `guardian_warning` overlay when the guardian warns or blocks, including the guardian reason and action summary.
- The same path clears the overlay when a later guardian review is `allow`, preventing stale approval risk guidance from lingering in session context.
- Added focused tests for:
  - guardian observer event emission
  - guardian-warning hook overlay creation
  - guardian-warning overlay clearing on allow

### Next Slice

- Continue migrating runtime-owned reminders off ad hoc notes and into keyed hook overlays, starting with review workflow guidance.
- Keep the implementation log in this file current as each slice lands.

### 2026-03-31 Checkpoint 16

- Migrated review workflow guidance from `RuntimeNote` into the keyed hook-overlay path.
- The runner now publishes `review_guidance` as a runtime-owned high-priority overlay whenever the active task mode is `review`, and clears it automatically outside review mode.
- This leaves `RuntimeNote` for genuinely aggregated workflow state while keeping mode-specific runtime nudges on the same overlay path as:
  - suggested skills
  - blocked-plan guidance
  - guardian warnings
- Updated runner coverage so review guidance is asserted through `[hook:runtime]` overlay rendering instead of a dedicated ad hoc system note.

### Next Slice

- Continue collapsing remaining runtime-only guidance into overlays or a dedicated policy surface, with validation/search workflow nudges as the next candidates.
- Keep the implementation log in this file current as each slice lands.

### 2026-03-31 Checkpoint 17

- Migrated two more transient runtime nudges from `RuntimeNote` into keyed hook overlays:
  - validation-failure guidance
  - same-file search-thrash guidance
- The runner now publishes:
  - `validation_failure` when the most recent validation command failed
  - `search_thrash` when repeated `search`/`code_search` calls keep hitting the same file without a direct read
- Both overlays are high-priority runtime-owned hints and clear automatically once the underlying workflow state no longer applies.
- This leaves `RuntimeNote` increasingly focused on aggregated workflow state such as synthesis/merge state rather than one-off coaching.
- Updated focused runner coverage so validation/search nudges are asserted through `[hook:runtime]` overlay rendering.

### Next Slice

- Reassess the remaining `RuntimeNote` users and decide whether synthesis and git workflow guidance should stay aggregated notes or move behind the same policy/overlay layer.
- Keep the implementation log in this file current as each slice lands.

### 2026-03-31 Checkpoint 18

- Migrated synthesis-budget nudges for `plan` and `analysis` work out of `RuntimeNote` and into the keyed hook-overlay path.
- The runner now publishes a high-priority `synthesis_guidance` overlay whenever exploration has crossed the configured budget and the model should stop researching and synthesize an answer or plan.
- This unifies all transient runtime coaching under the overlay system:
  - suggested skills
  - blocked-plan reminders
  - guardian warnings
  - review guidance
  - validation failure
  - search thrash
  - synthesis guidance
- Left git workflow state on `RuntimeNote` for now because it behaves more like durable workflow context than a transient hint.
- Updated focused runner coverage so plan/analysis exploration nudges are asserted through `[hook:runtime]` overlay rendering.

### 2026-03-31 Checkpoint 19

- Fixed two pre-existing test failures:
  - `looksLikeReviewFindings` was too strict (required literal `"Finding:"` label); broadened to accept any bulleted list with ≥2 items so realistic review output passes without the prescriptive label.
  - Review corpus fixture responses updated to bullet format; plan follow-up fixture response updated to bullet format so both satisfy the completion enforcement rules.
  - `"Current mode: chat"` overlay was injected even in the default mode; suppressed mode overlay for `ModeChat` since surfacing the default adds no value and broke two prompt-assembly tests.
- Migrated git workflow guidance from `RuntimeNote` into the keyed hook-overlay path:
  - Added `gitWorkflowState.overlayContent()` and wired a `git_workflow` keyed overlay in `syncRuntimeOverlays()`.
  - All transient runtime coaching is now on the unified overlay path: suggested skills, plan blocker, synthesis guidance, review guidance, validation failure, search thrash, git workflow.
- Fixed `isSuccessfulGitCommit` to also recognise singular `"file changed"` (single-file commits) alongside `"files changed"`.
- Cleaned up the now-dead `RuntimeNote` infrastructure:
  - Removed all five `runtimeNote()` methods (all returned `""`).
  - Removed redundant `interruptedTurnRuntimeNote` constant and its `SetRuntimeNote` call from `MarkInterrupted()` (the `session.Interrupted` flag already generates the interrupted overlay in `BuildMessages`).
  - Collapsed `syncRuntimeNote()` to a thin wrapper around `syncRuntimeOverlays()`.
- Added focused git workflow overlay tests:
  - overlay appears on unmerged files
  - overlay shifts to shorter merge-active message after conflict resolution
  - overlay clears on successful commit

### Next Slice

- Start Task 9: add TUI-level nudges that surface suggested skills, plan mode, and verification prompts driven by runtime policy and task state.
- Keep the implementation log in this file current as each slice lands.

## Program Structure

This program should be implemented in dependency order:

1. Prompt platform
2. Tool contracts
3. Plan mode + structured questioning
4. Task-state overhaul
5. Approval guardian
6. Memory pipeline
7. Skills policy engine
8. Hooks/overlays
9. UX nudges

The later tasks depend on the earlier architectural layers. Do not start memory, hooks, or nudges before prompt composition and runtime state are in place.

## File Map

- Create: `internal/agent/promptcomposer/composer.go`
  Purpose: assemble core prompt sections and dynamic overlays.
- Create: `internal/agent/promptcomposer/composer_test.go`
  Purpose: verify prompt section ordering, inclusion rules, and budgets.
- Create: `internal/agent/promptcomposer/sections.go`
  Purpose: define static and dynamic prompt sections.
- Modify: `internal/agent/system.go`
  Purpose: shrink the current monolithic system prompt into core sections or adapt it into the new composer.
- Modify: `internal/react/prompt.go`
  Purpose: route runtime task, plan, approval, and memory overlays through the composer.
- Create: `internal/react/mode.go`
  Purpose: define explicit runtime modes and mode transitions.
- Create: `internal/react/tools/enter_plan_mode.go`
  Purpose: add explicit plan-mode entry.
- Create: `internal/react/tools/exit_plan_mode.go`
  Purpose: add explicit plan-mode exit and approval handoff.
- Create: `internal/react/tools/ask_user_question.go`
  Purpose: add structured clarification/questions workflow.
- Modify: `internal/react/tools/update_plan.go`
  Purpose: extend plan state to richer task semantics.
- Modify: `internal/react/session.go`
  Purpose: persist richer task state, mode state, and approval/memory metadata.
- Modify: `internal/react/loop.go`
  Purpose: enforce mode-aware completion, approval, and task-state transitions.
- Create: `internal/agent/tools/guardian_review.go`
  Purpose: compact transcript plus planned-action review for risky approvals.
- Create: `internal/agent/tools/guardian_review_test.go`
  Purpose: verify guardian prompt assembly and risk output parsing.
- Create: `internal/memory/pipeline.go`
  Purpose: orchestrate extraction and consolidation phases.
- Create: `internal/memory/extract.go`
  Purpose: extract per-session/per-thread structured memories.
- Create: `internal/memory/consolidate.go`
  Purpose: maintain bounded local memory artifacts and summary state.
- Create: `internal/memory/redact.go`
  Purpose: redact secrets and sensitive values from stored memory.
- Create: `internal/memory/pipeline_test.go`
  Purpose: verify phase boundaries, retention, and redaction.
- Modify: `internal/skills/runtime.go`
  Purpose: support richer activation state and policy-driven use.
- Create: `internal/skills/policy.go`
  Purpose: layer required/suggested/auto skill decisions with config rules.
- Create: `internal/skills/policy_test.go`
  Purpose: verify skill selection, overrides, and disabled-skill handling.
- Create: `internal/hooks/overlays.go`
  Purpose: define runtime-owned prompt overlays with provenance.
- Create: `internal/hooks/runtime.go`
  Purpose: load and apply hook-derived overlays and reminders.
- Modify: `internal/tui/chatmodel.go`
  Purpose: expose new mode/task/skill suggestions and structured prompts.
- Create: `internal/tui/nudges.go`
  Purpose: centralize suggested plan/skill/verification UI surfaces.
- Modify: `internal/config/config.go`
  Purpose: add config for modes, memory, skills policy, and hook behavior.
- Modify: `internal/config/validate.go`
  Purpose: validate the new config surfaces.

## Task 1: Build the prompt composition platform

**Files:**
- Create: `internal/agent/promptcomposer/composer.go`
- Create: `internal/agent/promptcomposer/composer_test.go`
- Create: `internal/agent/promptcomposer/sections.go`
- Modify: `internal/agent/system.go`
- Modify: `internal/react/prompt.go`

- [ ] **Step 1: Write failing tests for prompt section assembly**

Cover:
- static sections render in stable order
- dynamic overlays can be added without mutating static sections
- section omission works when inputs are empty
- prompt budgeting prefers high-priority overlays

- [ ] **Step 2: Run focused tests to verify failure**

Run: `go test ./internal/agent/... ./internal/react/... -run 'TestPromptComposer|TestBuildMessages'`
Expected: FAIL because Forge does not yet have a prompt composer.

- [ ] **Step 3: Implement the prompt composer**

Implement:
- section registry
- static core sections
- dynamic overlay injection
- prompt budgeting
- migration path from `BuildNativeSystemPrompt`

- [ ] **Step 4: Re-run focused tests to verify pass**

Run: `go test ./internal/agent/... ./internal/react/... -run 'TestPromptComposer|TestBuildMessages'`
Expected: PASS

## Task 2: Add tool contracts for high-leverage tools

**Files:**
- Modify: `internal/react/tools/update_plan.go`
- Create: `internal/react/tools/ask_user_question.go`
- Create: `internal/react/tools/enter_plan_mode.go`
- Create: `internal/react/tools/exit_plan_mode.go`
- Modify: `internal/runtime/chat.go`
- Modify: `internal/runtime/chat_test.go`

- [ ] **Step 1: Write failing tests for tool registration and prompt exposure**

Cover:
- new tools register on the default chat path
- tool descriptions include contract-level usage guidance
- plan/question tools remain auto-approved only where intended

- [ ] **Step 2: Run focused tests to verify failure**

Run: `go test ./internal/react/tools ./internal/runtime -run 'TestRegisterTools.*Plan|TestRegisterTools.*Question'`
Expected: FAIL because these tools and contracts do not yet exist.

- [ ] **Step 3: Implement initial tool-contract surfaces**

Implement:
- explicit plan-mode tool pair
- structured ask-user tool
- stronger task-state contract on `update_plan`

- [ ] **Step 4: Re-run focused tests to verify pass**

Run: `go test ./internal/react/tools ./internal/runtime -run 'TestRegisterTools.*Plan|TestRegisterTools.*Question'`
Expected: PASS

## Task 3: Add explicit runtime modes and plan mode

**Files:**
- Create: `internal/react/mode.go`
- Modify: `internal/react/session.go`
- Modify: `internal/react/loop.go`
- Modify: `internal/react/prompt.go`
- Modify: `internal/react/loop_test.go`

- [ ] **Step 1: Write failing tests for mode transitions**

Cover:
- session enters `plan` mode explicitly
- plan mode changes injected guidance
- plan approval exits into `implement` mode
- ordinary chat stays on the lightweight path

- [ ] **Step 2: Run focused tests to verify failure**

Run: `go test ./internal/react -run 'TestModeTransitions|TestPlanModePrompting'`
Expected: FAIL because modes are not yet first-class.

- [ ] **Step 3: Implement runtime modes and plan-mode state**

Implement:
- mode enum and session persistence
- plan-mode entry/exit
- prompt overlays keyed by mode
- implementation handoff behavior

- [ ] **Step 4: Re-run focused tests to verify pass**

Run: `go test ./internal/react -run 'TestModeTransitions|TestPlanModePrompting'`
Expected: PASS

## Task 4: Upgrade task state beyond basic plans

**Files:**
- Modify: `internal/react/session.go`
- Modify: `internal/react/tools/update_plan.go`
- Modify: `internal/react/prompt.go`
- Modify: `internal/react/loop.go`
- Modify: `internal/react/loop_test.go`

- [ ] **Step 1: Write failing tests for richer task-state semantics**

Cover:
- exactly one active step is enforced
- `blocked` steps persist a blocker reason
- required verification is carried into prompt state
- task completion requires relevant state transitions

- [ ] **Step 2: Run focused tests to verify failure**

Run: `go test ./internal/react -run 'TestTaskState|TestRunner.*Blocked|TestRunner.*Verification'`
Expected: FAIL because task state is currently too shallow.

- [ ] **Step 3: Implement richer task-state model**

Implement:
- `blocked` step status
- single active-step enforcement
- verification metadata
- prompt rendering for task state

- [ ] **Step 4: Re-run focused tests to verify pass**

Run: `go test ./internal/react -run 'TestTaskState|TestRunner.*Blocked|TestRunner.*Verification'`
Expected: PASS

## Task 5: Add the approval guardian

**Files:**
- Create: `internal/agent/tools/guardian_review.go`
- Create: `internal/agent/tools/guardian_review_test.go`
- Modify: `internal/react/loop.go`
- Modify: `internal/agent/event_render.go`

- [ ] **Step 1: Write failing tests for guardian review triggering**

Cover:
- risky actions can trigger guardian review before approval prompt
- guardian sees compact transcript plus exact planned action
- guardian output can block, warn, or allow approval flow

- [ ] **Step 2: Run focused tests to verify failure**

Run: `go test ./internal/agent/tools ./internal/react -run 'TestGuardian|TestApprovalFlow.*Guardian'`
Expected: FAIL because Forge has no guardian reviewer.

- [ ] **Step 3: Implement guardian review path**

Implement:
- compact transcript assembly
- planned-action serialization
- structured guardian risk result
- approval flow integration for risky actions

- [ ] **Step 4: Re-run focused tests to verify pass**

Run: `go test ./internal/agent/tools ./internal/react -run 'TestGuardian|TestApprovalFlow.*Guardian'`
Expected: PASS

## Task 6: Implement the two-phase memory pipeline

**Files:**
- Create: `internal/memory/pipeline.go`
- Create: `internal/memory/extract.go`
- Create: `internal/memory/consolidate.go`
- Create: `internal/memory/redact.go`
- Create: `internal/memory/pipeline_test.go`
- Modify: `internal/react/prompt.go`
- Modify: `internal/runtime/chat.go`

- [ ] **Step 1: Write failing tests for extraction, consolidation, and redaction**

Cover:
- extraction creates structured memory records
- consolidation maintains bounded artifacts
- secret-like values are redacted
- memory summary can be injected into prompt context

- [ ] **Step 2: Run focused tests to verify failure**

Run: `go test ./internal/memory ./internal/react -run 'TestMemory|TestBuildMessages.*Memory'`
Expected: FAIL because the memory subsystem does not yet exist.

- [ ] **Step 3: Implement phase 1 and phase 2 memory flow**

Implement:
- extraction jobs
- consolidation pass
- bounded local artifacts
- summary injection hook
- redaction layer

- [ ] **Step 4: Re-run focused tests to verify pass**

Run: `go test ./internal/memory ./internal/react -run 'TestMemory|TestBuildMessages.*Memory'`
Expected: PASS

## Task 7: Add skills policy and layered activation rules

**Files:**
- Create: `internal/skills/policy.go`
- Create: `internal/skills/policy_test.go`
- Modify: `internal/skills/runtime.go`
- Modify: `internal/tui/chatmodel.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/validate.go`

- [ ] **Step 1: Write failing tests for skill-policy resolution**

Cover:
- required skills override suggestion mode
- disabled skills can be matched by name or path
- runtime can distinguish suggested vs auto vs required activation

- [ ] **Step 2: Run focused tests to verify failure**

Run: `go test ./internal/skills ./internal/config -run 'TestSkillPolicy|TestConfig.*Skills'`
Expected: FAIL because Forge lacks layered skill policy.

- [ ] **Step 3: Implement skill policy engine**

Implement:
- config-based rules
- required/suggested/auto resolution
- runtime activation state
- TUI integration for surfaced suggestions

- [ ] **Step 4: Re-run focused tests to verify pass**

Run: `go test ./internal/skills ./internal/config -run 'TestSkillPolicy|TestConfig.*Skills'`
Expected: PASS

## Task 8: Add hooks and prompt overlays with provenance

**Files:**
- Create: `internal/hooks/overlays.go`
- Create: `internal/hooks/runtime.go`
- Modify: `internal/react/prompt.go`
- Modify: `internal/runtime/chat.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/validate.go`

- [ ] **Step 1: Write failing tests for overlay provenance and ordering**

Cover:
- hooks can add overlays without mutating core prompt sections
- overlay provenance is preserved
- ordering between core, mode, task, memory, and hook overlays is deterministic

- [ ] **Step 2: Run focused tests to verify failure**

Run: `go test ./internal/hooks ./internal/react ./internal/config -run 'TestOverlay|TestPrompt.*Provenance|TestConfig.*Hooks'`
Expected: FAIL because Forge does not yet have hook-driven overlays.

- [ ] **Step 3: Implement overlay-capable hook runtime**

Implement:
- overlay model
- provenance metadata
- config loading
- prompt-composer integration

- [ ] **Step 4: Re-run focused tests to verify pass**

Run: `go test ./internal/hooks ./internal/react ./internal/config -run 'TestOverlay|TestPrompt.*Provenance|TestConfig.*Hooks'`
Expected: PASS

## Task 9: Add user-facing nudges and structured suggestions

**Files:**
- Create: `internal/tui/nudges.go`
- Modify: `internal/tui/chatmodel.go`
- Modify: `internal/tui/chatmodel_test.go`

- [ ] **Step 1: Write failing tests for suggestion rendering**

Cover:
- suggested skill can be surfaced
- suggested plan mode can be surfaced
- suggested verification can be surfaced
- suggestions respect mode and task state

- [ ] **Step 2: Run focused tests to verify failure**

Run: `go test ./internal/tui -run 'TestNudges|TestChatModel.*Suggestion'`
Expected: FAIL because the nudge subsystem does not yet exist.

- [ ] **Step 3: Implement runtime-driven nudges**

Implement:
- suggestion selection logic
- TUI rendering
- links to mode/task/skill state

- [ ] **Step 4: Re-run focused tests to verify pass**

Run: `go test ./internal/tui -run 'TestNudges|TestChatModel.*Suggestion'`
Expected: PASS

## Task 10: Run integration hardening for the new behavior stack

**Files:**
- Modify: `internal/runtime/chat_test.go`
- Modify: `internal/react/loop_test.go`
- Modify: `internal/tui/chatmodel_test.go`
- Modify: `docs/superpowers/specs/2026-03-31-forge-rich-agent-architecture-design.md`
- Modify: `docs/superpowers/plans/2026-03-31-forge-rich-agent-architecture.md`

- [ ] **Step 1: Add end-to-end behavior tests**

Cover:
- lightweight chat path remains direct
- ambiguous implementation tasks enter plan mode
- risky actions can require guardian review
- memory and skill overlays do not corrupt base prompt assembly

- [ ] **Step 2: Run broader verification**

Run: `go test ./internal/...`
Expected: PASS, or isolate legitimate unrelated failures without weakening the stack.

- [ ] **Step 3: Update spec and plan with any implementation-driven adjustments**

Revise:
- file map
- sequencing notes
- risk tradeoffs discovered during implementation

- [ ] **Step 4: Run final targeted verification**

Run: `go test ./internal/agent/... ./internal/react/... ./internal/runtime/... ./internal/tui/... ./internal/skills/... ./internal/memory/... ./internal/hooks/...`
Expected: PASS
