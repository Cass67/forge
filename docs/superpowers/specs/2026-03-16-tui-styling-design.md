# TUI Styling Design — forge

**Date:** 2026-03-16
**Status:** Approved

## Summary

Apply a minimal/quiet visual style with a green accent color across all three TUI screens (input, startup, done). Uses `lipgloss` which is already a project dependency. No changes to Update logic, tests, or the running/live screens.

**Files changed:** `internal/tui/input.go`, `internal/tui/startup.go`, `internal/tui/done.go`, plus a new `internal/tui/styles.go`.

## Design Decisions

- **Style direction:** Minimal/quiet — dark background, mostly grays, no decorative chrome
- **Accent color:** Green (`#4ade80`) for active/selected elements; very dark green (`#1a5c30`) for dimmed accents
- **No layout restructuring** beyond adding a small `TASK` label above the prompt box

## Color Palette

| Role | Color | Usage |
|---|---|---|
| Bright text | `#ebebeb` | Primary content, selected model name |
| Mid text | `#666` | Secondary labels, unselected model names |
| Dim text | `#3a3a3a` | Hints, keybinds, box borders |
| Green accent | `#4ade80` | Selected row arrow `▶`, `✓` success |
| Dim green | `#1a5c30` | `TASK` label, `↵` hint |
| Red | `#f87171` | `✗` error, abort indicator |
| Yellow | `#fbbf24` | `●` checking/waiting |

## styles.go — Shared Style Variables

Create `internal/tui/styles.go` with the following named vars (replaces all existing per-file style vars):

```go
var (
    styleBright   = lipgloss.NewStyle().Foreground(lipgloss.Color("#ebebeb"))
    styleMid      = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
    styleDim      = lipgloss.NewStyle().Foreground(lipgloss.Color("#3a3a3a"))
    styleGreen    = lipgloss.NewStyle().Foreground(lipgloss.Color("#4ade80"))
    styleDimGreen = lipgloss.NewStyle().Foreground(lipgloss.Color("#1a5c30"))
    styleRed      = lipgloss.NewStyle().Foreground(lipgloss.Color("#f87171"))
    styleYellow   = lipgloss.NewStyle().Foreground(lipgloss.Color("#fbbf24"))
    styleBold     = lipgloss.NewStyle().Foreground(lipgloss.Color("#ebebeb")).Bold(true)
)
```

Delete the old per-file vars: `okStyle`, `errStyle`, `waitStyle`, `successStyle`, `focusedStyle` (and its `var _ = focusedStyle` workaround).

## Per-Screen Changes

### input.go

**Header line:**
```
forge  v0.1.0
```
- `forge` → `styleBold`
- `v0.1.0` → `styleDim`

**Model rows** — two rows, one for writer one for auditor:
- Selected row: `▶` in `styleGreen`, label (`Writer  ` / `Auditor `) in `styleDim`, model name in `styleBright`, followed by `  ← tab · ← → cycle` in `styleDim`
- Unselected row: `  ` (two spaces, no arrow), label in `styleDim`, model name in `styleMid`, no hint

**Prompt section:**
- Remove the `const divider` declaration and its call site `sb.WriteString("\n" + divider)` entirely
- Replace with one blank line followed by the `TASK` label: `sb.WriteString("\n")` then `sb.WriteString(styleDimGreen.Render("task") + "\n")` — preserving the same vertical gap between the model rows and the prompt box
- Box border chars (`┌`, `─`, `┐`, `│`, `└`, `┘`) all rendered in `styleDim`
- Prompt text inside box in `styleBright`
- Cursor `_` unchanged (part of the prompt text, inherits bright style)

**Keybind line (below box):**
- When `m.Prompt != ""`: `styleDimGreen.Render("↵") + styleDim.Render(" Start  ·  ") + styleDim.Render("^C Quit")`
- When `m.Prompt == ""`: `styleDim.Render("type your task...")` — intentionally omits the quit hint; the placeholder is sufficient and `^C` is always available

### startup.go

**Header:** identical treatment to input.go — `forge` in `styleBold`, `v0.1.0` in `styleDim`.

**"Checking configuration..." line:** rendered in `styleDim`.

**Check result rows:**
- `✓ Name` → `styleGreen.Render("✓")` + `styleMid.Render(" " + r.Name)`
- `✗ Name: detail` → `styleRed.Render("✗")` + `styleMid.Render(" " + r.Name)` + `styleDim.Render(": " + r.Detail)`

**Waiting line:** Shown in the `else` branch (same condition as existing code — i.e. `!m.Failed`). Since the screen transitions away via `StartupComplete` as soon as all checks pass, this line is only visible while checks are still running. Render as: `styleYellow.Render("●")` + `styleDim.Render(" Checking...")`

**Error footer** (when failed): `styleRed.Render("Check your API keys and try again.")` + `styleDim.Render("  (q) quit")`

### done.go

**Header:** same as input.go.

**Success state:**
- `styleGreen.Render("✓")` + `styleBright.Render(" Session complete")`

**Abort state:**
- `styleRed.Render("✗")` + `styleBright.Render(" Session aborted")`
- If `m.AbortReason != ""`: `styleDim.Render("Reason: ")` + `styleRed.Render(m.AbortReason)` on the next line

**Output path line:** `styleDim.Render("Output  ")` + `styleMid.Render(m.OutputDir)` — preserves existing content, adds styling. No colon after "Output" (change from existing `"Output: "`).

**Keybind line:** `styleDim.Render("o  open in Finder   n  new session   q  quit")`

**"Return to setup" line** (abort state): `styleDim.Render("Press (n) to return to setup with the same prompt...")`

## Implementation Notes

- Lipgloss `Color()` accepts hex strings directly.
- The `const divider` and its single call site in input.go `View()` are both deleted.
- No changes to any `Update()` methods, no changes to running.go or live.go, no test changes.
