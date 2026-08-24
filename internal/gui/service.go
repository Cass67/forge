// Package gui exposes the chat runtime to the forge-gui window as a Wails
// service. The frontend calls these methods by name and receives streamed
// output as application events; nothing listens on a network port except the
// preview proxy in preview_proxy.go, which binds loopback only and runs solely
// while a preview pane is open.
package gui

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"forge/internal/agent/tools"
	"forge/internal/chatstate"
	"forge/internal/llm"
	"forge/internal/protocol"
	"forge/internal/providerauth"
	"forge/internal/tui"
	"forge/internal/workspace"
)

// Event names emitted to the frontend.
const (
	EventChat     = "forge:event"
	EventApproval = "forge:approval"
	EventTurnDone = "forge:done"
	EventReady    = "forge:ready"
	EventTerminal = "forge:terminal"
	// EventStarting fires when a workspace runtime begins its asynchronous
	// spin-up (building setup, starting the chat loop), before Attach reports
	// it ready. The UI shows progress for these directories.
	EventStarting = "forge:starting"
)

var (
	errNotReady      = errors.New("the chat runtime is still starting")
	errNoDelete      = errors.New("deleting threads is not available in this session")
	errNoRename      = errors.New("renaming threads is not available in this session")
	errNoWorkDir     = errors.New("no workspace directory")
	errNoSession     = errors.New("no such session")
	errNoThread      = errors.New("no thread id")
	errNoStop        = errors.New("this session cannot be closed")
	errLastWorkspace = errors.New("the last workspace cannot be closed")
	// Browsing is read-only on purpose. Anything that would change a file or a
	// repository says so rather than acting on the workspace being looked at.
	errBrowsing   = errors.New("browsing, so this is read-only — open the workspace to change it")
	errBadImage   = errors.New("unsupported image")
	errTooManyImg = fmt.Errorf("at most %d images per message", chatstate.MaxAttachments)
)

// Service is the surface bound into the window: every exported method here is
// callable from the frontend, so runtime plumbing lives on Controller instead.
type Service struct {
	mu sync.RWMutex
	// runtimes holds one chat runtime per workspace directory. Runtimes are
	// expensive (tools, sandbox rules and thread stores are bound to their
	// directory) and independent, so switching workspaces activates another
	// entry rather than tearing anything down.
	// runtimes holds one entry per live chat session, keyed by the session id
	// the window layer minted for it. Several sessions can share a workspace
	// directory; each keeps its own conversation, and all of them keep
	// streaming whether or not they are the one on screen.
	runtimes map[string]*guiRuntime
	// activeSession is the session the frontend is addressing; activeDir is
	// its directory, kept alongside because terminals, git and the file tree
	// are scoped to the workspace rather than to the conversation.
	activeSession string
	activeDir     string
	// pendingFocus is the session id of a runtime that was started to be
	// looked at: it takes the window as soon as it attaches. A runtime started
	// for any other reason attaches in the background.
	pendingFocus string
	// browseDir points the file tree, editor and source control at a workspace
	// without a chat running in it. Empty means they follow the chat.
	browseDir string
	// Directories being torn down by CloseWorkspace, so a session ending there
	// is not mistaken for one that died and reopened.
	closing map[string]bool

	emit      func(name string, data any)
	connected sync.Once
	flows     *providerauth.Flows

	// StartRuntime asks the window layer to start a chat runtime under a
	// session id, optionally resuming a stored thread. Set by main.go;
	// starting is lazy, on first activation.
	StartRuntime func(dir, sessionID, resumeThreadID string)
	// nextSession numbers minted session ids.
	nextSession atomic.Uint64

	// PickDir opens the platform's folder chooser. Set by the window layer,
	// which owns the dialog API.
	PickDir func() (string, error)
	// Registry remembers opened workspaces across launches.
	Registry *workspace.Registry

	terminals map[string]*terminalSession

	// The preview proxy is kept behind its own mutex so starting or
	// re-targeting a preview cannot block a chat turn.
	previewMu sync.Mutex
	preview   *previewProxy
}

// guiRuntime is one live chat session. Sessions are independent and
// long-lived: activating another one never tears this one down, so a turn
// started here keeps running while the user reads somewhere else.
type guiRuntime struct {
	id      string
	dir     string
	cfg     tui.ChatLiveConfig
	inputCh chan<- string
	ready   bool
	// stop ends the session's chat loop and releases its MCP servers and
	// shells. Nil for a runtime attached by a test.
	stop func()
}

// threadID reports which stored thread this session is writing to, which is
// empty until its first message is persisted.
func (r *guiRuntime) threadID() string {
	if r == nil || r.cfg.CurrentThreadID == nil {
		return ""
	}
	return r.cfg.CurrentThreadID()
}

// Controller drives the service from the Go side. It is deliberately a
// separate type: Wails binds every exported method of the service it is given,
// and none of this belongs in the frontend's reach.
type Controller struct{ s *Service }

