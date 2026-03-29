# Forge vs Codex vs OpenCode: Prompt Size and Fluidity

**Date:** 2026-03-29  
**Purpose:** Capture a source-grounded comparison of why Forge still feels tighter and less fluid than Codex or OpenCode.  
**Sources:** Local Forge repo, upstream `openai/codex`, upstream `sst/opencode`.

## Summary

Forge does not currently feel worse because its visible default prompt is larger.

Measured in this repo:

- Forge generated visible prompt: about `6012` chars
- OpenCode default prompt file: about `8661` chars
- Codex base instructions file: about `20923` chars

The difference is prompt shape and execution model, not just prompt size:

- Forge injects tool descriptions directly into prompt text every turn
- Forge injects the loaded skill catalog directly into prompt text every turn
- Forge still routes React-mode delegation into the older role-based subagent stack
- Forge subagents still operate under filtered tool access and structured-output retry contracts

Codex and OpenCode feel looser because they keep more capability in structured runtime/tool definitions and let the model drive the loop more directly.

## Main Finding

Forge's current `react` runtime is the default chat runtime, but it does not fully replace the older constrained subagent model.

- Default runtime resolves to `react` at [internal/runtime/chat.go:621](/Users/cass/git/forge/internal/runtime/chat.go:621)
- React delegation tools are registered at [internal/runtime/chat.go:601](/Users/cass/git/forge/internal/runtime/chat.go:601)
- Those delegation calls still map into legacy specialist roles like `builder` and `scout` at [internal/react/agent_pool.go:150](/Users/cass/git/forge/internal/react/agent_pool.go:150)
- `spawn_agent` in React mode ultimately calls `SpawnSubAgent` at [internal/runtime/chat.go:605](/Users/cass/git/forge/internal/runtime/chat.go:605)

That means Forge still inherits older constraints even when the top-level runtime is more ReAct-like.

## Prompt Comparison

### Forge

Forge builds its visible system prompt as plain text and injects tool descriptions directly:

- Base system prompt is built at [internal/agent/system.go:13](/Users/cass/git/forge/internal/agent/system.go:13)
- Tool descriptions are appended at [internal/agent/system.go:27](/Users/cass/git/forge/internal/agent/system.go:27)
- Tool descriptions come from [internal/agent/tools/registry.go:111](/Users/cass/git/forge/internal/agent/tools/registry.go:111)
- Skill descriptions are always injected at [internal/agent/agent.go:127](/Users/cass/git/forge/internal/agent/agent.go:127)
- Skill description rendering is at [internal/skills/skills.go:156](/Users/cass/git/forge/internal/skills/skills.go:156)

Measured locally for the current Forge prompt shape:

- `skills=14`
- `tools_visible=16`
- `tools_hidden=3`
- `system_chars=6012`
- `strict_chars=5906`
- `worker_reader_chars=5848`
- skill description text alone is about `2422` chars
- visible tool description text alone is about `1838` chars

So the prompt is not huge in absolute terms, but a large share of it is spent on tool/skill catalog text rather than pure behavioral instruction.

### OpenCode

OpenCode uses a larger prompt file than Forge, but it keeps tools outside that prompt as structured tool definitions:

- Provider prompt selection happens at [/tmp/opencode-upstream/packages/opencode/src/session/system.ts:19](/tmp/opencode-upstream/packages/opencode/src/session/system.ts:19)
- The default prompt text lives at [/tmp/opencode-upstream/packages/opencode/src/session/prompt/default.txt:1](/tmp/opencode-upstream/packages/opencode/src/session/prompt/default.txt:1)
- System prompt assembly happens at [/tmp/opencode-upstream/packages/opencode/src/session/llm.ts:90](/tmp/opencode-upstream/packages/opencode/src/session/llm.ts:90)
- Structured tool definitions are built at [/tmp/opencode-upstream/packages/opencode/src/session/prompt.ts:780](/tmp/opencode-upstream/packages/opencode/src/session/prompt.ts:780)

OpenCode also keeps its main control flow simple:

- Main session loop is `while (true)` at [/tmp/opencode-upstream/packages/opencode/src/session/prompt.ts:298](/tmp/opencode-upstream/packages/opencode/src/session/prompt.ts:298)

### Codex

Codex's base instructions are much larger than Forge's, but tools are also passed separately as structured tool specs:

- Base instructions file is at [/tmp/codex-upstream/codex-rs/protocol/src/prompts/base_instructions/default.md:1](/tmp/codex-upstream/codex-rs/protocol/src/prompts/base_instructions/default.md:1)
- Prompt object includes structured `tools` at [/tmp/codex-upstream/codex-rs/core/src/codex.rs:6309](/tmp/codex-upstream/codex-rs/core/src/codex.rs:6309)
- Visible tool specs come from `router.model_visible_specs()` at [/tmp/codex-upstream/codex-rs/core/src/codex.rs:6300](/tmp/codex-upstream/codex-rs/core/src/codex.rs:6300)
- API request sends `instructions` and `tools` separately at [/tmp/codex-upstream/codex-rs/core/src/client.rs:369](/tmp/codex-upstream/codex-rs/core/src/client.rs:369)

Codex also keeps the main loop model-led:

- Turn loop is in `run_turn()` starting at [/tmp/codex-upstream/codex-rs/core/src/codex.rs:5548](/tmp/codex-upstream/codex-rs/core/src/codex.rs:5548)
- Sampling continues until `needs_follow_up` is false at [/tmp/codex-upstream/codex-rs/core/src/codex.rs:5886](/tmp/codex-upstream/codex-rs/core/src/codex.rs:5886)
- Tool building is runtime-driven at [/tmp/codex-upstream/codex-rs/core/src/codex.rs:6462](/tmp/codex-upstream/codex-rs/core/src/codex.rs:6462)

