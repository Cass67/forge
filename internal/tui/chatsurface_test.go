package tui

import (
	"reflect"
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestDefaultSurfaceModeDisablesAltScreen(t *testing.T) {
	cfg := ChatLiveConfig{SurfaceKind: ChatSurfaceDefault, DebugEnabled: false}
	mode := cfg.SurfaceMode()

	if mode.UseAltScreen {
		t.Fatalf("default mode = %#v", mode)
	}
	if !mode.EnableMouseCapture {
		t.Fatalf("default mode should enable mouse capture: %#v", mode)
	}
	if !mode.EnableBracketedPaste || !mode.EnableLiveRegion {
		t.Fatalf("default mode missing required flags: %#v", mode)
	}
}

func TestDebugSurfaceModeUsesSharedTranscriptSurface(t *testing.T) {
	cfg := ChatLiveConfig{SurfaceKind: ChatSurfaceDebug, DebugEnabled: false}
	mode := cfg.SurfaceMode()

	if mode.UseAltScreen {
		t.Fatalf("debug mode should reuse transcript surface: %#v", mode)
	}
	if !mode.EnableMouseCapture {
		t.Fatalf("debug mode should enable mouse capture: %#v", mode)
	}
	if !mode.EnableBracketedPaste || !mode.EnableLiveRegion {
		t.Fatalf("debug mode missing required flags: %#v", mode)
	}
}

func TestDefaultSurfaceModeProgramOptions(t *testing.T) {
	opts := programOptionsForSurfaceMode(ChatLiveConfig{SurfaceKind: ChatSurfaceDefault, DebugEnabled: true}.SurfaceMode())

	if hasProgramOption(opts, "WithAltScreen") {
		t.Fatalf("default options should not enable alt screen: %#v", programOptionNames(opts))
	}
	if !hasProgramOption(opts, "WithMouseCellMotion") {
		t.Fatalf("default options should enable mouse capture: %#v", programOptionNames(opts))
	}
	if hasProgramOption(opts, "WithoutBracketedPaste") {
		t.Fatalf("default options should preserve bracketed paste: %#v", programOptionNames(opts))
	}
}

func TestDebugSurfaceModeProgramOptions(t *testing.T) {
	opts := programOptionsForSurfaceMode(ChatLiveConfig{SurfaceKind: ChatSurfaceDebug, DebugEnabled: false}.SurfaceMode())

	if hasProgramOption(opts, "WithAltScreen") {
		t.Fatalf("debug options should not enable alt screen: %#v", programOptionNames(opts))
	}
	if !hasProgramOption(opts, "WithMouseCellMotion") {
		t.Fatalf("debug options should enable mouse capture: %#v", programOptionNames(opts))
	}
	if hasProgramOption(opts, "WithoutBracketedPaste") {
		t.Fatalf("debug options should preserve bracketed paste: %#v", programOptionNames(opts))
	}
}

func hasProgramOption(opts []tea.ProgramOption, want string) bool {
	for _, name := range programOptionNames(opts) {
		if strings.Contains(name, want) {
			return true
		}
	}
	return false
}

func programOptionNames(opts []tea.ProgramOption) []string {
	names := make([]string, 0, len(opts))
	for _, opt := range opts {
		ptr := reflect.ValueOf(opt).Pointer()
		fn := runtime.FuncForPC(ptr)
		if fn == nil {
			names = append(names, "<nil>")
			continue
		}
		names = append(names, fn.Name())
	}
	return names
}