// New returns the bound service and the controller that feeds it.
func New(emit func(name string, data any)) (*Service, *Controller) {
	s := &Service{
		emit: emit, flows: providerauth.NewFlows(),
		runtimes:  make(map[string]*guiRuntime),
		terminals: make(map[string]*terminalSession),
		closing:   make(map[string]bool),
	}
	return s, &Controller{s: s}
}

func cleanDir(dir string) string {
	return filepath.Clean(strings.TrimSpace(dir))
}

// Attach wires a running chat loop into the service under its session id and
// tells the frontend it can ask for its init payload. A session that was
// started to be looked at becomes the active one; one started in the
// background does not steal the window.
func (c *Controller) Attach(sessionID string, cfg tui.ChatLiveConfig, inputCh chan<- string, stop func()) {
	s := c.s
	dir := cleanDir(cfg.WorkDir)
	s.mu.Lock()
	rt := &guiRuntime{id: sessionID, dir: dir, cfg: cfg, inputCh: inputCh, ready: true, stop: stop}
	s.runtimes[sessionID] = rt
	if s.activeSession == "" || s.pendingFocus == sessionID {
		s.activeSession, s.activeDir = sessionID, dir
	}
	if s.pendingFocus == sessionID {
		s.pendingFocus = ""
	}
	s.mu.Unlock()
	log.Printf("gui: session %s ready (model %s, workspace %s)", sessionID, cfg.Model, cfg.WorkDir)
	s.emit(EventReady, nil)
	s.emit(EventSessions, s.Sessions())
}

// Starting announces that a workspace runtime is spinning up asynchronously,
// which lets the frontend show progress instead of a silent stall. The matching
// Attach→EventReady clears it.
func (c *Controller) Starting(dir string) {
	c.s.emit(EventStarting, dir)
}

// Forget unregisters a session whose stream has ended. If it was the one on
// screen, another session in the same workspace takes over so the window is
// never left addressing nothing.
func (c *Controller) Forget(sessionID string) {
	s := c.s
	s.mu.Lock()
	gone := s.runtimes[sessionID]
	delete(s.runtimes, sessionID)
	if s.activeSession == sessionID {
		s.activeSession = ""
		if gone != nil {
			for id, rt := range s.runtimes {
				if rt.dir == gone.dir {
					s.activeSession, s.activeDir = id, rt.dir
					break
				}
			}
		}
	}
	orphaned := s.activeSession == "" && gone != nil && !s.closing[gone.dir]
	if gone != nil && s.closing[gone.dir] && !s.hasRuntimeInLocked(gone.dir) {
		delete(s.closing, gone.dir)
	}
	s.mu.Unlock()
	// Closing the last session would leave the window addressing nothing, with
	// no way back: give its workspace a fresh conversation instead.
	if orphaned && s.StartRuntime != nil {
		if _, err := s.start(gone.dir, "", true); err != nil {
			log.Printf("gui: could not reopen %s: %v", gone.dir, err)
		}
	}
	s.emit(EventSessions, s.Sessions())
}

// Shutdown tears down every runtime's terminals. The runtimes themselves are
// stopped by process exit.
func (c *Controller) Shutdown() { c.s.closeTerminals() }

// hasRuntimeInLocked reports whether any session is still live in a directory.
// Callers hold s.mu.
func (s *Service) hasRuntimeInLocked(dir string) bool {
	for _, rt := range s.runtimes {
		if rt.dir == dir {
			return true
		}
	}
	return false
}

// active returns the session the frontend is talking to.
func (s *Service) active() (*guiRuntime, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rt, ok := s.runtimes[s.activeSession]
	return rt, ok && rt.ready
}

// snapshot returns the active runtime's config and input channel. Every
// frontend call addresses the active workspace.
func (s *Service) snapshot() (tui.ChatLiveConfig, chan<- string, bool) {
	rt, ok := s.active()
	if !ok {
		return tui.ChatLiveConfig{}, nil, false
	}
	return rt.cfg, rt.inputCh, true
}

// currentDir reports which workspace the frontend is addressing.
func (s *Service) currentDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeDir
}

// ---- streaming ----------------------------------------------------------

// PumpEvents forwards one session's event stream to the window, tagging every
// event with its session id and workspace so the frontend can route output
// from several live sessions at once. It returns only when the stream closes.
func (c *Controller) PumpEvents(sessionID, dir string, events <-chan llm.Event) {
	for ev := range events {
		w := toWireEvent(ev)
		w.Workspace = dir
		w.Session = sessionID
		c.s.emit(EventChat, w)
	}
	c.s.emit(EventTurnDone, DonePayload{Workspace: dir, Session: sessionID})
}

// EventFilesDropped carries OS file drops to the frontend.
const EventFilesDropped = "forge:files"

// FilesDropped forwards paths dropped onto the window. The webview does not
// hand OS drags to the DOM, so this is the only route by which a dragged file
// reaches the app.
func (c *Controller) FilesDropped(paths []string) {
	if len(paths) == 0 {
		return
	}
	c.s.emit(EventFilesDropped, paths)
}

