package gui

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"forge/internal/chatstate"
	"forge/internal/skills"
	"forge/internal/tui"
)

// Wails binds every exported method of the service, and the frontend calls
// them by name over in bridge.ts. Pin the surface so a rename or an
// accidentally exported helper fails here rather than silently in the window.
func TestBoundMethodSurface(t *testing.T) {
	want := []string{
		"Approve", "AttachImage", "AwaitProviderLogin", "Cancel", "ChooseWorkspace",
		"Clear", "CompleteProviderLogin", "DeleteThread", "Efforts", "History", "Init", "Models",
		"NewSession", "OpenURL", "OpenWorkspaceAt", "Providers", "Restore", "Send",
		"SendWithImages", "SetEffort", "SetProviderKey", "SignOutProvider",
		"StartProviderLogin", "SwitchModel", "Threads", "Workspaces",
	}
	var got []string
	typ := reflect.TypeOf(&Service{})
	for i := range typ.NumMethod() {
		got = append(got, typ.Method(i).Name)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bound methods changed:\n got: %v\nwant: %v", got, want)
	}
}

func unmarshal(data string, v any) error { return json.Unmarshal([]byte(data), v) }

func TestEncodeInput(t *testing.T) {
	cfg := tui.ChatLiveConfig{
		Skills: []skills.Skill{{Name: "review", Description: "review code", Body: "Review the diff."}},
	}

	if got := encodeInput(cfg, "fix the bug", nil); got != "fix the bug" {
		t.Fatalf("plain text = %q, want passthrough", got)
	}
	if got := encodeInput(cfg, "/unknown thing", nil); got != "/unknown thing" {
		t.Fatalf("unknown slash = %q, want passthrough", got)
	}

	var ui chatstate.ChatUserInput
	if err := unmarshal(encodeInput(cfg, "/review focus on errors", nil), &ui); err != nil {
		t.Fatalf("skill input did not encode as ChatUserInput: %v", err)
	}
	if !ui.IsInput || ui.SkillName != "review" || ui.SkillBody != "Review the diff." || ui.Text != "focus on errors" {
		t.Fatalf("skill input = %+v", ui)
	}

	// An attachment forces the structured form even for plain text.
	ui = chatstate.ChatUserInput{}
	att := []chatstate.ChatAttachment{{ID: "a", Path: "/tmp/x.png"}}
	if err := unmarshal(encodeInput(cfg, "look at this", att), &ui); err != nil {
		t.Fatalf("attachment input did not encode: %v", err)
	}
	if len(ui.Attachments) != 1 || ui.Text != "look at this" {
		t.Fatalf("attachment input = %+v", ui)
	}
}

func TestWorkspacesGroupsByThreadCWD(t *testing.T) {
	s, c := New(func(string, any) {})
	c.Attach(tui.ChatLiveConfig{
		WorkDir: "/work/active",
		ListThreads: func() []tui.ThreadSummary {
			return []tui.ThreadSummary{
				{ThreadID: "1", CWD: "/work/active"},
				{ThreadID: "2", CWD: "/work/other"},
				{ThreadID: "3", CWD: "/work/other"},
				{ThreadID: "4", CWD: ""},
			}
		},
	}, make(chan string, 1))

	got := s.Workspaces()
	if len(got) != 2 {
		t.Fatalf("want 2 workspaces, got %d: %+v", len(got), got)
	}
	if !got[0].Active || got[0].Path != "/work/active" {
		t.Fatalf("active workspace should sort first, got %+v", got[0])
	}
	if got[1].Threads != 2 {
		t.Fatalf("other workspace thread count = %d, want 2", got[1].Threads)
	}
	// Neither directory exists on disk in the test environment.
	for _, w := range got {
		if !w.Missing {
			t.Fatalf("%s should be flagged missing", w.Path)
		}
	}
}
