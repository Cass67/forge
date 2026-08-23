package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDetectServerConfig(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"main.go", "gopls"},
		{"src/app.ts", "typescript-language-server"},
		{"src/app.js", "typescript-language-server"},
		{"src/lib.rs", "rust-analyzer"},
		{"app.py", "pyright-langserver"},
		{"config.yaml", "yaml-language-server"},
	}
	for _, tc := range cases {
		cfg, ok := DetectServerConfig(tc.path)
		if !ok {
			t.Fatalf("expected config for %s", tc.path)
		}
		if cfg.Command != tc.want {
			t.Fatalf("config command for %s = %q, want %q", tc.path, cfg.Command, tc.want)
		}
	}
}

func TestClientSessionRoundTrip(t *testing.T) {
	if len(os.Args) > 1 && os.Args[len(os.Args)-1] == "--forge-fake-lsp" {
		runFakeLSPServer()
		return
	}

	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	if err := os.WriteFile(source, []byte("package main\n\nfunc greet() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := ServerConfig{
		Command:    exe,
		Args:       []string{"-test.run=TestClientSessionRoundTrip", "--", "--forge-fake-lsp"},
		LanguageID: "go",
	}

	sess, err := newSession(context.Background(), cfg, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close(context.Background())

	if err := sess.OpenDocument(context.Background(), source); err != nil {
		t.Fatal(err)
	}

	var hover hoverResponse
	if err := sess.Request(context.Background(), "textDocument/hover", textDocumentPositionParams{
		TextDocument: textDocumentIdentifier{URI: pathToURI(source)},
		Position:     position{Line: 2, Character: 5},
	}, &hover); err != nil {
		t.Fatal(err)
	}
	if got := hoverText(hover); !strings.Contains(got, "fake hover") {
		t.Fatalf("hover text = %q", got)
	}

	var defs []location
	if err := sess.Request(context.Background(), "textDocument/definition", textDocumentPositionParams{
		TextDocument: textDocumentIdentifier{URI: pathToURI(source)},
		Position:     position{Line: 2, Character: 5},
	}, &defs); err != nil {
		t.Fatal(err)
	}
	if len(defs) != 1 || defs[0].URI != pathToURI(source) {
		t.Fatalf("definitions = %#v", defs)
	}
}

// A server is pooled per (workDir, language): spawning a fresh one per call
// made every request pay the cold index, which is what the 8s timeout used to
// be spent on.
func TestServiceReusesPooledSession(t *testing.T) {
	if len(os.Args) > 1 && os.Args[len(os.Args)-1] == "--forge-fake-lsp" {
		runFakeLSPServer()
		return
	}

	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	if err := os.WriteFile(source, []byte("package main\n\nfunc greet() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	svc := NewService()
	svc.servers["go"] = ServerConfig{
		Command:    exe,
		Args:       []string{"-test.run=TestServiceReusesPooledSession", "--", "--forge-fake-lsp"},
		LanguageID: "go",
		Extensions: []string{".go"},
	}
	svc.lookPath = func(name string) (string, error) { return name, nil }
	defer svc.Close(context.Background())

	for i := 0; i < 2; i++ {
		out, err := svc.Hover(context.Background(), dir, source, 3, 6)
		if err != nil {
			t.Fatalf("hover %d: %v", i, err)
		}
		if !strings.Contains(out, "fake hover") {
			t.Fatalf("hover %d = %q", i, out)
		}
	}

	if len(svc.sessions) != 1 {
		t.Fatalf("pooled sessions = %d, want 1", len(svc.sessions))
	}
	var pid int
	for _, sess := range svc.sessions {
		pid = sess.cmd.Process.Pid
	}

	if _, err := svc.DocumentSymbols(context.Background(), dir, source); err != nil {
		t.Fatalf("document symbols: %v", err)
	}
	if len(svc.sessions) != 1 {
		t.Fatalf("pooled sessions after third call = %d, want 1", len(svc.sessions))
	}
	for _, sess := range svc.sessions {
		if sess.cmd.Process.Pid != pid {
			t.Fatalf("server respawned: pid %d then %d", pid, sess.cmd.Process.Pid)
		}
	}
}

// A server that died must not be handed out again.
func TestServiceReplacesDeadSession(t *testing.T) {
	if len(os.Args) > 1 && os.Args[len(os.Args)-1] == "--forge-fake-lsp" {
		runFakeLSPServer()
		return
	}

	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	if err := os.WriteFile(source, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	svc := NewService()
	svc.servers["go"] = ServerConfig{
		Command:    exe,
		Args:       []string{"-test.run=TestServiceReplacesDeadSession", "--", "--forge-fake-lsp"},
		LanguageID: "go",
		Extensions: []string{".go"},
	}
	svc.lookPath = func(name string) (string, error) { return name, nil }
	defer svc.Close(context.Background())

	if _, err := svc.Hover(context.Background(), dir, source, 1, 1); err != nil {
		t.Fatalf("first hover: %v", err)
	}
	var first *session
	for _, sess := range svc.sessions {
		first = sess
	}
	_ = first.cmd.Process.Kill()
	deadline := time.Now().Add(2 * time.Second)
	for first.alive() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if first.alive() {
		t.Fatal("killed server still reports alive")
	}

	if _, err := svc.Hover(context.Background(), dir, source, 1, 1); err != nil {
		t.Fatalf("second hover: %v", err)
	}
	for _, sess := range svc.sessions {
		if sess == first {
			t.Fatal("dead session was handed out again")
		}
	}
}

// Diagnostics are unsolicited notifications, not a request/response pair; the
// service must hold them per URI and hand back errors only.
func TestServiceDiagnosticsReturnsErrorsOnly(t *testing.T) {
	if len(os.Args) > 1 && os.Args[len(os.Args)-1] == "--forge-fake-lsp" {
		runFakeLSPServer()
		return
	}

	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	if err := os.WriteFile(source, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	svc := NewService()
	svc.servers["go"] = ServerConfig{
		Command:    exe,
		Args:       []string{"-test.run=TestServiceDiagnosticsReturnsErrorsOnly", "--", "--forge-fake-lsp"},
		LanguageID: "go",
		Extensions: []string{".go"},
	}
	svc.lookPath = func(name string) (string, error) { return name, nil }
	defer svc.Close(context.Background())

	out, err := svc.Diagnostics(context.Background(), dir, []string{source})
	if err != nil {
		t.Fatalf("diagnostics: %v", err)
	}
	if !strings.Contains(out, "undefined: greet") {
		t.Fatalf("diagnostics = %q, want the error", out)
	}
	if strings.Contains(out, "unused variable") {
		t.Fatalf("diagnostics = %q, want warnings dropped", out)
	}
	if !strings.Contains(out, "main.go:3:2") {
		t.Fatalf("diagnostics = %q, want 1-based line:column", out)
	}
}

// A clean file publishes an empty set (or nothing at all); either way the
// report must be empty so no feedback is injected into the turn.
func TestServiceDiagnosticsQuietWhenClean(t *testing.T) {
	if len(os.Args) > 1 && os.Args[len(os.Args)-1] == "--forge-fake-lsp" {
		runFakeLSPServer()
		return
	}

	dir := t.TempDir()
	source := filepath.Join(dir, "clean.go")
	if err := os.WriteFile(source, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	svc := NewService()
	svc.servers["go"] = ServerConfig{
		Command:    exe,
		Args:       []string{"-test.run=TestServiceDiagnosticsQuietWhenClean", "--", "--forge-fake-lsp"},
		LanguageID: "go",
		Extensions: []string{".go"},
	}
	svc.lookPath = func(name string) (string, error) { return name, nil }
	defer svc.Close(context.Background())

	out, err := svc.Diagnostics(context.Background(), dir, []string{source})
	if err != nil {
		t.Fatalf("diagnostics: %v", err)
	}
	if out != "" {
		t.Fatalf("diagnostics = %q, want empty", out)
	}
}

func runFakeLSPServer() {
	reader := bufio.NewReader(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	for {
		payload, err := readMessage(reader)
		if err != nil {
			if err == io.EOF {
				_ = writer.Flush()
				os.Exit(0)
			}
			panic(err)
		}
		var msg map[string]json.RawMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			panic(err)
		}
		var method string
		_ = json.Unmarshal(msg["method"], &method)

		if method == "textDocument/didOpen" || method == "textDocument/didChange" {
			var params map[string]json.RawMessage
			_ = json.Unmarshal(msg["params"], &params)
			var doc struct {
				URI string `json:"uri"`
			}
			_ = json.Unmarshal(params["textDocument"], &doc)
			if strings.Contains(doc.URI, "clean.go") {
				writeNotification(writer, "textDocument/publishDiagnostics", map[string]any{
					"uri":         doc.URI,
					"diagnostics": []any{},
				})
				continue
			}
			writeNotification(writer, "textDocument/publishDiagnostics", map[string]any{
				"uri": doc.URI,
				"diagnostics": []map[string]any{
					{
						"severity": 1,
						"message":  "undefined: greet",
						"range": map[string]any{
							"start": map[string]any{"line": 2, "character": 1},
							"end":   map[string]any{"line": 2, "character": 6},
						},
					},
					{
						"severity": 2,
						"message":  "unused variable",
						"range": map[string]any{
							"start": map[string]any{"line": 4, "character": 0},
							"end":   map[string]any{"line": 4, "character": 3},
						},
					},
				},
			})
			continue
		}
		if method == "initialized" || method == "exit" {
			continue
		}
		if method == "initialize" {
			writeResponse(writer, msg["id"], map[string]any{
				"capabilities": map[string]any{
					"definitionProvider":     true,
					"referencesProvider":     true,
					"hoverProvider":          true,
					"documentSymbolProvider": true,
					"textDocumentSync":       1,
				},
			})
			continue
		}
		if method == "shutdown" {
			writeResponse(writer, msg["id"], nil)
			continue
		}

		switch method {
		case "textDocument/hover":
			writeResponse(writer, msg["id"], map[string]any{
				"contents": map[string]any{"kind": "plaintext", "value": "fake hover"},
			})
		case "textDocument/definition":
			var params textDocumentPositionParams
			_ = json.Unmarshal(msg["params"], &params)
			writeResponse(writer, msg["id"], []map[string]any{{
				"uri": params.TextDocument.URI,
				"range": map[string]any{
					"start": map[string]any{"line": 2, "character": 5},
					"end":   map[string]any{"line": 2, "character": 10},
				},
			}})
		case "textDocument/references":
			writeResponse(writer, msg["id"], []map[string]any{})
		case "textDocument/documentSymbol":
			writeResponse(writer, msg["id"], []map[string]any{{
				"name": "greet",
				"kind": 12,
				"range": map[string]any{
					"start": map[string]any{"line": 2, "character": 0},
					"end":   map[string]any{"line": 2, "character": 14},
				},
				"selectionRange": map[string]any{
					"start": map[string]any{"line": 2, "character": 5},
					"end":   map[string]any{"line": 2, "character": 10},
				},
			}})
		default:
			writeError(writer, msg["id"], -32601, "method not found")
		}
	}
}

func readMessage(r *bufio.Reader) ([]byte, error) {
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
			_, err := fmt.Sscanf(line, "Content-Length: %d", &length)
			if err != nil {
				return nil, err
			}
		}
	}
	if length == 0 {
		return nil, io.EOF
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func writeNotification(w *bufio.Writer, method string, params any) {
	writeMessage(w, map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func writeResponse(w *bufio.Writer, id json.RawMessage, result any) {
	msg := map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": result}
	writeMessage(w, msg)
}

func writeError(w *bufio.Writer, id json.RawMessage, code int, message string) {
	msg := map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "error": map[string]any{"code": code, "message": message}}
	writeMessage(w, msg)
}

func writeMessage(w *bufio.Writer, msg any) {
	data, err := json.Marshal(msg)
	if err != nil {
		panic(err)
	}
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
		panic(err)
	}
	if _, err := w.Write(data); err != nil {
		panic(err)
	}
	if err := w.Flush(); err != nil {
		panic(err)
	}
}

func TestServiceUsesServerConfig(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{
		servers: map[string]ServerConfig{
			"go": {
				Command:    exe,
				Args:       []string{"-test.run=TestClientSessionRoundTrip", "--", "--forge-fake-lsp"},
				LanguageID: "go",
				Extensions: []string{".go"},
			},
		},
		lookPath: func(file string) (string, error) { return file, nil },
		timeout:  3 * time.Second,
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	if err := os.WriteFile(source, []byte("package main\n\nfunc greet() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := service.Hover(context.Background(), dir, source, 3, 6)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "fake hover") {
		t.Fatalf("hover output = %q", out)
	}
}

// Chat teardown closes the process-wide service, so a later chat in the same
// process must get a live server back rather than a corpse from the old pool.
func TestServiceReusableAfterClose(t *testing.T) {
	if len(os.Args) > 1 && os.Args[len(os.Args)-1] == "--forge-fake-lsp" {
		runFakeLSPServer()
		return
	}

	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	if err := os.WriteFile(source, []byte("package main\n\nfunc greet() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	svc := NewService()
	svc.servers["go"] = ServerConfig{
		Command:    exe,
		Args:       []string{"-test.run=TestServiceReusableAfterClose", "--", "--forge-fake-lsp"},
		LanguageID: "go",
		Extensions: []string{".go"},
	}
	svc.lookPath = func(name string) (string, error) { return name, nil }
	defer svc.Close(context.Background())

	if _, err := svc.Hover(context.Background(), dir, source, 3, 6); err != nil {
		t.Fatalf("hover before close: %v", err)
	}
	var firstPID int
	for _, sess := range svc.sessions {
		firstPID = sess.cmd.Process.Pid
	}

	svc.Close(context.Background())
	if len(svc.sessions) != 0 {
		t.Fatalf("pooled sessions after close = %d, want 0", len(svc.sessions))
	}

	out, err := svc.Hover(context.Background(), dir, source, 3, 6)
	if err != nil {
		t.Fatalf("hover after close: %v", err)
	}
	if !strings.Contains(out, "fake hover") {
		t.Fatalf("hover after close = %q", out)
	}
	if len(svc.sessions) != 1 {
		t.Fatalf("pooled sessions after respawn = %d, want 1", len(svc.sessions))
	}
	for _, sess := range svc.sessions {
		if sess.cmd.Process.Pid == firstPID {
			t.Fatalf("reused the closed server pid %d", firstPID)
		}
	}
}