// ReportError surfaces a backend failure in the window, for problems that
// happen outside any call the frontend made.
func (c *Controller) ReportError(msg string) {
	c.s.emit(EventChat, wireEvent{Kind: "error", Error: msg})
}

// PumpApprovals forwards tool approval requests to the window, tagged with the
// workspace they came from.
func (c *Controller) PumpApprovals(sessionID, dir string, ch <-chan tools.Action) {
	for a := range ch {
		c.s.emit(EventApproval, wireAction{
			Tool: a.Tool, Summary: a.Summary, Detail: a.Detail, Path: a.Path,
			Workspace: dir, Session: sessionID,
		})
	}
}

// PumpDone signals the end of each turn for one runtime.
func (c *Controller) PumpDone(sessionID, dir string, doneCh <-chan struct{}) {
	for range doneCh {
		c.s.emit(EventTurnDone, DonePayload{Workspace: dir, Session: sessionID})
	}
}

// ---- calls from the frontend --------------------------------------------

// Init returns everything the window needs to render its chrome.
func (s *Service) Init() InitPayload {
	s.connected.Do(func() { log.Printf("gui: frontend connected") })
	cfg, _, ready := s.snapshot()
	if !ready {
		return InitPayload{Ready: false}
	}
	s.mu.RLock()
	sessionID := s.activeSession
	s.mu.RUnlock()
	return InitPayload{
		Ready:       true,
		Session:     sessionID,
		Model:       cfg.Model,
		WorkDir:     cfg.WorkDir,
		Models:      cfg.AvailableModels,
		Providers:   providerPayloads(cfg.Providers),
		Effort:      call(cfg.CurrentEffort),
		Efforts:     efforts(cfg, cfg.Model),
		Skills:      skillPayloads(cfg.Skills),
		ThreadID:    call(cfg.CurrentThreadID),
		RequestMode: call(cfg.RequestMode),
		Yolo:        cfg.Yolo != nil && cfg.Yolo(),
		Notice:      cfg.StartupNotice,
	}
}

// Send submits a user message. A leading /skill-name is resolved to the
// skill's body, matching what the terminal UI submits.
func (s *Service) Send(text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return s.submit(text, nil)
}

// submit hands input to the active runtime.
func (s *Service) submit(text string, attachments []chatstate.ChatAttachment) error {
	cfg, inputCh, ready := s.snapshot()
	if !ready {
		return errNotReady
	}
	inputCh <- encodeInput(cfg, text, attachments)
	return nil
}

// control sends a runtime control message to the active runtime.
func (s *Service) controlLocked(msg string) {
	_, inputCh, ready := s.snapshot()
	if ready {
		inputCh <- msg
	}
}

// SendWithImages submits a message with attached images.
func (s *Service) SendWithImages(text string, attachments []chatstate.ChatAttachment) error {
	if strings.TrimSpace(text) == "" && len(attachments) == 0 {
		return nil
	}
	if len(attachments) > chatstate.MaxAttachments {
		return errTooManyImg
	}
	return s.submit(text, attachments)
}

// Approve answers a pending tool approval prompt.
func (s *Service) Approve(ok bool) {
	cfg, _, ready := s.snapshot()
	if ready && cfg.ResponseCh != nil {
		cfg.ResponseCh <- ok
	}
}

// Yolo reports whether tool approvals are being skipped.
func (s *Service) Yolo() bool {
	cfg, _, ready := s.snapshot()
	if !ready || cfg.Yolo == nil {
		return false
	}
	return cfg.Yolo()
}

// SetYolo turns approval prompts off or back on for the running session.
func (s *Service) SetYolo(on bool) (bool, error) {
	cfg, _, ready := s.snapshot()
	if !ready || cfg.SetYolo == nil {
		return false, errNotReady
	}
	cfg.SetYolo(on)
	return s.Yolo(), nil
}

// Cancel interrupts the running turn.
func (s *Service) Cancel() { s.control("__cancel_turn__") }

// NewSession clears history and starts a fresh thread.
// NewSession opens another conversation and puts it on screen. An empty dir
// means the active workspace. The session it replaces on screen keeps running,
// so starting a new chat never interrupts one that is mid-turn.
func (s *Service) NewSession(dir string) error {
	if strings.TrimSpace(dir) == "" {
		dir = s.currentDir()
	}
	if dir == "" {
		return errNoWorkDir
	}
	_, err := s.start(dir, "", true)
	return err
}

func (s *Service) control(msg string) { s.controlLocked(msg) }

// Clear drops the in-memory conversation without starting a new thread.
func (s *Service) Clear() {
	if cfg, _, ready := s.snapshot(); ready && cfg.ClearHistory != nil {
		cfg.ClearHistory()
	}
}

// SwitchModel changes the active model and returns the name actually applied.
func (s *Service) SwitchModel(model string) (string, error) {
	cfg, _, ready := s.snapshot()
	if !ready || cfg.SwitchModel == nil {
		return "", errNotReady
	}
	return cfg.SwitchModel(model)
}

