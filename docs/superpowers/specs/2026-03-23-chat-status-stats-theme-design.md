# Chat Status, Stats, and Theme Design

## Summary

Improve the Bubble Tea chat UI so its default surface exposes the most important operational state at a glance, while `/stats` becomes a comprehensive diagnostics overlay and themes evolve from a boolean low-contrast flag into a chat-only named theme system.

The approved direction is a compact two-line header:

- line 1 shows app name, active model, working directory, and active theme
- line 2 shows live operational state with provider-aware summaries keyed off the selected model

The overlay at `/stats` should become the deep diagnostics surface for the current chat session, including turn/session totals, provider-specific quota or subscription data, and model metadata when known.

## Problem

The current Bubble Tea chat path works, but it is still missing key operational visibility that existed in or was expected from earlier chat surfaces:

- the active model is technically present, but it is not prominent enough for quick scanning
- the default screen does not clearly show theme, session state, or provider-specific usage
- `/stats` only shows basic token counts and duration
- Copilot quota and Codex/OpenAI usage are not surfaced where users expect them
- the theme system is still effectively a boolean toggle rather than an intentional chat presentation system

This leaves the user without enough feedback during normal chat use and makes the UI feel like a partial migration rather than a finished chat surface.

## Goals

- Make the active model obvious at all times.
- Keep the default chat chrome compact while increasing operational visibility.
- Show provider-aware usage summaries in the always-visible header, based on the currently selected model.
- Expand `/stats` into the primary diagnostics surface for chat.
- Surface as much model/provider/runtime metadata as the system can reliably know.
- Replace the boolean low-contrast flag with a chat-only named theme system.
- Preserve backward compatibility for existing `/theme` usage where practical.

## Non-Goals

- No shared cross-TUI theme system for non-chat screens.
- No redesign of the startup, input, running, or post-run screens.
- No attempt to make all providers expose identical stats when their data sources differ.
- No provider account-management redesign beyond what already exists in `/provider`.
- No full dashboard mode on the main chat screen.

## Approved Product Direction

### Chat chrome

Use a two-line status header rather than a dense dashboard bar.

This was selected because it improves scanability without taking too much vertical space from the transcript.

### Provider-aware visibility

The second header line should be model-aware:

- if the active model is a Copilot-backed model, show Copilot allowance/quota summary
- if the active model uses the ChatGPT/OpenAI/Codex path, show Codex/OpenAI usage summary when available
- if the active model belongs to another provider, show token/context/request-mode information without irrelevant subscription text

### `/stats`

`/stats` should be as complete as the runtime can make it, even if some fields are best-effort or unavailable for some providers.

### Themes

The chat surface should support a fuller named theme system, including:

- a solid dark/default theme
- the current low-contrast variant
- a light theme
- at least one mid-contrast or in-between theme

## Current Behavior

### Header

The chat view currently renders a single header line of the form:

`forge • <model> • <workdir>`

This is functional, but too sparse for normal operational use.

### Stats overlay

The current `/stats` overlay shows:

- latest turn duration
- latest turn input/output tokens
- session input/output/total tokens

This leaves out request mode, context information, provider-specific quotas, and model metadata.

### Themes

Themes are currently represented by a boolean `lowContrast` state with slash command aliases:

- `/theme`
- `/theme low`
- `/theme default`

That implementation is too narrow for the intended chat-only polish pass.

## Proposed Behavior

### Two-Line Header

### Line 1: identity

Line 1 should contain stable identity information:

- `forge`
- active model
- working directory
- active theme name

This line answers “where am I and what am I using?”

### Line 2: live status

Line 2 should contain compact operational information:

- chat state such as `ready`, `streaming`, `error`, `awaiting approval`
- latest turn token summary
- session token total
- context headroom summary when known
- provider-aware usage summary based on the active model

This line answers “what is happening right now and what are my limits?”

### Provider-aware summary behavior

The provider-specific block should be conditional:

- `copilot/...`
  - show relevant Copilot allowance/quota data
  - prefer live user quota when available
  - fall back to last-turn Copilot quota data if live data is unavailable
- `chatgpt/...` and OpenAI/Codex path
  - show Codex/OpenAI usage snapshot when available
  - keep wording concise because the header is not the full diagnostics sheet
- other providers
  - do not show empty or misleading subscription placeholders
  - show context and request-mode data instead

## `/stats` Overlay

`/stats` becomes the comprehensive diagnostics overlay for the active chat session.

### Sections

#### Turn

The first section should remain `Turn`, because that concept will scale into future multi-agent chat.

It should include:

- duration
- latest input/output tokens
- active model and provider for that request
- request mode when available
- any provider quota captured from the last request

#### Session

This section should include:

- cumulative input/output/total tokens
- context file count
- estimated context usage and remaining headroom when available
- any session-level counters already tracked in chat state

#### Provider

