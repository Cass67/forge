# TUI Styling Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Apply a minimal/quiet visual style with green accent to the input, startup, and done screens using lipgloss.

**Architecture:** Centralise all lipgloss style vars in a new `styles.go` file, then rewrite each screen's `View()` to use those vars. No Update logic, tests, or running/live screens change.

**Tech Stack:** Go, `github.com/charmbracelet/lipgloss` (already a dependency)

**Spec:** `docs/superpowers/specs/2026-03-16-tui-styling-design.md`

---

## Chunk 1: styles.go + input.go

### Task 1: Create styles.go

**Files:**
- Create: `internal/tui/styles.go`

- [ ] **Step 1: Create the file**

```go
package tui

import "github.com/charmbracelet/lipgloss"

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

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/tui/...
```
Expected: no errors.

---

### Task 2: Rewrite input.go View()

**Files:**
- Modify: `internal/tui/input.go`

> Note: This is a pure visual change. The existing tests in `input_test.go` test Update logic, not View output, so they will continue to pass unchanged.

- [ ] **Step 1: Remove the old style var and divider constant**

Delete these two lines from `input.go`:
```go
var focusedStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)

const divider = "─────────────────────────────────────────────\n"
```
Also delete the workaround at the bottom of the file:
```go
// keep focusedStyle used to avoid unused import warning
var _ = focusedStyle
```

- [ ] **Step 2: Remove the lipgloss import from input.go**

`input.go` no longer defines any style vars so the `lipgloss` import is unused. Remove it from the import block. The file still imports `fmt`, `strconv`, `strings`, and `tea`.

- [ ] **Step 3: Rewrite the View() function**

Replace the entire `View()` body with:

```go
func (m InputModel) View() string {
	var sb strings.Builder

	// Header
	sb.WriteString(styleBold.Render("forge") + "  " + styleDim.Render("v0.1.0") + "\n\n")

	// Model rows
	for i, label := range []string{"Writer  ", "Auditor "} {
		var modelName string
		if i == 0 {
			modelName = m.writerModel()
		} else {
			modelName = m.auditorModel()
		}
		if m.ModelFocus == i {
			sb.WriteString(styleGreen.Render("▶") + " " + styleDim.Render(label) + " " + styleBright.Render(modelName))
			sb.WriteString(styleDim.Render("  ← tab · ← → cycle"))
		} else {
			sb.WriteString("  " + styleDim.Render(label) + " " + styleMid.Render(modelName))
		}
		sb.WriteString("\n")
	}

	// Context files
	for _, f := range m.ContextFiles {
		sb.WriteString("  " + styleDim.Render("File     ") + styleMid.Render(f) + "\n")
	}

	// Prompt box
	boxWidth := m.Width - 4
	if boxWidth < 40 {
		boxWidth = 40
	}
	if boxWidth > 100 {
		boxWidth = 100
	}
	innerWidth := boxWidth
	dashes := strings.Repeat("─", boxWidth+2)

	sb.WriteString("\n")
	sb.WriteString(styleDimGreen.Render("task") + "\n")
	sb.WriteString(styleDim.Render("┌"+dashes+"┐") + "\n")

	remaining := append([]rune(m.Prompt), '_')
	for len(remaining) > 0 {
		var chunk []rune
		if len(remaining) >= innerWidth {
			chunk = remaining[:innerWidth]
			remaining = remaining[innerWidth:]
		} else {
			chunk = remaining
			remaining = nil
		}
		pad := strings.Repeat(" ", innerWidth-len(chunk))
		sb.WriteString(styleDim.Render("│") + " " + styleBright.Render(string(chunk)) + pad + " " + styleDim.Render("│") + "\n")
	}
	sb.WriteString(styleDim.Render("└"+dashes+"┘") + "\n")

	// Keybind hint
	if m.Prompt != "" {
		sb.WriteString(styleDimGreen.Render("↵") + styleDim.Render(" Start  ·  ") + styleDim.Render("^C Quit") + "\n")
	} else {
		sb.WriteString(styleDim.Render("type your task...") + "\n")
	}

	if m.RoundsErr != "" {
		sb.WriteString("\n" + styleRed.Render("⚠  "+m.RoundsErr) + "\n")
	}
	return sb.String()
}
```

- [ ] **Step 4: Build and run tests**

