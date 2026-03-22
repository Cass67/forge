package tui_test

import (
	"forge/internal/tui"
	"testing"
)

func TestAppInitialScreenIsStartup(t *testing.T) {
	m := tui.NewApp(tui.AppConfig{})
	if m.Screen != tui.ScreenStartup {
		t.Errorf("expected startup screen, got %v", m.Screen)
	}
}
