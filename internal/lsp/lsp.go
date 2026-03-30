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
	timeout  time.Duration
}

func NewService() *Service {
	return &Service{
		servers:  defaultServers(),
		lookPath: exec.LookPath,
		timeout:  8 * time.Second,
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
}

func newSession(ctx context.Context, cfg ServerConfig, workDir string) (*session, error) {
	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
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
		cmd:     cmd,
		stdin:   stdin,
		stdout:  bufio.NewReader(stdout),
		pending: map[int64]chan rpcEnvelope{},
	}
	go s.readLoop()

	rootURI := pathToURI(workDir)
	var initResult map[string]any
	if err := s.Request(ctx, "initialize", map[string]any{
		"processId":    os.Getpid(),
		"rootUri":      rootURI,
		"capabilities": map[string]any{},
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

func (s *session) OpenDocument(ctx context.Context, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	cfg, ok := DetectServerConfig(path)
	if !ok {
		return fmt.Errorf("unsupported language for %s", path)
	}
	return s.Notify("textDocument/didOpen", map[string]any{
		"textDocument": textDocumentItem{
			URI:        pathToURI(path),
			LanguageID: cfg.LanguageID,
			Version:    1,
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

func (s *Service) withSession(ctx context.Context, workDir, path string, fn func(*session) (string, error)) (string, error) {
	cfg, err := s.configForPath(path)
	if err != nil {
		return "", err
	}
	timeout := s.timeout
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	sess, err := newSession(callCtx, cfg, workDir)
	if err != nil {
		return "", err
	}
	defer sess.Close(context.Background())
	if err := sess.OpenDocument(callCtx, path); err != nil {
		return "", err
	}
	return fn(sess)
}

func (s *Service) Definition(ctx context.Context, workDir, path string, line, column int) (string, error) {
	return s.withSession(ctx, workDir, path, func(sess *session) (string, error) {
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
	return s.withSession(ctx, workDir, path, func(sess *session) (string, error) {
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
	return s.withSession(ctx, workDir, path, func(sess *session) (string, error) {
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
	return s.withSession(ctx, workDir, path, func(sess *session) (string, error) {
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
