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

		if method == "initialized" || method == "textDocument/didOpen" || method == "exit" {
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
