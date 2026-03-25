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

- exactly one live working line should be visible in the default transcript for the current turn
- repeated or low-signal progress lines should update/replace the current working line rather than create a growing stack
- working lines should be visually lighter than final answers
- working state should disappear or collapse once the final answer is available

Clarification:

- the approved default is one working line total, not one line per role
- if multiple specialist roles run sequentially in the same turn, the working line should update from one role to the next
- if multiple roles ever become active concurrently in a future implementation, the default transcript should still show one aggregated line such as `working: scout + architect...`, not multiple stacked working messages

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

### 4a. Details UX

The default inspection path is `/expand`.

Approved interaction model:

- when a final answer hides additional useful detail, the visible answer may end with a short suffix such as `... (/expand)`
- `/expand` expands only the most recent hidden detail payload
- the expanded content is rendered as a new full-width inline detail message directly below the answer it belongs to
- expanding does not reopen or recreate a side tools pane
- only one expandable payload is tracked at a time via the existing `lastExpandable` concept
- a new meaningful result replaces the previous expandable payload

Content source for `/expand`:

- the full structured artifact string when the inline answer is a summary of a larger artifact
- the full untruncated answer body when the visible answer was shortened for readability
- explicit debug detail only when it was already associated with the most recent meaningful answer

When no expandable payload exists:

- `/expand` must not change the transcript
- the UI should surface a compact no-op message such as `nothing to expand`
- this no-op state should be testable and deterministic

Not in scope for this redesign:

- a dedicated keyboard shortcut for expand
- per-message expand history
- restoring the old tools pane behind a toggle

## Behavior Changes

## Delegate Result Summarization

The delegate boundary already accepts typed envelopes and now also coerces bare JSON objects. The next step is to ensure the visible result is useful.

### Role-aware summary extraction

When a structured delegate result is available, the system should derive a user-facing summary from the most meaningful available field in this order:

1. explicit `message` if it is specific and non-generic
2. role-aware extraction from the artifact payload
3. only then a role default fallback

Role defaults are allowed only when no meaningful extraction is possible.

### Generic-message suppression

The transcript renderer must treat the following normalized messages as generic placeholders:

- `evidence gathered`
- `architect output ready`
- `diagnosis ready`
- `implementation complete`
- `recommendations ready`
- `plan ready`

Normalization rules:

- trim leading/trailing whitespace
- lowercase
- strip one trailing period

Selection rules:

- if `message` is not generic, prefer it
- if `message` is generic and extraction succeeds, use the extracted summary
- if `message` is generic and extraction fails, use the role default fallback
- if both `message` and extracted summary exist and the message is specific, keep the message and store the extracted artifact for `/expand`

Single owner:

- placeholder suppression and summary selection logic should live in the delegate-result path, not separately in transcript rendering
- transcript rendering should consume the already-selected display text and only decide how to present it visually

### Minimal role artifact schemas

These are extraction schemas, not hard validation contracts. Missing fields are allowed. Extraction should use the first available populated field in the listed order.

### Role-specific expectations

#### Scout

Preferred artifact fields:

- `source_file`
- `source_line`
- `exact_text`
- `trigger`
- `most_likely_trigger`
- `why_it_was_sent`
- `source`
- `evidence`

Extraction precedence:

1. `source_file` + `source_line`
2. `source`
3. `trigger` or `most_likely_trigger`
4. first useful entry from `evidence`

Expected visible result:

- source location
- what was found
- trigger or origin if known

Examples:

- `The alert is sent by util-rancid/update_cerner_daily.sh:753.`
- `Found the alert source and mailx subject in update_cerner_daily.sh.`

Example artifact:

```json
{
  "source_file": "util-rancid/update_cerner_daily.sh",
  "source_line": 753,
  "most_likely_trigger": "missing verify script at runtime"
}
```

#### Architect

Preferred artifact fields:

- `severity`
- `worry_level`
- `actionability`
- `assessment`
- `recommended_next_check`
- `suggested_next_checks`
- `likely_impact`

Extraction precedence:

1. `assessment`
2. `severity` or `worry_level` plus `recommended_next_check`
3. `actionability` plus `likely_impact`
4. first useful item from `suggested_next_checks`

Expected visible result:

- severity
- actionability
- smallest next action

Examples:

- `Low-to-medium severity. Check the verify script path and permissions.`
- `Actionable maintenance issue, not a panic-level incident.`

Example artifact:

```json
{
  "worry_level": "low_to_medium",
  "actionability": "actionable",
  "recommended_next_check": "Verify the expected verify script path exists and is executable."
}
```

#### Doctor

Preferred artifact fields:

- `root_cause`
- `fix`
- `risk`
- `evidence`

Extraction precedence:

1. `root_cause`
2. `root_cause` plus `fix`
3. `fix`

Expected visible result:

- root cause
- recommended fix

Example artifact:

```json
{
  "root_cause": "delegate parser rejected a bare JSON object",
  "fix": "coerce bare JSON objects into typed delegate outcomes"
}
```

#### Builder

Preferred artifact fields:

- `summary`
- `files_changed`
- `verification`
- `result`

Extraction precedence:

1. `summary`
2. `result`
3. `files_changed` plus `verification`

Expected visible result:

- what changed
- verification result

Example artifact:

```json
{
  "summary": "Removed the tools pane and switched to a transcript-first layout.",
  "verification": "go test ./internal/tui && go build ./cmd/forge"
}
```

### Generic fallback text

The following strings are not acceptable as the final visible result if a structured artifact contains more meaning:

- `Evidence gathered.`
- `Architect output ready.`
- `Diagnosis ready.`
- `Implementation complete.`

These may remain internal defaults, but the transcript renderer must not prefer them over meaningful extracted content.

## Interpretive Auto-Chaining

Dispatch should not stop on a scout/evidence-only result when the user asked for interpretation, worry level, urgency, recommendation, or actionability.

This should be implemented without adding brittle English-language patch tables for every wording variant.

### Intent classes

At turn start, dispatch should classify the user turn into one of these existing routing intents:

- `trace`: origin, source, evidence, repository inspection
- `interpret`: meaning, severity, risk, actionability, recommendation, next step
- `debug`: root cause and fix explanation
- `implement`: code or file changes

This classification is a routing decision, not a transcript heuristic. The redesign only changes what happens after a completed evidence result.

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

### Decision table

| Turn intent | Existing scout artifact for same topic | Delegate sequence | Stop condition |
|-------------|-----------------------------------------|-------------------|----------------|
| `trace` | no | `scout` | stop after scout unless scout explicitly requests next role |
| `trace` | yes | `scout` or none, at dispatch's discretion | stop after evidence answer |
| `interpret` | no | `scout -> architect` | stop after architect |
| `interpret` | yes | `architect` | stop after architect |

Rules:

- dispatch should not run a second scout if a current-turn or immediately prior-turn scout artifact already covers the same topic
- dispatch should not synthesize additional follow-on roles after architect for this redesign
- if scout explicitly returns `next_role:"architect"` and a concrete `next_task`, dispatch should honor it
- if scout returns complete evidence for an `interpret` turn but no `next_role`, dispatch should synthesize one architect follow-on using the scout artifact

Definition of `same topic`:

- dispatch should carry a per-turn `topic_key` for the latest successful scout artifact
- `topic_key` is the normalized subject of the scout result, derived from the concrete source being discussed
- preferred derivation order:
  1. `source_file[:source_line]` when present
  2. `source`
  3. a dispatch-provided normalized task label when the artifact has no source location
- a follow-up user turn may reuse the prior scout artifact only when dispatch resolves it to the same `topic_key`
- if no stable `topic_key` can be derived, dispatch must not treat the prior artifact as reusable evidence

### Failure and timeout behavior

- if architect blocks, errors, or times out after scout has already produced evidence, dispatch should surface the best scout evidence summary plus a one-line note that interpretation was unavailable
- if scout blocks, dispatch should stop and surface the blocked result
- no retry loop should create more than one automatic architect follow-on for a single user turn

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
- `/expand` with no payload yields a deterministic `nothing to expand`-style no-op

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