// Models re-reads the available model list.
func (s *Service) Models() []string {
	cfg, _, ready := s.snapshot()
	if !ready {
		return nil
	}
	if cfg.RefreshModels != nil {
		return cfg.RefreshModels()
	}
	return cfg.AvailableModels
}

// Efforts lists the reasoning-effort levels a model supports.
func (s *Service) Efforts(model string) []string {
	cfg, _, ready := s.snapshot()
	if !ready {
		return nil
	}
	return efforts(cfg, model)
}

// SetEffort changes the reasoning effort level.
func (s *Service) SetEffort(effort string) error {
	cfg, _, ready := s.snapshot()
	if !ready || cfg.SetEffort == nil {
		return errNotReady
	}
	return cfg.SetEffort(effort)
}

// Threads lists stored threads, newest first.
func (s *Service) Threads() []tui.ThreadSummary {
	cfg, _, ready := s.snapshot()
	if !ready || cfg.ListThreads == nil {
		return []tui.ThreadSummary{}
	}
	items := cfg.ListThreads()
	if items == nil {
		return []tui.ThreadSummary{}
	}
	return items
}

// History returns a stored thread's items for rendering, without making it
// the active conversation.
func (s *Service) History(threadID string) []protocol.Item {
	cfg, _, ready := s.snapshot()
	if !ready {
		return []protocol.Item{}
	}
	return readItems(cfg.ReadThreadItems, threadID)
}

// DeleteThread permanently removes a stored thread and returns the updated
// list. The active thread is refused: it is still being written to.
func (s *Service) DeleteThread(threadID string) ([]tui.ThreadSummary, error) {
	cfg, _, ready := s.snapshot()
	if !ready {
		return []tui.ThreadSummary{}, errNotReady
	}
	if cfg.DeleteThread == nil {
		return s.Threads(), errNoDelete
	}
	if err := cfg.DeleteThread(threadID); err != nil {
		return s.Threads(), err
	}
	return s.Threads(), nil
}

// ClearResult reports what a bulk clear removed.
type ClearResult struct {
	Removed int                 `json:"removed"`
	Failed  int                 `json:"failed"`
	Threads []tui.ThreadSummary `json:"threads"`
}

// ClearThreads deletes every stored thread except the active one. The sidebar
// groups threads under the workspace they were started in, so a thread whose
// directory is no longer a registered workspace — a worktree, a scratch dir, a
// bench run — had no row to tick and survived every "select all". This works
// off the stored list instead of the rendered one.
func (s *Service) ClearThreads() (ClearResult, error) {
	cfg, _, ready := s.snapshot()
	if !ready {
		return ClearResult{}, errNotReady
	}
	if cfg.DeleteThread == nil || cfg.ListThreads == nil {
		return ClearResult{Threads: s.Threads()}, errNoDelete
	}
	active := call(cfg.CurrentThreadID)

	var result ClearResult
	// A thread that refuses to go is remembered rather than retried, so it is
	// counted once no matter how many passes run.
	stuck := map[string]bool{}
	// ListThreads is capped per call, so keep going while a pass still
	// removes something rather than assuming one pass sees every thread.
	for {
		progress := 0
		for _, t := range cfg.ListThreads() {
			if t.ThreadID == "" || t.ThreadID == active || stuck[t.ThreadID] {
				continue
			}
			if err := cfg.DeleteThread(t.ThreadID); err != nil {
				stuck[t.ThreadID] = true
				continue
			}
			result.Removed++
			progress++
		}
		if progress == 0 {
			break
		}
	}
	result.Failed = len(stuck)
	result.Threads = s.Threads()
	return result, nil
}

// RenameThread gives a stored thread a name of the user's choosing. A manual
// title is never overwritten by the automatic first-message naming.
func (s *Service) RenameThread(threadID, title string) ([]tui.ThreadSummary, error) {
	cfg, _, ready := s.snapshot()
	if !ready {
		return []tui.ThreadSummary{}, errNotReady
	}
	if cfg.RenameThread == nil {
		return s.Threads(), errNoRename
	}
	if err := cfg.RenameThread(threadID, title); err != nil {
		return s.Threads(), err
	}
	return s.Threads(), nil
}

// RestoreResult reports what a restore loaded.
type RestoreResult struct {
	ThreadID string          `json:"thread_id"`
	Session  string          `json:"session,omitempty"`
	Restored int             `json:"restored"`
	Items    []protocol.Item `json:"items"`
}

// Restore puts a stored thread on screen as its own live session, and returns
// its items so the window can paint the transcript without waiting for the
// session to attach. A thread already live is activated rather than resumed
// twice.
func (s *Service) Restore(threadID string) (RestoreResult, error) {
	cfg, _, ready := s.snapshot()
	if !ready {
		return RestoreResult{}, errNotReady
	}
	sessionID, err := s.OpenThread(threadID)
	if err != nil {
		return RestoreResult{}, err
	}
	items := readItems(cfg.ReadThreadItems, threadID)
	return RestoreResult{
		ThreadID: threadID,
		Session:  sessionID,
		Restored: len(items),
		Items:    items,
	}, nil
}

