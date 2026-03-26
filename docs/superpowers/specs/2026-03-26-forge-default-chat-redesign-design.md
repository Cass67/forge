# Forge Default Chat Redesign Design

## Summary

Redesign the default `forge` chat experience around one calm, transcript-first assistant that behaves like a strong coding console rather than a managed dashboard.

This redesign makes four product-level changes:

- default chat leaves the alternate screen and runs in the normal terminal buffer
- the UI collapses to a single-column transcript with a roomy composer and minimal chrome
- internal workers stay hidden in normal use and only surface as brief progress when that helps
- workers gain the same real skill access model as the primary assistant, instead of bluffing about slash commands or downgraded capability

The result should feel closer to Codex CLI:

- one coherent assistant in front
- strong prose and useful answers
- copyable terminal output
- native terminal scrollback
- debug depth available only when Forge runs with `-d`

This spec supersedes the default-chat parts of:

- `docs/superpowers/specs/2026-03-25-forge-harness-redesign-design.md`
- `docs/superpowers/specs/2026-03-25-transcript-first-chat-ui-design.md`

Those earlier specs remain useful background, but this document is the source of truth for the default chat product surface and the worker skill contract.

## Problem

Forge still feels heavier and less capable than the model behind it.

Observed failures:

- the default chat UI still behaves like a managed TUI instead of a terminal transcript
- `tea.WithAltScreen()` and mouse capture trap the conversation inside a private viewport, which breaks normal terminal scrollback and makes copy and selection awkward
- left-border message cards, boxed transcript treatment, and decorative header chrome compete with the actual content
- progress rendering exposes too much harness machinery and not enough useful meaning
- hidden-worker behavior is still leaky enough that the product can sound like a coordinator talking about workers instead of an assistant doing work
- workers can claim skill availability without having the same real skill invocation model as the primary assistant
- when that happens, Forge sounds dishonest and brittle, for example by talking about `/brainstorming` as if it were a shell executable

This creates two user-facing failures:

1. the interface feels cramped, old, and harder to use than a normal terminal session
2. the assistant voice degrades when orchestration details leak into the visible transcript

## Goals

- Make default `forge` chat live in the normal terminal buffer.
- Restore native terminal scrollback, mouse selection, and copy behavior in default chat.
- Remove dashboard-style chrome from the default chat surface.
- Make the transcript the primary surface and the product identity.
- Keep the product centered on one visible assistant, `Forge`.
- Allow hidden workers only when they add real execution value.
- Ensure hidden workers can use the same skill system contract as the primary assistant.
- Keep worker orchestration, traces, and internal debugging behind `forge -d`.
- Preserve strong code-reading ergonomics through clear code block styling and syntax highlighting.
- Make the composer visually calmer and functionally clearer.
- Leave enough structural clarity in the runtime that implementation planning can decompose the work into isolated units.

## Non-Goals

- No visible named-worker chat transcript in the default product.
- No restoration of the old tools pane or status panel in normal chat.
- No keyword-patch orchestration model for deciding when to delegate.
- No shell-based fake skill invocation such as trying to execute `/brainstorming`.
- No requirement to preserve the old visual hierarchy if it conflicts with transcript readability.
- No product affordance that lets the user address workers by name.

## Approved Product Direction

### 1. Default Chat Uses the Normal Terminal Buffer

Default `forge` chat must not depend on the alternate screen.

Requirements:

- default chat renders into the normal terminal buffer
- the conversation becomes part of native terminal scrollback
- standard terminal selection and copy behavior work without modifier-key workarounds introduced by Forge
- the terminal owns scrolling in default mode
- Forge owns the active composer and input submission flow, not a private transcript viewport

This is a product decision, not a cosmetic one. The current alternate-screen model is the main reason the app feels trapped and hard to copy from.

### 2. Transcript First, Not Dashboard First

The default screen becomes a single-column conversation transcript with the composer at the bottom.

Requirements:

- no side panels
- no persistent tools panel
- no decorative message cards in the main transcript
- no left-border framing as the primary way of distinguishing messages
- no status boxes that dominate the conversation surface

