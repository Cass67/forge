# Tool Enforcement Survey For The Latest Forge Failure

## Purpose

This document compares how CCI, Codex, OpenCode, and DeepSeek TUI prevent or contain the failure class described in `latest-failure-11May.md`.

The failure class is:

```text
User asks for a scoped artifact and git operation.
Agent writes the artifact, delegates follow-up, commits unrelated files, fails to push, then overwrites the artifact with control-plane failure text.
```

The key question is not whether each system has better prompts. The key question is where each system moves behavior out of model discretion and into runtime-enforced structure.

## Executive Finding

None of the surveyed tools fully solves scoped `write -> commit only these files -> push this branch -> verify remote` as a first-class transaction.

The reliable tools feel more dependable because they combine several partial controls:

- Tool affordances are narrower than raw shell where possible.
- Permission and approval systems interrupt high-risk side effects.
- Read-only subagents are actually deprived of write tools.
- Child-agent output is treated as a report, not proof.
- Snapshots or side-git state make file edits reversible.
- Durable session records make state inspectable after failures.
- Diagnostics and verification evidence are surfaced close to the action.

Forge has implemented many of those same pieces, but the latest failure shows the missing layer: user-intent-scoped side-effect transactions. The runtime still lets the model improvise the sequence after the intent is known.

## Failure Lens

For this class of bug, the relevant safeguards are:

- Does the tool prevent unrelated files from being staged or committed?
- Does the runtime distinguish user artifact content from child-agent/control-plane reports?
- Does a child handoff block final synthesis until the parent verifies state?
- Are write/edit/patch operations checkpointed or reversible?
- Are shell/git operations permissioned, sandboxed, or semantically constrained?
- Does final success require evidence, not narrative?
- Is the whole workflow tested as one transaction under dirty-worktree conditions?

## CCI / Claude Code

### What It Enforces

CCI relies heavily on prompt contracts, permissions, specialized agents, and file checkpoints.

Git commit guidance is explicit. The Bash prompt says commits must only happen when requested, destructive git commands are forbidden unless explicitly requested, hooks must not be skipped, and specific file staging is preferred over `git add -A` or `git add .` because broad adds can include secrets or binaries (`/Users/cass/git/cci/src/tools/BashTool/prompt.ts:81-118`).

The `/commit` command narrows the model's available tool patterns to `Bash(git add:*)`, `Bash(git status:*)`, and `Bash(git commit:*)`, and injects current status, full diff, branch, and recent commits into the prompt (`/Users/cass/git/cci/src/commands/commit.ts:6-10`, `20-54`). This is not a semantic commit validator, but it does reduce the command surface during commit creation.

The permission pipeline has hard rule checks before general allow behavior. It checks whole-tool deny rules, ask rules, tool-specific checks, and bypass-immune safety checks (`/Users/cass/git/cci/src/utils/permissions/permissions.ts:1060-1155`). That matters because side effects are not only prompt-mediated; they go through a permission decision layer.

Read-only subagents are genuinely constrained. The Explore agent is explicitly read-only and disallows edit/write tools in its agent definition (`/Users/cass/git/cci/src/tools/AgentTool/built-in/exploreAgent.ts:24-56`, `64-73`). It also prohibits mutating shell commands such as `mkdir`, `touch`, `rm`, `cp`, `mv`, `git add`, and `git commit` in its prompt.

CCI has a verification specialist with a strong adversarial contract. It is prohibited from modifying project files, installing dependencies, or running git write operations, and its output format requires actual command output for each check (`/Users/cass/git/cci/src/tools/AgentTool/built-in/verificationAgent.ts:10-22`, `42-52`, `81-129`). This is important because verification is a separate role with fewer side-effect powers.

CCI also has file checkpointing. `fileHistoryTrackEdit` is called before edits and writes so prior contents can be restored (`/Users/cass/git/cci/src/utils/fileHistory.ts:63-99`).

Child/fork behavior is structured. The agent prompt warns not to peek at fork output before completion and not to fabricate results (`/Users/cass/git/cci/src/tools/AgentTool/prompt.ts:91-95`). Fork children must report in a structured shape beginning with `Scope:` and include changed files only when they actually modified files (`/Users/cass/git/cci/src/tools/AgentTool/forkSubagent.ts:171-198`). A handoff classifier can warn the parent when a subagent performed suspicious actions (`/Users/cass/git/cci/src/tools/AgentTool/agentToolUtils.ts:390-480`).

### Remaining Gap

