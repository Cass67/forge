package gui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"forge/internal/llm"

	"forge/internal/chatstate"
	"forge/internal/skills"
	"forge/internal/tui"
)

// Wails binds every exported method of the service, and the frontend calls
// them by name over in bridge.ts. Pin the surface so a rename or an
// accidentally exported helper fails here rather than silently in the window.
func TestBoundMethodSurface(t *testing.T) {
	want := []string{
		"Approve", "AttachImage", "AttachPath", "AwaitProviderLogin", "Cancel", "ChooseWorkspace",
		"Clear", "CompleteProviderLogin", "DeleteThread", "Efforts", "ForgetWorkspace",
		"History", "ImagePreview", "Init", "MCPServers", "Models", "NewSession", "OpenURL", "PinWorkspace",
		"Providers", "RenameThread", "Restore", "Send",
		"SendWithImages", "SetEffort", "SetProviderKey", "SignOutProvider",
		"StartProviderLogin", "SwitchModel", "SwitchWorkspace", "Threads", "Workspaces",
		"ListWorkspaceDir", "ReadWorkspaceFile", "WriteWorkspaceFile", "GitStatus",
		"StartTerminal", "WriteTerminal", "ResizeTerminal", "CloseTerminal",
		"Yolo", "SetYolo",
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

// Switching workspace has to unwind the chat runtime: the event pump must
// return so RunChatLive can tear down and be rebuilt against the new
// directory. Getting this wrong leaves two runtimes racing on one input
// channel.
func TestSwitchWorkspaceUnwindsTheEventPump(t *testing.T) {
	dir := t.TempDir()
	s, c := New(func(string, any) {})
	inputCh := make(chan string, 1)
	c.Attach(tui.ChatLiveConfig{WorkDir: t.TempDir()}, inputCh)

	events := make(chan llm.Event)
	returned := make(chan struct{})
	go func() {
		c.PumpEvents(events)
		close(returned)
	}()

	if err := s.SwitchWorkspace(dir); err != nil {
		t.Fatalf("SwitchWorkspace: %v", err)
	}
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("PumpEvents did not return, so the runtime would never be rebuilt")
	}
	if got := c.PendingWorkspace(); got != dir {
		t.Fatalf("PendingWorkspace = %q, want %q", got, dir)
	}
	// Taken once only: a second read must not trigger another switch.
	if got := c.PendingWorkspace(); got != "" {
		t.Fatalf("PendingWorkspace repeated = %q, want empty", got)
	}
	// Input is refused between the switch and the next Attach, so nothing
	// writes to a channel the runtime is about to close.
	if err := s.Send("hello"); err == nil {
		t.Fatal("Send accepted input while detached")
	}
}

func TestSwitchWorkspaceClosesTerminals(t *testing.T) {
	dir := t.TempDir()
	s, c := New(func(string, any) {})
	c.Attach(tui.ChatLiveConfig{WorkDir: t.TempDir()}, make(chan string, 1))

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() {
		if err := reader.Close(); err != nil {
			t.Errorf("close pipe reader: %v", err)
		}
	})
	s.terminals["terminal"] = &terminalSession{ptmx: writer}

	if err := s.SwitchWorkspace(dir); err != nil {
		t.Fatalf("SwitchWorkspace: %v", err)
	}
	if len(s.terminals) != 0 {
		t.Fatal("SwitchWorkspace left a terminal registered")
	}
	if _, err := writer.Write([]byte("x")); err == nil {
		t.Fatal("SwitchWorkspace left a terminal open")
	}
}

func TestSwitchWorkspaceRejectsBadDirectories(t *testing.T) {
	s, c := New(func(string, any) {})
	c.Attach(tui.ChatLiveConfig{WorkDir: t.TempDir()}, make(chan string, 1))

	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for _, bad := range []string{"", "   ", file, filepath.Join(t.TempDir(), "missing")} {
		if err := s.SwitchWorkspace(bad); err == nil {
			t.Errorf("SwitchWorkspace(%q) = nil, want rejection", bad)
		}
	}
}

// Switching to the directory already open is a no-op, not a teardown.
func TestSwitchWorkspaceIgnoresTheCurrentDirectory(t *testing.T) {
	dir := t.TempDir()
	s, c := New(func(string, any) {})
	c.Attach(tui.ChatLiveConfig{WorkDir: dir}, make(chan string, 1))

	if err := s.SwitchWorkspace(dir); err != nil {
		t.Fatalf("SwitchWorkspace: %v", err)
	}
	if got := c.PendingWorkspace(); got != "" {
		t.Fatalf("PendingWorkspace = %q, want no switch", got)
	}
	if err := s.Send(""); err != nil {
		t.Fatalf("service should still be attached: %v", err)
	}
}

func TestEncodeInputExpandsReview(t *testing.T) {
	cfg := tui.ChatLiveConfig{}
	var ui chatstate.ChatUserInput
	if err := unmarshal(encodeInput(cfg, "/review develop", nil), &ui); err != nil {
		t.Fatalf("/review did not encode as ChatUserInput: %v", err)
	}
	if !strings.Contains(ui.Text, "review_diff") || !strings.Contains(ui.Text, "develop") {
		t.Fatalf("review prompt missing: %q", ui.Text)
	}
	if ui.SkillName != "" {
		t.Fatalf("built-in /review should not activate a skill, got %q", ui.SkillName)
	}

	// A user-installed skill named "review" still wins, so nothing they added
	// is shadowed by the built-in.
	withSkill := tui.ChatLiveConfig{Skills: []skills.Skill{{Name: "review", Body: "my own review process"}}}
	if err := unmarshal(encodeInput(withSkill, "/review", nil), &ui); err != nil {
		t.Fatalf("skill input did not encode: %v", err)
	}
	if ui.SkillName != "review" || ui.SkillBody != "my own review process" {
		t.Fatalf("installed skill was shadowed: %+v", ui)
	}
}
