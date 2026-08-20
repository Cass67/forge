package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type ServerConfig struct {
	Command    string
	Args       []string
	LanguageID string
}

type Service struct {
	servers  map[string]ServerConfig
	lookPath func(string) (string, error)
	// coldTimeout covers the first call against a server, which pays for the
	// initial index (gopls and rust-analyzer spend most of a cold start there).
	// Once the server is warm and pooled, later calls answer in milliseconds
	// and a short timeout is what keeps a wedged server from stalling a turn.
	coldTimeout time.Duration
	timeout     time.Duration

	mu       sync.Mutex
	sessions map[string]*session
}

// Shared is the process-wide service. Language servers are pooled inside it,
// so every caller must go through the same instance or the pool buys nothing.
var Shared = sync.OnceValue(NewService)

func NewService() *Service {
	return &Service{
		servers:     defaultServers(),
		lookPath:    exec.LookPath,
		coldTimeout: 8 * time.Second,
		timeout:     2 * time.Second,
		sessions:    map[string]*session{},
	}
}

func defaultServers() map[string]ServerConfig {
	return map[string]ServerConfig{
		"go":         {Command: "gopls", LanguageID: "go"},
		"typescript": {Command: "typescript-language-server", Args: []string{"--stdio"}, LanguageID: "typescript"},
		"javascript": {Command: "typescript-language-server", Args: []string{"--stdio"}, LanguageID: "javascript"},
		"rust":       {Command: "rust-analyzer", LanguageID: "rust"},
		"python":     {Command: "pyright-langserver", Args: []string{"--stdio"}, LanguageID: "python"},
		"yaml":       {Command: "yaml-language-server", Args: []string{"--stdio"}, LanguageID: "yaml"},
	}
}

func DetectServerConfig(path string) (ServerConfig, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return defaultServers()["go"], true
	case ".ts", ".tsx":
		return defaultServers()["typescript"], true
	case ".js", ".jsx", ".mjs", ".cjs":
		return defaultServers()["javascript"], true
	case ".rs":
		return defaultServers()["rust"], true
	case ".py":
		return defaultServers()["python"], true
	case ".yaml", ".yml":
		return defaultServers()["yaml"], true
	default:
		return ServerConfig{}, false
	}
}

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type textDocumentIdentifier struct {
	URI string `json:"uri"`
}

type textDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

type textDocumentPositionParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     position               `json:"position"`
}

type referenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

type referenceParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     position               `json:"position"`
	Context      referenceContext       `json:"context"`
}

type rangeValue struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

type location struct {
	URI   string     `json:"uri"`
	Range rangeValue `json:"range"`
}

type hoverResponse struct {
	Contents any         `json:"contents"`
	Range    *rangeValue `json:"range,omitempty"`
}

type diagnostic struct {
	Range    rangeValue `json:"range"`
	Severity int        `json:"severity"`
	Source   string     `json:"source,omitempty"`
	Code     any        `json:"code,omitempty"`
	Message  string     `json:"message"`
}

type publishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Version     *int         `json:"version,omitempty"`
	Diagnostics []diagnostic `json:"diagnostics"`
}

type documentSymbol struct {
	Name           string           `json:"name"`
	Kind           int              `json:"kind"`
	Range          rangeValue       `json:"range"`
	SelectionRange rangeValue       `json:"selectionRange"`
	Children       []documentSymbol `json:"children,omitempty"`
}

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type session struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan rpcEnvelope
	opened  map[string]int
	dead    bool

	// Diagnostics arrive unsolicited, at the server's leisure, and replace the
	// previous set for a URI wholesale.
	diagnostics map[string][]diagnostic
	published   map[string]chan struct{}
}

// newSession starts a language server. The process is deliberately not tied to
// ctx: pooled servers outlive the call that spawned them, and ctx only bounds
// the initialize handshake below.
func newSession(ctx context.Context, cfg ServerConfig, workDir string) (*session, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Dir = workDir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	s := &session{
		cmd:         cmd,
		stdin:       stdin,
		stdout:      bufio.NewReader(stdout),
		pending:     map[int64]chan rpcEnvelope{},
		opened:      map[string]int{},
		diagnostics: map[string][]diagnostic{},
		published:   map[string]chan struct{}{},
	}
	go s.readLoop()

	rootURI := pathToURI(workDir)
	var initResult map[string]any
	if err := s.Request(ctx, "initialize", map[string]any{
		"processId": os.Getpid(),
		"rootUri":   rootURI,
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"publishDiagnostics": map[string]any{"relatedInformation": false},
			},
		},
	}, &initResult); err != nil {
		_ = s.cmd.Process.Kill()
		return nil, err
	}
	if err := s.Notify("initialized", map[string]any{}); err != nil {
		_ = s.cmd.Process.Kill()
		return nil, err
	}
	return s, nil
}