CCI does not appear to have a universal runtime check that compares staged files to the user's intended file allowlist. The commit flow says to inspect and stage relevant files, but broad staging remains possible if a permitted Bash command runs it.

It also does not provide a generic artifact type that prevents a child report from being copied into a user artifact path. It uses prompts, structured reports, and warnings, not hard provenance boundaries.

### Lesson For Forge

CCI's useful pattern is not "let the model decide." It is constrained agent roles plus narrow command phases plus a separate verification role. Forge should copy the role separation, but go further for git: make the commit operation a first-class transaction instead of a shell sequence.

## Codex

### What It Enforces

Codex has strong command approval and sandbox routing, especially around shell execution and patches.

Approval policy is explicit. Under `UnlessTrusted`, only known-safe commands that only read files are auto-approved; everything else asks (`/Users/cass/git/codex/codex-rs/protocol/src/protocol.rs:900-930`). Sandbox policy is also explicit: `read-only`, `workspace-write`, `danger-full-access`, and `external-sandbox` (`/Users/cass/git/codex/codex-rs/protocol/src/protocol.rs:990-1042`). Workspace-write also protects metadata paths such as `.codex` and `.git` (`/Users/cass/git/codex/codex-rs/protocol/src/protocol.rs:1044-1080`).

Tool execution runs through a central orchestrator: determine approval requirement, request approval if needed, choose sandbox, run, and only escalate after sandbox denial with a fresh approval path (`/Users/cass/git/codex/codex-rs/core/src/tools/orchestrator.rs:145-386`). This is a major difference from letting shell commands run as ordinary model text.

Codex's safe git command list is read-only. It only treats `git status`, `git log`, `git diff`, `git show`, and read-only forms of `git branch` as safe (`/Users/cass/git/codex/codex-rs/shell-command/src/command_safety/is_safe_command.rs:171-196`). Unsafe global git options such as `--git-dir`, `--work-tree`, `--exec-path`, and config override flags are excluded from the safelist (`is_safe_command.rs:233-291`). Therefore `git add`, `git commit`, and `git push` are not auto-approved as safe read-only commands.

Patches receive their own safety check. Empty patches are rejected, patches outside writable roots ask or reject depending on policy, and auto-approval only happens when the sandbox can enforce the write boundary (`/Users/cass/git/codex/codex-rs/core/src/safety.rs:33-116`, `138-193`).

Codex also treats multi-agent communication as protocol data. `wait_agent` in multi-agent v2 explicitly says it does not return child content; it returns a mailbox update or timeout summary (`/Users/cass/git/codex/codex-rs/core/src/tools/handlers/multi_agents_spec.rs:224-228`). The wait implementation returns a structured wait result rather than dumping final content (`/Users/cass/git/codex/codex-rs/core/src/tools/handlers/multi_agents_v2/wait.rs:79-101`). Inter-agent communication is represented as a typed protocol object with author, recipient, content, and `trigger_turn` (`/Users/cass/git/codex/codex-rs/protocol/src/protocol.rs:797-845`).

Child agents inherit live runtime policy, not stale config. Spawn config is built from the active turn's model, provider, approval policy, cwd, sandbox, and permission profile (`/Users/cass/git/codex/codex-rs/core/src/tools/handlers/multi_agents_common.rs:198-280`).

Codex tracks turn diffs from committed `apply_patch` deltas without rereading arbitrary workspace state (`/Users/cass/git/codex/codex-rs/core/src/turn_diff_tracker.rs:16-24`, `49-67`). This provides a structured view of what the current turn changed.

### Remaining Gap

Codex rollback explicitly does not revert filesystem changes: clients are responsible for undoing disk edits (`/Users/cass/git/codex/codex-rs/protocol/src/protocol.rs:753-757`).

More importantly for the Forge incident, Codex does not appear to provide a first-class scoped commit/push transaction either. Mutating git commands are not safelisted, but if approved under policy, there is no semantic check that the staged set matches the user's requested file set.

### Lesson For Forge

Codex's useful pattern is the central orchestrator and read-only-git-by-default posture. Forge should not expose git mutation as generic shell if the user asked for a scoped commit. The runtime should own commit semantics the way Codex owns patch safety.

## OpenCode

### What It Enforces

OpenCode is explicit that its permission system is not a security sandbox. Its security document says permissions are a UX awareness feature and true isolation requires Docker or a VM (`/Users/cass/git/opencode/SECURITY.md:15-19`). That honesty matters: OpenCode does not pretend that prompts equal enforcement.

