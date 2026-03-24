# Multi-Agent: Next Steps

Tracked improvements and known issues from live testing of the hierarchical delegation system.

## Dispatch Discipline

- **Narration before tool calls**: Dispatch sometimes writes "Let me delegate this to scout" before the actual `delegate` call. The prompt forbids this but some models slip. Options:
  - Strip leading prose from dispatch responses before parsing tool calls
  - Add a post-processing step that auto-retries if dispatch's first token isn't `<tool_call>`
  - Fine-tune the prompt further with few-shot examples of correct behavior

- **Self-research attempts**: Dispatch occasionally tries to answer research questions directly instead of delegating to scout. Currently mitigated by removing `read_file` and `run_command` from dispatch tools, but dispatch can still produce plausible-sounding answers from training data.

## Sub-Agent Result Presentation

- **Result passthrough**: When a sub-agent returns, dispatch should present findings to the user without rewriting or summarizing away detail. Currently the prompt says this but dispatch sometimes adds its own commentary.
- **Multi-step chains**: For IMPLEMENT flows (scout → builder → scout verify), dispatch needs to carry context between delegations. Scratchpad is available but not always used effectively.
- **Failure recovery**: When a sub-agent hits max turns, dispatch should re-delegate with narrower scope. Not yet tested under real failure conditions.

## TUI Rendering

- **Token interleaving**: During fast streaming, sub-agent tokens and dispatch tokens can interleave in the tools pane if dispatch produces output while a sub-agent is running. Should not happen with current architecture (dispatch waits for delegation to return) but worth verifying.
- **Tools pane overflow**: Long sub-agent sessions produce a lot of output. Consider:
  - Collapsible sub-agent sections in tools pane
  - Auto-summarize completed sub-agent output after N lines
  - Separate scrollback per sub-agent
- **Sub-agent switching notification**: When dispatch chains from one sub-agent to another (e.g., scout → builder), the transition should be clearly visible in both chat and tools panes. Currently shows `delegating to builder` in chat but the tools pane lifecycle markers could be more prominent.

## Tool Access

- **Builder verification**: Builder should run `go build` / `go test` after changes. Currently has `run_command` but no enforcement that it actually verifies.
- **Doctor read-only enforcement**: Doctor has `run_command` for reproducing failures but could accidentally modify state. Consider a read-only wrapper or restricted command allowlist.
- **Architect scope creep**: Architect is read-only by design but could benefit from `run_command` for running existing test suites to understand current state.

## Configuration

- **Per-role model selection**: `config.Chat.Agents.RoleModels` is wired up but untested with different models per role. Scout and architect could use cheaper/faster models while builder uses the most capable.
- **Dynamic agent enable/disable**: Currently agents are enabled/disabled via config only. Could add a `/agents` toggle command in the TUI.
- **Max turns tuning**: Current values (scout: 25, builder: 25, doctor: 15, architect: 10) are initial guesses. Need real-world data on typical turn counts.

## New Capabilities

- **Parallel delegation**: Dispatch currently delegates sequentially. For tasks like "search for X and also check Y", dispatch could delegate to two scouts in parallel.
- **Agent memory/context**: Sub-agents start fresh each delegation. For multi-step workflows, prior sub-agent findings are only available if dispatch passes them via CONTEXT in the task description. Consider a shared context store beyond scratchpad.
- **User interruption**: No way to cancel a running sub-agent without killing the whole session. Add Ctrl+C handling that cancels the current sub-agent and returns control to dispatch.