// MCPServers lists the configured MCP servers and what each contributed, so
// the window can show which are enabled and which actually loaded tools.
func (s *Service) MCPServers() []tui.MCPServerStatus {
	cfg, _, ready := s.snapshot()
	if !ready || cfg.MCPServers == nil {
		return []tui.MCPServerStatus{}
	}
	if servers := cfg.MCPServers(); servers != nil {
		return servers
	}
	return []tui.MCPServerStatus{}
}

// ---- live sessions -------------------------------------------------------

// EventSessions announces that the set of live sessions changed, so the
// sidebar can redraw which threads are open and which are still running.
const EventSessions = "forge:sessions"

// SessionInfo describes one live session for the sidebar.
type SessionInfo struct {
	ID        string `json:"id"`
	Workspace string `json:"workspace"`
	// ThreadID is empty for a session whose first message has not been
	// persisted yet.
	ThreadID string `json:"thread_id,omitempty"`
	Active   bool   `json:"active"`
	Ready    bool   `json:"ready"`
}

// Sessions lists the live sessions, newest ids last. Every one of them is a
// running chat loop, whether or not it is the one on screen.
func (s *Service) Sessions() []SessionInfo {
	s.mu.RLock()
	out := make([]SessionInfo, 0, len(s.runtimes))
	for id, rt := range s.runtimes {
		out = append(out, SessionInfo{
			ID: id, Workspace: rt.dir, ThreadID: rt.threadID(),
			Active: id == s.activeSession, Ready: rt.ready,
		})
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// mintSession returns the next session id. Ids are opaque and per process:
// a thread id cannot serve, because a conversation has none until its first
// message is written.
func (s *Service) mintSession() string {
	return fmt.Sprintf("s%d", s.nextSession.Add(1))
}

// activate makes a live session the one the window addresses. Nothing is torn
// down: the session that was on screen keeps running.
func (s *Service) activate(sessionID string) bool {
	s.mu.Lock()
	rt, ok := s.runtimes[sessionID]
	if ok {
		s.activeSession, s.activeDir = sessionID, rt.dir
		// Moving the chat is a decision about where to work, so the panels
		// stop looking wherever they were pointed and follow it again.
		s.browseDir = ""
	}
	s.mu.Unlock()
	if ok {
		s.emit(EventReady, nil)
		s.emit(EventSessions, s.Sessions())
	}
	return ok
}

// ActivateSession puts an already-live session on screen.
func (s *Service) ActivateSession(sessionID string) error {
	if !s.activate(sessionID) {
		return errNoSession
	}
	return nil
}

// start asks the window layer for a new runtime and, when focus is wanted,
// arranges for it to take the window the moment it attaches.
func (s *Service) start(dir, resumeThreadID string, focus bool) (string, error) {
	if s.StartRuntime == nil {
		return "", errNotReady
	}
	id := s.mintSession()
	if focus {
		s.mu.Lock()
		s.pendingFocus = id
		s.mu.Unlock()
	}
	s.StartRuntime(cleanDir(dir), id, resumeThreadID)
	return id, nil
}

// OpenThread puts a stored thread on screen. If it is already live somewhere
// that session is activated — reopening a thread must never fork it into two
// conversations writing to the same file — otherwise a session is started to
// resume it, alongside whatever else is running.
func (s *Service) OpenThread(threadID string) (string, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return "", errNoThread
	}
	s.mu.RLock()
	dir := s.activeDir
	var live string
	for id, rt := range s.runtimes {
		if rt.threadID() == threadID {
			live = id
			break
		}
	}
	s.mu.RUnlock()
	if live != "" {
		s.activate(live)
		return live, nil
	}
	// A thread belongs to the directory it was recorded in, which is not
	// always the one on screen: the sidebar lists other workspaces' threads
	// too, and resuming one under the wrong root would point its tools at the
	// wrong tree.
	if home := s.threadWorkDir(threadID); home != "" {
		dir = home
	}
	if dir == "" {
		return "", errNoWorkDir
	}
	return s.start(dir, threadID, true)
}

// threadWorkDir reports the directory a stored thread was recorded in, empty
// if it is unknown or no longer a directory.
func (s *Service) threadWorkDir(threadID string) string {
	cfg, _, ready := s.snapshot()
	if !ready || cfg.ListThreads == nil {
		return ""
	}
	for _, t := range cfg.ListThreads() {
		if t.ThreadID != threadID {
			continue
		}
		clean, err := workspaceDir(t.CWD)
		if err != nil {
			return ""
		}
		return clean
	}
	return ""
}

// CloseSession ends a live session and releases its MCP servers and shells.
// The thread it was writing to stays on disk: closing is not deleting.
func (s *Service) CloseSession(sessionID string) error {
	s.mu.RLock()
	rt, ok := s.runtimes[sessionID]
	s.mu.RUnlock()
	if !ok {
		return errNoSession
	}
	if rt.stop == nil {
		return errNoStop
	}
	// Forget, and with it the choice of a successor session, happens when the
	// stream actually ends.
	rt.stop()
	return nil
}

// ---- workspaces ----------------------------------------------------------

// Workspace is a directory that has threads stored against it.
type Workspace struct {
	Path    string    `json:"path"`
	Name    string    `json:"name"`
	Threads int       `json:"threads"`
	LastUse time.Time `json:"last_use"`
	Active  bool      `json:"active"`
	Missing bool      `json:"missing"`
	Pinned  bool      `json:"pinned"`
}

// Workspaces derives the workspace list from the CWD recorded on each stored
// thread, so previously used directories are offered without a separate
// registry. The active workspace is always present, even with no threads yet.
func (s *Service) Workspaces() []Workspace {
	cfg, _, ready := s.snapshot()
	if !ready {
		return []Workspace{}
	}
	active := filepath.Clean(strings.TrimSpace(cfg.WorkDir))
	byPath := map[string]*Workspace{}
	if active != "" && active != "." {
		byPath[active] = &Workspace{Path: active, Name: filepath.Base(active), Active: true}
	}
	// Remembered workspaces appear even before they have any threads, and
	// survive a relaunch.
	if s.Registry != nil {
		for _, e := range s.Registry.List() {
			w, ok := byPath[e.Path]
			if !ok {
				w = &Workspace{Path: e.Path, Name: filepath.Base(e.Path), Active: e.Path == active}
				byPath[e.Path] = w
			}
			w.Pinned = e.Pinned
			if e.LastUsed.After(w.LastUse) {
				w.LastUse = e.LastUsed
			}
		}
	}
	if cfg.ListThreads != nil {
		for _, t := range cfg.ListThreads() {
			dir := strings.TrimSpace(t.CWD)
			if dir == "" {
				continue
			}
			dir = filepath.Clean(dir)
			w, ok := byPath[dir]
			if !ok {
				w = &Workspace{Path: dir, Name: filepath.Base(dir), Active: dir == active}
				byPath[dir] = w
			}
			w.Threads++
			if t.UpdatedAt.After(w.LastUse) {
				w.LastUse = t.UpdatedAt
			}
		}
	}
	out := make([]Workspace, 0, len(byPath))
	for _, w := range byPath {
		if st, err := os.Stat(w.Path); err != nil || !st.IsDir() {
			w.Missing = true
		}
		out = append(out, *w)
	}
	// The order is deliberately stable: neither activating a workspace nor
	// using it moves it, so the list under the pointer stays where it was.
	// Pinning is the only thing that promotes an entry, because that is the
	// user asking for it.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Pinned != out[j].Pinned {
			return out[i].Pinned
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// PinWorkspace keeps a workspace in the list regardless of how long ago it was
// last opened.
func (s *Service) PinWorkspace(path string, pinned bool) ([]Workspace, error) {
	if s.Registry == nil {
		return s.Workspaces(), errNoWorkDir
	}
	if err := s.Registry.SetPinned(path, pinned); err != nil {
		return s.Workspaces(), err
	}
	return s.Workspaces(), nil
}

// ForgetWorkspace drops a workspace from the list. The directory and its
// threads are left alone.
func (s *Service) ForgetWorkspace(path string) ([]Workspace, error) {
	if s.Registry == nil {
		return s.Workspaces(), errNoWorkDir
	}
	cfg, _, _ := s.snapshot()
	if filepath.Clean(strings.TrimSpace(cfg.WorkDir)) == filepath.Clean(strings.TrimSpace(path)) {
		return s.Workspaces(), errors.New("cannot remove the workspace you are in")
	}
	if err := s.Registry.Forget(path); err != nil {
		return s.Workspaces(), err
	}
	return s.Workspaces(), nil
}

// ChooseWorkspace prompts for a folder and switches to it. Returns the chosen
// path, or "" when the user cancelled.
func (s *Service) ChooseWorkspace() (string, error) {
	if s.PickDir == nil {
		return "", errNoWorkDir
	}
	dir, err := s.PickDir()
	if err != nil || strings.TrimSpace(dir) == "" {
		return "", err
	}
	return dir, s.SwitchWorkspace(dir)
}

// SwitchWorkspace activates another directory's runtime in this same window.
// Runtimes are bound to the directory they started in — tools, sandbox rules
// and thread store cannot be repointed — so each directory gets its own, and
// switching only changes which one the frontend talks to. A directory without
// a runtime yet is started lazily via StartRuntime; runtimes already running
// are left untouched.
func (s *Service) SwitchWorkspace(dir string) error {
	clean, err := workspaceDir(dir)
	if err != nil {
		return err
	}

	s.mu.RLock()
	if s.activeDir == clean && s.activeSession != "" {
		s.mu.RUnlock()
		return nil
	}
	// Prefer a session already live in that directory: switching back to a
	// workspace should land on the conversation it was left in, still running.
	var live string
	for id, rt := range s.runtimes {
		if rt.dir == clean {
			live = id
			break
		}
	}
	s.mu.RUnlock()
	s.StopPreview()

	if live != "" {
		s.activate(live)
		return nil
	}
	s.mu.Lock()
	s.activeDir = clean
	s.browseDir = ""
	s.mu.Unlock()
	if _, err := s.start(clean, "", true); err != nil {
		return err
	}
	// The frontend re-initialises once the new session attaches.
	s.emit(EventReady, nil)
	return nil
}

// CloseWorkspace ends every conversation live in a directory and drops it from
// the list. The threads themselves stay on disk: closing a workspace is not
// deleting its history, the same way closing a chat is not deleting its thread.
// The workspace on screen can be closed too, in which case the window moves to
// whatever is left.
func (s *Service) CloseWorkspace(dir string) ([]Workspace, error) {
	clean, err := workspaceDir(dir)
	if err != nil {
		// A directory that no longer exists still has to be removable from the
		// list, so a bad path is only fatal when nothing here knows it either.
		clean = cleanDir(dir)
		if clean == "" {
			return s.Workspaces(), errNoWorkDir
		}
	}

	s.mu.Lock()
	victims := make([]*guiRuntime, 0, len(s.runtimes))
	elsewhere := ""
	for id, rt := range s.runtimes {
		if rt.dir == clean {
			victims = append(victims, rt)
			continue
		}
		if elsewhere == "" {
			elsewhere = id
		}
	}
	closingActive := s.activeDir == clean
	if closingActive && elsewhere == "" {
		// Nothing else is live. Somewhere to go is needed before the window is
		// left addressing a workspace that is being torn down.
		fallback := s.otherRememberedLocked(clean)
		s.mu.Unlock()
		if fallback == "" {
			return s.Workspaces(), errLastWorkspace
		}
		if err := s.SwitchWorkspace(fallback); err != nil {
			return s.Workspaces(), err
		}
		s.mu.Lock()
	}
	// Marked before the runtimes are stopped: Forget reopens a workspace whose
	// last session ends, which is right for a session that died and wrong for
	// one being deliberately closed.
	s.closing[clean] = true
	s.mu.Unlock()

	for _, rt := range victims {
		if rt.stop != nil {
			rt.stop()
		}
	}
	if elsewhere != "" && closingActive {
		s.activate(elsewhere)
	}
	if s.Registry != nil {
		if err := s.Registry.Forget(clean); err != nil {
			return s.Workspaces(), err
		}
	}
	return s.Workspaces(), nil
}

// otherRememberedLocked names a remembered workspace that is not the one being
// closed, preferring the most recent.
func (s *Service) otherRememberedLocked(skip string) string {
	if s.Registry == nil {
		return ""
	}
	for _, entry := range s.Registry.List() {
		if cleanDir(entry.Path) != skip {
			return entry.Path
		}
	}
	return ""
}

// maxWorkspaceTree caps how many subdirectories one folder can contribute.
// Pointed at a home directory this would otherwise register hundreds of
// entries, and a sidebar that long is not navigable anyway.
const maxWorkspaceTree = 200

// AddWorkspaceTree remembers every immediate subdirectory of dir as its own
// workspace, which is how a folder holding a pile of repositories is opened:
// one pick, and each repository underneath becomes a workspace one click away.
// Nothing is started — runtimes stay lazy, so this costs a directory listing
// and no processes.
func (s *Service) AddWorkspaceTree(dir string) ([]Workspace, error) {
	root, err := workspaceDir(dir)
	if err != nil {
		return s.Workspaces(), err
	}
	if s.Registry == nil {
		return s.Workspaces(), errNoWorkDir
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return s.Workspaces(), err
	}
	added := 0
	for _, entry := range entries {
		if added >= maxWorkspaceTree {
			break
		}
		name := entry.Name()
		// Hidden directories are caches and tooling state, not projects.
		if strings.HasPrefix(name, ".") {
			continue
		}
		if !entry.IsDir() {
			// A symlink to a directory is how people keep a project in one
			// place and list it in another, so it counts.
			if entry.Type()&os.ModeSymlink == 0 {
				continue
			}
			if st, statErr := os.Stat(filepath.Join(root, name)); statErr != nil || !st.IsDir() {
				continue
			}
		}
		if err := s.Registry.Remember(filepath.Join(root, name)); err != nil {
			return s.Workspaces(), err
		}
		added++
	}
	// A folder with nothing under it is the workspace, rather than an empty
	// gesture that leaves the list unchanged.
	if added == 0 {
		if err := s.Registry.Remember(root); err != nil {
			return s.Workspaces(), err
		}
	}
	return s.Workspaces(), nil
}

// workspaceDir validates a directory chosen in the UI.
func workspaceDir(dir string) (string, error) {
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" {
		return "", errNoWorkDir
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(abs)
	if err != nil || !st.IsDir() {
		return "", fmt.Errorf("%w: %s", errNoWorkDir, dir)
	}
	return abs, nil
}

// ---- attachments ---------------------------------------------------------

// AttachImage accepts an image dropped or pasted into the window, writes it to
// a temporary file and returns the attachment record the runtime expects.
// The runtime reads attachments from disk, so the bytes cannot stay in the
// renderer.
func (s *Service) AttachImage(name string, dataB64 string) (chatstate.ChatAttachment, error) {
	cfg, _, ready := s.snapshot()
	if !ready {
		return chatstate.ChatAttachment{}, errNotReady
	}
	if i := strings.Index(dataB64, ","); strings.HasPrefix(dataB64, "data:") && i > 0 {
		dataB64 = dataB64[i+1:]
	}
	raw, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return chatstate.ChatAttachment{}, errBadImage
	}
	if len(raw) > chatstate.MaxImageBytes {
		return chatstate.ChatAttachment{}, fmt.Errorf("image is larger than %d MB", chatstate.MaxImageBytes/(1024*1024))
	}
	dir, err := os.MkdirTemp("", "forge-attach-")
	if err != nil {
		return chatstate.ChatAttachment{}, err
	}
	base := filepath.Base(strings.TrimSpace(name))
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = "pasted-image"
	}
	path := filepath.Join(dir, base)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return chatstate.ChatAttachment{}, err
	}
	att, err := chatstate.ValidateImageAttachment(path)
	if err != nil {
		_ = os.RemoveAll(dir)
		return chatstate.ChatAttachment{}, err
	}
	_ = cfg
	return *att, nil
}