OpenCode still has useful structural controls. File writes ask for edit permission and include the diff in metadata before writing (`/Users/cass/git/opencode/packages/opencode/src/tool/write.ts:53-64`). After writing, it touches LSP state and returns diagnostics for the edited file and a capped number of other files (`write.ts:74-90`).

Shell execution asks permission based on parsed command patterns and external directories (`/Users/cass/git/opencode/packages/opencode/src/tool/shell.ts:267-287`). Permission evaluation is simple and deterministic: last matching wildcard rule wins, otherwise the action defaults to `ask` (`/Users/cass/git/opencode/packages/opencode/src/permission/evaluate.ts:9-15`).

Agent defaults narrow roles. The default build agent allows broad tool use, but asks for external directories and `.env` reads; plan mode denies edit tools except plan files; explore mode denies everything then allows read/search/web/bash-style research tools (`/Users/cass/git/opencode/packages/opencode/src/agent/agent.ts:90-189`).

Task/subagent execution creates a real child session with `parentID`, carries selected parent deny/external-directory rules forward, and can disable nested task/todo or primary tools (`/Users/cass/git/opencode/packages/opencode/src/tool/task.ts:63-100`, `134-163`). The parent receives a `task_id` and a bounded `<task_result>` wrapper rather than raw continuous child transcript.

OpenCode snapshots file state using a separate git directory under OpenCode data, not the user's `.git` (`/Users/cass/git/opencode/packages/opencode/src/snapshot/index.ts:81-90`). It records snapshots at step start and step finish, and records patch parts for changed files (`/Users/cass/git/opencode/packages/opencode/src/session/processor.ts:427-497`). Revert restores file states from patch parts and snapshots (`/Users/cass/git/opencode/packages/opencode/src/session/revert.ts:43-102`).

Durable session state is stored in SQLite: sessions, messages, parts, summaries, revert state, and permissions are persisted (`/Users/cass/git/opencode/packages/opencode/src/session/session.sql.ts:16-131`). The sync layer has sequence checks for replay, rejecting mismatched event sequences (`/Users/cass/git/opencode/packages/opencode/src/sync/index.ts:56-121`).

### Remaining Gap

OpenCode git mutations run through the generic shell tool. I did not find a Git-aware commit tool that checks an intended file set, blocks `git add .`, or verifies branch/push/remote state before allowing success.

The Task tool still returns child text as a tool result. It is wrapped, but there is no hard artifact provenance rule preventing the parent from writing that report into a target document.

### Lesson For Forge

OpenCode feels reliable not because it magically trusts the model. It keeps persistent state, snapshots file changes, limits roles, asks before side effects, and makes reversions cheap. But it also documents the boundary honestly: permissions are not isolation. Forge needs that same honesty plus a stronger git transaction layer.

## DeepSeek TUI

### What It Enforces

DeepSeek TUI has several controls directly relevant to the Forge failure.

Plan mode blocks shell and code execution tools (`/Users/cass/git/deepseek/crates/tui/src/core/engine/turn_loop.rs:1115-1129`). Tool calls derive approval requirements from the tool spec; approval-required tools emit `ApprovalRequired` and wait for approve, deny, or retry-with-policy (`turn_loop.rs:1585-1651`).

`exec_shell` is always approval-required (`/Users/cass/git/deepseek/crates/tui/src/tools/shell.rs:1723-1733`). It runs a command safety analyzer and, outside auto-approve/YOLO mode, blocks commands classified as dangerous (`shell.rs:1792-1819`). The analyzer blocks multi-line/null-byte commands, catches explicit destructive patterns, treats command chaining and substitution as approval-required, blocks `curl|sh`, escalates `rm -r/-f`, and requires approval for `git push` (`/Users/cass/git/deepseek/crates/tui/src/command_safety.rs:572-724`).

DeepSeek provides read-only git tools. `git_status` and `git_diff` are read-only, sandboxable, auto-approved wrappers scoped to the workspace; `git_diff` supports staged diffs with `cached=true` (`/Users/cass/git/deepseek/crates/tui/src/tools/git.rs:1-4`, `50-56`, `124-146`, `163-165`). This encourages safe inspection without generic shell.

