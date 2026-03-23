# Input Box Growth Design

## Summary

Update the live TUI composer so the input box grows with multiline input, especially pasted content, up to 50% of terminal height. As the input expands, the transcript area should shrink to make room. Cursor navigation must continue to work across the full logical input buffer, including wrapped lines, with the visible input window following the cursor once the composer reaches its maximum height.

## Problem

The current live TUI composer visually caps input to a small number of wrapped lines. Large multiline pastes are preserved in the input buffer, but the composer does not expose enough visible space for comfortable review or editing. This makes pasted content appear truncated and makes multiline editing feel constrained.

## Goals

- Grow the composer as multiline content is entered or pasted.
- Cap composer growth at 50% of terminal height.
- Shrink the transcript area dynamically as the composer grows.
- Preserve full cursor-key navigation across the entire input buffer.
- Keep wrapped-line behavior consistent with the current editor model.
- Maintain usable behavior on small terminals.

## Non-Goals

- No modal editor or alternate full-screen compose mode.
- No changes to submit semantics.
- No changes to slash command behavior beyond existing multiline rules.
- No redesign of transcript rendering.

## Current Behavior

The live TUI wraps the full input buffer into visual lines, but only exposes a small visible slice in the composer. The visible line cap is currently fixed and independent of actual input size beyond a small upper bound. The visible input box height is derived from the number of currently visible wrapped lines rather than the full wrapped input size.

As a result:

- multiline paste is stored but not shown with enough visible context,
- long wrapped input quickly exceeds the visible area,
- users can perceive the composer as truncating pasted content.

## Proposed Behavior

### Adaptive composer height

The composer height should be based on the number of wrapped visual lines needed to display the current input around the cursor, subject to a maximum of 50% of terminal height.

Behavior:

- Short input keeps the composer compact.
- Multiline or wrapped input grows the composer as needed.
- Once the composer reaches its maximum height, it stops growing and instead scrolls internally.
- The transcript/body area uses the remaining vertical space above the composer.

### Input windowing and cursor visibility

The input layout should continue to wrap the entire logical input buffer. Cursor movement operates over the full buffer and the visible input window follows the cursor when necessary.

Behavior:

- Left and right arrows move by rune as they do today.
- Up and down arrows move across wrapped visual lines.
- Preferred horizontal cursor position should continue to be preserved where supported by the current implementation.
- When the cursor moves outside the visible input window, the input window scrolls to keep the cursor visible.

### Transcript space negotiation

As the composer grows, the transcript pane should shrink accordingly. This should happen automatically as part of layout calculation rather than through separate mode switches.

Behavior:

- Input growth takes space from the transcript area.
- The transcript remains visible with a minimum usable height, subject to terminal constraints.
- Clearing or submitting the input restores transcript space.

## Layout Rules

### Maximum input height

Use an adaptive visible-line cap based on terminal height, targeting 50% of available vertical space.

Design intent:

- Composer can grow substantially for pasted code, logs, and long prompts.
- Transcript remains visible and usable.
- Very small terminals still reserve enough space for both regions to function.

### Minimum heights

The layout should maintain reasonable minimums for both transcript and input regions.

Suggested constraints:

- minimum visible input height equivalent to the current compact box,
- minimum transcript height sufficient to show at least a few lines of conversation,
- graceful degradation when terminal height is extremely small.

Exact values can be chosen during implementation to match current layout conventions.

## Affected Components

### `internal/tui/chatlive.go`

Likely changes:

- Replace the fixed visible-line cap with a terminal-relative cap.
- Update input height calculation to use adaptive visible input sizing.
- Ensure body height and input height are computed together consistently.
- Preserve current wrapped-line and cursor-tracking logic.

### `internal/tui/chatlive_render.go`

Likely changes:

- Render the larger input region correctly.
- Ensure cursor placement remains correct within the expanded or internally scrolled composer.
- Preserve clipping and border behavior as height changes.

### `internal/tui/chatlive_mouse.go`

Likely changes:

- Confirm mouse hit-testing maps correctly into the visible input slice when the composer is taller and internally scrolled.

### `internal/tui/chatlive_test.go`

Add or update tests for:

- compact input remaining compact,
- multiline paste expanding the composer,
- composer growth capping at 50% height,
- cursor movement keeping the cursor visible after internal scrolling begins,
- wrapped-line cases,
- small terminal behavior.

## Edge Cases

### Small terminals

When terminal height is constrained, the composer and transcript should both remain minimally usable. The implementation should avoid collapsing either area to zero or producing unstable layout.

### Wrapped long lines

Composer growth should be driven by wrapped visual lines, not just newline count. A single very long line should expand the composer if wrapping requires more rows.

### Large paste bursts

Pasting many lines should immediately expand the composer up to its limit. Additional lines beyond that limit should remain editable through internal scrolling.

### Cursor movement beyond visible input window

Once the composer is at maximum height, moving through the input with arrow keys should shift the visible input window to keep the cursor in view.

### Mouse interaction

Clicking within the visible composer should still position the cursor correctly when the input window is offset from the first wrapped line.

## Testing Strategy

### Unit and model tests

Add tests covering:

1. **Compact input**
   - Single-line input keeps a compact composer.

2. **Multiline growth**
   - Multiple explicit lines increase visible composer height.

3. **Wrapped growth**
   - Long input that wraps increases composer height even without newlines.

4. **50% cap**
   - Large input does not exceed the configured half-height limit.

5. **Cursor-following after cap**
   - When the cursor moves through content larger than the visible composer, the visible input window scrolls to keep the cursor visible.

6. **Reset behavior**
   - Clearing or submitting the input returns the composer to compact height.

### Manual verification

Verify interactively that:

- pasting 10–20 lines causes visible growth,
- the transcript shrinks as expected,
- arrow-key navigation works across the entire pasted input,
- cursor visibility is maintained at top, middle, and bottom of large input,
- behavior remains usable in narrow and short terminals.

## Risks

- Layout regressions in very small terminals.
- Off-by-one errors in cursor visibility or visible-line windowing.
- Mouse hit-testing drift if input rect and visible line offsets are not updated together.

These risks are moderate and localized to the live TUI input/layout path.

## Recommended Implementation Direction

Implement this as a focused update to the existing live TUI composer. Reuse the current wrapping and cursor model, but replace the small fixed visible-line cap with a terminal-relative cap and let the composer claim up to 50% of terminal height before internal scrolling takes over.

This delivers the requested behavior with minimal conceptual change to the existing UI.