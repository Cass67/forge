# Claude Code Feature Gaps Worth Considering for Forge

This document compares the public analysis in `~/git/claude-code-analysis` with Forge's current documented architecture and code surface. It focuses on capabilities Claude Code appears to have that Forge either does not have yet or has only in a lighter form, plus why each may be worth adding.

Forge already has several comparable foundations: a local terminal-first coding loop, provider/model routing, approvals, preview workflows, typed runtime hooks, MCP/LSP tools, exec sessions, skills, bounded hidden worker delegation, memory summaries, usage/audit artifacts, and a Bubble Tea chat TUI. The gaps below are therefore mostly about production hardening, scale, extensibility, and long-session ergonomics rather than basic coding-agent functionality.

## High-priority gaps

### 1. AI-assisted permission classifier / auto mode

**Claude Code capability:** A multi-stage permission pipeline with explicit modes (`default`, `acceptEdits`, `plan`, `bypassPermissions`, `dontAsk`, `auto`) and a two-stage LLM safety classifier for auto-approving or denying risky actions. The classifier first runs a cheap fast pass, then a deeper deterministic reasoning pass when needed. It also tracks repeated denials and falls back to manual approval.

**Forge today:** Forge has approvals, yolo mode, safety checks, preview helpers, and typed hooks, but not an equivalent dedicated side-model permission classifier that can safely reduce prompts while still blocking ambiguous or dangerous actions.

**Why we might want it:**

- Reduces approval fatigue without making `--yolo` an all-or-nothing trust jump.
- Creates a middle ground between manual approval and blanket bypass.
- Gives enterprise/team users a clearer story for unattended or semi-attended workflows.
- Lets Forge encode safety policy in one auditable classifier prompt and test corpus instead of scattering heuristics.

**Possible Forge shape:** Add an `auto` permission mode where static rules run first, then a small/cheap classifier decides `allow`, `deny`, or `ask`. Keep bypass-immune checks for destructive file/system operations. Log classifier decisions for auditability.

### 2. Rich scoped permission and policy rules

**Claude Code capability:** Permission rules can be sourced from user, project, local, managed policy, CLI flags, SDK messages, and session state. Rules support allow/deny/ask behavior at tool and content levels, e.g. shell subcommand patterns.

**Forge today:** Forge has user-controlled approvals and config, but the documented config precedence is mostly env/config/auth/defaults. It does not appear to expose a comparable managed/project/session permission-rule cascade with allow/deny/ask semantics per tool pattern.

**Why we might want it:**

- Makes Forge safer in shared repositories: teams can commit project rules for dangerous commands.
- Enables admin-managed restrictions without forking Forge.
- Lets users permanently approve common safe operations while keeping risky ones interactive.
- Improves compatibility with Claude Code-style mental models for users migrating over.

**Possible Forge shape:** Add `[permissions]` blocks in Forge config with scoped files (`~/.config/forge`, repo-local, managed path/env override) and rules like `allow = ["read", "search", "bash(go test:*)"]`, `deny = ["bash(rm:*)"]`, `ask = ["write(**/*.go)"]`.

### 3. Multi-tier context compaction

**Claude Code capability:** Six compaction strategies: microcompaction of old tool results, provider cache-aware edits, reactive prompt-too-long recovery, session-memory compaction, full forked-agent summarization, and user-selected partial compaction. It uses thresholds, warning states, circuit breakers, and post-compaction reinjection of important attachments/skills/plans.

**Forge today:** Forge has bounded memory summaries and compacted history in prompt assembly, but not an explicit multi-tier compaction state machine with prompt-too-long recovery, partial compaction, cache-aware microcompaction, or circuit breakers.

**Why we might want it:**

- Long coding sessions are where local agents become most valuable; context loss or prompt-too-long failures break trust.
- Microcompaction can preserve recent reasoning while removing bulky tool output.
- Circuit breakers prevent loops when a session cannot be compacted successfully.
- Partial/user-driven compaction gives power users control over what gets forgotten.

**Possible Forge shape:** Introduce a compaction manager with thresholds per model, old-tool-result clearing, full summarization fallback, prompt-too-long retry behavior, and explicit `/compact` controls.

### 4. First-class plugin marketplace / plugin lifecycle

