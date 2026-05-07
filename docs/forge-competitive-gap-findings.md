# Forge Competitive Assessment

> Scope: Practical competitive assessment of Forge against Claude Code, OpenAI Codex / ChatGPT Codex-style coding agents, OpenCode, and GitHub Copilot CLI. Competitor details are general-knowledge and **not live-web-verified** because no web search was used. Forge findings are grounded in repository evidence with file/path citations. No secrets, token files, or credential stores were inspected.

## Executive Summary

Forge is already more than a thin CLI wrapper around an LLM. The repository shows a terminal-first local coding agent with a host-owned runtime, provider switching, approvals, exec sessions, file/edit/patch/search/LSP/git tools, MCP, OpenCode plugin compatibility, skills, hooks, memory, usage/perf reporting, and an older writer/auditor pipeline. The strongest current strategic position is:

> **Forge as a local, provider-flexible, extensible coding-agent runtime that can become a polished Claude Code/OpenCode-style terminal agent.**

Forge's main competitive weakness is not the absence of primitives. It is productization: the default loop, onboarding, documentation, task durability, GitHub workflow integration, and enterprise/GitHub distribution are less obviously complete than the market leaders. The gap is especially visible against:

1. **Claude Code**: likely stronger polished terminal UX, model/runtime pairing, and low-friction coding loop.
2. **OpenAI Codex / ChatGPT Codex-style agents**: likely stronger ChatGPT-account integration, hosted/semi-hosted task workflows, and task-to-result product surfaces.
3. **OpenCode**: likely clearer ecosystem identity and mental model for terminal-agent users.
4. **GitHub Copilot CLI**: far stronger GitHub distribution, enterprise identity, repository/PR adjacency, and procurement path.

Forge's biggest advantages are provider choice, local control, explicit extensibility, and tool/runtime breadth. If Forge focuses the product around a reliable local terminal loop plus opinionated workflows, it can compete credibly without matching every hosted or enterprise feature.

## Bottom-Line Recommendations

1. **Make the primary `forge` chat loop feel production-grade before expanding scope.** The README says `forge` is the primary interactive loop and `forge make` is legacy compatibility (`README.md`). Competitive success depends on making that common path excellent.
2. **Package Forge around 5 canonical workflows**: explain, edit/fix, test/debug, review, and GitHub issue-to-PR.
3. **Turn existing tools into visible trust UX**: approval cards, diffs, command status, verification summaries, and resumable sessions.
4. **Create a durable task layer** for background work with branch/worktree isolation, progress logs, cancel/resume/retry, and patch/PR output.
5. **Clarify extensibility hierarchy**: when to use MCP, OpenCode plugins, Forge plugins, skills, hooks, memory, and internal tools.
6. **Compete where Forge is naturally strong**: local-first, provider-neutral, extensible, power-user workflows. Do not try to out-distribute GitHub or out-host OpenAI immediately.

---

## Methodology and Caveats

### Method Used

This assessment used:

- The existing `docs/forge-competitive-gap-findings.md` as the starting document.
- Repo-backed inspection of README and implementation files.
- General product knowledge for competitors, explicitly caveated as not live-web-verified.
- No web search and no inspection of credential stores or secret files.

### Scoring Scale

Scores are directional, from **1 = weak / absent** to **5 = strong / market-leading**. Competitor scores are approximate general-knowledge estimates. Forge scores are based on repository evidence, not a live usability benchmark.

### Important Limitation

A repo-backed assessment can confirm implemented primitives and documented intent, but it cannot fully measure real-world UX quality, latency, model behavior, install friction, or reliability without interactive product testing.

---

## Repo-Backed Forge Evidence

### Product Positioning

Forge is described as a terminal-first coding agent for local repositories (`README.md`). It has one primary interactive mode and one legacy compatibility mode:

- `forge`: primary interactive coding loop with host-owned runtime, typed hooks, provider switching, approvals, preview workflows, exec sessions, and bounded hidden worker delegation.
- `forge make`: legacy writer/auditor pipeline.

Evidence:

- `README.md` describes Forge as “a terminal-first coding agent for local repositories.”
- `README.md` lists highlights including provider-aware model routing, host-owned React runtime, live chat TUI, exec sessions, session artifacts, summaries, audit logs, and usage tracking.
- `cmd/forge/main.go` routes bare `forge` or flag-only invocations to `runChat`, while retaining `make`, `improve`, `list`, `show`, `perf`, `status`, `skills`, `mcp`, and `plugin` commands.

### CLI and TUI Surface

Forge is terminal-first and uses Bubble Tea for the interactive chat frontend.

Evidence:

- `cmd/forge/main.go` imports Bubble Tea and routes primary chat startup.
- `README.md` notes that `forge` uses the Bubble Tea chat frontend through `internal/tui/chatmodel.go`.
- `internal/tui/` contains chat surface, composer, progress, stats, startup, nudges, review, trace, and viewport-related implementation files.

Competitive implication: Forge is in the right category for Claude Code and OpenCode comparisons. The key question is polish, not whether a TUI exists.

### Runtime Architecture

Forge has a host-owned runtime and a React-style agent loop.

Evidence:

- `README.md` architecture diagram links `cmd/forge/main.go`, `internal/bootstrap`, `internal/runtime/chat.go`, `internal/react`, `internal/hooks`, `internal/memory`, `internal/tui`, `internal/agent/tools`, and provider drivers.
- `internal/runtime/chat.go` builds chat setup, resolves working directory, loads config/tokens, lists available models/providers, creates retry-wrapped drivers, and registers tools.
- `internal/react/` contains approval, sandbox, shell rules, session, loop, compacting, completion, and agent pool files.

Competitive implication: Forge has a deeper local runtime than command-suggestion CLIs. It can position as an agent runtime, not merely a terminal helper.

### Tool Breadth

The tool registry and `internal/agent/tools/` show broad local agent capabilities.

Evidence from `internal/runtime/chat.go` tool registration and `internal/agent/tools/`:

- File operations: `read.go`, `write.go`, `edit.go`, `apply_patch.go`, `list.go`.
- Search/navigation: `search.go`, `code_search.go`, `glob.go`, `lsp.go`.
- Shell/terminal: `command.go`, `exec_session.go`.
- Git: `git.go`, `git_branch.go`, `git_merge.go`.
- Preview/artifacts: `preview.go`, `artifact.go`.
- Safety/review: `safety.go`, `ignore_guard.go`, `guardian_review.go`.
- MCP/dynamic tools: `mcp.go` and registration in `internal/runtime/chat.go`.
- Support tools: `scratchpad.go`, `think.go`, `tool_help.go`, `view_image.go`, `web.go`.

Competitive implication: Forge has enough local tooling to support high-quality coding loops. Product work should emphasize orchestration, presentation, and reliability.

### Model and Provider Support

Forge has unusually broad provider support, including both subscription-backed and API-key-backed paths.

Evidence:

- `README.md` lists interactive login providers: `chatgpt`, `claude`, `copilot`.
- `README.md` lists API-key providers including `openai`, `anthropic`, `groq`, `mistral`, `xai`, `nvidia`, and more via the truncated provider section.
- `README.md` documents custom OpenAI-compatible provider configuration.
- `internal/bootstrap/runtime.go` resolves Claude OAuth, ChatGPT, Anthropic, OpenAI, Copilot, and OpenAI-compatible providers.
- `docs/chatgpt-provider.md` documents separate `chatgpt/<model>` and `openai/<model>` paths, ChatGPT/Codex subscription auth, Responses API use, WebSocket transport, ZDR mode for GPT-5.x models, and provider selection.

Competitive implication: provider flexibility is one of Forge's most credible differentiators versus vertically integrated products.

### Approval, Safety, and Sandbox Controls

Forge has local safety foundations.

Evidence:

- `internal/react/approval.go`, `approval_config.go`, `sandbox.go`, `known_safe.go`, and `shell_rules.go`.
- `internal/agent/tools/safety.go`, `ignore_guard.go`, and `guardian_review.go`.
- `README.md` mentions approvals and keeping users in control of destructive actions.
- `cmd/forge/main.go` exposes `--yolo` in the documented command syntax through README command examples.

