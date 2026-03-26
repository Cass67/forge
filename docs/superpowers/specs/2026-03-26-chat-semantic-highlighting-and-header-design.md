# Chat Semantic Highlighting and Header Design

## Summary

Improve Forge's chat readability by adding balanced semantic highlighting across every text surface and refreshing the startup header so it feels closer to Codex: quieter, better padded, and structurally intentional.

This spec approves two related UI changes:

- a shared semantic highlighter for plain-text chat, status, progress, trace, and running surfaces
- a compact split-rail startup header with a small ASCII `FORGE` mark and no helper line underneath it

The result should make dense output easier to scan without turning the TUI into a rainbow log viewer, while also making the first impression at startup feel calmer and more polished.

## Problem

Forge still makes the user work too hard to read important information.

Observed problems:

- plain-text output is visually flat, so paths, commands, env vars, numbers, and status words blur into surrounding prose
- dense trace output is especially hard to scan because labels and values carry the same visual weight
- live progress and status rows do not surface the important parts strongly enough
- the current startup header feels abrupt and under-designed compared with the Codex-like direction the product wants
- the existing helper line under startup copy is too prescriptive and too heavy for the simplified product surface

This creates two user-facing failures:

1. long answers, reviews, and trace buffers are harder to parse than they should be
2. the startup experience feels rougher and noisier than the rest of the product direction

## Goals

- Make dense output materially easier to scan.
- Highlight the most useful token classes without overwhelming normal prose.
- Apply one consistent semantic model across all plain-text surfaces.
- Preserve existing code-block and diff rendering where it already works well.
- Keep status and progress surfaces calmer than trace surfaces.
- Refresh the startup header so it feels simpler and more like Codex.
- Add more padding and clearer hierarchy at startup without wasting vertical space.

## Non-Goals

- No full markdown renderer replacement.
- No new panels, panes, or dashboard furniture.
- No badge-heavy styling across normal transcript prose.
- No multi-line ASCII banner that pushes the transcript too far down.
- No changes to the core product copy beyond the agreed startup/header text adjustments.
- No attempt to syntax-highlight every non-code sentence.

## Approved Product Direction

### 1. Shared Semantic Highlighter

Forge should use one shared semantic highlighter for plain text in the Bubble Tea chat surfaces.

This highlighter is responsible for detecting stable token classes and applying per-surface styling intensity. The tokenizer should live in `internal/tui`, not inside one specific pane renderer.

The approved reason for this design:

- consistency across surfaces
- one place to tune highlighting behavior
- lower risk of drift than per-surface regex rules
- easier testing than scattered ad hoc styling

### 2. Balanced Visual Intensity

The semantic highlighting style should follow the approved `B. Balanced` direction.

Rules:

- highlight only high-signal tokens
- keep ordinary prose mostly unchanged
- dim labels and structure where appropriate
- reserve the strongest accents for explicit outcome tokens such as success, failure, and warning states
- avoid turning every interesting token into a pill or badge

The goal is scannability, not decoration.

### 3. Approved Token Classes

The shared tokenizer should detect a small, stable set of token classes:

- `path`
  - file paths
  - directory paths
  - filenames when they are clearly path-like or code-like
- `command`
  - shell commands
  - tool names
  - obvious command snippets such as `go test ./...`
- `env`
  - environment variables like `$FORGE_THEME`
  - config-style keys when they are functioning like environment/config identifiers
- `number`
  - counts
  - token totals
  - durations such as `1.2s`
  - percentages and other compact numeric summaries
- `status-good`
  - words such as `complete`, `ready`, `approved`, `ok`, `success`
- `status-bad`
  - words such as `error`, `failed`, `denied`, `blocked`
- `status-warn`
  - words such as `warning`, `retry`, `pending`
- `label`
  - structural prefixes like `status:`
  - trace markers such as `tool_call:`, `tool_result:`, `trace:`

The tokenizer should remain intentionally conservative. If a token class is ambiguous, prefer not highlighting it.

### 3a. Detection Rules

The tokenizer should classify only plain, unstyled text spans. It should not guess aggressively.

Shared rules:

- classification happens on raw plain text before ANSI styling is applied
- existing ANSI escape sequences are treated as opaque input and passed through without additional semantic styling
- trailing punctuation such as `.`, `,`, `;`, `)`, and `]` should not prevent classification, but punctuation should not inherit the token style unless it is part of the token itself
- URLs such as `https://...` are out of scope for semantic highlighting in this slice and should remain plain text
- if a candidate span could reasonably be prose or code, prefer leaving it plain

Per-class rules:

- `path`
  - match tokens beginning with `/`, `./`, `../`, or `~/`
  - match Windows-style absolute paths such as `C:\work\forge` or `C:/work/forge`
  - match slash-separated or backslash-separated path-like tokens with no URL scheme
  - bare filenames such as `main.go` or `forge-debug.jsonl` may be highlighted only when they use a conservative file-extension allowlist or appear in a code-like context
  - ordinary dotted prose words should not be highlighted as paths
