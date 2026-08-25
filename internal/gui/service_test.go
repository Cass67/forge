package gui

import (
	"encoding/json"
	"errors"
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
	"forge/internal/workspace"
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
		// live sessions
		"Sessions", "ActivateSession", "OpenThread", "CloseSession",
		"CloseWorkspace", "AddWorkspaceTree", "RefreshWorkspaceTrees",
		"SetExplorerRoot", "ExplorerRoot",
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
		// skills management
		"InstallSkill", "RemoveSkill", "Remember",
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

func TestApprovalsRouteByIDAndRemainFIFO(t *testing.T) {
	s, c := New(func(string, any) {})
	responsesA := make(chan bool, 2)
	responsesB := make(chan bool, 1)
	c.Attach("a", tui.ChatLiveConfig{WorkDir: "/a", ResponseCh: responsesA}, make(chan string, 1), nil)
	c.Attach("b", tui.ChatLiveConfig{WorkDir: "/b", ResponseCh: responsesB}, make(chan string, 1), nil)

	a1, ok := s.queueApproval("a")
	if !ok {
		t.Fatal("queue first approval for a")
	}
	a2, _ := s.queueApproval("a")
	b1, _ := s.queueApproval("b")
	if a1 == a2 || a1 == b1 || a2 == b1 {
		t.Fatalf("approval ids are not unique: %q %q %q", a1, a2, b1)
	}

	if err := s.Approve(a2, true); !errors.Is(err, errApprovalOrder) {
		t.Fatalf("out-of-order approval error = %v", err)
	}
	if err := s.Approve(b1, false); err != nil {
		t.Fatalf("approve b: %v", err)
	}
	if got := <-responsesB; got {
		t.Fatal("b received approve instead of deny")
	}
	if err := s.Approve(a1, true); err != nil {
		t.Fatalf("approve a1: %v", err)
	}
	if err := s.Approve(a2, false); err != nil {
		t.Fatalf("approve a2: %v", err)
	}
	if first, second := <-responsesA, <-responsesA; !first || second {
		t.Fatalf("a responses = %v, %v; want true, false", first, second)
	}
	if err := s.Approve(a1, true); !errors.Is(err, errNoApproval) {
		t.Fatalf("duplicate approval error = %v", err)
	}

	stale, ok := s.queueApproval("b")
	if !ok {
		t.Fatal("queue approval before forgetting b")
	}
	c.Forget("b")
	if err := s.Approve(stale, true); !errors.Is(err, errNoApproval) {
		t.Fatalf("forgotten session approval error = %v", err)
	}
}

func TestWorkspacesGroupsByThreadCWD(t *testing.T) {
	s, c := New(func(string, any) {})
	c.Attach("s1", tui.ChatLiveConfig{
		WorkDir: "/work/active",
		ListThreads: func() []tui.ThreadSummary {
			return []tui.ThreadSummary{
				{ThreadID: "1", CWD: "/work/active"},
				{ThreadID: "2", CWD: "/work/other"},
				{ThreadID: "3", CWD: "/work/other"},
				{ThreadID: "4", CWD: ""},
			}
		},
	}, make(chan string, 1), nil)

	got := s.Workspaces()
	if len(got) != 2 {
		t.Fatalf("want 2 workspaces, got %d: %+v", len(got), got)
	}
	// Order is by name and does not depend on which workspace is active: the
	// sidebar must not reshuffle under the pointer when one is activated.
	if got[0].Path != "/work/active" || got[1].Path != "/work/other" {
		t.Fatalf("workspaces out of name order: %+v", got)
	}
	if !got[0].Active || got[1].Active {
		t.Fatalf("wrong workspace marked active: %+v", got)
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

// Sessions are independent: switching workspaces starts or activates another
// session without tearing the old one down, so both keep accepting input and
// their events stay tagged with their own session and workspace.
func TestSwitchWorkspaceKeepsBothSessionsAlive(t *testing.T) {
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
	c.Attach("a", tui.ChatLiveConfig{WorkDir: dirA}, inputA, nil)
	s.StartRuntime = func(dir, sessionID, _ string) {
		c.Attach(sessionID, tui.ChatLiveConfig{WorkDir: dir}, inputB, nil)
	}

	eventsA := make(chan llm.Event)
	done := make(chan struct{})
	go func() {
		c.PumpEvents("a", dirA, eventsA)
		close(done)
	}()

	if err := s.SwitchWorkspace(dirB); err != nil {
		t.Fatalf("SwitchWorkspace: %v", err)
	}
	if got := s.currentDir(); filepath.Clean(got) != filepath.Clean(dirB) {
		t.Fatalf("ActiveDir = %q, want %q", got, dirB)
	}

	// Both sessions still accept input.
	inputA <- "to a"
	inputB <- "to b"

	// The first session's pump is long-lived: it must not have returned on
	// the switch, and its events carry its own tags.
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
		if we, ok := e.(wireEvent); ok && we.Workspace == dirA && we.Session == "a" && we.Text == "hi" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no event tagged %s among %+v", dirA, emitted)
	}
}

// Two conversations in the same workspace run at once: opening a second one
// leaves the first streaming, and each addresses its own runtime.
func TestTwoSessionsInOneWorkspaceRunConcurrently(t *testing.T) {
	dir := t.TempDir()
	s, c := New(func(string, any) {})

	firstIn := make(chan string, 1)
	secondIn := make(chan string, 1)
	c.Attach("boot", tui.ChatLiveConfig{
		WorkDir:         dir,
		CurrentThreadID: func() string { return "thread-1" },
	}, firstIn, nil)

	var startedFor string
	s.StartRuntime = func(d, sessionID, resume string) {
		startedFor = resume
		c.Attach(sessionID, tui.ChatLiveConfig{
			WorkDir:         d,
			CurrentThreadID: func() string { return resume },
		}, secondIn, nil)
	}

	// Opening a stored thread that is not live starts a session for it and
	// puts it on screen.
	id, err := s.OpenThread("thread-2")
	if err != nil {
		t.Fatalf("OpenThread: %v", err)
	}
	if startedFor != "thread-2" {
		t.Fatalf("started session resumed %q, want thread-2", startedFor)
	}
	if err := s.Send("second"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case got := <-secondIn:
		if !strings.Contains(got, "second") {
			t.Fatalf("second session got %q", got)
		}
	default:
		t.Fatal("input did not reach the newly opened session")
	}
	if len(firstIn) != 0 {
		t.Fatal("input leaked into the session that is no longer on screen")
	}

	// The first session is still live, and going back to it does not start
	// another runtime for the same thread.
	sessions := s.Sessions()
	if len(sessions) != 2 {
		t.Fatalf("want 2 live sessions, got %+v", sessions)
	}
	startedFor = ""
	back, err := s.OpenThread("thread-1")
	if err != nil {
		t.Fatalf("OpenThread(first): %v", err)
	}
	if back != "boot" || startedFor != "" {
		t.Fatalf("reopening a live thread forked it: session %q, started %q", back, startedFor)
	}
	if err := s.Send("first"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(firstIn) != 1 {
		t.Fatal("input did not reach the reactivated session")
	}
	_ = id
}

// The sidebar lists other workspaces' threads too. Opening one resumes it
// under the directory it was recorded in, not the one on screen, or its tools
// would be pointed at the wrong tree.
func TestOpenThreadResumesItInItsOwnWorkspace(t *testing.T) {
	here := t.TempDir()
	elsewhere := t.TempDir()
	s, c := New(func(string, any) {})
	c.Attach("boot", tui.ChatLiveConfig{
		WorkDir: here,
		ListThreads: func() []tui.ThreadSummary {
			return []tui.ThreadSummary{{ThreadID: "away", CWD: elsewhere}}
		},
	}, make(chan string, 1), nil)

	var startedIn string
	s.StartRuntime = func(dir, sessionID, _ string) {
		startedIn = dir
		c.Attach(sessionID, tui.ChatLiveConfig{WorkDir: dir}, make(chan string, 1), nil)
	}
	if _, err := s.OpenThread("away"); err != nil {
		t.Fatalf("OpenThread: %v", err)
	}
	if filepath.Clean(startedIn) != filepath.Clean(elsewhere) {
		t.Fatalf("started in %q, want %q", startedIn, elsewhere)
	}
}

// A folder holding a pile of repositories is opened once and every repository
// under it becomes a workspace, without starting any of them.
func TestAddWorkspaceTreeRemembersEachSubdirectory(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"alpha", "beta", ".cache"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Keep the registry out of the real config directory.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s, c := New(func(string, any) {})
	s.Registry = workspace.LoadRegistry()
	c.Attach("boot", tui.ChatLiveConfig{WorkDir: root}, make(chan string, 1), nil)
	var started int
	s.StartRuntime = func(string, string, string) { started++ }

	if _, err := s.AddWorkspaceTree(root); err != nil {
		t.Fatalf("AddWorkspaceTree: %v", err)
	}
	remembered := map[string]bool{}
	for _, entry := range s.Registry.List() {
		remembered[filepath.Base(entry.Path)] = true
	}
	if !remembered["alpha"] || !remembered["beta"] {
		t.Fatalf("subdirectories missing: %v", remembered)
	}
	// Hidden directories are tooling state, and a file is not a workspace.
	if remembered[".cache"] || remembered["notes.md"] {
		t.Fatalf("registered something that is not a project: %v", remembered)
	}
	// Opening a folder full of repositories must not start a chat in each.
	if started != 0 {
		t.Fatalf("started %d runtimes, want 0", started)
	}
}

// A folder with nothing under it is itself the workspace, rather than a pick
// that quietly changes nothing.
func TestAddWorkspaceTreeFallsBackToTheFolderItself(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s, c := New(func(string, any) {})
	s.Registry = workspace.LoadRegistry()
	c.Attach("boot", tui.ChatLiveConfig{WorkDir: root}, make(chan string, 1), nil)

	if _, err := s.AddWorkspaceTree(root); err != nil {
		t.Fatalf("AddWorkspaceTree: %v", err)
	}
	list := s.Registry.List()
	if len(list) != 1 || filepath.Clean(list[0].Path) != filepath.Clean(root) {
		t.Fatalf("registry = %+v, want just the folder", list)
	}
}

func TestRefreshWorkspaceTreesAddsNewChildrenWithoutChangingPins(t *testing.T) {
	root := t.TempDir()
	alpha := filepath.Join(root, "alpha")
	if err := os.Mkdir(alpha, 0o755); err != nil {
		t.Fatalf("mkdir alpha: %v", err)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s, c := New(func(string, any) {})
	s.Registry = workspace.LoadRegistry()
	c.Attach("boot", tui.ChatLiveConfig{WorkDir: alpha}, make(chan string, 1), nil)
	// Simulate registry written before container roots were persisted.
	if err := s.Registry.Remember(root); err != nil {
		t.Fatalf("Remember root: %v", err)
	}
	if err := s.Registry.Remember(alpha); err != nil {
		t.Fatalf("Remember alpha: %v", err)
	}
	if err := s.Registry.SetPinned(alpha, true); err != nil {
		t.Fatalf("SetPinned: %v", err)
	}
	beta := filepath.Join(root, "beta")
	if err := os.Mkdir(beta, 0o755); err != nil {
		t.Fatalf("mkdir beta: %v", err)
	}

	list, err := s.RefreshWorkspaceTrees()
	if err != nil {
		t.Fatalf("RefreshWorkspaceTrees: %v", err)
	}
	if len(list) != 3 || list[0].Path != alpha || !list[0].Pinned {
		t.Fatalf("refreshed workspaces = %+v, want pinned alpha first", list)
	}
	foundBeta := false
	for _, item := range list {
		foundBeta = foundBeta || item.Path == beta
	}
	if !foundBeta {
		t.Fatalf("new workspace %q missing from %+v", beta, list)
	}
}

// A workspace can be looked at without starting anything in it: the panels
// follow the browse while the chat stays where it is.
func TestSetExplorerRootMovesThePanelsNotTheChat(t *testing.T) {
	chatDir := t.TempDir()
	browseDir := t.TempDir()
	s, c := New(func(string, any) {})
	started := 0
	s.StartRuntime = func(string, string, string) { started++ }
	c.Attach("chat", tui.ChatLiveConfig{WorkDir: chatDir}, make(chan string, 1), nil)

	if err := s.SetExplorerRoot(browseDir); err != nil {
		t.Fatalf("SetExplorerRoot: %v", err)
	}
	if got := s.ExplorerRoot(); filepath.Clean(got) != mustEval(t, browseDir) {
		t.Fatalf("panels are on %q, want %q", got, browseDir)
	}
	// Browsing must not start a chat, and must not move the one running.
	if started != 0 {
		t.Fatalf("browsing started %d runtimes", started)
	}
	if filepath.Clean(s.currentDir()) != filepath.Clean(chatDir) {
		t.Fatalf("the chat moved to %q", s.currentDir())
	}

	// Clearing hands the panels back to the chat's workspace.
	if err := s.SetExplorerRoot(""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got := s.ExplorerRoot(); got != mustEval(t, chatDir) {
		t.Fatalf("panels did not follow the chat back: %q", got)
	}

	// Deliberately moving the chat also drops a stale browse.
	if err := s.SetExplorerRoot(browseDir); err != nil {
		t.Fatalf("SetExplorerRoot: %v", err)
	}
	c.Attach("second", tui.ChatLiveConfig{WorkDir: chatDir}, make(chan string, 1), nil)
	if err := s.ActivateSession("second"); err != nil {
		t.Fatalf("ActivateSession: %v", err)
	}
	if got := s.ExplorerRoot(); got != mustEval(t, chatDir) {
		t.Fatalf("browse survived a chat switch: %q", got)
	}
}

func mustEval(t *testing.T, dir string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	return resolved
}

// A section exists for every directory a stored thread ran in, so forgetting a
// folder that still has chats used to remove the entry and derive it straight
// back — the button looked broken. It says why instead.
func TestForgetWorkspaceRefusesWhileItStillHasChats(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	chatDir := t.TempDir()
	other := t.TempDir()
	stored := []tui.ThreadSummary{{ThreadID: "1", CWD: other}}

	s, c := New(func(string, any) {})
	s.Registry = workspace.LoadRegistry()
	if err := s.Registry.Remember(other); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	c.Attach("chat", tui.ChatLiveConfig{
		WorkDir:     chatDir,
		ListThreads: func() []tui.ThreadSummary { return stored },
	}, make(chan string, 1), nil)

	if _, err := s.ForgetWorkspace(other); !errors.Is(err, errWorkspaceHasThreads) {
		t.Fatalf("ForgetWorkspace = %v, want errWorkspaceHasThreads", err)
	}

	// With its chats gone the folder goes too, and stays gone.
	stored = nil
	list, err := s.ForgetWorkspace(other)
	if err != nil {
		t.Fatalf("ForgetWorkspace: %v", err)
	}
	for _, w := range list {
		if filepath.Clean(w.Path) == filepath.Clean(other) {
			t.Fatalf("workspace still listed after removal: %+v", w)
		}
	}
}

// Closing a workspace ends every conversation live in it and hands the window
// to what is left, without deleting any threads.
func TestCloseWorkspaceStopsItsSessionsAndMovesOn(t *testing.T) {
	here := t.TempDir()
	other := t.TempDir()
	s, c := New(func(string, any) {})

	stopped := make(chan string, 2)
	c.Attach("here-1", tui.ChatLiveConfig{WorkDir: here}, make(chan string, 1), func() {
		stopped <- "here-1"
	})
	c.Attach("here-2", tui.ChatLiveConfig{WorkDir: here}, make(chan string, 1), func() {
		stopped <- "here-2"
	})
	c.Attach("elsewhere", tui.ChatLiveConfig{WorkDir: other}, make(chan string, 1), nil)
	if err := s.ActivateSession("here-1"); err != nil {
		t.Fatalf("ActivateSession: %v", err)
	}

	if _, err := s.CloseWorkspace(here); err != nil {
		t.Fatalf("CloseWorkspace: %v", err)
	}
	// Both of its conversations are told to stop, not just the one on screen.
	got := map[string]bool{<-stopped: true, <-stopped: true}
	if !got["here-1"] || !got["here-2"] {
		t.Fatalf("not every session was closed: %v", got)
	}
	// The window moves to the workspace that is still live.
	if dir := s.currentDir(); filepath.Clean(dir) != filepath.Clean(other) {
		t.Fatalf("active dir = %q, want %q", dir, other)
	}

	// The runtimes report their streams ending, as the window layer would.
	c.Forget("here-1")
	c.Forget("here-2")
	if s.currentDir() != filepath.Clean(other) {
		t.Fatalf("a closed workspace was reopened: %q", s.currentDir())
	}
}

// The last workspace has nowhere to hand the window on to, so closing it is
// refused rather than leaving a window addressing nothing.
func TestCloseWorkspaceRefusesTheLastOne(t *testing.T) {
	only := t.TempDir()
	s, c := New(func(string, any) {})
	c.Attach("only", tui.ChatLiveConfig{WorkDir: only}, make(chan string, 1), func() {
		t.Error("the last workspace's session was stopped")
	})
	if _, err := s.CloseWorkspace(only); !errors.Is(err, errLastWorkspace) {
		t.Fatalf("CloseWorkspace error = %v, want errLastWorkspace", err)
	}
	if s.currentDir() != filepath.Clean(only) {
		t.Fatalf("the refused close still moved the window: %q", s.currentDir())
	}
}

// Closing the last session must not leave the window addressing nothing: its
// workspace gets a fresh conversation instead.
func TestForgettingTheLastSessionReopensItsWorkspace(t *testing.T) {
	dir := t.TempDir()
	s, c := New(func(string, any) {})
	c.Attach("only", tui.ChatLiveConfig{WorkDir: dir}, make(chan string, 1), nil)
	var restarted string
	s.StartRuntime = func(d, sessionID, _ string) {
		restarted = d
		c.Attach(sessionID, tui.ChatLiveConfig{WorkDir: d}, make(chan string, 1), nil)
	}
	c.Forget("only")
	if filepath.Clean(restarted) != filepath.Clean(dir) {
		t.Fatalf("workspace not reopened, restarted %q", restarted)
	}
	if got := s.Sessions(); len(got) != 1 || !got[0].Active {
		t.Fatalf("after the last session ended, sessions = %+v", got)
	}
}

// Closing a session stops its loop and hands the window to another session in
// the same workspace rather than leaving it addressing nothing.
func TestCloseSessionFallsBackToAnotherSession(t *testing.T) {
	dir := t.TempDir()
	s, c := New(func(string, any) {})
	stopped := make(chan struct{})
	c.Attach("s1", tui.ChatLiveConfig{WorkDir: dir}, make(chan string, 1), nil)
	c.Attach("s2", tui.ChatLiveConfig{WorkDir: dir}, make(chan string, 1), func() { close(stopped) })
	if err := s.ActivateSession("s2"); err != nil {
		t.Fatalf("ActivateSession: %v", err)
	}
	if err := s.CloseSession("s2"); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	<-stopped
	// The window layer reports the ended stream.
	c.Forget("s2")
	if got := s.Sessions(); len(got) != 1 || !got[0].Active || got[0].ID != "s1" {
		t.Fatalf("after closing, sessions = %+v", got)
	}
}

func TestSwitchWorkspaceKeepsTerminalsAlive(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	s, c := New(func(string, any) {})
	c.Attach("boot", tui.ChatLiveConfig{WorkDir: dirA}, make(chan string, 1), nil)
	s.StartRuntime = func(dir, sessionID, _ string) {
		c.Attach(sessionID, tui.ChatLiveConfig{WorkDir: dir}, make(chan string, 1), nil)
	}

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
	c.Attach("s5", tui.ChatLiveConfig{WorkDir: t.TempDir()}, make(chan string, 1), nil)

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
	c.Attach("s6", tui.ChatLiveConfig{WorkDir: dir}, make(chan string, 1), nil)

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

// Browsing is read-only, and that is enforced where it cannot be forgotten:
// in the service, not in the buttons. Every bound method that would change a
// file or a repository refuses while the panels are pointed somewhere the
// chat is not, so no click can commit to the wrong repository.
func TestBrowsingRefusesEveryMutation(t *testing.T) {
	chatDir := t.TempDir()
	browseDir := t.TempDir()
	s, c := New(func(string, any) {})
	c.Attach("chat", tui.ChatLiveConfig{WorkDir: chatDir}, make(chan string, 1), nil)
	if err := s.SetExplorerRoot(browseDir); err != nil {
		t.Fatalf("SetExplorerRoot: %v", err)
	}

	mutations := map[string]func() error{
		"WriteWorkspaceFile": func() error {
			_, err := s.WriteWorkspaceFile("a.txt", "x", "")
			return err
		},
		"GitStage":     func() error { _, err := s.GitStage([]string{"a"}); return err },
		"GitUnstage":   func() error { _, err := s.GitUnstage([]string{"a"}); return err },
		"GitDiscard":   func() error { _, err := s.GitDiscard([]string{"a"}); return err },
		"GitCommit":    func() error { _, err := s.GitCommit("m", false); return err },
		"GitCheckout":  func() error { _, err := s.GitCheckout("main"); return err },
		"GitCreate":    func() error { _, err := s.GitCreateBranch("b", "main", true); return err },
		"GitRename":    func() error { _, err := s.GitRenameBranch("a", "b"); return err },
		"GitDelete":    func() error { _, err := s.GitDeleteBranch("b", false); return err },
		"GitFetch":     func() error { _, err := s.GitFetch(); return err },
		"GitPull":      func() error { _, err := s.GitPull(false); return err },
		"GitPush":      func() error { _, err := s.GitPush(false); return err },
		"GitStash":     func() error { _, err := s.GitStash("m", false); return err },
		"GitStashPop":  func() error { _, err := s.GitStashApply(0, true); return err },
		"GitStashDrop": func() error { _, err := s.GitStashDrop(0); return err },
		"GitResolve":   func() error { _, err := s.GitResolve("a", "ours"); return err },
		"GitContinue":  func() error { _, err := s.GitContinue("merge"); return err },
		"GitAbort":     func() error { _, err := s.GitAbort("merge"); return err },
		"AddWorktree":  func() error { _, err := s.GitAddWorktree("b", "/tmp/x", "main", true); return err },
		"RemoveTree":   func() error { _, err := s.GitRemoveWorktree("/tmp/x", false, false); return err },
		"Integrate":    func() error { _, err := s.GitIntegrate("a", "b", false); return err },
		"StartRuns":    func() error { _, err := s.StartRuns(RunSpec{}); return err },
	}
	for name, call := range mutations {
		if err := call(); !errors.Is(err, errBrowsing) {
			t.Errorf("%s while browsing: err = %v, want errBrowsing", name, err)
		}
	}

	// Reading is exactly what browsing is for, so it keeps working.
	if _, err := s.ListWorkspaceDir(""); err != nil {
		t.Fatalf("listing a browsed workspace: %v", err)
	}

	// Back on the chat's own workspace, mutations are allowed through again:
	// the guard is about where the panels point, not a permanent lock.
	if err := s.SetExplorerRoot(""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, err := s.WriteWorkspaceFile("a.txt", "x", ""); errors.Is(err, errBrowsing) {
		t.Fatal("still refusing writes after the browse was cleared")
	}
}