**Claude Code capability:** A full plugin system with marketplace discovery, installation from npm/GitHub/git/local sources, versioned cache, manifest validation, components (commands, agents, skills, hooks, MCP servers, output styles), dependency resolution, homograph/path-traversal protections, enterprise policy, and offline cache behavior.

**Forge today:** Forge has a skills system and MCP support, and there are docs/plans around plugin compatibility, but no equivalent production plugin lifecycle or marketplace/cache/dependency model appears to be present.

**Why we might want it:**

- Skills alone are useful, but plugins would let teams package commands + skills + hooks + MCP wiring together.
- Versioned plugin cache makes workflows reproducible.
- A manifest and policy layer would make third-party extensions safer.
- Marketplace-compatible packaging could grow Forge's ecosystem without bloating core.

**Possible Forge shape:** Start with local/Git plugin manifests that can contribute skills, slash commands, hooks, and MCP server definitions. Add versioned cache and policy checks before considering a public marketplace.

### 5. First-class team/swarm orchestration

**Claude Code capability:** Leader/follower swarms with tmux, iTerm2, and in-process backends; file-based mailbox IPC; permission bridging back to the leader; agent lifecycle management; team metadata; and worker-to-leader communication.

**Forge today:** Forge has bounded hidden worker delegation via child agents, and docs around multi-agent next steps, but not visible first-class teams with separate panes/processes, mailbox IPC, or leader-mediated permission flows.

**Why we might want it:**

- Makes larger tasks easier to parallelize: one worker investigates tests, another reads architecture, another drafts edits.
- Separate panes/processes improve observability and user trust compared with invisible delegation.
- Permission bridging keeps the human in control even when workers act independently.
- Could turn Forge from a single-agent tool into a team-oriented local workbench.

**Possible Forge shape:** Add a `forge team` or chat-mode team panel that spawns named workers with bounded tasks, shared scratch/artifacts, and leader-approved tool use. Start in-process; add tmux/iTerm later.

## Medium-priority gaps

### 6. Terminal rendering engine with frame diffing

**Claude Code capability:** A custom React/Ink terminal rendering pipeline with Yoga layout, screen buffers, frame diffing, and efficient idle rendering.

**Forge today:** Forge uses Bubble Tea for the live chat UI. It has strong TUI pieces (model/provider pickers, approvals, stats, trace), but not a custom diffing renderer of the same depth.

**Why we might want it:**

- Better performance on long transcripts and busy streaming/tool-output screens.
- More flexible layout primitives for future team panels, timelines, and trace views.
- Less flicker and lower terminal bandwidth.

**Caution:** This is expensive and may not beat Bubble Tea's cost/benefit. Prefer targeted transcript virtualization and render diff optimizations before a full renderer rewrite.

### 7. Enterprise-grade settings and policy infrastructure

**Claude Code capability:** Five-scope settings/policy merge (`managed > user > project > local > flag` in the analysis), with enterprise controls for plugins, permissions, and runtime gates.

**Forge today:** Forge has config files, auth storage, env overrides, custom provider files, and some precedence rules, but no broad enterprise policy layer.

**Why we might want it:**

- Teams need consistent defaults for providers, models, permissions, plugin allowlists, telemetry, and secret handling.
- Managed policy enables safe adoption in companies without relying on each developer's local config.
- Scope merging lets project-specific behavior be explicit and reviewable.

**Possible Forge shape:** Define a general config merge model for managed/user/project/local/session scopes, then migrate providers, permissions, skills/plugins, and hooks onto it over time.

### 8. Secret scanning integrated into permissions

**Claude Code capability:** The analysis describes gitleaks-style secret scanning rules integrated with the broader security model.

**Forge today:** Forge has security rules in repo guidance and ignore guards, but there is no obvious built-in pre-write/pre-command secret scanner that blocks accidental exfiltration or commits.

**Why we might want it:**

- Prevents accidental printing, editing, or committing of credentials.
- Supports Forge's local-agent trust story.
- Complements approval prompts: users should not need to notice every secret manually.

**Possible Forge shape:** Add lightweight secret scanning to read/write/search/tool-output paths and git commit/diff workflows, with redaction and block/ask policies.

### 9. Bash parser and command-risk AST

**Claude Code capability:** A hand-rolled bash parser flags dangerous AST node types, blocked builtins, and command-injection patterns before execution.

**Forge today:** Forge has command tools and approvals, but not a comparable shell AST safety analyzer.

**Why we might want it:**