The transcript should read like a clean terminal conversation:

1. user turn
2. brief inline activity when needed
3. one useful assistant answer

The transcript should not read like a control dashboard with stacked boxes and orchestration furniture.

### 3. Minimal Visual Language

The visual direction is restrained and dark, with the transcript carrying the experience.

Approved visual rules:

- one slim top status line only
- muted labels such as `You` and `Forge`
- generous spacing between turns
- soft off-white text on a graphite or ink background
- one cool accent color for focus or selection
- green and red reserved for genuine success and error states
- code blocks remain intentionally styled and syntax highlighted
- activity lines remain visually subordinate to final answers

The UI should feel closer to a sharp coding console than a themed terminal app.

### 4. Roomy Composer

The composer is part of the product feel and must stop feeling crushed.

Requirements:

- clear vertical gap between the transcript and the composer
- 3 to 5 lines of visible input height in the normal state
- very light border treatment, only enough to orient the eye
- immediate local echo of the submitted user prompt into the transcript
- immediate clearing of the input box after submit

The user should not see the previous prompt linger in the composer after Enter.

#### Composer Interaction Contract

The composer behavior is fixed by this spec.

Keybindings:

- `Enter` submits the current prompt
- `Shift+Enter` inserts a newline
- bracketed paste inserts pasted text verbatim and must never auto-submit

Sizing:

- the composer opens at 3 visible lines when empty or short
- it may grow to 5 visible lines as content wraps
- beyond 5 visible lines, the composer scrolls internally rather than expanding further

Submission acceptance criteria:

- once `Enter` submits, the user prompt is appended to the visible transcript immediately
- the input buffer clears immediately after the prompt is echoed
- the cursor returns to an empty composer without waiting for model output
- resize events must not restore the previous submitted prompt into the composer

Edge cases:

- `Ctrl+C` while a turn is running cancels the active turn and preserves transcript history
- `Ctrl+C` while idle clears the current draft and does not exit the app
- `Ctrl+D` or EOF exits only when no turn is running and the composer is empty
- only one submitted turn may be active at a time
- while a turn is still running, the user may type a draft, but `Enter` does not submit a second turn; Forge keeps the draft and waits for the current turn to finish or be cancelled

This keeps multi-line composition explicit while preserving the fast single-key submit behavior the product wants.

### 5. One Visible Assistant, Hidden Workers Behind It

The user talks to `Forge`, not to a roster of worker personalities.

Requirements:

- the default visible assistant identity is always `Forge`
- the user cannot address workers by name
- hidden workers remain an internal execution tool, not a product metaphor
- the final answer is authored and owned by the top-level assistant, even when worker outputs contribute to it

This preserves the Codex-like product feel: one competent assistant in front, bounded isolation behind the scenes only when useful.

### 6. Capability-Based Worker Use, Not Keyword-Based Routing

The runtime decides whether to use a worker from task shape, uncertainty, and execution benefit, not from brittle English phrase matching.

Approved decision criteria for worker use:

- parallel independent evidence gathering
- isolated bounded edits
- independent verification
- long-running tasks that should not block the main execution loop
- external research or documentation lookups that can safely run in isolation

The runtime should stay local when:

- the task is short or sequential
- the next step depends on immediate shared context
- delegation would mainly add latency
- the work is mostly one reasoning chain

Hard rules:

- workers cannot widen user scope
- workers cannot decide product-facing task identity
- workers cannot become visible conversational actors in default chat
- workers return bounded structured results or observations to the orchestrator

### 7. Quiet Progress in Default Chat

Normal chat may show progress, but only in a compact, useful, human-readable way.

Approved behavior:

- progress appears only when it materially helps the user understand that work is happening
- progress lines are sentence-like, for example `reviewing the repo`, `checking tests`, or `comparing classifier variants`
- progress lines are visually dimmer than final answers
- progress lines should update or collapse rather than stack into a work-log

Not allowed in default chat:

- `dispatching scout`
- `reader worker complete`
- classifier labels
- internal role transitions
- raw orchestration traces

#### Live Progress Semantics

Because default chat runs in the normal terminal buffer, live progress must not rewrite old scrollback.

The progress presenter uses one live progress slot anchored directly above the active composer.

Rules:

- at most one live progress row exists for the active turn
- new progress updates replace the current live progress row in place while the user remains at the live prompt
- the live region is limited to the active prompt area and the one progress row immediately above it
- the live progress row is not committed into scrollback on every update
- when the turn completes, the live progress row either disappears or collapses into one final dim transcript line if that context is still useful
- if Forge can no longer safely redraw the live region because of terminal capability limits, interrupt handling, external output interleaving, or loss of prompt ownership, it stops in-place updates and waits for the final answer
- progress updates must never create an unbounded stack of near-duplicate lines in normal chat

This resolves the tension between native terminal scrollback and useful live feedback.

### 8. Debug Mode Owns the Extra Machinery

`forge -d` is the place where the harness can expose its deeper machinery.

Debug mode may show:

- worker launches and completions
- tool calls and tool outputs
- classifier decisions
- trace events
- timings
- retries
- skill-use metadata

Default chat must not show those details.

This creates a clean product split:

- default mode optimizes for usability and trust
- debug mode optimizes for harness development and diagnosis

#### Mode Matrix

The terminal feature split is part of the design, not an implementation detail.

| Feature | Default `forge` | `forge -d` |
|---|---|---|
| Alternate screen | disabled | allowed |
| Mouse capture | disabled | allowed |
| Private transcript viewport | disabled | allowed |
| Native terminal scrollback | required | optional |
| Bracketed paste | enabled | enabled |
| Inline live progress slot | enabled | optional |
| Worker / tool / trace detail | hidden | visible |
| Skill metadata | hidden | visible |

The default chat path must keep the terminal in the least surprising state. Debug mode may use a richer managed surface because its purpose is harness diagnosis, not transcript ergonomics.

## Terminal I/O Model

Default mode uses an append-first terminal model with one small live region.

Append-only region:

- durable transcript rows
- final assistant answers
- durable error rows

Live region:

- the active composer
- the one progress row immediately above it

Rules:

- Forge may redraw only the live region in default mode
- Forge must not rewrite older durable transcript rows in the normal buffer
- when live-region redraw is unsafe or unsupported, Forge falls back to append-only behavior until the turn completes
- debug mode may use broader managed redraw because it is explicitly a diagnostics surface

## Worker Skill Access Model

### Problem to Solve

A hidden worker currently can produce behavior like:

- claiming skills are available
- then attempting to execute `/brainstorming` as if it were a shell command
- then exposing that failure directly in user-facing prose

This is a harness failure. It makes Forge sound confused about its own capabilities.

### Approved Model

Hidden workers must have access to the same real skill system contract as the primary assistant.

Requirements:

- workers can discover the same available skills exposed to the primary assistant
- workers can read the relevant `SKILL.md`
- workers can follow the instructions inside the skill for their bounded task
- workers must treat skills as orchestration instructions, not shell binaries
- workers must not narrate slash skill names in user-facing prose unless the top-level assistant deliberately chooses to mention a skill

### Runtime Contract

Worker execution should receive:

- the bounded task
- the relevant repo instructions and agent rules
- the visible skill catalog or resolved applicable skills
- the same internal skill-loading mechanism used by the primary assistant
- working directory
- tool permission profile
- deadline or cancellation token
- output schema for the worker class

The orchestrator remains responsible for the worker boundary, but the worker must not be put into a fake environment where skills are only mentioned textually.

#### Adapter Contract

The shared skill runtime adapter must expose a non-shell contract equivalent to:

- `ListSkills() -> []SkillDescriptor`
- `LoadSkill(id) -> SkillDocument`
- `RecordSkillUse(id, workerID, outcome)`

`SkillDescriptor` must at least include:

- stable skill identifier
- short description
- path to `SKILL.md`

