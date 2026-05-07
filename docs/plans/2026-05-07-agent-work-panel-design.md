# Agent Work Panel Design

## Purpose

Show delegated agent work in the normal chat UI without requiring debug mode. The panel should make agent activity visible while keeping the main transcript concise.

## Design

- Add `/panel`, `/panel on`, `/panel off`, and `/toggle panel` commands for the agent work side panel.
- Keep `/tools` disabled so the old raw tools buffer does not reappear in the default UI.
- Auto-open the panel when sub-agent work starts unless the user explicitly hid it with `/panel off`.
- If `/panel` is used with no current agent work, arm auto-open and tell the user the panel will open when agent work starts.
- Store agent work separately from legacy tool output by only rendering side-panel sections that have an agent role.

## Alternatives Considered

- Reuse `/tools`: rejected because it would conflict with the previous removal of raw tool output from the default UI.
- Always auto-open the panel: rejected because the user wants `/panel off` to be respected.
- Never auto-open the panel: rejected because delegated work should be visible by default.

## Testing

- Slash command tests for `/panel`, `/panel on`, `/panel off`, and `/toggle panel`.
- Regression tests that legacy tool output stays hidden.
- Regression tests that sub-agent events populate and auto-open the panel only when not explicitly hidden.