File-mutating tools get per-tool snapshots before `write_file`, `edit_file`, and `apply_patch` execute (`/Users/cass/git/deepseek/crates/tui/src/core/engine/turn_loop.rs:1656-1672`). The engine also takes pre-turn and post-turn snapshots (`/Users/cass/git/deepseek/crates/tui/src/core/engine.rs:912-923`, `1134-1145`). The snapshot design is side-git based and non-fatal, explicitly intended for `/restore` and `revert_turn` (`/Users/cass/git/deepseek/crates/tui/src/core/turn.rs:6-14`, `137-181`). `revert_turn` requires approval and restores workspace files without modifying conversation history (`/Users/cass/git/deepseek/crates/tui/src/tools/revert_turn.rs:1-10`, `33-39`, `56-65`, `111-125`).

DeepSeek's subagent tool states the trust model directly: `agent_result` is a self-report, not verified fact. It gives a table of side effects and the tools that should be used to re-verify them, including `git_status` / `git_diff` for git operations and `read_file` / `list_dir` for file claims (`/Users/cass/git/deepseek/crates/tui/src/tools/subagent/mod.rs:1882-1889`). A regression test asserts that after an `agent_result` claims a file was written, the parent must call `read_file` before claiming success (`/Users/cass/git/deepseek/crates/tui/tests/integration_mock_llm.rs:413-479`).

Subagent cwd is constrained to the parent workspace (`/Users/cass/git/deepseek/crates/tui/src/tools/subagent/mod.rs:1985-2010`). Child runtime setup inherits parent approval state and supports bounded depth/cancellation behavior in the same subsystem (`subagent/mod.rs:1974-2019`).

DeepSeek also has durable task records with gates, attempts, artifacts, GitHub events, tool summaries, and timelines (`/Users/cass/git/deepseek/crates/tui/src/task_manager.rs:180-223`). The `task_gate_run` tool runs approved verification gate commands and records exit code, status, classification, duration, and log artifacts (`/Users/cass/git/deepseek/crates/tui/src/tools/tasks.rs:230-379`).

### Remaining Gap

DeepSeek does not have a dedicated safe staging/commit/push tool. Mutating git operations still go through approval-gated shell. `git add` and `git commit` are not semantically checked against a task-scoped allowlist, and auto-approve/YOLO can bypass safety blocking for dangerous shell classifications (`shell.rs:1792-1819`).

Snapshots are intentionally non-fatal. That is good for responsiveness but means rollback is best-effort, not guaranteed (`core/turn.rs:137-181`).

### Lesson For Forge

DeepSeek's strongest applicable pattern is explicit self-report distrust. It teaches the parent that child output is not proof and has a regression test for re-verification. Forge needs to make that runtime-enforced for side effects, not only prompt/test enforced.

## Why OpenCode And DeepSeek Can Feel Reliable While Letting The Model Drive

They do not actually let the model drive everything.

They let the model choose intent and next steps inside a narrowed operating envelope:

- The model can ask to run tools, but tools carry approval requirements.
- The model can edit files, but writes produce diffs, diagnostics, and snapshots.
- The model can delegate, but child sessions are tracked separately.
- The model can receive child reports, but mature systems mark them as reports, not facts.
- The model can inspect git freely through read-only wrappers, while mutation stays behind shell approval.
- Session state and file deltas are durable enough that the UI/runtime can recover truth after failure.

Reliability comes from making the easy path the safe path and making risky actions observable, reversible, or approval-gated.

Where they still rely on the model is exactly where Forge failed: semantic git transactions. They generally do not know that the intended commit must contain exactly `FORGE_VS_CODEX.md`. They know `git push` is risky, but not whether this specific push satisfies the user request.

## Why Forge Still Fails After Planning And Hardening

Forge planned and implemented many correct components:

- Durable event logging.
- Tool schema validation.
- Agent handoff state.
- Parent-visible child state.
- Checkpoints.
- Diagnostics.
- Recovery metadata.

But the latest failure crossed a boundary those components do not enforce.

Forge knew the user's target artifact. Forge knew the child handoff reported an incident. Forge knew the task had an unresolved commit/push outcome. But the runtime did not bind those facts into a transaction that constrained the next side effect.

The failure was therefore not:

- A missing prompt.
- A missing planning document.
- A missing event log.
- A missing child-state display.

The failure was:

```text
Known user intent was not converted into mandatory runtime gates.
```

That is why signing off component tests produced confidence but not reliability. The tests proved pieces. The user-visible workflow needed a transaction test.

## Plan To Fix This Once And For All

### 1. Add A Runtime Intent Contract

When the user asks for a scoped side effect, Forge must create an `IntentContract` before mutation.

