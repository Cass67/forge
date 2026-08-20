// Package gui exposes the chat runtime to the forge-gui window as a Wails
// service. The frontend calls these methods by name and receives streamed
// output as application events; there is no HTTP server and no open port.
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
)

var (
	errNotReady   = errors.New("the chat runtime is still starting")
	errNoRestore  = errors.New("restore is not available in this session")
	errNoDelete   = errors.New("deleting threads is not available in this session")
	errNoWorkDir  = errors.New("no workspace directory")
	errBadImage   = errors.New("unsupported image")
	errTooManyImg = fmt.Errorf("at most %d images per message", chatstate.MaxAttachments)
)

// Service is the surface bound into the window: every exported method here is
// callable from the frontend, so runtime plumbing lives on Controller instead.
type Service struct {
	mu      sync.RWMutex
	cfg     tui.ChatLiveConfig
	inputCh chan<- string
	ready   bool

	emit      func(name string, data any)
	connected sync.Once
	flows     *providerauth.Flows

	// switchSig carries a workspace change to the event pump, which unwinds
	// the chat runtime so it can be rebuilt against nextDir.
	switchSig chan struct{}
	nextDir   string // guarded by mu

	// PickDir opens the platform's folder chooser. Set by the window layer,
	// which owns the dialog API.
	PickDir func() (string, error)
	// Registry remembers opened workspaces across launches.
	Registry *workspace.Registry
}

// Controller drives the service from the Go side. It is deliberately a
// separate type: Wails binds every exported method of the service it is given,
// and none of this belongs in the frontend's reach.
type Controller struct{ s *Service }

// New returns the bound service and the controller that feeds it.
func New(emit func(name string, data any)) (*Service, *Controller) {
	s := &Service{emit: emit, flows: providerauth.NewFlows(), switchSig: make(chan struct{}, 1)}
	return s, &Controller{s: s}
}

// Attach wires the running chat loop into the service and tells the frontend
// it can ask for its init payload.
func (c *Controller) Attach(cfg tui.ChatLiveConfig, inputCh chan<- string) {
	s := c.s
	s.mu.Lock()
	s.cfg = cfg
	s.inputCh = inputCh
	s.ready = true
	s.mu.Unlock()
	log.Printf("gui: chat runtime ready (model %s, workspace %s)", cfg.Model, cfg.WorkDir)
	s.emit(EventReady, nil)
}

func (s *Service) snapshot() (tui.ChatLiveConfig, chan<- string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg, s.inputCh, s.ready
}

// ---- streaming ----------------------------------------------------------

// PumpEvents forwards the runtime's event stream to the window. It returns
// when the stream closes or a workspace switch is requested, which is what
// unwinds RunChatLive so the runtime can be rebuilt elsewhere.
func (c *Controller) PumpEvents(events <-chan llm.Event) {
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				c.s.emit(EventTurnDone, nil)
				return
			}
			c.s.emit(EventChat, toWireEvent(ev))
		case <-c.s.switchSig:
			return
		}
	}
}

// ReportError surfaces a backend failure in the window, for problems that
// happen outside any call the frontend made.
func (c *Controller) ReportError(msg string) {
	c.s.emit(EventChat, wireEvent{Kind: "error", Error: msg})
}

// PendingWorkspace returns the directory a switch asked for, clearing it. An
// empty string means the chat ended for some other reason.
func (c *Controller) PendingWorkspace() string {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	dir := c.s.nextDir
	c.s.nextDir = ""
	return dir
}

// PumpApprovals forwards tool approval requests to the window.
func (c *Controller) PumpApprovals(ch <-chan tools.Action) {
	for a := range ch {
		c.s.emit(EventApproval, wireAction{Tool: a.Tool, Summary: a.Summary, Detail: a.Detail, Path: a.Path})
	}
}

// PumpDone signals the end of each turn.
func (c *Controller) PumpDone(doneCh <-chan struct{}) {
	for range doneCh {
		c.s.emit(EventTurnDone, nil)
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
	return InitPayload{
		Ready:       true,
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

// submit hands input to the runtime while holding the read lock, so a
// workspace switch waits for it rather than closing the channel underneath.
func (s *Service) submit(text string, attachments []chatstate.ChatAttachment) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.ready {
		return errNotReady
	}
	s.inputCh <- encodeInput(s.cfg, text, attachments)
	return nil
}

// control sends a runtime control message under the same lock as submit.
func (s *Service) controlLocked(msg string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.ready {
		s.inputCh <- msg
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
func (s *Service) NewSession() { s.control("__new_session__") }

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

// RestoreResult reports what a restore loaded.
type RestoreResult struct {
	ThreadID string          `json:"thread_id"`
	Restored int             `json:"restored"`
	Items    []protocol.Item `json:"items"`
}

// Restore makes a stored thread the active conversation.
func (s *Service) Restore(threadID string) (RestoreResult, error) {
	cfg, _, ready := s.snapshot()
	if !ready {
		return RestoreResult{}, errNotReady
	}
	if cfg.RestoreHistory == nil {
		return RestoreResult{}, errNoRestore
	}
	n, err := cfg.RestoreHistory(threadID)
	return RestoreResult{
		ThreadID: threadID,
		Restored: n,
		Items:    readItems(cfg.ReadThreadItems, threadID),
	}, err
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
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Active != out[j].Active {
			return out[i].Active
		}
		if out[i].Pinned != out[j].Pinned {
			return out[i].Pinned
		}
		return out[i].LastUse.After(out[j].LastUse)
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

// SwitchWorkspace rebuilds the chat runtime against another directory, in this
// same window. The agent's tools, sandbox rules and thread store are all bound
// to the directory the runtime started in, so the runtime is torn down and
// started again rather than repointed.
func (s *Service) SwitchWorkspace(dir string) error {
	clean, err := workspaceDir(dir)
	if err != nil {
		return err
	}

	s.mu.Lock()
	if !s.ready {
		s.mu.Unlock()
		return errNotReady
	}
	if filepath.Clean(strings.TrimSpace(s.cfg.WorkDir)) == clean {
		s.mu.Unlock()
		return nil
	}
	// Taking the write lock waits for any in-flight submit, so nothing is
	// still writing to the input channel when the runtime closes it.
	s.ready = false
	s.nextDir = clean
	s.mu.Unlock()

	select {
	case s.switchSig <- struct{}{}:
	default:
	}
	return nil
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
	clean := strings.TrimSpace(path)
	if after, ok := strings.CutPrefix(clean, "file://"); ok {
		clean = after
		if decoded, err := url.PathUnescape(clean); err == nil {
			clean = decoded
		}
	}
	if clean == "" {
		return chatstate.ChatAttachment{}, errBadImage
	}
	att, err := chatstate.ValidateImageAttachment(clean)
	if err != nil {
		return chatstate.ChatAttachment{}, err
	}
	return *att, nil
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