// AttachPath accepts a file the window received as a path rather than as
// bytes. Dragging from Finder into a webview commonly delivers a file:// URI
// instead of a File object, and the file is already on disk, so there is
// nothing to copy.
func (s *Service) AttachPath(path string) (chatstate.ChatAttachment, error) {
	_, _, ready := s.snapshot()
	if !ready {
		return chatstate.ChatAttachment{}, errNotReady
	}
	clean, err := resolveLocalPath(path)
	if err != nil {
		return chatstate.ChatAttachment{}, err
	}
	att, err := chatstate.ValidateImageAttachment(clean)
	if err != nil {
		return chatstate.ChatAttachment{}, err
	}
	return *att, nil
}

// resolveLocalPath turns what the window hands over into a filesystem path.
// Drags may arrive as plain paths or as percent-encoded file:// URIs.
func resolveLocalPath(path string) (string, error) {
	clean := strings.TrimSpace(path)
	if after, ok := strings.CutPrefix(clean, "file://"); ok {
		clean = after
		if decoded, err := url.PathUnescape(clean); err == nil {
			clean = decoded
		}
	}
	if clean == "" {
		return "", errBadImage
	}
	return clean, nil
}

// ---- helpers -------------------------------------------------------------

func call(fn func() string) string {
	if fn == nil {
		return ""
	}
	return fn()
}

