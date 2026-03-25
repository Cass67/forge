# Transcript-First Chat UI Design

## Summary

Redesign the live chat TUI so it behaves like a useful terminal transcript rather than a dashboard. The default surface becomes a single full-width conversation stream with compact inline working status, then the final useful answer inline. The right-side tools panel is removed from the default experience.

In parallel, tighten the delegate result loop so the user never lands on empty placeholders such as `Evidence gathered.` or `Architect output ready.` when the underlying structured artifact already contains actionable meaning.

## Problem

The current multi-agent chat UI is solving the transport problem, but not the user problem.

Observed failures:

- the right-side tools panel consumes space without improving decisions
- subagent work is surfaced as a mixture of big panels, status lines, and generic completion messages
- the transcript ends up with visually heavy but low-value blocks like `Recent activity • scout`, `scout complete • 2 turns • 5 tools`, and `Agent complete • 09:52:26`
- when the delegate boundary coerces a bare JSON object into a valid result, the visible message can still collapse to a generic fallback instead of the meaningful content inside the artifact
- users can finish a turn seeing only `Evidence gathered.` even when the subagent actually found a source file, trigger condition, severity, or recommended next action

This creates the worst combination:

- too much UI chrome while working
- too little useful information when complete

## Goals

- Make the default chat UI a readable terminal transcript.
- Remove the right-side tools panel from the main chat experience.
- Show compact inline working state while subagents are active.
- Show one useful final answer inline, not a generic placeholder.
- Preserve enough detail for deliberate inspection without making it the default surface.
- Keep the design close to Codex CLI's interaction model: transcript first, status second, chrome last.
- Ensure delegate results are summarized into user-meaningful text by role.
- Auto-chain from evidence-only subagent output to interpretive output when the user asked an interpretive or actionable question.

## Non-Goals

- No split-pane dashboard replacement.
- No attempt to expose every raw tool call inline by default.
- No new permanent debug surface in the main transcript.
- No rewrite of the runtime event system if the existing event stream can support the redesign.
- No regression back to English-keyword heuristics for delegate parsing.

## Approved Product Direction

### 1. Transcript First

The main chat view becomes one full-width transcript. There is no default side panel, no collapsed tool sections, and no special recent-activity box.

The transcript should read like this:

1. user message
2. one compact inline working line while the system is doing work
3. one useful answer message when the turn completes

The transcript should not read like this:

1. user message
2. dispatch box
3. recent activity box
4. subagent complete status line
5. generic role box
6. agent complete banner

### 2. Compact Working State

While a subagent is active, the UI should surface only a lightweight inline progress entry such as:

- `scout: searching update_cerner_daily.sh...`
- `architect: assessing severity from scout findings...`
- `builder: running go test ./internal/agent...`

Requirements:

- only one live working item per active role should be visible by default
- repeated or low-signal progress lines should update/replace the current working line rather than create a growing stack
- working lines should be visually lighter than final answers
- working state should disappear or collapse once the final answer is available

### 3. Final Answer Over Status Chrome

When a delegated role completes successfully, the visible transcript should show the useful result, not the machinery around it.

Preferred examples:

- `The email comes from util-rancid/update_cerner_daily.sh:753.`
- `Low-to-medium severity. Actionable, not panic-level. Check whether the verify script exists and is executable.`
- `Root cause: the delegate parser rejected a bare JSON object and forced a second retry.`

Not acceptable as the only visible result:

- `Evidence gathered.`
- `Architect output ready.`
- `Implementation complete.`
- `Agent complete • <timestamp>`

Completion should be implied by the final answer itself plus the header/footer state. Completion banners should not dominate the transcript.

### 4. Detail By Demand, Not By Default

Raw tool details and full structured artifacts remain available for explicit inspection, but are not shown inline by default.

This design allows:

- `/expand` for long or truncated material
- a lightweight details affordance for the most recent meaningful result
- preservation of `lastExpandable` and equivalent debug hooks

This design does not allow:

- a permanent tools pane
- automatic dumping of raw delegate JSON into the transcript
- showing every tool result inline by default

## Behavior Changes

## Delegate Result Summarization

The delegate boundary already accepts typed envelopes and now also coerces bare JSON objects. The next step is to ensure the visible result is useful.

### Role-aware summary extraction

When a structured delegate result is available, the system should derive a user-facing summary from the most meaningful available field in this order:

1. explicit `message` if it is specific and non-generic
2. role-aware extraction from the artifact payload
3. only then a role default fallback

Role defaults are allowed only when no meaningful extraction is possible.

### Role-specific expectations

#### Scout

Expected visible result:

- source location
- what was found
- trigger or origin if known

Examples:

- `The alert is sent by util-rancid/update_cerner_daily.sh:753.`
- `Found the alert source and mailx subject in update_cerner_daily.sh.`

#### Architect

Expected visible result:

- severity
- actionability
- smallest next action

Examples:

- `Low-to-medium severity. Check the verify script path and permissions.`
- `Actionable maintenance issue, not a panic-level incident.`

#### Doctor

Expected visible result:

- root cause
- recommended fix

#### Builder

Expected visible result:

- what changed
- verification result

### Generic fallback text

The following strings are not acceptable as the final visible result if a structured artifact contains more meaning:

- `Evidence gathered.`
- `Architect output ready.`
- `Diagnosis ready.`
- `Implementation complete.`

These may remain internal defaults, but the transcript renderer must not prefer them over meaningful extracted content.

## Interpretive Auto-Chaining

Dispatch should not stop on a scout/evidence-only result when the user asked for interpretation, worry level, urgency, recommendation, or actionability.

Examples of interpretive user asks:

- `is this something i need to worry about?`
- `what does that mean?`
- `what should i do next?`
- `is this safe to ignore?`

Approved behavior:

- scout gathers evidence
- dispatch carries the scout artifact into architect
- architect returns the user-facing interpretation in the same turn

This avoids ending the turn on evidence-only text when the user asked a decision question.

## UI Structure

## Transcript messages

The transcript should contain three main message classes:

- user messages
- inline working messages
- final answer messages

Status banners should be minimized. A successful turn should generally end with the answer message, not with a separate completion block.

## Styling direction

The styling should move toward:

- fewer borders
- less stacked chrome
- clearer spacing between user, working, and answer messages
- stronger visual hierarchy in the text itself rather than via panels

The current rounded boxed message style may remain for user/answer messages if simplified, but the transcript should not feel like a set of adjacent admin cards.

## Header and footer

The header/footer can continue to carry operational state such as:

- current model
- working directory
- ready/running state
- token stats

These are the right place for runtime state. The transcript should carry the conversation and the result.

## Implementation Boundaries

The redesign should stay localized to existing boundaries.

Primary files:

- `internal/tui/chatmodel.go`
- `internal/tui/chatmsg.go`
- `internal/tui/chattheme.go`
- `internal/tui/view_test.go`
- `internal/tui/chatmodel_test.go`
- `internal/agent/delegate_result.go`
- `internal/agent/agent.go`
- `internal/agent/agent_test.go`

Expected responsibilities:

- `chatmodel.go`: map events to transcript behavior, remove panel logic from default view, and collapse working state to one useful inline line
- `chatmsg.go`: simplify rendering toward a transcript-first presentation
- `delegate_result.go`: extract meaningful display summaries from structured artifacts
- `agent.go`: decide when dispatch should continue from evidence to interpretation in the same turn

## Acceptance Criteria

### UI

- the default chat view renders as a single transcript with no side tools pane
- subagent work appears as compact inline working text
- the transcript does not show `Recent activity • <role>` boxes
- the transcript does not show `<role> complete • N turns • N tools` lines by default
- the transcript does not end successful turns with a dominant `Agent complete • <timestamp>` banner

### Delegate result quality

- a bare JSON scout result that contains source and trigger information is surfaced as a meaningful inline answer
- a bare JSON architect result that contains severity and next check is surfaced as a meaningful inline answer
- fallback strings do not hide more useful structured content

### Loop behavior

- when the user asks an interpretive question after an evidence-gathering turn, dispatch auto-chains to architect using scout findings in the same turn
- the final user-visible result is actionable for interpretive asks
- evidence-only turns remain allowed when the user explicitly asked only for evidence/source tracing

## Testing Strategy

Required regression coverage:

- transcript rendering without the tools pane
- compact working-line updates instead of recent-activity blocks
- no generic placeholder final message when structured artifact contains extractable meaning
- scout bare-JSON result yields a meaningful visible summary
- architect bare-JSON result yields a meaningful visible summary
- interpretive follow-up auto-chains from scout to architect in the same turn
- evidence-only follow-up still stops correctly when the user only asked for origin/source

## Risks

- removing the tools pane without a usable explicit-details path could frustrate debugging
- over-aggressive summary extraction could become misleading if it guesses instead of extracting
- too much auto-chaining could make origin-only questions feel slower

## Risk Controls

- keep a deliberate expand/details path for the latest meaningful result
- prefer extraction from explicit fields over inference
- chain only when the user intent is clearly interpretive/actionable

## Outcome

Forge should stop feeling like a multi-panel debugger and start feeling like a competent terminal coding assistant:

- brief while working
- specific when done
- actionable by default
- detailed only when asked