## Why Forge Still Feels Tight

### 1. Prompt text carries too much runtime protocol

Forge spends visible prompt space on:

- tool inventory and call wrapper rules
- a full skills list
- strict local/worker execution contracts
- repo metadata such as file count and language detection

The system prompt includes tool serialization at [internal/agent/system.go:27](/Users/cass/git/forge/internal/agent/system.go:27), and `detectProject()` even walks the repo to synthesize environment metadata at [internal/agent/system.go:231](/Users/cass/git/forge/internal/agent/system.go:231).

This is not catastrophic on size, but it is less efficient than Codex/OpenCode, which move more of this into runtime structures rather than natural-language prompt text.

### 2. Delegation still flows through constrained legacy roles

Forge subagents are still role-based and filtered:

- Role tool allowlists are defined at [internal/agent/roles.go:12](/Users/cass/git/forge/internal/agent/roles.go:12)
- Subagent tool filtering happens at [internal/agent/subagent.go:105](/Users/cass/git/forge/internal/agent/subagent.go:105)
- The subagent system prompt is base prompt plus a large role prompt at [internal/agent/subagent.go:108](/Users/cass/git/forge/internal/agent/subagent.go:108)

That makes delegated work feel less fluid than Codex/OpenCode, where delegation is more native to the loop and not as dependent on rigid persona prompts and per-role tool fences.

### 3. Structured-output retry contracts still add friction

Forge subagents still normalize toward structured results:

- Structured retry for subagent results starts at [internal/agent/subagent.go:174](/Users/cass/git/forge/internal/agent/subagent.go:174)
- Legacy worker manager does the same with validation and up to three retries at [internal/harness/workers.go:93](/Users/cass/git/forge/internal/harness/workers.go:93)

Even if the visible runtime is now `react`, this older contract model still shapes delegation behavior and recovery behavior.

OpenCode and Codex are comparatively looser here:

- OpenCode task results are returned as `<task_result>` wrapped text at [/tmp/opencode-upstream/packages/opencode/src/tool/task.ts:148](/tmp/opencode-upstream/packages/opencode/src/tool/task.ts:148)
- OpenCode subtask invocation re-enters the normal loop at [/tmp/opencode-upstream/packages/opencode/src/session/prompt.ts:362](/tmp/opencode-upstream/packages/opencode/src/session/prompt.ts:362)

### 4. Forge still filters tool access instead of trusting the loop more

Forge workers and roles still rely on hard allowlists:

- Worker allowlists live at [internal/harness/workers.go:121](/Users/cass/git/forge/internal/harness/workers.go:121)
- Role allowlists live at [internal/agent/roles.go:12](/Users/cass/git/forge/internal/agent/roles.go:12)

Codex and OpenCode do more filtering through runtime policy and structured tool availability:

- Codex tool exposure is assembled dynamically in `built_tools()` at [/tmp/codex-upstream/codex-rs/core/src/codex.rs:6462](/tmp/codex-upstream/codex-rs/core/src/codex.rs:6462)
- OpenCode resolves enabled tools through permission filtering at [/tmp/opencode-upstream/packages/opencode/src/session/llm.ts:317](/tmp/opencode-upstream/packages/opencode/src/session/llm.ts:317)

This matters because tool restrictions tend to make agents feel hesitant or brittle when a task crosses boundaries.

## What Is Not The Problem

The visible Forge prompt is not obviously too large in raw size terms.

Measured char counts:

- Forge `internal/agent/system.go` source file: `12021` bytes
- OpenCode default prompt file: `8661` bytes
- Codex base instructions file: `20923` bytes

More importantly, the generated Forge visible prompt in this workspace is around `6012` chars. So the problem is not that Forge has a larger default prompt than Codex or OpenCode.

The problem is that Forge still spends a relatively high fraction of its prompt and runtime logic on:

- natural-language tool protocol
- full skill catalog injection
- role-specific subagent governance
- structured-output contracts
- hard tool fences

## Most Likely Root Causes Of The UX Difference

If the goal is to make Forge feel more like Codex or OpenCode, the main reasons it still does not are:

1. `react` mode still falls back to the older specialist subagent architecture rather than using truly native, lightweight delegation.
2. Too much tool and skill metadata is serialized into plain prompt text every turn.
3. Delegated work is still over-governed by role prompts, filtered tool sets, and structured-output retries.
4. Forge still feels host-directed in the delegated path, while Codex/OpenCode feel more model-directed.

## Practical Direction

The highest-value changes are likely:

1. Stop always injecting the full skill catalog into the base prompt; only inject skill summaries when relevant or requested.
2. Move more tool semantics out of plain-text prompt prose and into structured tool definitions.
3. In `react` mode, replace legacy role-prompt subagents with lighter native delegation that does not require structured-output retries.
4. Reduce or remove hard per-role tool allowlists in the React path.
5. Cache or simplify `detectProject()` so prompt construction does less work and carries less synthesized metadata.

## Conclusion

Forge's prompt is not mainly too large.

Forge feels less fluid because its runtime still carries older architectural constraints:

- prompt-text-heavy tool and skill description
- role-based delegated execution
- tool allowlists
- structured-output enforcement

Codex and OpenCode both feel looser because they let the model operate inside a simpler loop with more capability expressed in structured runtime/tool definitions rather than in restrictive prompt contracts.