```bash
go build ./... && go test ./internal/tui/...
```
Expected: build succeeds, all tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/styles.go internal/tui/input.go
git commit -m "feat: add tui styles.go and restyle input screen"
```

---

## Chunk 2: startup.go + done.go

### Task 3: Restyle startup.go View()

**Files:**
- Modify: `internal/tui/startup.go`

- [ ] **Step 1: Delete the old style vars from startup.go**

Remove these lines (they are replaced by styles.go):
```go
var (
    okStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
    errStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
    waitStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
)
```

- [ ] **Step 2: Remove the lipgloss import from startup.go**

`startup.go` no longer defines style vars; remove `"github.com/charmbracelet/lipgloss"` from its import block. The new `View()` body uses no `fmt` calls, so remove `"fmt"` as well. Remaining imports: `"strings"` and `tea` only.

- [ ] **Step 3: Rewrite the View() function**

Replace the entire `View()` body with:

```go
func (m StartupModel) View() string {
	var sb strings.Builder
	sb.WriteString(styleBold.Render("forge") + "  " + styleDim.Render("v0.1.0") + "\n\n")
	sb.WriteString(styleDim.Render("Checking configuration...") + "\n\n")
	for _, r := range m.Results {
		if r.OK {
			sb.WriteString(styleGreen.Render("✓") + styleMid.Render(" "+r.Name) + "\n")
		} else {
			sb.WriteString(styleRed.Render("✗") + styleMid.Render(" "+r.Name) + styleDim.Render(": "+r.Detail) + "\n")
		}
	}
	if m.Failed {
		sb.WriteString("\n" + styleRed.Render("Check your API keys and try again.") + styleDim.Render("  (q) quit") + "\n")
	} else {
		sb.WriteString("\n" + styleYellow.Render("●") + styleDim.Render(" Checking...") + "\n")
	}
	return sb.String()
}
```

> ⚠️ Do NOT run `go build` here. `done.go` still references `errStyle` which was just deleted. The build will be clean after Task 4 is applied. Proceed directly to Task 4.

---

### Task 4: Restyle done.go View()

**Files:**
- Modify: `internal/tui/done.go`

- [ ] **Step 1: Delete the old style var from done.go**

Remove this line:
```go
var successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
```

- [ ] **Step 2: Remove the lipgloss import from done.go**

Remove `"github.com/charmbracelet/lipgloss"` from the import block. Remaining imports: `"os/exec"`, `"runtime"`, `"strings"`, `tea`. Note: the `fmt` import was used only for `fmt.Sprintf("Output: %s\n\n", ...)` — if the new View no longer uses `fmt.Sprintf`, remove `"fmt"` too.

- [ ] **Step 3: Rewrite the View() function**

Replace the entire `View()` body with:

```go
func (m DoneModel) View() string {
	var sb strings.Builder
	sb.WriteString(styleBold.Render("forge") + "  " + styleDim.Render("v0.1.0") + "\n\n")
	if m.Aborted {
		sb.WriteString(styleRed.Render("✗") + styleBright.Render(" Session aborted") + "\n")
		if m.AbortReason != "" {
			sb.WriteString(styleDim.Render("Reason: ") + styleRed.Render(m.AbortReason) + "\n")
		}
		sb.WriteString("\n" + styleDim.Render("Press (n) to return to setup with the same prompt so you can choose different models.") + "\n")
	} else {
		sb.WriteString(styleGreen.Render("✓") + styleBright.Render(" Session complete") + "\n")
	}
	sb.WriteString("\n" + styleDim.Render("Output  ") + styleMid.Render(m.OutputDir) + "\n")
	sb.WriteString("\n" + styleDim.Render("o  open in Finder   n  new session   q  quit") + "\n")
	return sb.String()
}
```

- [ ] **Step 4: Build and run all tests**

```bash
go build ./... && go test ./...
```
Expected: build succeeds, all tests pass.

- [ ] **Step 5: Build the binary and smoke test**

```bash
go build -o forge ./cmd/forge/
```
Run `./forge` and verify:
- Startup screen shows `forge` bold + dimmed version
- Input screen shows green `▶` on selected model row, dim box borders, `task` label, and dim keybind hints
- Done screen (after a run) shows green `✓` or red `✗`

- [ ] **Step 6: Commit**

```bash
git add internal/tui/startup.go internal/tui/done.go
git commit -m "feat: restyle startup and done screens"
```
