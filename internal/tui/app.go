package tui

import (
    tea "github.com/charmbracelet/bubbletea"
    "forge/internal/llm"
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
    WriterModels  []string
    AuditorModels []string
    // OnStart is called when the user submits the input form.
    // Returns the event channel and the resolved output directory path.
    OnStart func(SessionStarted) (<-chan llm.Event, string)
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
}

func NewApp(cfg AppConfig) App {
    return App{
        Screen:  ScreenStartup,
        Config:  cfg,
        startup: NewStartupModel(),
        input:   NewInputModel(cfg.WriterModels, cfg.AuditorModels),
    }
}

func (a App) Init() tea.Cmd {
    return a.startup.Init()
}

func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case StartupComplete:
        a.Screen = ScreenInput
        return a, nil

    case SessionStarted:
        a.Screen = ScreenRunning
        a.running = NewRunningModel(4, a.input.Rounds)
        if a.Config.OnStart != nil {
            var outDir string
            a.events, outDir = a.Config.OnStart(msg)
            a.outputDir = outDir
            return a, waitForEvent(a.events)
        }
        return a, nil

    case llm.Event:
        updated, cmd := a.running.Update(msg)
        a.running = updated.(RunningModel)
        switch msg.Kind {
        case llm.EventDone:
            a.Screen = ScreenDone
            a.done = NewDoneModel(a.outputDir, false, "")
        case llm.EventAbort, llm.EventError:
            a.Screen = ScreenDone
            errMsg := ""
            if msg.Err != nil {
                errMsg = msg.Err.Error()
            }
            a.done = NewDoneModel(a.outputDir, true, errMsg)
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
        newInput := NewInputModel(a.Config.WriterModels, a.Config.AuditorModels)
        newInput.WriterIdx = a.input.WriterIdx
        newInput.AuditorIdx = a.input.AuditorIdx
        newInput.Rounds = a.input.Rounds
        newInput.LangHint = a.input.LangHint
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