// OpenDocument syncs a file into a pooled server. A second didOpen for the same
// URI is a protocol error, so an already-open document is re-sent as didChange
// with a bumped version — which also picks up edits made since the last call.
func (s *session) OpenDocument(ctx context.Context, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	cfg, ok := DetectServerConfig(path)
	if !ok {
		return fmt.Errorf("unsupported language for %s", path)
	}
	uri := pathToURI(path)

	s.mu.Lock()
	version, already := s.opened[uri]
	version++
	s.opened[uri] = version
	s.mu.Unlock()

	if already {
		return s.Notify("textDocument/didChange", map[string]any{
			"textDocument": map[string]any{"uri": uri, "version": version},
			"contentChanges": []map[string]any{
				{"text": string(data)},
			},
		})
	}
	return s.Notify("textDocument/didOpen", map[string]any{
		"textDocument": textDocumentItem{
			URI:        uri,
			LanguageID: cfg.LanguageID,
			Version:    version,
			Text:       string(data),
		},
	})
}

func (s *session) Close(ctx context.Context) {
	if s == nil {
		return
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var ignored any
	_ = s.Request(shutdownCtx, "shutdown", map[string]any{}, &ignored)
	_ = s.Notify("exit", map[string]any{})
	_ = s.stdin.Close()
	_ = s.cmd.Wait()
}

func (s *session) Request(ctx context.Context, method string, params any, out any) error {
	id := s.allocateID()
	respCh := make(chan rpcEnvelope, 1)
	s.mu.Lock()
	s.pending[id] = respCh
	s.mu.Unlock()

	if err := s.writeMessage(rpcEnvelope{JSONRPC: "2.0", ID: id, Method: method, Params: mustMarshal(params)}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case resp := <-respCh:
		if resp.Error != nil {
			return fmt.Errorf("lsp error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		if out == nil || len(resp.Result) == 0 || bytes.Equal(resp.Result, []byte("null")) {
			return nil
		}
		return json.Unmarshal(resp.Result, out)
	}
}

func (s *session) Notify(method string, params any) error {
	return s.writeMessage(rpcEnvelope{JSONRPC: "2.0", Method: method, Params: mustMarshal(params)})
}

func (s *session) storeDiagnostics(params json.RawMessage) {
	var payload publishDiagnosticsParams
	if err := json.Unmarshal(params, &payload); err != nil {
		return
	}
	s.mu.Lock()
	s.diagnostics[payload.URI] = payload.Diagnostics
	waiter := s.published[payload.URI]
	delete(s.published, payload.URI)
	s.mu.Unlock()
	if waiter != nil {
		close(waiter)
	}
}

// waitForDiagnostics blocks until the server publishes for uri, or ctx expires.
// A server with nothing to say never publishes, so the timeout is the normal
// exit for clean files, not an error.
func (s *session) waitForDiagnostics(ctx context.Context, uri string) []diagnostic {
	s.mu.Lock()
	waiter, ok := s.published[uri]
	if !ok {
		waiter = make(chan struct{})
		s.published[uri] = waiter
	}
	s.mu.Unlock()

	select {
	case <-waiter:
	case <-ctx.Done():
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.diagnostics[uri]
}

// invalidateDiagnostics drops the previous set for uri so a stale publish from
// an earlier version is never mistaken for a fresh one.
func (s *session) invalidateDiagnostics(uri string) {
	s.mu.Lock()
	delete(s.diagnostics, uri)
	s.mu.Unlock()
}

func (s *session) alive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.dead
}

func (s *session) allocateID() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	return s.nextID
}

func (s *session) writeMessage(msg rpcEnvelope) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := fmt.Fprintf(s.stdin, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
		return err
	}
	_, err = s.stdin.Write(data)
	return err
}

func (s *session) readLoop() {
	defer func() {
		s.mu.Lock()
		s.dead = true
		s.mu.Unlock()
	}()
	for {
		payload, err := readFrame(s.stdout)
		if err != nil {
			return
		}
		var env rpcEnvelope
		if err := json.Unmarshal(payload, &env); err != nil {
			continue
		}
		if env.ID == 0 {
			if env.Method == "textDocument/publishDiagnostics" {
				s.storeDiagnostics(env.Params)
			}
			continue
		}
		s.mu.Lock()
		ch := s.pending[env.ID]
		delete(s.pending, env.ID)
		s.mu.Unlock()
		if ch != nil {
			ch <- env
		}
	}
}

