package tui

import (
	"fmt"

	"forge/internal/llm"

	tea "github.com/charmbracelet/bubbletea"
)

type Screen int

const (
	ScreenStartup Screen = iota
	ScreenInput
	ScreenRunning
	ScreenDone
)

// AppConfig holds dependencies injected at startup.
type AppConfig struct {
	WriterModels   []string
	AuditorModels  []string
	DefaultWriter  string // model pre-selected for writer (from config)
	DefaultAuditor string // model pre-selected for auditor (from config)
	// OnSnapshot is called when the user requests a snapshot.
	OnSnapshot func()
	// OnPause/OnResume are called for pause toggling.
	OnPause  func()
	OnResume func()
}

// App is the root Bubble Tea model that routes between screens.
type App struct {
	Screen    Screen
	Config    AppConfig
	startup   StartupModel
	input     InputModel
	running   RunningModel
	done      DoneModel
	events    <-chan llm.Event
	paused    bool
	outputDir string
	lastStart SessionStarted
	width     int
	height    int
	Started   bool
}

func NewApp(cfg AppConfig) App {
	return App{
		Screen:  ScreenStartup,
		Config:  cfg,
		startup: NewStartupModel(),
		input:   NewInputModel(cfg.WriterModels, cfg.AuditorModels, cfg.DefaultWriter, cfg.DefaultAuditor),
	}
}

func (a App) Init() tea.Cmd {
	return a.startup.Init()
}

func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
	case StartupComplete:
		a.Screen = ScreenInput
		a.input.Width = a.width
		return a, nil

	case SessionStarted:
		a.lastStart = msg
		a.Started = true
		return a, tea.Quit

	case llm.Event:
		updated, cmd := a.running.Update(msg)
		a.running = updated.(RunningModel)
		switch msg.Kind {
		case llm.EventDone:
			a.Screen = ScreenDone
			a.done = NewDoneModel(a.outputDir, false, "", "")
		case llm.EventAbort, llm.EventError:
			a.Screen = ScreenDone
			errMsg := ""
			if msg.Err != nil {
				errMsg = msg.Err.Error()
			}
			a.done = NewDoneModel(a.outputDir, true, errMsg, "")
		default:
			return a, tea.Batch(cmd, waitForEvent(a.events))
		}
		return a, cmd

	case PauseToggled:
		a.paused = !a.paused
		if a.paused && a.Config.OnPause != nil {
			a.Config.OnPause()
		} else if !a.paused && a.Config.OnResume != nil {
			a.Config.OnResume()
		}
		return a, nil

	case SnapshotRequested:
		if a.Config.OnSnapshot != nil {
			a.Config.OnSnapshot()
		}
		return a, nil

	case NewSessionRequested:
		a.Screen = ScreenInput
		newInput := NewInputModel(a.Config.WriterModels, a.Config.AuditorModels, a.Config.DefaultWriter, a.Config.DefaultAuditor)
		if idx := indexOf(a.Config.WriterModels, a.lastStart.WriterModel); idx >= 0 {
			newInput.WriterIdx = idx
		}
		if idx := indexOf(a.Config.AuditorModels, a.lastStart.AuditorModel); idx >= 0 {
			newInput.AuditorIdx = idx
		}
		newInput.Rounds = a.lastStart.Rounds
		if newInput.Rounds == 0 {
			newInput.Rounds = a.input.Rounds
		}
		newInput.RoundsInput = fmt.Sprintf("%d", newInput.Rounds)
		newInput.LangHint = a.lastStart.LangHint
		newInput.Prompt = a.lastStart.Prompt
		newInput.ContextFiles = append([]string(nil), a.lastStart.ContextFiles...)
		newInput.Width = a.width
		a.input = newInput
		return a, nil

	case CheckResult:
		updated, cmd := a.startup.Update(msg)
		a.startup = updated.(StartupModel)
		return a, cmd
	}

	// delegate to current screen
	switch a.Screen {
	case ScreenStartup:
		updated, cmd := a.startup.Update(msg)
		a.startup = updated.(StartupModel)
		return a, cmd
	case ScreenInput:
		updated, cmd := a.input.Update(msg)
		a.input = updated.(InputModel)
		return a, cmd
	case ScreenRunning:
		updated, cmd := a.running.Update(msg)
		a.running = updated.(RunningModel)
		return a, cmd
	case ScreenDone:
		updated, cmd := a.done.Update(msg)
		a.done = updated.(DoneModel)
		return a, cmd
	}
	return a, nil
}

func (a App) View() string {
	switch a.Screen {
	case ScreenStartup:
		return a.startup.View()
	case ScreenInput:
		return a.input.View()
	case ScreenRunning:
		return a.running.View()
	case ScreenDone:
		return a.done.View()
	}
	return ""
}

func (a App) LastStart() SessionStarted {
	return a.lastStart
}

// StartupComplete is sent when all startup checks pass.
type StartupComplete struct{}

// waitForEvent returns a command that reads the next event from the session runner.
func waitForEvent(events <-chan llm.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-events
		if !ok {
			return llm.Event{Kind: llm.EventDone}
		}
		return ev
	}
}