- `command`
  - match shell-like snippets that begin with an executable or tool-shaped token and continue through flags and path-like arguments until a clause boundary
  - examples include `go test ./...`, `git status`, `forge -d`, and `npm run build`
  - a lone flag such as `--count=1` is not a command by itself
  - natural-language phrases such as `run tests` or `check the repo` are not commands
- `env`
  - match `$NAME` and `${NAME}` forms
  - match config-style identifiers such as `FORGE_THEME` only when they appear in assignment or key/value context
  - mid-sentence uppercase words that are not config-like identifiers should remain plain
- `number`
  - match compact numeric summaries such as `12`, `1.2s`, `45%`, `2/7`, and `128k`
  - in prose, prefer highlighting numeric summaries that look operational rather than every incidental year or chapter number
- `status-good`, `status-bad`, `status-warn`
  - match whole-word outcome tokens only
  - in `prose`, highlight them only when they appear in structured status-like contexts such as `status: failed` or a short verdict line
  - in `status` and `trace`, the same tokens may be highlighted anywhere on the line
- `label`
  - match identifier-like prefixes that end with `:` and introduce a structured value
  - bare words without a trailing `:` should not be highlighted as labels in this slice

### 4. Surface Profiles

The tokenizer should be paired with three rendering profiles:

- `prose`
  - for normal chat answers and transcript prose
  - balanced and restrained
- `status`
  - for status rows and live progress
  - calmer than `trace`, with its strongest accents reserved for explicit success/error/warning tokens
- `trace`
  - for debug trace surfaces
  - slightly stronger than prose because trace lines are denser and more structured

Surface mapping:

- normal chat prose uses `prose`
- `MsgStatus` and `MsgWorking` use `status`
- trace dock and trace overlay use `trace`
- running and live two-pane views use the shared tokenizer with a restrained profile appropriate for continuous updates

### 5. Code Blocks Stay on Their Existing Path

Fenced code blocks and diffs keep the current rendering path.

Requirements:

- do not run semantic prose highlighting inside fenced code blocks
- preserve current diff coloring
- preserve current code block structure and labeling
- only improve non-fenced text around those blocks

This avoids fighting the code renderer and keeps the scope focused.

### 5a. Inline Code and Existing Styling

The semantic renderer should stay out of text that is already structurally marked or already styled.

Requirements:

- inline code spans delimited by backticks are treated as opaque and should keep their existing inline-code treatment
- markdown-style links should not trigger path highlighting inside their URL targets unless the URL is rendered literally as plain text
- existing ANSI-styled spans from tools or logs should pass through unchanged and should not be re-tokenized

### 6. ANSI-Safe Wrapping and Layout Stability

The semantic renderer must preserve wrapping, scrolling, and viewport behavior.

Requirements:

- wrapping must account for ANSI styling correctly
- highlighting must not break width calculations
- scrolling behavior must remain unchanged
- progress rows must not become wider or noisier than the current layout allows
- trace dock and overlay wrapping must stay stable when styling is present

The implementation contract for this slice:

- the shared module should first return semantic spans with no ANSI applied yet
- profile styling should be applied only at the final render step for those spans
- existing wrapping and viewport code remains the source of truth for layout and scrolling
- width calculations must continue to use stripped text or the existing ANSI-aware width helpers already used by the TUI
- the highlighter may style inline segments, but it must not replace the existing layout structure with a new block model

### 7. Startup Header Refresh

The startup header should move away from the current flat full-width line and toward a simpler Codex-like compact card.

Approved direction: `B. Split Rail`

Requirements:

- use a compact bordered card aligned to the top-left of the chat screen
- include a small ASCII `FORGE` mark as a decorative identity element
- keep the ASCII mark restrained and single-purpose, not a large banner
- increase padding and breathing room around the header content
- reduce the visual dominance of the previous all-caps/pill-like treatment
- preserve quick scan access to model and working directory

Approved content structure:

- left rail: small ASCII `FORGE` mark
- right side:
  - prominent active model
  - dim label/value rows for model and directory

### 7a. Narrow-Width Header Rules

The startup header must degrade predictably on smaller terminals.

Requirements:

- model and directory rows stay single-line; they should not wrap inside the card
- if the working directory is inside the home directory, render it home-relative before truncation
- long directories should use left-side ellipsis so the tail of the path remains visible
- long model names should use right-side truncation with ellipsis
- at widths of 72 columns or more, render the approved split-rail layout
- at widths from 56 to 71 columns, collapse the left rail to a smaller one-line ASCII wordmark rather than overflowing
- below 56 columns, hide the ASCII mark and keep a simple stacked metadata card
- the header must never cause horizontal overflow

### 8. Startup Copy Under the Header

The startup helper line should be removed.

Approved startup copy:

- keep `Forge is ready.`
- remove the old helper line such as `Ask for a code change, bugfix, or investigation.`
- do not replace it with another persistent helper line

The product should trust the simpler startup surface.

## Integration Plan

### Shared Highlighter Module