Minimum fields:

- Requested artifact paths.
- Allowed file paths or path globs.
- Explicitly requested side effects: write, verify, commit, push, PR, issue close, etc.
- Forbidden side effects by default.
- Target branch and remote when git operations are requested.
- Required verification gates.
- Whether child agents may mutate, commit, or push.

This contract must be runtime state, not prose in the model context.

### 2. Taint Control-Plane Text Separately From Artifact Content

Child reports, handoff blocks, incident summaries, tool errors, and runtime diagnostics must be typed as control-plane content.

Rules:

- Control-plane content cannot be written to an artifact path unless the user explicitly asks for a log/report of that control-plane event.
- Artifact content must come from a user request, a model draft, or an approved child artifact payload, not from the child handoff wrapper.
- If the parent tries to write a handoff report into the requested artifact path, the runtime should block it with recoverable feedback.

This would have prevented `FORGE_VS_CODEX.md` from being overwritten with the child agent's failure report.

### 3. Replace Shell-Based Git Mutation With First-Class Git Transaction Tools

Add scoped tools for git mutation:

- `git_stage_scoped` stages only allowlisted files or hunks.
- `git_commit_scoped` requires a clean staged set that exactly matches the intent contract.
- `git_push_scoped` requires branch/remote/ref confirmation and verifies the remote contains the commit after push.
- `git_transaction_status` reports unresolved gates and why final success is blocked.

These tools should not be wrappers around arbitrary shell strings. They should inspect repository state with library calls or fixed git commands.

Hard gates:

- Refuse if staged files include anything outside the allowlist.
- Refuse if unstaged changes in allowlisted files would make commit content ambiguous.
- Refuse if the commit file list differs from the allowlist, unless the user approved expansion.
- Refuse push if current branch is not the intended branch.
- Refuse final success until local commit and remote ref are verified.

### 4. Remove Git Mutation From Child Agents By Default

Child agents should be read-only unless the parent explicitly grants a scoped mutation contract.

Default child permissions:

- Research children: no writes, no commits, no pushes.
- Implementation children: may edit only paths granted by the parent contract.
- Commit/push remains parent-owned unless the user explicitly says the child should own it.

If a child does mutate unexpectedly, the parent enters incident mode and cannot finalize until it inspects status, diffs, and relevant file contents.

### 5. Make Final Synthesis Depend On Gate State

Final responses that imply success must be blocked if any required gate is unresolved.

Examples:

- User asked to write a file: runtime verifies the path exists and content is not a control-plane report.
- User asked to commit: runtime verifies commit hash and commit file list.
- User asked to push: runtime verifies remote ref contains the commit.
- Child claimed success: runtime requires independent verification using tools appropriate to the side effect.

The model can still explain failure or ask the user for a decision. It cannot claim completion while gate state says incomplete.

### 6. Add Dirty-Worktree Acceptance Tests

The acceptance test must reproduce the ugly case, not the happy path.

Test shape:

```text
given a dirty worktree with unrelated changes
and a user asks for one document to be written, committed to main, and pushed
and a child agent reports an accidental over-commit plus unresolved push
then Forge must not overwrite the document with handoff text
and Forge must not commit unrelated files
and Forge must not claim completion
and Forge must surface the unresolved gate with exact state evidence
```

Additional cases:

- Child claims a file was written but it does not exist.
- Child commits extra files.
- Staged set differs from intent allowlist.
- Branch differs from requested target.
- Push fails or remote does not contain the commit.
- Existing unrelated dirty changes are preserved and never reverted automatically.

### 7. Treat This As The New Reliability Kernel

The previous reliability kernel made turns durable and observable. The next kernel must make side effects transactional.

Definition of done:

- Scoped side effects have runtime contracts.
- Git mutation cannot happen through unconstrained shell when a scoped git transaction is active.
- Child handoff text cannot become artifact content by accident.
- Parent final success is gate-checked.
- Dirty-worktree live acceptance passes against the real binary.
- The failure in `latest-failure-11May.md` is reproduced and prevented by an automated test.

## Conclusion

The other tools are not reliable because they found the perfect prompt. They are reliable to the extent that they constrain the model with typed tools, approval gates, snapshots, durable state, role-specific permissions, and verification evidence.

Forge has already implemented many of those pieces. The remaining gap is that scoped side effects are not yet transactions. Until Forge binds user intent to runtime-enforced write/commit/push gates, every plan will eventually degrade into another patch after the model mis-sequences a side effect.
