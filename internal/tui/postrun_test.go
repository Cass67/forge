package tui_test

import (
	"testing"

	"forge/internal/tui"
)

func makePostRunApp(aborted bool) tui.PostRunApp {
	return tui.NewPostRunApp(
		"/tmp/out",
		aborted,
		"",
		tui.SessionStarted{WriterModel: "claude-sonnet-4-6", AuditorModel: "gpt-4o", Rounds: 3},
		[]string{"claude-sonnet-4-6", "gpt-4o"},
		[]string{"claude-sonnet-4-6", "gpt-4o"},
	)
}

func TestPostRunAppInitialScreenIsDone(t *testing.T) {
	app := makePostRunApp(false)
	if app.Screen != tui.PostRunScreenDone {
		t.Errorf("expected PostRunScreenDone, got %v", app.Screen)
	}
}

func TestPostRunAppFixTransition(t *testing.T) {
	app := makePostRunApp(false)
	app2, _ := app.Update(tui.FixRequested{})
	pa := app2.(tui.PostRunApp)
	if pa.Screen != tui.PostRunScreenFix {
		t.Errorf("expected PostRunScreenFix after FixRequested, got %v", pa.Screen)
	}
}

func TestPostRunAppFixStartedQuitsWithResult(t *testing.T) {
	app := makePostRunApp(false)
	// transition to fix screen
	app2, _ := app.Update(tui.FixRequested{})
	app = app2.(tui.PostRunApp)

	// submit fix — capture the returned model so result is populated
	app3, cmd := app.Update(tui.FixStarted{
		Issue:        "broken auth",
		WriterModel:  "claude-sonnet-4-6",
		AuditorModel: "gpt-4o",
	})
	app = app3.(tui.PostRunApp)
	if cmd == nil {
		t.Fatal("expected quit command")
	}
	_ = cmd() // tea.QuitMsg
	if app.Result().Fix != true {
		t.Error("expected Fix=true in result")
	}
	if app.Result().Issue != "broken auth" {
		t.Errorf("issue = %q", app.Result().Issue)
	}
}