Competitive implication: Forge can compete on trust if it exposes these controls clearly. The remaining gap is visible, consistent UX around approvals, risk labels, diffs, and verification.

### Extensibility

Forge has several extension surfaces.

Evidence:

- `cmd/forge/main.go` includes `mcp`, `plugin`, and `skills` commands.
- `internal/runtime/chat.go` creates an MCP manager, refreshes configured MCP servers, and registers dynamic MCP tools.
- `internal/plugins/` exists and `cmd/forge/main.go` supports `forge plugin install/list/remove`; plugin install currently supports OpenCode plugins through `--runtime opencode`.
- `internal/skills/`, `internal/hooks/`, and `internal/memory/` are present and wired in the README architecture diagram.
- `README.md` highlights MCP, plugins, skills, hooks, and memory-style runtime features.

Competitive implication: extensibility is strong, but may be confusing. Documentation and examples need to reduce cognitive load.

### Session, Usage, and Legacy Pipeline

Forge has session artifacts and usage/perf reporting.

Evidence:

- `README.md` documents `forge list`, `forge show <session-id>`, `forge perf`, and pipeline artifacts under `./output/<timestamp>/`.
- `cmd/forge/main.go` implements command routing for `list`, `show`, `perf`, `make`, and `improve`.
- `README.md` states `forge make` is legacy compatibility rather than the main architectural direction.

Competitive implication: Forge has useful operational primitives, but should avoid splitting user attention between legacy and primary experiences.

### Multi-Agent / Delegation

Forge has early delegation infrastructure but not yet a clearly productized durable agent-task system.

Evidence:

- `internal/react/agent_pool.go` exists.
- `README.md` mentions bounded hidden worker delegation.
- `docs/multi-agent-next.md` is explicitly marked “Legacy” and says current runtime uses `internal/react/loop.go`.
- `docs/multi-agent-next.md` identifies remaining gaps including parallel delegation, shared context, failure recovery, and verification enforcement.

Competitive implication: Forge has foundations, but Codex-style task agents and hosted coding agents compete on durable task execution. This is a major roadmap area.

---

## Competitive Scoring Matrix

| Dimension | Forge | Claude Code | OpenAI Codex / ChatGPT Codex-style | OpenCode | GitHub Copilot CLI |
|---|---:|---:|---:|---:|---:|
| Terminal coding-agent UX | 3 | 5 | 3 | 4 | 3 |
| Local repo editing/tools | 4 | 5 | 4 | 4 | 2 |
| Provider flexibility | 5 | 2 | 2 | 3 | 1 |
| Model/runtime vertical polish | 3 | 5 | 5 | 3 | 4 |
| Hosted/durable task workflow | 2 | 3 | 5 | 2 | 3 |
| GitHub issue/PR integration | 2 | 3 | 4 | 2 | 5 |
| Enterprise identity/admin/distribution | 1 | 3 | 4 | 1 | 5 |
| Extensibility | 4 | 3 | 2 | 4 | 2 |
| MCP/plugin ecosystem clarity | 3 | 3 | 2 | 4 | 2 |
| Safety/approval primitives | 4 | 4 | 4 | 3 | 4 |
| Persistent repo intelligence | 2 | 3 | 4 | 2 | 4 |
| Observability/session reporting | 3 | 3 | 4 | 2 | 3 |
| Onboarding/install polish | 2 | 4 | 5 | 3 | 5 |
| Documentation/positioning clarity | 3 | 4 | 5 | 4 | 5 |

### Readout

- **Forge is strongest** in provider flexibility, local control, extensibility, and breadth of tool primitives.
- **Forge is weakest** in distribution, enterprise integration, hosted/durable task execution, and polished default workflows.
- **The most addressable near-term gap** is terminal UX polish and workflow packaging.
- **The hardest gap** is GitHub/Copilot-level distribution and enterprise governance.

---

## Per-Competitor Analysis

## 1. Claude Code

### Competitive Baseline

Claude Code is best understood as a polished terminal-native coding agent tightly integrated with Anthropic’s model stack. This is general knowledge and not live-web-verified. Its likely strengths are:

- Fast, low-friction terminal onboarding.
- Strong default prompt/runtime behavior for coding.
- Tight model/provider integration.
- Familiar approval and diff-based edit loop.
- Strong performance on natural-language coding, debugging, and repo edits.
- Clear category positioning: a coding agent in the terminal.

### Where Forge Is Stronger

Forge can plausibly out-position Claude Code for users who want:

- Provider choice across ChatGPT, Claude, Copilot, OpenAI, Anthropic, and OpenAI-compatible providers (`README.md`, `internal/bootstrap/runtime.go`).
- Local runtime customization and extensibility (`internal/mcp/`, `internal/plugins/`, `internal/skills/`, `internal/hooks/`, `internal/memory/`).
- OpenCode plugin compatibility (`cmd/forge/main.go`, `internal/plugins/`).
- Explicit local tools including LSP, git, exec sessions, preview, and guardian review (`internal/runtime/chat.go`, `internal/agent/tools/`).

### Where Forge Is Behind

Forge likely trails Claude Code in:

- First-run polish and zero-config confidence.
- Consistency of the end-to-end coding loop.
- Product documentation and user mental model.
- Trust UX: clean approval prompts, diffs, command summaries, and completion reports.
- Model-specific optimization for one best default path.

### Practical Gap

Forge appears architecturally capable but productically less sharp. Claude Code-style products win when a user can run one command, ask for a fix, see a clean plan/diff/test loop, and trust the result. Forge should make this path excellent before adding more surfaces.

### Response Strategy

- Treat Claude Code as the UX benchmark.
- Make `forge` startup fast and self-explanatory.
- Add “happy path” workflows: `/fix`, `/test`, `/review`, `/explain`, `/model`, `/provider` if not already fully surfaced.
- Always end coding tasks with an evidence-based summary: files changed, tests run, remaining risk.
- Use provider flexibility as the differentiator, not as a reason to expose complexity.

## 2. OpenAI Codex / ChatGPT Codex-Style Coding Agents

### Competitive Baseline

Codex-style agents are OpenAI-backed coding experiences that may combine ChatGPT UX, repo understanding, code editing, command execution, hosted task execution, and issue/task handoff. Exact product shape changes over time, so this is general-knowledge only and not live-web-verified.

Likely strengths:

- Familiar ChatGPT interaction model.
- Strong OpenAI model integration.
- Account, billing, and organization integration through OpenAI.
- Potential hosted/semi-hosted task execution.
- Strong task-to-result workflows: assign work, wait, inspect output.
- Potentially strong integration with cloud sandboxes, PR generation, and review loops.

### Where Forge Is Stronger

Forge has differentiated local and provider-neutral strengths:

- Forge does not rely on the Codex CLI binary for inference on the ChatGPT path (`docs/chatgpt-provider.md`).
- Forge supports both `chatgpt/<model>` and `openai/<model>` provider paths (`docs/chatgpt-provider.md`).
- Forge can route among ChatGPT, Copilot, OpenAI, Anthropic, Claude OAuth, and OpenAI-compatible providers (`README.md`, `internal/bootstrap/runtime.go`).
- Forge works directly against the local working tree rather than requiring a hosted sandbox (`README.md`).
- Forge exposes extensibility via MCP/plugins/skills/hooks/memory.

### Where Forge Is Behind

Forge likely trails Codex-style agents in:

- Hosted task execution and background autonomy.
- Durable task state, logs, retries, notifications, and result packaging.
- Account/workspace/team integration.
- Productized task intake from issues, prompts, or ChatGPT conversations.
- PR-like output workflow with review/CI integration.

### Practical Gap

Forge has local command/file/git tools, but the repo does not show a first-class durable task product: assign issue → create branch/worktree → implement → test → summarize → open PR → monitor CI → respond to comments. `docs/multi-agent-next.md` explicitly notes legacy multi-agent gaps such as parallel delegation, failure recovery, shared context, and verification enforcement.

### Response Strategy

- Build a durable local task runner before attempting cloud hosting.
- Add branch/worktree isolation as the default for longer tasks.
- Persist task plans, logs, commands, diffs, and verification evidence.
- Produce patch/branch/PR-ready output.
- Later, add optional remote execution or CI integration.

## 3. OpenCode

