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