- Static command strings are easy to misjudge visually, especially with pipes, substitutions, redirects, and encoded payloads.
- A parser can downgrade safe commands (`go test`, `rg`) and highlight risky ones (`curl | sh`, destructive globs).
- Improves the quality of auto permission decisions.

**Possible Forge shape:** Begin with a conservative command classifier for common shell constructs, then add parser-backed checks for substitutions, remote-code execution, destructive paths, and credential access.

### 10. Session teleport / portable state transfer

**Claude Code capability:** Session teleportation using git bundles/state transfer across environments.

**Forge today:** Forge writes session artifacts and can show/list sessions, but there is no clear portable session handoff mechanism that recreates worktree state plus conversation context elsewhere.

**Why we might want it:**

- Useful for moving from laptop to remote workstation/CI reproduction machine.
- Helps share a failing agent session with another developer.
- Supports durable recovery after local environment issues.

**Possible Forge shape:** Add `forge export-session` that captures transcript, compacted memory, relevant artifacts, and a git bundle/patch set; add `forge import-session` to resume.

## Lower-priority / situational gaps

### 11. Browser/computer-use integration

**Claude Code capability:** Analysis documents screen capture, input automation, Chrome extension/DOM integration, and computer-use protocols.

**Forge today:** Forge focuses on local repositories and terminal tooling; no comparable browser/computer-use layer is apparent.

**Why we might want it:**

- End-to-end web app debugging often requires browser inspection, screenshots, DOM state, and console errors.
- Could make Forge more useful for frontend work without asking users to paste browser state manually.

**Caution:** This expands Forge's trust boundary substantially. Prefer explicit, opt-in browser debugging tools over general desktop automation.

### 12. Voice input and coordinator voice workflows

**Claude Code capability:** The analysis references voice input and coordinator voice/team policy work.

**Forge today:** No voice interface is apparent.

**Why we might want it:**

- Nice for pair-programming and accessibility.
- Potentially useful in team/swarm mode for quick high-level steering.

**Caution:** Not central to Forge's coding-agent reliability. Defer until core session/team/security gaps are stronger.

### 13. GrowthBook-style runtime feature flags and kill switches

**Claude Code capability:** Large build-time and runtime feature-flag system with remote gates and kill switches.

**Forge today:** Forge has config and flags, but not a remote experimentation/kill-switch infrastructure.

**Why we might want it:**

- Safer rollout for high-risk capabilities like auto permissions, plugins, or team agents.
- Allows disabling a dangerous feature quickly without shipping a new binary.

**Caution:** Remote flags add operational complexity and may conflict with Forge's local-first posture. A local/managed policy kill-switch model may be enough.

## What Forge already covers reasonably well

- **Local repository tools:** read/write/edit/search/glob/apply patch/git/command/web/image/LSP/MCP are present in Forge's tool surface.
- **Provider/model routing:** Forge supports subscription-backed and API-key-backed providers plus OpenAI-compatible custom providers.
- **Interactive TUI:** Forge has chat UI, model/provider pickers, approvals, stats, nudges, trace views, and exec sessions.
- **Hooks:** Forge has typed runtime hooks and overlays, a strong base for policy and workflow extensions.
- **Skills:** Forge has a skills subsystem, though not the broader plugin lifecycle described above.
- **Delegation:** Forge can use bounded hidden workers, but this is not the same as first-class visible swarm/team orchestration.
- **Artifacts and observability:** Forge records session artifacts, audit logs, summaries, and usage/perf data.

## Suggested implementation order

1. **Scoped permission rules**: foundational, testable, and useful immediately.
2. **Command-risk analyzer + secret scanner**: improves safety before adding more automation.
3. **Auto permission classifier**: build on rules/analyzer; keep conservative `ask` fallback.
4. **Compaction manager**: protects long sessions and high-context work.
5. **Local plugin manifest system**: start without marketplace/network complexity.
6. **Visible team mode**: reuse hidden worker infrastructure, then add IPC/panes.
7. **Session export/import**: valuable once sessions/teams become more durable.
8. **TUI performance upgrades**: optimize specific pain points before considering renderer replacement.

## Notes on TDD for future implementation

This document is analysis only, so no production code was changed. For any implementation above, follow TDD strictly: write one failing test for the behavior first, verify the expected failure, implement the smallest passing slice, then refactor while green.