func readFrame(r *bufio.Reader) ([]byte, error) {
	length := 0
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			if _, err := fmt.Sscanf(line, "Content-Length: %d", &length); err != nil {
				return nil, err
			}
		}
	}
	if length <= 0 {
		return nil, io.EOF
	}
	buf := make([]byte, length)
	_, err := io.ReadFull(r, buf)
	return buf, err
}

func mustMarshal(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage("null")
	}
	return data
}

func pathToURI(path string) string {
	abs, _ := filepath.Abs(path)
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	return u.String()
}

func uriToPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "file" {
		return raw
	}
	return filepath.FromSlash(u.Path)
}

func hoverText(hover hoverResponse) string {
	switch value := hover.Contents.(type) {
	case string:
		return value
	case map[string]any:
		if text, _ := value["value"].(string); text != "" {
			return text
		}
	case []any:
		var parts []string
		for _, item := range value {
			switch typed := item.(type) {
			case string:
				parts = append(parts, typed)
			case map[string]any:
				if text, _ := typed["value"].(string); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func (s *Service) configForPath(path string) (ServerConfig, error) {
	cfg, ok := DetectServerConfig(path)
	if !ok {
		return ServerConfig{}, fmt.Errorf("no LSP server configured for %s", path)
	}
	override, ok := s.servers[cfg.LanguageID]
	if ok {
		cfg = override
	}
	if s.lookPath == nil {
		s.lookPath = exec.LookPath
	}
	resolved, err := s.lookPath(cfg.Command)
	if err != nil {
		return ServerConfig{}, fmt.Errorf("language server %s not installed", cfg.Command)
	}
	cfg.Command = resolved
	return cfg, nil
}

// acquire returns a pooled server for (workDir, language), starting one if there
// is none or the previous one died. cold reports whether the server was just
// started and still owes an index.
func (s *Service) acquire(ctx context.Context, cfg ServerConfig, workDir string) (sess *session, cold bool, err error) {
	key := workDir + "\x00" + cfg.LanguageID

	s.mu.Lock()
	if s.sessions == nil {
		s.sessions = map[string]*session{}
	}
	existing := s.sessions[key]
	s.mu.Unlock()

	if existing != nil {
		if existing.alive() {
			return existing, false, nil
		}
		s.mu.Lock()
		if s.sessions[key] == existing {
			delete(s.sessions, key)
		}
		s.mu.Unlock()
	}

	started, err := newSession(ctx, cfg, workDir)
	if err != nil {
		return nil, false, err
	}

	s.mu.Lock()
	// Another call may have raced us to a server for the same key; keep theirs
	// so the pool never holds two servers for one workspace.
	if winner := s.sessions[key]; winner != nil && winner.alive() {
		s.mu.Unlock()
		started.Close(context.Background())
		return winner, false, nil
	}
	s.sessions[key] = started
	s.mu.Unlock()
	return started, true, nil
}

// Close shuts down every pooled server. Safe to call more than once.
func (s *Service) Close(ctx context.Context) {
	s.mu.Lock()
	pooled := s.sessions
	s.sessions = map[string]*session{}
	s.mu.Unlock()
	for _, sess := range pooled {
		sess.Close(ctx)
	}
}

func (s *Service) withSession(ctx context.Context, workDir, path string, fn func(context.Context, *session) (string, error)) (string, error) {
	cfg, err := s.configForPath(path)
	if err != nil {
		return "", err
	}
	cold := s.coldTimeout
	if cold <= 0 {
		cold = 8 * time.Second
	}
	spawnCtx, cancelSpawn := context.WithTimeout(ctx, cold)
	defer cancelSpawn()
	sess, wasCold, err := s.acquire(spawnCtx, cfg, workDir)
	if err != nil {
		return "", err
	}

	timeout := s.timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	if wasCold {
		timeout = cold
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := sess.OpenDocument(callCtx, path); err != nil {
		return "", err
	}
	return fn(callCtx, sess)
}

func (s *Service) Definition(ctx context.Context, workDir, path string, line, column int) (string, error) {
	return s.withSession(ctx, workDir, path, func(ctx context.Context, sess *session) (string, error) {
		var locations []location
		err := sess.Request(ctx, "textDocument/definition", textDocumentPositionParams{
			TextDocument: textDocumentIdentifier{URI: pathToURI(path)},
			Position:     zeroBasedPosition(line, column),
		}, &locations)
		if err != nil {
			return "", err
		}
		return formatLocations("definitions", locations), nil
	})
}

func (s *Service) References(ctx context.Context, workDir, path string, line, column int, includeDeclaration bool) (string, error) {
	return s.withSession(ctx, workDir, path, func(ctx context.Context, sess *session) (string, error) {
		var locations []location
		err := sess.Request(ctx, "textDocument/references", referenceParams{
			TextDocument: textDocumentIdentifier{URI: pathToURI(path)},
			Position:     zeroBasedPosition(line, column),
			Context:      referenceContext{IncludeDeclaration: includeDeclaration},
		}, &locations)
		if err != nil {
			return "", err
		}
		return formatLocations("references", locations), nil
	})
}

func (s *Service) Hover(ctx context.Context, workDir, path string, line, column int) (string, error) {
	return s.withSession(ctx, workDir, path, func(ctx context.Context, sess *session) (string, error) {
		var hover hoverResponse
		err := sess.Request(ctx, "textDocument/hover", textDocumentPositionParams{
			TextDocument: textDocumentIdentifier{URI: pathToURI(path)},
			Position:     zeroBasedPosition(line, column),
		}, &hover)
		if err != nil {
			return "", err
		}
		text := strings.TrimSpace(hoverText(hover))
		if text == "" {
			return "no hover information", nil
		}
		return text, nil
	})
}

func (s *Service) DocumentSymbols(ctx context.Context, workDir, path string) (string, error) {
	return s.withSession(ctx, workDir, path, func(ctx context.Context, sess *session) (string, error) {
		var symbols []documentSymbol
		err := sess.Request(ctx, "textDocument/documentSymbol", map[string]any{
			"textDocument": textDocumentIdentifier{URI: pathToURI(path)},
		}, &symbols)
		if err != nil {
			return "", err
		}
		if len(symbols) == 0 {
			return "no document symbols", nil
		}
		var parts []string
		for _, sym := range symbols {
			parts = append(parts, fmt.Sprintf("- %s (%d) %d:%d", sym.Name, sym.Kind, sym.SelectionRange.Start.Line+1, sym.SelectionRange.Start.Character+1))
		}
		return strings.Join(parts, "\n"), nil
	})
}

// maxReportedDiagnostics caps what goes back into the turn. A file with 40
// errors is usually one broken import; the model does not need every line.
const maxReportedDiagnostics = 20

// diagnosticSettleWindow is how long to wait for a server to publish after a
// document is synced. Diagnostics are unsolicited, so there is nothing to
// block on except time.
const diagnosticSettleWindow = 3 * time.Second

// Diagnostics type-checks the given files through their language servers and
// returns errors only, formatted one per line. Files with no configured server
// are skipped rather than reported: a missing gopls is not a code problem.
func (s *Service) Diagnostics(ctx context.Context, workDir string, paths []string) (string, error) {
	var lines []string
	truncated := false

	for _, path := range paths {
		if truncated {
			break
		}
		cfg, err := s.configForPath(path)
		if err != nil {
			continue
		}
		sess, _, err := s.acquire(ctx, cfg, workDir)
		if err != nil {
			continue
		}
		uri := pathToURI(path)
		sess.invalidateDiagnostics(uri)
		if err := sess.OpenDocument(ctx, path); err != nil {
			continue
		}
		waitCtx, cancel := context.WithTimeout(ctx, diagnosticSettleWindow)
		found := sess.waitForDiagnostics(waitCtx, uri)
		cancel()

		for _, diag := range found {
			// Severity 1 is Error; warnings, hints, and info are noise here and
			// cost tokens on every edit.
			if diag.Severity != 1 {
				continue
			}
			lines = append(lines, fmt.Sprintf("%s:%d:%d: %s",
				uriToPath(uri),
				diag.Range.Start.Line+1,
				diag.Range.Start.Character+1,
				strings.TrimSpace(diag.Message)))
			if len(lines) >= maxReportedDiagnostics {
				truncated = true
				break
			}
		}
	}

	if len(lines) == 0 {
		return "", nil
	}
	out := strings.Join(lines, "\n")
	if truncated {
		out += "\n... (more diagnostics not shown)"
	}
	return out, nil
}

func formatLocations(label string, locations []location) string {
	if len(locations) == 0 {
		return "no " + label
	}
	var parts []string
	parts = append(parts, label+":")
	for _, loc := range locations {
		parts = append(parts, fmt.Sprintf("- %s:%d:%d", uriToPath(loc.URI), loc.Range.Start.Line+1, loc.Range.Start.Character+1))
	}
	return strings.Join(parts, "\n")
}

func zeroBasedPosition(line, column int) position {
	if line < 1 {
		line = 1
	}
	if column < 1 {
		column = 1
	}
	return position{Line: line - 1, Character: column - 1}
}