### Competitive Baseline

OpenCode is a close comparator because it occupies the terminal/open-source coding-agent space and has a plugin ecosystem/mental model that Forge explicitly intersects with. This is general-knowledge only and not live-web-verified.

Likely strengths:

- Clear category recognition among terminal-agent users.
- Simpler mental model if the product has fewer extension surfaces.
- Ecosystem familiarity for OpenCode-compatible plugin authors/users.
- Strong open-source-style adoption path.
- Terminal-native workflow expectations.

### Where Forge Is Stronger

Forge can present itself as a superset runtime if it makes compatibility reliable:

- `forge plugin install` currently supports OpenCode plugins via `--runtime opencode` (`cmd/forge/main.go`).
- Forge also has MCP, skills, hooks, memory, and native tools.
- Forge has broad provider support beyond any single model ecosystem.
- Forge has substantial built-in tools including LSP, git merge status, exec sessions, preview, and guardian review.

### Where Forge Is Behind

Forge risks being harder to understand:

- MCP vs plugins vs skills vs hooks vs memory may feel overlapping.
- OpenCode compatibility must be documented as either a primary migration path or a secondary feature.
- Users need examples, plugin install flows, trust boundaries, and debugging guidance.
- Ecosystem maturity may lag if Forge-specific extension APIs are still evolving.

### Practical Gap

The gap is ecosystem clarity. If Forge supports OpenCode plugins but also exposes several Forge-native extension systems, it needs a crisp hierarchy and “choose this when…” documentation.

### Response Strategy

- Publish an extension decision guide.
- Provide 3-5 real plugin examples and 3-5 MCP examples.
- Define stability guarantees for plugin APIs.
- Add `forge plugin doctor` and better install diagnostics.
- Position OpenCode support as a compatibility advantage, not an implementation detail.

## 4. GitHub Copilot CLI

### Competitive Baseline

GitHub Copilot CLI is strongest where GitHub/Microsoft distribution, identity, enterprise governance, and GitHub workflow adjacency matter. This is general-knowledge only and not live-web-verified. It is typically less about being a fully customizable local agent runtime and more about bringing Copilot assistance into terminal and GitHub-adjacent workflows.

Likely strengths:

- GitHub distribution and brand trust.
- GitHub identity, billing, and enterprise administration.
- Strong fit for terminal command help and explanations.
- Natural adjacency to repositories, issues, pull requests, and CI.
- Enterprise procurement and policy posture.
- Integration with the broader Copilot/editor ecosystem.

### Where Forge Is Stronger

Forge can offer more autonomy and flexibility:

- More local coding-agent primitives than command help alone (`internal/agent/tools/`).
- Provider switching, including Copilot plus non-Copilot providers (`README.md`, `internal/bootstrap/runtime.go`).
- Local extensibility through MCP/plugins/skills/hooks/memory.
- Direct file/patch/edit workflows in the current working tree.

### Where Forge Is Behind

Forge likely trails badly in:

- GitHub-native issue/PR workflows.
- Enterprise admin, audit, policy, procurement, and identity.
- Editor-to-terminal continuity with Copilot.
- Marketplace/distribution.
- Organization-wide rollout and support story.

### Practical Gap

Forge has git tools (`internal/agent/tools/git.go`, `git_branch.go`, `git_merge.go`) but repo evidence does not show first-class GitHub issue ingestion, PR creation, CI watching, review-comment handling, or enterprise policy integration.

### Response Strategy

- Do not try to beat GitHub on distribution first.
- Add practical GitHub workflows for individual developers:
  - `forge gh issue <url-or-number>`
  - create branch/worktree
  - implement with checkpoints
  - run tests
  - create PR body
  - optionally call `gh pr create` with approval
- Later add enterprise policy controls and audit exports.

---

## Feature Gap Table