func efforts(cfg tui.ChatLiveConfig, model string) []string {
	if cfg.ModelEfforts == nil {
		return nil
	}
	return cfg.ModelEfforts(model)
}

// encodeInput builds the runtime's input string: structured JSON when there is
// a skill or an attachment to carry, plain text otherwise.
func encodeInput(cfg tui.ChatLiveConfig, text string, attachments []chatstate.ChatAttachment) string {
	trimmed := strings.TrimSpace(text)
	ui := chatstate.ChatUserInput{IsInput: true, Text: trimmed, Attachments: attachments}
	if strings.HasPrefix(trimmed, "/") {
		name, rest, _ := strings.Cut(strings.TrimPrefix(trimmed, "/"), " ")
		// /review expands here rather than in the frontend, so the window and
		// the TUI send the same review instructions.
		if name == "review" && !hasSkill(cfg, name) {
			ui.Text = tools.ReviewPromptFor(strings.TrimSpace(rest))
			return marshalInput(ui, text)
		}
		for _, sk := range cfg.Skills {
			if sk.Name == name {
				ui.SkillName = sk.Name
				ui.SkillBody = sk.Body
				ui.Text = strings.TrimSpace(rest)
				break
			}
		}
	}
	if ui.SkillName == "" && len(ui.Attachments) == 0 {
		return text
	}
	return marshalInput(ui, text)
}

func hasSkill(cfg tui.ChatLiveConfig, name string) bool {
	for _, sk := range cfg.Skills {
		if sk.Name == name {
			return true
		}
	}
	return false
}

// marshalInput serialises structured input; on the (impossible) marshal error
// the caller's plain text is used instead.
func marshalInput(ui chatstate.ChatUserInput, fallback string) string {
	data, err := json.Marshal(ui)
	if err != nil {
		return fallback
	}
	return string(data)
}

func readItems(fn func(string) []protocol.Item, threadID string) []protocol.Item {
	if fn == nil || strings.TrimSpace(threadID) == "" {
		return []protocol.Item{}
	}
	items := fn(threadID)
	if items == nil {
		return []protocol.Item{}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Seq < items[j].Seq })
	return items
}