This section should include provider-specific live or cached data:

- Copilot
  - live allowance windows such as chat, completions, premium interactions
  - reset information
  - last-turn quota capture as fallback or supplement
- ChatGPT/OpenAI/Codex path
  - subscription or usage snapshot from the available usage backend
  - best-available status if live values are unavailable
- other providers
  - provider label and whatever reliable usage/limit information is actually known

#### Model

This section should include best-available model metadata:

- context window
- output limit if known
- capability metadata when available from the local model catalog
- qualified provider/model name

#### Diagnostics

This section should include runtime details that help explain behavior:

- current request mode
- recent expansion/truncation state when useful
- notable unavailable-data messages phrased explicitly

### Data quality rule

The overlay should prefer “best available data” and degrade gracefully.

That means:

- show a field when it is known
- show a clear unavailable message when it is not
- never fabricate parity between providers with different capabilities

## Theme System

### Chat-only theme registry

Replace the boolean low-contrast state with a named chat theme registry.

The registry should drive:

- header colors
- pane borders and backgrounds
- overlay chrome
- message header/body accents
- selection/focus styles
- status and warning accents

This work should remain chat-only and should not yet be generalized to the rest of the TUI.

### Initial themes

The initial theme set should include at least:

- `default`
  - primary dark theme and current baseline
- `low`
  - low-contrast dark theme
- `light`
  - light theme suitable for brighter terminals
- `dusk`
  - mid-contrast theme between dark and light

Names can change during implementation, but the shape of the set should remain:

- dark
- low-contrast dark
- light
- in-between

### Theme commands

Maintain backward compatibility where possible:

- `/theme`
  - cycle themes or open a future picker
- `/theme <name>`
  - select explicit theme
- `/theme low`
  - still works
- `/theme default`
  - still works

The active theme should be shown in the header.

## Data Sources and Integration

### Existing sources already in the repo

The design should reuse the data paths already present:

- `llm.Usage`
  - latest token usage and Copilot quota capture
- `llm.RequestModeReporter`
  - request mode reporting from drivers that support it
- `internal/copilot/quota.go`
  - last-response Copilot quota extraction
- `internal/copilot/user_quota.go`
  - live Copilot user allowance windows
- `internal/codexusage/usage.go`
  - Codex/OpenAI usage snapshots
- `internal/modelcatalog/catalog.go`
  - model metadata and capabilities

### Chat model responsibilities

`ChatModel` should become the integration point for:

- caching the active theme ID
- storing and rendering richer status snapshots
- tracking the latest provider-aware usage data needed by the header
- populating `/stats` from the best available live and cached sources

## Implementation Shape

### `internal/tui/chatmodel.go`

Expected changes:

- replace `lowContrast bool` with a theme ID plus theme lookup
- add richer status/header rendering helpers
- expand stats state to include provider/model/runtime diagnostics
- render the two-line header
- update `/stats` overlay rendering
- expose provider-aware header summaries keyed off the current model

### Supporting chat/theme types

Introduce focused helpers for:

- chat theme palette lookup
- provider summary formatting
- stats section assembly
- model metadata formatting

These should be kept modular so the file does not grow into a monolith.

### Runtime/data plumbing

Where needed, extend chat runtime wiring so the chat model can receive:

- live Copilot user quota snapshots
- Codex/OpenAI usage snapshots
- request mode details
- model catalog metadata for the active model

## Testing Strategy

### View-level tests

Add tests proving:

- the second header line renders
- the active model is clearly visible
- the active theme label renders
- provider-specific summary blocks change with the selected model

### `/stats` tests

Add tests for:

- turn/session sections rendering
- Copilot-specific stats content
- ChatGPT/OpenAI/Codex usage content
- graceful fallback when data is unavailable
- model metadata rendering

### Theme tests

Add tests for:

- selecting named themes
- backward-compatible `/theme low` and `/theme default`
- theme cycling behavior if retained
- visible rendering differences where practical

### Regression tests

Protect against:

- header overflow or unreadable truncation
- provider details appearing for the wrong provider
- empty subscription placeholders
- loss of current token/session counters during the refactor

## Risks

- Header overcrowding if provider summaries are too verbose.
- Inconsistent data availability across providers.
- Theme expansion causing duplicated color decisions across render paths.
- `chatmodel.go` growing too large if formatting and data plumbing are not separated cleanly.

These risks are manageable if the implementation keeps rendering helpers isolated and treats provider-specific data as optional rather than mandatory.

## Recommended Implementation Direction

Implement the work in this order:

1. replace the theme boolean with a named chat theme registry
2. refactor the header into a two-line provider-aware status surface
3. wire in provider-aware usage summaries for the active model
4. expand `/stats` into the full diagnostics overlay
5. add view-level and provider-conditioned tests to lock the behavior down

This order keeps the visible chat chrome coherent while progressively restoring and improving the missing parity features.
