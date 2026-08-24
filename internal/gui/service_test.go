package gui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

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
		"Clear", "ClearThreads", "CompleteProviderLogin", "DeleteThread", "Efforts", "ForgetWorkspace",
		"History", "ImagePreview", "Init", "MCPServers", "Models", "NewSession", "OpenURL", "PinWorkspace",
		"Providers", "RenameThread", "Restore", "Send",
		"SendWithImages", "SetEffort", "SetProviderKey", "SignOutProvider",
		"StartProviderLogin", "SwitchModel", "SwitchWorkspace", "Threads", "Workspaces",
		"ListWorkspaceDir", "ReadWorkspaceFile", "WriteWorkspaceFile",
		"StartTerminal", "WriteTerminal", "ResizeTerminal", "CloseTerminal",
		"Yolo", "SetYolo",
		// git panel
		"GitStatus", "GitDiff", "GitDiffScope", "GitDefaultBranch",
		"GitStage", "GitUnstage", "GitDiscard", "GitCommit",
		"GitBranches", "GitCheckout", "GitCreateBranch", "GitRenameBranch", "GitDeleteBranch",
		"GitFetch", "GitPull", "GitPush",
		"GitStash", "GitStashList", "GitStashApply", "GitStashDrop",
		"GitLog", "GitCommitDiff",
		"GitResolve", "GitContinue", "GitAbort",
		// worktree sessions and multi-run
		"GitWorktrees", "GitAddWorktree", "GitRemoveWorktree", "GitIntegrate", "StartRuns",
		// model-assisted review
		"GenerateCommitMessage", "GenerateWalkthrough", "WalkthroughStale",
		// preview pane
		"StartPreview", "StopPreview",
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

// Runtimes are independent: switching workspaces activates another directory's
// runtime without tearing the old one down, so both keep accepting input and
// their events stay tagged with their own workspace.
func TestSwitchWorkspaceKeepsBothRuntimesAlive(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	var mu sync.Mutex
	var emitted []any
	s, c := New(func(_ string, data any) {
		mu.Lock()
		emitted = append(emitted, data)
		mu.Unlock()
	})

	inputA := make(chan string, 1)
	inputB := make(chan string, 1)
	c.Attach(tui.ChatLiveConfig{WorkDir: dirA}, inputA)

	eventsA := make(chan llm.Event)
	done := make(chan struct{})
	go func() {
		c.PumpEvents(dirA, eventsA)
		close(done)
	}()

	if err := s.SwitchWorkspace(dirB); err != nil {
		t.Fatalf("SwitchWorkspace: %v", err)
	}
	if got := s.currentDir(); filepath.Clean(got) != filepath.Clean(dirB) {
		t.Fatalf("ActiveDir = %q, want %q", got, dirB)
	}
	c.Attach(tui.ChatLiveConfig{WorkDir: dirB}, inputB)

	// Both runtimes still accept input.
	inputA <- "to a"
	inputB <- "to b"

	// The first runtime's pump is long-lived: it must not have returned on
	// the switch, and its events carry its own workspace tag.
	select {
	case <-done:
		t.Fatal("PumpEvents returned on a workspace switch")
	default:
	}
	eventsA <- llm.Event{Kind: "text", Text: "hi"}
	close(eventsA)
	<-done

	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, e := range emitted {
		if we, ok := e.(wireEvent); ok && we.Workspace == dirA && we.Text == "hi" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no event tagged %s among %+v", dirA, emitted)
	}
}

func TestSwitchWorkspaceKeepsTerminalsAlive(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	s, c := New(func(string, any) {})
	c.Attach(tui.ChatLiveConfig{WorkDir: dirA}, make(chan string, 1))

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() {
		if err := reader.Close(); err != nil {
			t.Errorf("close pipe reader: %v", err)
		}
	})
	resolvedA, err := filepath.EvalSymlinks(dirA)
	if err != nil {
		t.Fatalf("evalsymlinks: %v", err)
	}
	keyA := resolvedA + "/terminal"
	s.terminals[keyA] = &terminalSession{ptmx: writer}

	if err := s.SwitchWorkspace(dirB); err != nil {
		t.Fatalf("SwitchWorkspace: %v", err)
	}
	if len(s.terminals) != 1 {
		t.Fatalf("terminals = %d, want the other workspace's to survive", len(s.terminals))
	}
	if _, ok := s.terminals[keyA]; !ok {
		t.Fatal("switching workspaces closed another workspace's terminal")
	}
	if _, err := writer.Write([]byte("x")); err != nil {
		t.Fatalf("terminal should survive a switch: %v", err)
	}

	// Switching back addresses it again through the active-workspace key.
	if err := s.SwitchWorkspace(dirA); err != nil {
		t.Fatalf("SwitchWorkspace back: %v", err)
	}
	if err := s.WriteTerminal("terminal", "y"); err != nil {
		t.Fatalf("WriteTerminal after switching back: %v", err)
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

// Switching to the directory already open is a no-op.
func TestSwitchWorkspaceIgnoresTheCurrentDirectory(t *testing.T) {
	dir := t.TempDir()
	s, c := New(func(string, any) {})
	c.Attach(tui.ChatLiveConfig{WorkDir: dir}, make(chan string, 1))

	if err := s.SwitchWorkspace(dir); err != nil {
		t.Fatalf("SwitchWorkspace: %v", err)
	}
	if got := s.currentDir(); filepath.Clean(got) != filepath.Clean(dir) {
		t.Fatalf("ActiveDir = %q, want %q", got, dir)
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