Add a new shared semantic rendering helper in `internal/tui`, for example:

- `internal/tui/semantic.go`

Responsibilities:

- detect token classes in plain text
- return semantic spans for plain text with no ANSI styling applied
- render styled inline segments for one of the approved profiles
- expose helpers that can be reused by chat messages, status/progress rows, trace output, and running views

Suggested interface boundary:

- `TokenizePlain(text) -> spans`
- `RenderSemantic(spans, profile) -> styled string`
- one shared plain-text entry helper should sit above these functions so every non-code surface reaches semantic highlighting through the same path

### Primary Integration Points

The highlighter should plug into the existing plain-text entry points:

- `internal/tui/codeblock.go`
  - make the plain non-fenced block renderer the primary plain-text entry point for semantic highlighting
  - fenced code blocks and diffs should bypass the semantic helper completely
- `internal/tui/chatmsg.go`
  - route agent/user plain-text content through the shared plain-text entry helper rather than duplicating token logic here
  - use the `status` profile for `MsgStatus`
  - use a restrained profile for `MsgWorking`
- `internal/tui/traceview.go`
  - apply the `trace` profile in the dock and overlay through the same semantic module
- `internal/tui/running.go`
  - apply restrained semantic styling to pane content without changing the pane model
- `internal/tui/live.go`
  - apply the same semantic treatment to the older running/live surfaces where plain text is rendered
- `internal/tui/chatstats.go`
  - update `renderStatusHeader(...)` to use the new startup/header structure

## Styling Guidance

### Paths

- use the existing primary accent family
- paths should stand out clearly but not glow

### Commands and Tools

- use the secondary accent family or a command-specific accent closely related to it
- command snippets should be recognizable at a glance

### Env Vars

- highlight env vars distinctly from paths and commands
- subtle pill treatment is acceptable if it remains restrained and consistent

### Numbers and Durations

- use a success-leaning or neutral bright accent for scanability
- do not make every number heavy in prose

### Labels

- labels such as `status:` and `tool_call` should be dimmer than their values
- label/value distinction is a key readability improvement for traces

### Status Words

- success should use the existing success color family
- failure should use the existing error color family
- warnings and pending states should use the warning family

## Acceptance Criteria

### Highlighting

- plain transcript prose applies semantic styling only to the approved token classes and leaves ordinary prose unchanged
- trace output renders labels dimmer than their values and uses the `trace` profile rather than prose defaults
- status/progress rows use the `status` profile, with stronger emphasis only on explicit outcome tokens
- code fences, diffs, inline code spans, and pre-styled ANSI spans bypass semantic tokenization
- styled output fits within the same wrapped widths as the equivalent unstyled text

### Representative Examples

The implementation should support deterministic tests using examples like these:

- `Run go test ./... from ./internal/tui`
  - `go test ./...` is styled as `command`
  - `./internal/tui` is styled as `path`
- `status: approved in 1.2s`
  - `status:` is styled as `label`
  - `approved` is styled as `status-good`
  - `1.2s` is styled as `number`
- `tool_call: forge -d`
  - `tool_call:` is styled as `label`
  - `forge -d` is styled as `command`
- `Set $FORGE_THEME before restarting`
  - `$FORGE_THEME` is styled as `env`
- `Review main.go and docs/spec.md`
  - `main.go` and `docs/spec.md` are styled as `path`
- `Please review this carefully and let me know what you think`
  - no semantic styling is applied
- ``Use `go test ./...` if you want the exact command``
  - the inline-code span is not re-tokenized by the semantic highlighter
- `https://platform.openai.com should remain plain here`
  - the URL remains plain text in this slice

### Startup Header

- at comfortable terminal widths, the startup header renders as a compact split-rail card rather than a flat full-width strip
- at medium widths, the rail collapses without horizontal overflow
- at narrow widths, the ASCII mark hides and the metadata remains readable in a stacked card
- the small ASCII `FORGE` mark is present and restrained
- model and directory values remain single-line and truncate according to the approved rules
- the startup body keeps `Forge is ready.`
- no helper line appears underneath it

## Verification

Required verification after implementation:

- targeted renderer tests for token classes and profiles
- golden-style tests covering the representative examples in this spec
- trace rendering tests with ANSI-aware content
- wrapping/width tests so highlighted content does not exceed expected widths
- startup header rendering tests covering spacing, truncation, and the 72-column, 64-column, and 48-column layouts
- `go test ./internal/tui -count=1`
- `go test ./... -count=1`
- live manual check against a fresh `/tmp/forge-debug.jsonl` after implementation

## Risks

- token detection that is too aggressive will make transcript prose noisy
- token detection that is too weak will not justify the feature
- ANSI styling can subtly break width calculations if it is introduced in the wrong layer
- startup header polish can easily overshoot into decorative chrome if the ASCII treatment becomes too large

## Out of Scope Follow-Up Ideas

- optional user-tunable highlight intensity levels
- provider/model-specific startup header metadata
- richer semantic treatment for structured tables
- improved code fence syntax highlighting beyond the current path