The adapter may pre-resolve applicable skills for a worker, but the worker still receives a real skill document and not just a textual mention that a skill exists.

### Failure Handling

If a worker cannot access a required skill:

- treat it as an internal harness error
- fail closed with a structured internal error
- let the orchestrator retry, degrade gracefully, or keep the work local

Not allowed:

- bluffing
- shelling out to slash skill names
- leaking internal skill access failure into the user-facing transcript as though it were a normal assistant explanation

### Visibility Rules

Default chat:

- users see the improved outcome
- users do not see skill invocation chatter from workers

Debug mode:

- may show that a worker used a skill
- may show which skill was chosen
- may show failures in the worker skill path for harness debugging

## Architecture

The redesign should be implemented as clear, isolated units instead of one large TUI rewrite.

### 1. Surface Mode Selector

Purpose:

- choose between default chat surface and debug surface

Responsibilities:

- detect whether Forge is running in normal chat or `-d`
- configure terminal behavior accordingly
- keep the default mode out of the alternate screen

Boundary:

- decides presentation mode
- does not own transcript content, worker policy, or skill loading

Interface:

- input: launch flags, terminal capability probe
- output: `SurfaceModeConfig`

`SurfaceModeConfig` must at least specify:

- alternate-screen enabled or disabled
- mouse capture enabled or disabled
- bracketed paste enabled or disabled
- live-region support enabled or disabled

### 2. Transcript Renderer

Purpose:

- render the visible conversation stream for default chat

Responsibilities:

- render user turns, assistant turns, code blocks, status/error rows, and compact working rows
- apply the approved minimal visual language
- preserve spacing and readable turn rhythm

Boundary:

- consumes already-selected content
- does not decide whether a worker should run
- does not contain orchestration policy

Interface:

- input: durable transcript records plus optional live-region state
- output: rendered default-chat frame or append operations

### 3. Composer Controller

Purpose:

- own input editing, submission, local echo, and clearing behavior

Responsibilities:

- provide the roomy multi-line input
- echo the prompt into the transcript on submit
- clear the input immediately after submission
- keep input behavior predictable in normal terminal mode

Boundary:

- owns active input state only
- does not manage transcript history policy or worker execution

Interface:

- input: keyboard events, paste events, running-state notifications
- output: `SubmitRequest`, `DraftUpdated`, `InterruptRequested`, `ExitRequested`

### 4. Progress Presenter

Purpose:

- turn internal work state into one quiet, useful visible progress line

Responsibilities:

- aggregate low-level work state into human-readable progress
- suppress noisy internal role chatter in default mode
- collapse or replace progress rather than stacking noise

Boundary:

- presentation only
- does not invent policy or decide whether to delegate

Interface:

- input: internal work-state events from the orchestrator
- output: one `LiveProgressState` plus an optional final collapsed progress note

### 5. Worker Orchestrator

Purpose:

- decide when a bounded worker is warranted and supervise its lifecycle

Responsibilities:

- use capability-based delegation rules
- keep workers hidden from the default transcript
- accept structured worker observations
- preserve top-level ownership of the final answer

Boundary:

- can launch and supervise workers
- cannot let workers redefine visible product identity

#### Worker Contract

Workers use a typed bounded interface.

`WorkerRequest` must include:

- worker class
- bounded task text
- request family
- working directory
- instruction bundle
- skill context
- tool permission profile
- deadline or cancellation handle
- expected output schema

`WorkerResult` must include:

- terminal status: `success`, `blocked`, `failed`, `cancelled`, or `timed_out`
- short internal summary
- structured observations or artifact
- optional touched-file list
- optional verification evidence
- optional structured error payload

Streaming:

- workers may emit internal progress events
- workers do not stream user-facing transcript prose directly
- the top-level assistant remains the only owner of visible final-answer prose in default chat

Retries and cancellation:

- the orchestrator owns retry policy
- worker cancellation must yield a structured cancelled result
- worker timeouts must yield a structured timed-out result

Interface:

- input: `TurnRequest`, capability signals, tool results, worker results
- output: `TranscriptRecord`, `LiveProgressState`, `WorkerRequest`, `DebugTraceEvent`

### 6. Skill Runtime Adapter

Purpose:

- give both the primary assistant and hidden workers the same real skill access path

Responsibilities:

- discover available skills
- load `SKILL.md`
- expose skill application through a non-shell mechanism
- make skill availability explicit and testable

Boundary:

- owns skill loading and execution contract
- does not decide product copy or transcript rendering

Interface:

- input: skill lookup requests from the primary assistant or a worker
- output: concrete skill descriptors and loaded skill documents

### 7. Debug Trace Renderer

Purpose:

- render additional harness detail only in debug mode

Responsibilities:

- expose worker events, tool traces, classifier decisions, and skill metadata in `-d`
- keep this detail separate from the normal transcript-first surface

Boundary:

- debug-only
- must not leak back into the default chat path

Interface:

- input: debug trace events, worker events, tool events, classifier decisions
- output: debug-only rendered trace surface

## Data Flow

### Default Chat Turn

1. user types in the composer
2. composer controller submits the turn and immediately echoes it into the transcript
3. orchestrator decides whether the task stays local or uses a worker
4. progress presenter may emit one quiet working line if needed
5. primary assistant performs local work or integrates worker observations
6. transcript renderer shows one final Forge answer
7. any temporary progress line is replaced, collapsed, or left visually subordinate

### Transcript Event Model

The default renderer consumes a small typed event model:

- `UserTurn`
- `AssistantTurn`
- `ProgressUpdate`
- `ErrorRow`
- `SystemNote`

Rules:

- only `UserTurn`, `AssistantTurn`, `ErrorRow`, and the optional final collapsed progress note become durable transcript rows
- `ProgressUpdate` targets the one live progress slot rather than appending durable history each time
- `SystemNote` is reserved for compact, rare, non-error states such as `nothing to expand`
- streaming token chunks are assembled upstream and do not become independent durable transcript records in default mode

Minimum schema:

`UserTurn`

- `id`
- `turn_id`
- `text`

`AssistantTurn`

- `id`
- `turn_id`
- `segments`
- `final`

`ProgressUpdate`

- `turn_id`
- `message`
- `replace_key`

`ErrorRow`

- `id`
- `turn_id`
- `summary`
- optional `detail`

`SystemNote`

- `id`
- `turn_id`
- `message`

Segment schema for `AssistantTurn`:

- `TextSegment{text}`
- `CodeSegment{language, code}`

Ordering:

- records are ordered by emission within a turn
- the final `AssistantTurn` for a turn closes the live progress slot
- at most one unresolved live progress slot exists per active turn

### Worker Skill Path

1. orchestrator creates a bounded worker task
2. worker receives the same instruction and skill contract as the main assistant
3. worker decides whether a relevant skill applies
4. worker loads the skill through the shared skill runtime adapter
5. worker performs the bounded task
6. worker returns structured output or a structured internal error
7. top-level Forge integrates the result into one coherent user-facing answer

## Error Handling

### Default Surface Errors

Default chat must keep errors readable and contained.

Requirements:

- render genuine errors inline as compact, clearly distinct rows
- avoid dumping raw internal structures into the transcript
- preserve enough information for the user to understand what failed and what Forge will do next

Examples of acceptable user-facing outcomes:

- `test run failed in internal/tui: <brief reason>`
- `I couldn't access the required provider session, so I stayed local and inspected the code instead`

Hard surfacing rules:

- local tool failures that materially change the final answer must be surfaced
- recovered local tool failures may stay internal
- recovered worker failures stay internal
- worker failures that prevent completion are surfaced only as user-impact statements, not as harness chatter
- internal skill-loading failures stay internal unless they change the user-facing outcome
- user interrupts are surfaced as a compact visible state such as `stopped`

### Worker Errors

Worker failures remain internal by default.

Requirements:

- capture the failure as structured internal state
- allow retry, fallback-to-local, or graceful degradation
- prevent raw worker confusion from becoming the visible assistant answer

User-visible fallback policy:

- if Forge can recover locally or retry safely, the worker failure remains invisible
- if the failure materially changes what Forge can do for the user, Forge discloses the effect in user terms without naming internal workers unless that detail is necessary in `-d`
- user-facing phrasing describes the outcome and fallback, not harness internals

Examples:

- acceptable: `I couldn't verify that path automatically, so I inspected the code directly instead`
- not acceptable: `reader worker timed out, retrying architect`

### Skill Errors

Skill access failures are harness bugs, not normal user-facing discourse.

Requirements:

- fail closed
- expose detail in `-d`
- degrade to local handling only if that keeps behavior safe and coherent
- keep the normal transcript focused on task impact, not on the missing skill plumbing

Not acceptable:

- `I tried to run /brainstorming as a command`

## Testing And Verification

The redesign needs explicit regression coverage because these failures are easy to reintroduce.

### Test Strategy

The implementation plan must include three layers of verification:

1. unit tests for isolated policy and rendering helpers
2. golden transcript tests for stable visible output
3. PTY-backed integration tests for terminal-mode behavior

This redesign is not complete if it is only manually verified.

Golden examples:

The test suite should include at least:

1. one normal-mode transcript example with a user turn, one live progress update, one final Forge answer, and a cleared composer
2. one debug-mode example showing visible worker and trace detail
3. one error-path example showing a compact visible failure row without raw harness leakage

### Interaction Tests

- default chat does not enable the alternate screen
- default chat allows transcript accumulation in normal buffer mode
- submitted prompts appear in the transcript immediately
- submitted prompts clear from the input immediately
- composer retains multi-line behavior
- `Enter` submits and `Shift+Enter` inserts a newline
- bracketed paste inserts multi-line text without accidental submission

### Rendering Tests

- default chat renders one-column transcript layout
- no default tools pane is rendered
- message rendering uses spacing and labels instead of left-border cards
- code blocks remain visually distinct
- progress rows render in subdued style
- live progress updates do not append duplicate durable transcript rows

### Behavior Tests

- worker progress is aggregated into quiet user-facing lines
- default chat suppresses internal worker naming and role-transition chatter
- top-level assistant owns the final visible answer after worker use
- worker routing remains capability-based and does not depend on brittle keyword patches
- worker timeout and cancellation remain structured and recoverable

### Skill Contract Tests

- workers can discover available skills
- workers can load a real `SKILL.md`
- workers do not attempt shell execution of slash skill names
- missing skill access yields a structured internal error path
- successful worker skill use improves outcome without leaking skill chatter into normal chat

### Debug Separation Tests

- `forge -d` surfaces trace detail that normal chat hides
- default chat never shows debug-only orchestration metadata
- debug skill metadata remains off by default
- default mode disables alternate screen and mouse capture
- debug mode may enable them without affecting the default path

### Log-Driven Regression

Use real debug logs and paraphrased vague prompts as regression fixtures, especially prompts like:

- `describe this directory`
- `go over this directory`
- `review this directory`
- `help me understand this directory`
- `can you write me a script to clean this up?`
- `are you able to use skills?`

The goal is not to patch each phrase. The goal is to prove the runtime behaves coherently across paraphrases and vague task shapes.

## Rollout Notes

- The redesign can be delivered incrementally as long as the end state stays aligned with this spec.
- It is acceptable to preserve a richer managed debug surface under `-d` during migration.
- It is not acceptable to preserve alternate-screen behavior in default chat once this redesign lands.
- If an implementation shortcut conflicts with transcript-first usability, the usability goal wins.

## Open Decisions Resolved By This Spec

- Default `forge` chat is the chat app. No separate `forge chat` identity is required for the product model.
- The tools panel is removed from default chat.
- Hidden workers remain available but non-addressable.
- Hidden workers must support real skill usage through the same contract as the main assistant.
- Debug mode is the only place where the orchestration machinery becomes visible.
