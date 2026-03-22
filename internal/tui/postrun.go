package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

type PostRunScreen int

const (
	PostRunScreenDone PostRunScreen = iota
	PostRunScreenFix
	PostRunScreenReview
)

// PostRunResult is returned by RunPostSession to tell main.go what to do next.
type PostRunResult struct {
	Fix          bool
	Issue        string
	WriterModel  string
	AuditorModel string
}

// PostRunApp is the Bubble Tea root model for the post-session UI.
type PostRunApp struct {
	Screen    PostRunScreen
	done      DoneModel
	fix       FixModel
	review    ReviewModel
	outputDir string
	result    PostRunResult
}

func NewPostRunApp(
	outputDir string,
	aborted bool,
	reason string,
	tokenSummary string,
	lastStart SessionStarted,
	writerModels, auditorModels []string,
) PostRunApp {
	return PostRunApp{
		Screen:    PostRunScreenDone,
		done:      NewDoneModel(outputDir, aborted, reason, tokenSummary),
		fix:       NewFixModel(outputDir, lastStart, writerModels, auditorModels),
		outputDir: outputDir,
	}
}

func (a PostRunApp) Result() PostRunResult { return a.result }

func (a PostRunApp) Init() tea.Cmd { return nil }

func (a PostRunApp) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if sz, ok := msg.(tea.WindowSizeMsg); ok {
		a.fix.Width = sz.Width
		return a, nil
	}

	switch msg := msg.(type) {
	case FixRequested:
		a.Screen = PostRunScreenFix
		return a, nil
	case ReviewRequested:
		a.review = NewReviewModel(a.outputDir, "")
		a.Screen = PostRunScreenReview
		return a, nil
	case ReviewDone:
		a.Screen = PostRunScreenDone
		return a, nil
	case FixStarted:
		a.result = PostRunResult{
			Fix:          true,
			Issue:        msg.Issue,
			WriterModel:  msg.WriterModel,
			AuditorModel: msg.AuditorModel,
		}
		return a, tea.Quit
	case NewSessionRequested:
		return a, tea.Quit
	}

	switch a.Screen {
	case PostRunScreenDone:
		updated, cmd := a.done.Update(msg)
		a.done = updated.(DoneModel)
		return a, cmd
	case PostRunScreenFix:
		updated, cmd := a.fix.Update(msg)
		a.fix = updated.(FixModel)
		return a, cmd
	case PostRunScreenReview:
		updated, cmd := a.review.Update(msg)
		a.review = updated.(ReviewModel)
		return a, cmd
	}
	return a, nil
}

func (a PostRunApp) View() string {
	switch a.Screen {
	case PostRunScreenDone:
		return a.done.View()
	case PostRunScreenFix:
		return a.fix.View()
	case PostRunScreenReview:
		return a.review.View()
	}
	return ""
}

// RunPostSession runs the post-session UI (done + optional fix screen).
// It blocks until the user quits or submits a fix.
func RunPostSession(
	outputDir string,
	aborted bool,
	reason string,
	tokenSummary string,
	lastStart SessionStarted,
	writerModels, auditorModels []string,
) PostRunResult {
	app := NewPostRunApp(outputDir, aborted, reason, tokenSummary, lastStart, writerModels, auditorModels)
	p := tea.NewProgram(app, tea.WithAltScreen())
	retModel, err := p.Run()
	if err != nil {
		return PostRunResult{}
	}
	final, _ := retModel.(PostRunApp)
	return final.Result()
}
