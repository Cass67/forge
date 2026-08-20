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

	// PickDir opens the platform's folder chooser. Set by the window layer,
	// which owns the dialog API.
	PickDir func() (string, error)
	// OpenWorkspace starts a window rooted at another directory.
	OpenWorkspace func(dir string) error
}

// Controller drives the service from the Go side. It is deliberately a
// separate type: Wails binds every exported method of the service it is given,
// and none of this belongs in the frontend's reach.
type Controller struct{ s *Service }

// New returns the bound service and the controller that feeds it.
func New(emit func(name string, data any)) (*Service, *Controller) {
	s := &Service{emit: emit, flows: providerauth.NewFlows()}
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
// when the stream closes.
func (c *Controller) PumpEvents(events <-chan llm.Event) {
	for ev := range events {
		c.s.emit(EventChat, toWireEvent(ev))
	}
	c.s.emit(EventTurnDone, nil)
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
	}
}

// Send submits a user message. A leading /skill-name is resolved to the
// skill's body, matching what the terminal UI submits.
func (s *Service) Send(text string) error {
	cfg, inputCh, ready := s.snapshot()
	if !ready {
		return errNotReady
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	inputCh <- encodeInput(cfg, text, nil)
	return nil
}

// SendWithImages submits a message with attached images.
func (s *Service) SendWithImages(text string, attachments []chatstate.ChatAttachment) error {
	cfg, inputCh, ready := s.snapshot()
	if !ready {
		return errNotReady
	}
	if strings.TrimSpace(text) == "" && len(attachments) == 0 {
		return nil
	}
	if len(attachments) > chatstate.MaxAttachments {
		return errTooManyImg
	}
	inputCh <- encodeInput(cfg, text, attachments)
	return nil
}

// Approve answers a pending tool approval prompt.
func (s *Service) Approve(ok bool) {
	cfg, _, ready := s.snapshot()
	if ready && cfg.ResponseCh != nil {
		cfg.ResponseCh <- ok
	}
}

// Cancel interrupts the running turn.
func (s *Service) Cancel() { s.control("__cancel_turn__") }

// NewSession clears history and starts a fresh thread.
func (s *Service) NewSession() { s.control("__new_session__") }

func (s *Service) control(msg string) {
	if _, inputCh, ready := s.snapshot(); ready {
		inputCh <- msg
	}
}

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
		return out[i].LastUse.After(out[j].LastUse)
	})
	return out
}

// ChooseWorkspace prompts for a folder and opens it. Returns the chosen path,
// or "" when the user cancelled.
func (s *Service) ChooseWorkspace() (string, error) {
	if s.PickDir == nil {
		return "", errNoWorkDir
	}
	dir, err := s.PickDir()
	if err != nil || strings.TrimSpace(dir) == "" {
		return "", err
	}
	return dir, s.OpenWorkspaceAt(dir)
}

// OpenWorkspaceAt opens a window rooted at dir.
func (s *Service) OpenWorkspaceAt(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return errNoWorkDir
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return fmt.Errorf("%w: %s", errNoWorkDir, dir)
	}
	if s.OpenWorkspace == nil {
		return errNoWorkDir
	}
	return s.OpenWorkspace(dir)
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