| Area | Forge Evidence | Current Assessment | Competitive Risk | Recommended Action |
|---|---|---|---|---|
| Primary chat loop | `README.md`, `cmd/forge/main.go`, `internal/runtime/chat.go`, `internal/tui/` | Real primary surface exists | Claude Code/OpenCode feel smoother | Polish startup, prompts, diffs, approvals, completion summaries |
| Provider switching | `README.md`, `internal/bootstrap/runtime.go`, `docs/chatgpt-provider.md` | Strong | Complexity can confuse users | Make provider/model status obvious and add setup diagnostics |
| Local file/edit tools | `internal/agent/tools/read.go`, `write.go`, `edit.go`, `apply_patch.go` | Strong primitives | Edit UX may lag leaders | Improve diff previews, rollback, and edit summaries |
| Shell execution | `command.go`, `exec_session.go`, `internal/runtime/chat.go` | Strong primitives | Long-running command UX must be reliable | Surface live command state, logs, stop/retry, and test detection |
| Git tools | `git.go`, `git_branch.go`, `git_merge.go` | Good local git primitives | No full GitHub issue-to-PR workflow | Add issue/branch/PR/CI workflows, likely via `gh` integration first |
| LSP/search | `lsp.go`, `search.go`, `code_search.go`, `glob.go` | Strong primitives | Repo intelligence not persistent | Add indexed repo map, symbol cache, and context planner |
| Approval/safety | `internal/react/approval*.go`, `sandbox.go`, `shell_rules.go`, `known_safe.go` | Good primitives | Trust UX may be under-productized | Standardize risk labels, approval cards, and audit trail |
| Guardian review | `guardian_review.go` | Differentiating primitive | Needs workflow integration | Make review a canonical post-change phase |
| MCP | `internal/mcp/`, `internal/runtime/chat.go` | Strong | Setup/debug complexity | Add `forge mcp doctor`, templates, and examples |
| OpenCode plugins | `cmd/forge/main.go`, `internal/plugins/` | Strong strategic bridge | Ecosystem clarity | Document compatibility, limitations, and plugin lifecycle |
| Skills/hooks/memory | `internal/skills/`, `internal/hooks/`, `internal/memory/` | Powerful but broad | User confusion | Publish extension decision tree and stable contracts |
| Durable tasks | `internal/react/agent_pool.go`, `docs/multi-agent-next.md` | Early/partial | Codex-style agents likely ahead | Persist task state, isolate worktrees, support resume/cancel/retry |
| Hosted/cloud execution | No clear repo evidence | Weak/absent | Codex/OpenAI advantage | Consider later; first build local durable runner |
| GitHub enterprise | No clear repo evidence | Weak/absent | Copilot advantage | Start with individual `gh` workflows; later enterprise controls |
| Onboarding | `README.md`, `BUILD.md` reference | Functional but developer-oriented | Commercial tools easier | Add install script, first-run wizard, sample workflow video/docs |
| Observability | `forge perf`, `forge list/show`, artifacts | Moderate | Needs unified session UX | Integrate session timeline, cost/tokens, tool calls, verification |

---

## Prioritized Roadmap

## P0: Define the Product Lane

Forge should explicitly choose a primary near-term lane:

1. Local autonomous coding agent.
2. Claude Code/OpenCode-style terminal coding agent.
3. Codex/GitHub-style issue-to-PR task agent.
4. Provider-flexible agent runtime platform.
5. Hybrid.

The repo most strongly supports **lane 1 + lane 4**, with a clear opportunity to productize **lane 2**. Lane 3 should be built incrementally via durable local tasks and GitHub CLI integration. Do not prioritize hosted cloud execution until the local workflow is reliable.

Success criteria:

- README and docs state the lane in one sentence.
- First-run UX reinforces that lane.
- Default workflows map to that lane.

## P1: Terminal Agent Polish

Goal: make `forge` competitive with Claude Code/OpenCode for daily local coding.

Actions:

- Add or polish first-run provider setup.
- Show model/provider/auth status clearly on startup.
- Make approval prompts visually consistent and risk-labeled.
- Show concise diffs for edits and patches.
- Make command execution status visible and non-blocking.
- Add standard completion summaries: changed files, tests run, failures, risk.
- Add workflow shortcuts: explain, fix, test, review, refactor.
- Ensure `/model` and `/provider` are discoverable, since provider flexibility is a differentiator.

Repo anchors:

- `internal/tui/`
- `internal/runtime/chat.go`
- `internal/react/approval.go`
- `internal/agent/tools/command.go`
- `internal/agent/tools/edit.go`
- `internal/agent/tools/apply_patch.go`

## P1: Durable Local Task System

Goal: close the gap with Codex-style task agents without needing hosted execution first.

Actions:

- Introduce persisted task records: prompt, plan, steps, tool calls, logs, diffs, verification.
- Run longer tasks in branch/worktree isolation.
- Support pause/resume/cancel/retry.
- Emit progress events to TUI.
- Produce patch, branch, or PR-ready result.
- Add failure recovery rules: narrow scope, rerun tests, ask only when blocked.

Repo anchors:

- `internal/react/agent_pool.go`
- `internal/react/session.go`
- `internal/agent/tools/git_branch.go`
- `internal/agent/tools/exec_session.go`
- `docs/multi-agent-next.md`

## P1: GitHub Issue-to-PR Workflow

Goal: offer a practical alternative to Copilot/Codex task flows for individual developers.

Actions:

- Add a workflow that can ingest a GitHub issue URL/number using `gh` if available.
- Create a branch or worktree.
- Generate and confirm a plan.
- Implement with checkpoints.
- Run tests.
- Generate PR title/body.
- Optionally run `gh pr create` after explicit approval.
- Watch CI and summarize failures where possible.

Repo anchors:

- `internal/agent/tools/git.go`
- `internal/agent/tools/git_branch.go`
- `internal/agent/tools/git_merge.go`
- `internal/agent/tools/command.go`

## P2: Repo Intelligence Layer

Goal: reduce context thrash and improve multi-turn coding reliability.

Actions:

- Build a lightweight repo map: languages, package managers, test commands, symbols, ownership boundaries.
- Cache LSP/code-search findings per session.
- Store memory summaries bounded by relevance.
- Surface “known project commands” and “likely test command” in startup/status.

Repo anchors:

- `internal/agent/tools/lsp.go`
- `internal/agent/tools/code_search.go`
- `internal/memory/`
- `internal/react/compact.go`

## P2: Extensibility Clarity

Goal: turn Forge’s many extension points into an advantage rather than confusion.

Actions:

- Create an extension decision guide:
  - MCP for external tools/data/resources.
  - OpenCode plugins for compatibility and portable tool bundles.
  - Forge plugins for native runtime extension when stable.
  - Skills for reusable instructions/workflows.
  - Hooks for lifecycle automation.
  - Memory for persistent project/user context.
- Add example extensions with tests.
- Add plugin/MCP diagnostics.
- Document trust boundaries and approval behavior for dynamic tools.

Repo anchors:

- `internal/mcp/`
- `internal/plugins/`
- `internal/skills/`
- `internal/hooks/`
- `internal/memory/`
- `cmd/forge/main.go`

## P2: Review and Verification Workflow

Goal: make Forge trustworthy for real code changes.

Actions:

- Make guardian review a visible post-change step.
- Detect likely test commands and ask/run them appropriately.
- Require evidence before success claims in first-party prompts.
- Record verification in session artifacts.
- Provide a review mode that checks goal satisfaction, code quality, security, and tests.

Repo anchors:

- `internal/agent/tools/guardian_review.go`
- `internal/agent/tools/command.go`
- `internal/react/completion.go`
- `internal/react/session.go`

## P3: Distribution and Enterprise Readiness

Goal: compete more seriously with Copilot-style adoption over time.

Actions:

- Provide one-line install paths for macOS/Linux.
- Sign/notarize binaries where relevant.
- Add organization config profiles.
- Add audit export.
- Add policy controls for allowed providers/tools/commands.
- Document data handling and provider routing.

Repo anchors:

- `BUILD.md`
- `internal/config/`
- `internal/auth/`
- `internal/react/approval_config.go`

---

## Risks

### Strategic Risks

1. **Too many identities**: local agent, runtime platform, plugin host, Codex alternative, OpenCode-compatible tool, and legacy pipeline. Forge needs one primary story.
2. **Extensibility overload**: MCP, plugins, skills, hooks, and memory are powerful but can overwhelm users.
3. **Provider complexity**: broad provider support is a differentiator only if setup and routing are understandable.
4. **Commercial polish gap**: Claude Code/OpenAI/GitHub can win through smoother UX even with fewer visible primitives.
5. **Distribution disadvantage**: GitHub and OpenAI have account, billing, organization, and marketplace advantages Forge cannot quickly replicate.

