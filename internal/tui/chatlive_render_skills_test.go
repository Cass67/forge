package tui

import (
	"strings"
	"testing"

	"forge/internal/chatstate"
	"forge/internal/skills"

	"github.com/gdamore/tcell/v2"
)

func TestSkillsCommandMarksActiveSkills(t *testing.T) {
	state := chatstate.New()
	state.ActivateSkill("brainstorming")
	m := chatLiveModel{
		state: state,
		skills: []skills.Skill{
			{Name: "brainstorming", Description: "Planning"},
			{Name: "systematic-debugging", Description: "Debugging"},
		},
		panes: chatPaneState{
			agent: chatPaneBufferState{follow: true},
			tools: chatPaneBufferState{follow: true},
		},
	}

	m.handleSlashCommand("/skills")
	out := m.panes.tools.buf
	if !strings.Contains(out, "● /brainstorming — Planning") {
		t.Fatalf("expected active skill marker in output: %q", out)
	}
	if !strings.Contains(out, "○ /systematic-debugging — Debugging") {
		t.Fatalf("expected inactive skill marker in output: %q", out)
	}
}

func TestRenderInputAreaPrefersRequiredSkillWarning(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(80, 10)

	m := chatLiveModel{}
	m.display.flash = "flash message"
	m.display.requiredSkillWarning = "activate /brainstorming before sending"
	colors := m.renderColors()
	styles := m.renderStyles(colors)

	m.renderInputArea(screen, styles.bodyDim, styles.prompt, styles.input, styles.approval, 0, 0, 80, 4, colors.panel, colors.yellow)

	var row strings.Builder
	for x := 0; x < 80; x++ {
		mainc, _, _ := screen.Get(x, 1)
		if mainc == "" {
			mainc = " "
		}
		row.WriteString(mainc)
	}
	got := row.String()
	if !strings.Contains(got, "activate /brainstorming before sending") {
		t.Fatalf("rendered row %q does not contain required skill warning", got)
	}
	if strings.Contains(got, "flash message") {
		t.Fatalf("rendered row %q unexpectedly contains flash text", got)
	}
}

func TestRenderPaneBodiesToolsPanelUsesBubbleCapsForToolSections(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(100, 24)

	m := chatLiveModel{
		width:  100,
		height: 24,
		panes: chatPaneState{
			tools: chatPaneBufferState{
				buf: strings.Join([]string{
					"● read_file {\"path\":\"main.go\"}",
					"status: ok",
					"done in 12ms",
				}, "\n"),
			},
			layout: chatPaneLayoutState{toolsVisible: true, leftWidth: 48},
		},
	}
	colors := m.renderColors()
	styles := m.renderStyles(colors)
	leftX, leftY, leftW, _ := m.leftPaneRect()
	rightX, rightY, rightW, _ := m.rightPaneRect()

	m.renderPaneBodies(screen, styles.body, styles.bodyDim, styles.accent, styles.prompt, styles.titleFocus,
		colors.panel, colors.bright, colors.dim, colors.blue, colors.purple, colors.orange, colors.cyan, colors.green, colors.red,
		styles.diffAdd, styles.diffRm, leftX, leftY, leftW, rightX, rightY, rightW)

	var firstRow strings.Builder
	for x := rightX + 1; x < rightX+rightW-1; x++ {
		mainc, _, _ := screen.Get(x, rightY+1)
		if mainc == "" {
			mainc = " "
		}
		firstRow.WriteString(mainc)
	}
	var paneText strings.Builder
	for y := rightY + 1; y < 24; y++ {
		for x := rightX + 1; x < rightX+rightW-1; x++ {
			mainc, _, _ := screen.Get(x, y)
			if mainc == "" {
				mainc = " "
			}
			paneText.WriteString(mainc)
		}
		paneText.WriteByte('\n')
	}

	if got := firstRow.String(); !strings.Contains(got, "╭─ ● read_file") {
		t.Fatalf("first tool row %q does not contain bubble header cap", got)
	}
	_ = paneText.String()
}

func TestRenderPaneBodiesAgentFencedCodeUsesCodeBubbleCaps(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(100, 24)

	m := chatLiveModel{
		width:  100,
		height: 24,
		panes: chatPaneState{
			agent: chatPaneBufferState{
				buf: strings.Join([]string{
					"Forge • assistant",
					" │ ```go",
					" │ fmt.Println(\"hi\")",
					" │ ```",
				}, "\n"),
			},
			layout: chatPaneLayoutState{leftWidth: 60},
		},
	}
	colors := m.renderColors()
	styles := m.renderStyles(colors)
	leftX, leftY, leftW, _ := m.leftPaneRect()
	rightX, rightY, rightW, _ := m.rightPaneRect()

	m.renderPaneBodies(screen, styles.body, styles.bodyDim, styles.accent, styles.prompt, styles.titleFocus,
		colors.panel, colors.bright, colors.dim, colors.blue, colors.purple, colors.orange, colors.cyan, colors.green, colors.red,
		styles.diffAdd, styles.diffRm, leftX, leftY, leftW, rightX, rightY, rightW)

	readRow := func(y int) string {
		var row strings.Builder
		for x := leftX + 1; x < leftX+leftW-1; x++ {
			mainc, _, _ := screen.Get(x, y)
			if mainc == "" {
				mainc = " "
			}
			row.WriteString(mainc)
		}
		return row.String()
	}

	if got := readRow(leftY + 2); !strings.Contains(got, "╭─ code: go") {
		t.Fatalf("code header row %q does not contain code bubble cap", got)
	}
	if got := readRow(leftY + 3); !strings.Contains(got, "fmt.Println(\"hi\")") {
		t.Fatalf("code body row %q does not contain code content", got)
	}
	if got := readRow(leftY + 4); !strings.Contains(got, "╰─ end code") {
		t.Fatalf("code footer row %q does not contain code bubble footer", got)
	}
}