### Execution Risks

1. **Legacy surface drag**: `forge make` remains useful but may confuse the primary product story if docs overemphasize it.
2. **Task reliability**: durable multi-step tasks need strong state management, recovery, and verification.
3. **Safety regression**: adding automation without clear approvals could reduce trust.
4. **Plugin security**: OpenCode/plugin compatibility increases supply-chain and trust-boundary concerns.
5. **Context persistence errors**: memory/repo intelligence can become stale or misleading if not bounded and inspectable.

### Market Risks

1. **Model vendors move fast**: Claude/OpenAI/GitHub may ship overlapping local/runtime features.
2. **User expectations rise**: “agent” increasingly implies issue-to-PR, CI, review, and resumability, not just chat plus tools.
3. **Enterprise buyers prefer incumbents**: Forge needs a power-user/open/local niche before chasing enterprise procurement.

---

## Recommended Next Steps

### Next 2 Weeks

- Rewrite README opening around one product lane.
- Add a “first successful task” walkthrough: install, provider setup, run `forge`, ask for a small fix, approve diff, run tests.
- Improve startup/status view to show workdir, model, provider, auth status, and likely project commands.
- Document extension choice: MCP vs plugin vs skill vs hook vs memory.
- Add a competitive demo script against Claude Code/OpenCode-style tasks.

### Next 4-6 Weeks

- Implement durable local task records and a task timeline UI.
- Add branch/worktree isolation for longer tasks.
- Create a GitHub issue-to-branch workflow using local git plus optional `gh` CLI.
- Integrate guardian review into the normal post-change flow.
- Add `forge doctor` or equivalent diagnostics for providers, MCP, plugins, git, and project tooling.

### Next 2-3 Months

- Add persistent repo intelligence and test-command discovery.
- Stabilize extension APIs and publish example plugins/MCP configs.
- Add audit/export controls for teams.
- Add optional PR creation and CI-watching workflows.
- Benchmark Forge on a fixed coding-agent eval suite against the competitor set.

---

## Suggested Evaluation Suite

To make future competitive claims evidence-based, run all products through the same tasks on the same repositories:

1. **Small edit**: change one function and update tests.
2. **Bug fix**: reproduce failing test, diagnose root cause, patch, verify.
3. **Refactor**: rename/extract across files with LSP/search use.
4. **Feature addition**: add a small endpoint/component/CLI flag with docs/tests.
5. **Review**: identify issues in a prepared diff.
6. **Git workflow**: create branch, commit-ready diff, PR body.
7. **Long command**: run dev server or watch test and interact with it.
8. **Extension workflow**: install/configure an MCP server or plugin.
9. **Provider switch**: switch model/provider mid-session and continue correctly.
10. **Recovery**: handle failing tests, command timeout, or merge conflict.

Metrics:

- Time to first useful action.
- Number of approvals/interactions.
- Correctness of final patch.
- Tests passed/failed.
- Quality of explanation.
- Context efficiency.
- Recovery from errors.
- User trust: clarity of diffs, commands, and risk.
- Setup friction.
- Session resumability.

---

## Final Assessment

Forge is competitively credible as a **local-first, provider-flexible coding-agent runtime**. It has many primitives that simpler terminal assistants lack: broad provider routing, local file/edit/patch/search/LSP/git/shell tools, MCP, plugin compatibility, skills/hooks/memory, approval/sandbox controls, exec sessions, and guardian review.

The gap to Claude Code, Codex-style agents, OpenCode, and GitHub Copilot CLI is mostly the gap between **capability** and **productized workflow**. Forge should not try to beat every competitor on their strongest axis at once. The highest-leverage path is:

1. Match Claude Code/OpenCode on local terminal polish.
2. Use provider flexibility and extensibility as the differentiator.
3. Add durable local tasks and GitHub issue-to-PR workflows.
4. Defer hosted execution and enterprise distribution until the local product is excellent.
