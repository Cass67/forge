# forge chat — Agentic Mode Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `forge chat` command — a conversational coding agent with 6 tools, approval-based execution, and streaming terminal UI.

**Architecture:** New `internal/agent/` package with tool registry, tool-call parser, approval system, and agent loop. Integrates with existing `llm.Driver` interface and config. No changes to existing session/tui/output packages.

**Tech Stack:** Go, existing LLM drivers, `os/exec` for commands, `bufio` for REPL, ANSI escape codes for rendering.

**Spec:** `docs/superpowers/specs/2026-03-21-forge-chat-agent-mode-design.md`

---

## Chunk 1: Foundation — Tool Registry, Safety, and Parser

### Task 1: Path Safety Utilities

**Files:**
- Create: `internal/agent/tools/safety.go`
- Create: `internal/agent/tools/safety_test.go`

- [ ] **Step 1: Write safety tests**

```go
// internal/agent/tools/safety_test.go
package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePath(t *testing.T) {
	workDir := t.TempDir()

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"relative file", "main.go", false},
		{"nested relative", "pkg/util/helper.go", false},
		{"dot-slash prefix", "./main.go", false},
		{"parent escape", "../etc/passwd", true},
		{"absolute path outside", "/etc/passwd", true},
		{"absolute inside workdir", filepath.Join(workDir, "foo.go"), false},
		{"sneaky dotdot", "pkg/../../etc/passwd", true},
		{"empty path", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ResolvePath(workDir, tt.path)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error for path %q, got resolved: %q", tt.path, result)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error for path %q: %v", tt.path, err)
				return
			}
			if !filepath.IsAbs(result) {
				t.Errorf("expected absolute path, got %q", result)
			}
		})
	}
}

func TestResolvePathSymlinkEscape(t *testing.T) {
	workDir := t.TempDir()
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644)
	os.Symlink(outside, filepath.Join(workDir, "escape"))

	_, err := ResolvePath(workDir, "escape/secret.txt")
	if err == nil {
		t.Error("expected error for symlink escape")
	}
}

func TestIsBinary(t *testing.T) {
	if IsBinary([]byte("hello world\nfoo bar\n")) {
		t.Error("text detected as binary")
	}
	if !IsBinary([]byte{0x00, 0x01, 0x02, 0xff, 0xfe}) {
		t.Error("binary not detected")
	}
}
```

- [ ] **Step 2: Run tests — verify they fail**

Run: `go test ./internal/agent/tools/ -run TestResolvePath -v`
Expected: FAIL — package doesn't exist

- [ ] **Step 3: Implement safety.go**

```go
// internal/agent/tools/safety.go
package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolvePath resolves a user-provided path relative to workDir.
// Rejects any path that escapes workDir via .., symlinks, or absolute paths.
func ResolvePath(workDir, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty path")
	}

	var abs string
	if filepath.IsAbs(path) {
		abs = filepath.Clean(path)
	} else {
		abs = filepath.Clean(filepath.Join(workDir, path))
	}

	// Evaluate symlinks to catch symlink escapes
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// If file doesn't exist yet (write_file), check the parent
		dir := filepath.Dir(abs)
		resolvedDir, dirErr := filepath.EvalSymlinks(dir)
		if dirErr != nil {
			// Parent doesn't exist either — check the cleaned path directly
			if !strings.HasPrefix(abs, workDir+string(os.PathSeparator)) && abs != workDir {
				return "", fmt.Errorf("path %q escapes working directory", path)
			}
			return abs, nil
		}
		if !strings.HasPrefix(resolvedDir, workDir+string(os.PathSeparator)) && resolvedDir != workDir {
			return "", fmt.Errorf("path %q escapes working directory", path)
		}
		return abs, nil
	}

	resolvedWorkDir, _ := filepath.EvalSymlinks(workDir)
	if resolvedWorkDir == "" {
		resolvedWorkDir = workDir
	}

	if !strings.HasPrefix(resolved, resolvedWorkDir+string(os.PathSeparator)) && resolved != resolvedWorkDir {
		return "", fmt.Errorf("path %q escapes working directory", path)
	}

	return abs, nil
}

// IsBinary returns true if data appears to be binary (contains null bytes in first 8KB).
func IsBinary(data []byte) bool {
	check := data
	if len(check) > 8192 {
		check = check[:8192]
	}
	for _, b := range check {
		if b == 0 {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run tests — verify they pass**

Run: `go test ./internal/agent/tools/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/tools/safety.go internal/agent/tools/safety_test.go
git commit -m "feat(agent): add path safety utilities for tool sandbox"
```

---

### Task 2: Tool Registry

**Files:**
- Create: `internal/agent/tools/registry.go`
- Create: `internal/agent/tools/registry_test.go`

- [ ] **Step 1: Write registry tests**

```go
// internal/agent/tools/registry_test.go
package tools

import (
	"context"
	"strings"
	"testing"
)

func TestRegistryRegisterAndGet(t *testing.T) {
	reg := NewRegistry()
	tool := Tool{
		Name:        "test_tool",
		Description: "A test tool",
		Parameters: []ParameterDef{
			{Name: "arg1", Type: "string", Description: "first arg", Required: true},
		},
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			return "ok", nil
		},
	}
	reg.Register(tool)

	got, ok := reg.Get("test_tool")
	if !ok {
		t.Fatal("tool not found")
	}
	if got.Name != "test_tool" {
		t.Errorf("got name %q", got.Name)
	}

	_, ok = reg.Get("nonexistent")
	if ok {
		t.Error("found nonexistent tool")
	}
}

func TestRegistryDescribe(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Tool{
		Name:        "read_file",
		Description: "Read a file",
		Parameters: []ParameterDef{
			{Name: "path", Type: "string", Description: "file path", Required: true},
		},
	})
	desc := reg.Describe()
	if !strings.Contains(desc, "read_file") {
		t.Error("describe missing tool name")
	}
	if !strings.Contains(desc, "path") {
		t.Error("describe missing parameter")
	}
}

func TestRegistryNeedsApproval(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Tool{Name: "read_file", AutoApprove: true})
	reg.Register(Tool{Name: "write_file", AutoApprove: false})

	r, _ := reg.Get("read_file")
	if !r.AutoApprove {
		t.Error("read_file should auto-approve")
	}
	w, _ := reg.Get("write_file")
	if w.AutoApprove {
		t.Error("write_file should not auto-approve")
	}
}
```

- [ ] **Step 2: Run tests — verify they fail**

Run: `go test ./internal/agent/tools/ -run TestRegistry -v`
Expected: FAIL — types not defined

- [ ] **Step 3: Implement registry.go**

```go
// internal/agent/tools/registry.go
package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Tool defines a single tool the agent can call.
type Tool struct {
	Name        string
	Description string
	Parameters  []ParameterDef
	AutoApprove bool
	Execute     func(ctx context.Context, args map[string]any) (string, error)
}

// ParameterDef describes one parameter.
type ParameterDef struct {
	Name        string
	Type        string // "string", "int", "bool"
	Description string
	Required    bool
}

// Action describes a tool action for the approval system.
type Action struct {
	Tool    string
	Summary string
	Detail  string // diff content, command text, or file content
}

// ApprovalFunc asks the user to approve an action. Returns true if approved.
type ApprovalFunc func(action Action) (bool, error)

// Registry holds available tools.
type Registry struct {
	tools map[string]Tool
	order []string
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register adds a tool to the registry.
func (r *Registry) Register(t Tool) {
	r.tools[t.Name] = t
	r.order = append(r.order, t.Name)
}

// Get retrieves a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// All returns all registered tools in registration order.
func (r *Registry) All() []Tool {
	result := make([]Tool, 0, len(r.order))
	for _, name := range r.order {
		result = append(result, r.tools[name])
	}
	return result
}

// Describe formats all tools for injection into the system prompt.
func (r *Registry) Describe() string {
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	sort.Strings(names)

	var sb strings.Builder
	sb.WriteString("You have access to the following tools:\n\n")
	for _, name := range names {
		t := r.tools[name]
		sb.WriteString(fmt.Sprintf("## %s\n%s\n", t.Name, t.Description))
		if len(t.Parameters) > 0 {
			sb.WriteString("Parameters:\n")
			for _, p := range t.Parameters {
				req := "optional"
				if p.Required {
					req = "required"
				}
				sb.WriteString(fmt.Sprintf("  - %s (%s, %s): %s\n", p.Name, p.Type, req, p.Description))
			}
		}
		sb.WriteString("\n")
	}
	sb.WriteString(`To call a tool, use this exact format:

<tool_call>
{"name": "tool_name", "args": {"param": "value"}}
</tool_call>

You may call multiple tools. After tool results are returned, continue your work.
Wait for results before making decisions based on them.
`)
	return sb.String()
}
```

- [ ] **Step 4: Run tests — verify they pass**

Run: `go test ./internal/agent/tools/ -run TestRegistry -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/tools/registry.go internal/agent/tools/registry_test.go
git commit -m "feat(agent): add tool registry with describe for system prompt"
```

---

### Task 3: Tool Call Parser

**Files:**
- Create: `internal/agent/parse.go`
- Create: `internal/agent/parse_test.go`

- [ ] **Step 1: Write parser tests**

```go
// internal/agent/parse_test.go
package agent

import (
	"testing"
)

func TestParseToolCalls(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{
			name: "single tool call",
			input: `Let me read the file.

<tool_call>
{"name": "read_file", "args": {"path": "main.go"}}
</tool_call>`,
			want: 1,
		},
		{
			name: "multiple tool calls",
			input: `<tool_call>
{"name": "read_file", "args": {"path": "a.go"}}
</tool_call>

Some reasoning.

<tool_call>
{"name": "read_file", "args": {"path": "b.go"}}
</tool_call>`,
			want: 2,
		},
		{
			name:  "no tool calls",
			input: "Just some text with no tools.",
			want:  0,
		},
		{
			name: "tool call inside code fence ignored",
			input: "Here is an example:\n```\n<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"x.go\"}}\n</tool_call>\n```\n",
			want:  0,
		},
		{
			name: "real call after code fence example",
			input: "Example:\n```\n<tool_call>\n{\"name\": \"fake\"}\n</tool_call>\n```\n\n<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"real.go\"}}\n</tool_call>",
			want:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls, text := ParseToolCalls(tt.input)
			if len(calls) != tt.want {
				t.Errorf("got %d tool calls, want %d", len(calls), tt.want)
			}
			if tt.want == 0 && text != tt.input {
				// When no tool calls, text should be the full input
			}
			if tt.want > 0 && calls[0].Name == "" {
				t.Error("tool call name is empty")
			}
		})
	}
}

func TestParseToolCallArgs(t *testing.T) {
	input := `<tool_call>
{"name": "edit_file", "args": {"path": "main.go", "old_text": "foo", "new_text": "bar"}}
</tool_call>`
	calls, _ := ParseToolCalls(input)
	if len(calls) != 1 {
		t.Fatal("expected 1 call")
	}
	if calls[0].Name != "edit_file" {
		t.Errorf("name = %q", calls[0].Name)
	}
	if calls[0].Args["path"] != "main.go" {
		t.Errorf("path = %v", calls[0].Args["path"])
	}
	if calls[0].Args["old_text"] != "foo" {
		t.Errorf("old_text = %v", calls[0].Args["old_text"])
	}
}
```

- [ ] **Step 2: Run tests — verify they fail**

Run: `go test ./internal/agent/ -run TestParse -v`
Expected: FAIL — package doesn't exist

- [ ] **Step 3: Implement parse.go**

```go
// internal/agent/parse.go
package agent

import (
	"encoding/json"
	"strings"
)

// ToolCall represents a parsed tool invocation from LLM output.
type ToolCall struct {
	Name string
	Args map[string]any
}

// ParseToolCalls extracts <tool_call> blocks from LLM output.
// Blocks inside markdown code fences (```) are ignored.
// Returns the parsed calls and the remaining text (reasoning/explanation).
func ParseToolCalls(text string) ([]ToolCall, string) {
	var calls []ToolCall
	var textParts []string

	lines := strings.Split(text, "\n")
	i := 0
	inCodeFence := false

	for i < len(lines) {
		line := lines[i]

		// Track code fences
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if inCodeFence {
				inCodeFence = false
				textParts = append(textParts, line)
				i++
				continue
			}
			// Opening fence — check if this is a tool_call inside a code block
			// by looking ahead: if we see <tool_call> inside, it's an example
			inCodeFence = true
			textParts = append(textParts, line)
			i++
			continue
		}

		if inCodeFence {
			textParts = append(textParts, line)
			i++
			continue
		}

		// Check for tool_call block
		if strings.TrimSpace(line) == "<tool_call>" {
			i++
			var block strings.Builder
			for i < len(lines) && strings.TrimSpace(lines[i]) != "</tool_call>" {
				block.WriteString(lines[i])
				block.WriteByte('\n')
				i++
			}
			if i < len(lines) {
				i++ // skip </tool_call>
			}

			call := parseCallJSON(block.String())
			if call.Name != "" {
				calls = append(calls, call)
			}
			continue
		}

		textParts = append(textParts, line)
		i++
	}

	return calls, strings.Join(textParts, "\n")
}

func parseCallJSON(raw string) ToolCall {
	raw = strings.TrimSpace(raw)
	var parsed struct {
		Name string         `json:"name"`
		Args map[string]any `json:"args"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return ToolCall{}
	}
	if parsed.Args == nil {
		parsed.Args = make(map[string]any)
	}
	return ToolCall{Name: parsed.Name, Args: parsed.Args}
}
```

- [ ] **Step 4: Run tests — verify they pass**

Run: `go test ./internal/agent/ -run TestParse -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/parse.go internal/agent/parse_test.go
git commit -m "feat(agent): add tool call parser with code fence awareness"
```

---

### Task 4: Config — Add Chat Section

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Write config test**

Add to `internal/config/config_test.go`:

```go
func TestChatConfigDefaults(t *testing.T) {
	cfg, err := Load("/nonexistent/path.toml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Chat.MaxTurns != 50 {
		t.Errorf("MaxTurns = %d, want 50", cfg.Chat.MaxTurns)
	}
	if cfg.Chat.CommandTimeout != 60 {
		t.Errorf("CommandTimeout = %d, want 60", cfg.Chat.CommandTimeout)
	}
	if cfg.Chat.Yolo {
		t.Error("Yolo should default to false")
	}
	if len(cfg.Chat.IgnoreDirs) == 0 {
		t.Error("IgnoreDirs should have defaults")
	}
}

func TestChatModel(t *testing.T) {
	cfg, _ := Load("/nonexistent/path.toml")
	if got := cfg.ChatModel(); got != cfg.Models.Writer {
		t.Errorf("ChatModel() = %q, want %q (writer default)", got, cfg.Models.Writer)
	}
	cfg.Chat.Model = "gpt-4o"
	if got := cfg.ChatModel(); got != "gpt-4o" {
		t.Errorf("ChatModel() = %q, want gpt-4o", got)
	}
}
```

- [ ] **Step 2: Run tests — verify they fail**

Run: `go test ./internal/config/ -run TestChat -v`
Expected: FAIL — Chat field doesn't exist

- [ ] **Step 3: Add ChatConfig to config.go**

Add the struct and field:

```go
type ChatConfig struct {
	Model          string   `toml:"model"`
	MaxTurns       int      `toml:"max_turns"`
	CommandTimeout int      `toml:"command_timeout"`
	Yolo           bool     `toml:"yolo"`
	IgnoreDirs     []string `toml:"ignore_dirs"`
}
```

Add `Chat ChatConfig` field to `Config` struct.

Add defaults in `setDefaults()`:
```go
c.Chat.MaxTurns = 50
c.Chat.CommandTimeout = 60
c.Chat.IgnoreDirs = []string{".git", "node_modules", "__pycache__", ".venv", "vendor"}
```

Add helper method:
```go
func (c *Config) ChatModel() string {
	if v := os.Getenv("FORGE_CHAT_MODEL"); v != "" {
		return v
	}
	if c.Chat.Model != "" {
		return c.Chat.Model
	}
	return c.Models.Writer
}
```

- [ ] **Step 4: Run tests — verify they pass**

Run: `go test ./internal/config/ -v`
Expected: PASS (all tests including existing ones)

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add [chat] section for agent mode"
```

---

## Chunk 2: The 6 Tools

### Task 5: read_file Tool

**Files:**
- Create: `internal/agent/tools/read.go`
- Create: `internal/agent/tools/read_test.go`

- [ ] **Step 1: Write tests**

```go
// internal/agent/tools/read_test.go
package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileBasic(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)

	tool := NewReadFile(dir)
	result, err := tool.Execute(context.Background(), map[string]any{"path": "hello.go"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "package main") {
		t.Error("result missing file content")
	}
	if !strings.Contains(result, "1 |") {
		t.Error("result missing line numbers")
	}
}

func TestReadFileLineRange(t *testing.T) {
	dir := t.TempDir()
	content := "line1\nline2\nline3\nline4\nline5\n"
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte(content), 0o644)

	tool := NewReadFile(dir)
	result, err := tool.Execute(context.Background(), map[string]any{
		"path": "test.txt", "start_line": float64(2), "end_line": float64(4),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "line2") {
		t.Error("missing line2")
	}
	if strings.Contains(result, "line1") {
		t.Error("should not contain line1")
	}
	if strings.Contains(result, "line5") {
		t.Error("should not contain line5")
	}
}

func TestReadFileEscape(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadFile(dir)
	_, err := tool.Execute(context.Background(), map[string]any{"path": "../etc/passwd"})
	if err == nil {
		t.Error("expected error for path escape")
	}
}

func TestReadFileBinary(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "bin"), []byte{0x00, 0x01, 0x02}, 0o644)

	tool := NewReadFile(dir)
	result, err := tool.Execute(context.Background(), map[string]any{"path": "bin"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "binary") {
		t.Error("expected binary file error message")
	}
}

func TestReadFileNotFound(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadFile(dir)
	result, err := tool.Execute(context.Background(), map[string]any{"path": "nope.go"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "error") && !strings.Contains(result, "no such file") {
		t.Errorf("expected error message, got: %s", result)
	}
}
```

- [ ] **Step 2: Run tests — verify they fail**

Run: `go test ./internal/agent/tools/ -run TestReadFile -v`

- [ ] **Step 3: Implement read.go**

```go
// internal/agent/tools/read.go
package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
)

func NewReadFile(workDir string) Tool {
	return Tool{
		Name:        "read_file",
		Description: "Read a file's contents. Returns content with line numbers.",
		Parameters: []ParameterDef{
			{Name: "path", Type: "string", Description: "file path relative to working directory", Required: true},
			{Name: "start_line", Type: "int", Description: "first line to read (1-indexed)", Required: false},
			{Name: "end_line", Type: "int", Description: "last line to read", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			path, _ := args["path"].(string)
			resolved, err := ResolvePath(workDir, path)
			if err != nil {
				return "", err
			}

			data, err := os.ReadFile(resolved)
			if err != nil {
				return fmt.Sprintf("error: %v", err), nil
			}

			if len(data) > 200*1024 {
				return fmt.Sprintf("error: file is %d bytes — use start_line/end_line to read a section", len(data)), nil
			}

			if IsBinary(data) {
				return "error: binary file, cannot display", nil
			}

			lines := strings.Split(string(data), "\n")
			start := 1
			end := len(lines)

			if v, ok := args["start_line"].(float64); ok && v > 0 {
				start = int(v)
			}
			if v, ok := args["end_line"].(float64); ok && v > 0 {
				end = int(v)
			}

			if start < 1 {
				start = 1
			}
			if end > len(lines) {
				end = len(lines)
			}
			if start > end {
				return "error: start_line > end_line", nil
			}

			var sb strings.Builder
			for i := start; i <= end; i++ {
				if i <= len(lines) {
					sb.WriteString(fmt.Sprintf("%4d | %s\n", i, lines[i-1]))
				}
			}
			return sb.String(), nil
		},
	}
}
```

- [ ] **Step 4: Run tests — verify they pass**

Run: `go test ./internal/agent/tools/ -run TestReadFile -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/tools/read.go internal/agent/tools/read_test.go
git commit -m "feat(agent): add read_file tool"
```

---

### Task 6: write_file Tool

**Files:**
- Create: `internal/agent/tools/write.go`
- Create: `internal/agent/tools/write_test.go`

- [ ] **Step 1: Write tests**

```go
// internal/agent/tools/write_test.go
package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFileNew(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteFile(dir, func(a Action) (bool, error) { return true, nil })

	result, err := tool.Execute(context.Background(), map[string]any{
		"path": "new.go", "content": "package main\n",
	})
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "new.go"))
	if string(data) != "package main\n" {
		t.Errorf("file content = %q", string(data))
	}
	if !strings.Contains(result, "wrote") || !strings.Contains(result, "new.go") {
		t.Errorf("unexpected result: %s", result)
	}
}

func TestWriteFileCreatesDirs(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteFile(dir, func(a Action) (bool, error) { return true, nil })

	_, err := tool.Execute(context.Background(), map[string]any{
		"path": "pkg/sub/file.go", "content": "package sub\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pkg", "sub", "file.go")); err != nil {
		t.Error("file not created with parent dirs")
	}
}

func TestWriteFileDenied(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteFile(dir, func(a Action) (bool, error) { return false, nil })

	result, err := tool.Execute(context.Background(), map[string]any{
		"path": "denied.go", "content": "package main\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "denied") {
		t.Errorf("expected denied message, got: %s", result)
	}
	if _, err := os.Stat(filepath.Join(dir, "denied.go")); err == nil {
		t.Error("file should not exist after denial")
	}
}

func TestWriteFileEscape(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteFile(dir, func(a Action) (bool, error) { return true, nil })
	_, err := tool.Execute(context.Background(), map[string]any{
		"path": "../escape.go", "content": "bad",
	})
	if err == nil {
		t.Error("expected error for path escape")
	}
}
```

- [ ] **Step 2: Run tests — verify they fail**

- [ ] **Step 3: Implement write.go**

```go
// internal/agent/tools/write.go
package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Note: Action and ApprovalFunc types live in registry.go

func NewWriteFile(workDir string, approve ApprovalFunc) Tool {
	return Tool{
		Name:        "write_file",
		Description: "Create or overwrite a file.",
		Parameters: []ParameterDef{
			{Name: "path", Type: "string", Description: "file path", Required: true},
			{Name: "content", Type: "string", Description: "full file content", Required: true},
		},
		AutoApprove: false,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			path, _ := args["path"].(string)
			content, _ := args["content"].(string)

			resolved, err := ResolvePath(workDir, path)
			if err != nil {
				return "", err
			}

			// Build diff for approval
			var detail string
			existing, readErr := os.ReadFile(resolved)
			if readErr == nil {
				detail = simpleDiff(string(existing), content, path)
			} else {
				preview := content
				lines := strings.Split(preview, "\n")
				if len(lines) > 20 {
					preview = strings.Join(lines[:20], "\n") + "\n... (truncated)"
				}
				detail = fmt.Sprintf("new file: %s\n%s", path, preview)
			}

			approved, err := approve(Action{
				Tool:    "write_file",
				Summary: fmt.Sprintf("write %s", path),
				Detail:  detail,
			})
			if err != nil {
				return "", err
			}
			if !approved {
				return "write_file denied by user", nil
			}

			if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
				return fmt.Sprintf("error creating directories: %v", err), nil
			}

			if err := os.WriteFile(resolved, []byte(content), 0o644); err != nil {
				return fmt.Sprintf("error writing file: %v", err), nil
			}

			return fmt.Sprintf("wrote %d bytes to %s", len(content), path), nil
		},
	}
}

func simpleDiff(old, new_, path string) string {
	oldLines := strings.Split(old, "\n")
	newLines := strings.Split(new_, "\n")
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("--- a/%s\n+++ b/%s\n", path, path))

	maxLen := len(oldLines)
	if len(newLines) > maxLen {
		maxLen = len(newLines)
	}

	for i := 0; i < maxLen; i++ {
		haveOld := i < len(oldLines)
		haveNew := i < len(newLines)
		switch {
		case haveOld && haveNew && oldLines[i] == newLines[i]:
			sb.WriteString(" " + oldLines[i] + "\n")
		case haveOld && haveNew:
			sb.WriteString("-" + oldLines[i] + "\n")
			sb.WriteString("+" + newLines[i] + "\n")
		case haveOld:
			sb.WriteString("-" + oldLines[i] + "\n")
		case haveNew:
			sb.WriteString("+" + newLines[i] + "\n")
		}
	}
	return sb.String()
}
```

- [ ] **Step 4: Run tests — verify they pass**

Run: `go test ./internal/agent/tools/ -run TestWriteFile -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/tools/write.go internal/agent/tools/write_test.go
git commit -m "feat(agent): add write_file tool with approval"
```

---

### Task 7: edit_file Tool

**Files:**
- Create: `internal/agent/tools/edit.go`
- Create: `internal/agent/tools/edit_test.go`

- [ ] **Step 1: Write tests**

```go
// internal/agent/tools/edit_test.go
package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditFileBasic(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc hello() {}\n"), 0o644)

	tool := NewEditFile(dir, func(a Action) (bool, error) { return true, nil })
	result, err := tool.Execute(context.Background(), map[string]any{
		"path":     "main.go",
		"old_text": "func hello() {}",
		"new_text": "func hello() {\n\tfmt.Println(\"hello\")\n}",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "edited") {
		t.Errorf("unexpected result: %s", result)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "main.go"))
	if !strings.Contains(string(data), "Println") {
		t.Error("edit not applied")
	}
}

func TestEditFileNotFound(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644)

	tool := NewEditFile(dir, func(a Action) (bool, error) { return true, nil })
	result, err := tool.Execute(context.Background(), map[string]any{
		"path": "main.go", "old_text": "nonexistent text", "new_text": "new",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "not found") {
		t.Errorf("expected 'not found' error, got: %s", result)
	}
}

func TestEditFileMultipleMatches(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("foo\nbar\nfoo\n"), 0o644)

	tool := NewEditFile(dir, func(a Action) (bool, error) { return true, nil })
	result, err := tool.Execute(context.Background(), map[string]any{
		"path": "main.go", "old_text": "foo", "new_text": "baz",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "matched 2") {
		t.Errorf("expected multiple match error, got: %s", result)
	}
}

func TestEditFileDenied(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644)

	tool := NewEditFile(dir, func(a Action) (bool, error) { return false, nil })
	result, err := tool.Execute(context.Background(), map[string]any{
		"path": "main.go", "old_text": "package main", "new_text": "package foo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "denied") {
		t.Error("expected denied message")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "main.go"))
	if !strings.Contains(string(data), "package main") {
		t.Error("file should be unchanged after denial")
	}
}
```

- [ ] **Step 2: Run tests — verify they fail**

- [ ] **Step 3: Implement edit.go**

```go
// internal/agent/tools/edit.go
package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
)

func NewEditFile(workDir string, approve ApprovalFunc) Tool {
	return Tool{
		Name:        "edit_file",
		Description: "Make a search-and-replace edit within a file.",
		Parameters: []ParameterDef{
			{Name: "path", Type: "string", Description: "file path", Required: true},
			{Name: "old_text", Type: "string", Description: "exact text to find (must be unique in file)", Required: true},
			{Name: "new_text", Type: "string", Description: "replacement text", Required: true},
		},
		AutoApprove: false,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			path, _ := args["path"].(string)
			oldText, _ := args["old_text"].(string)
			newText, _ := args["new_text"].(string)

			resolved, err := ResolvePath(workDir, path)
			if err != nil {
				return "", err
			}

			data, err := os.ReadFile(resolved)
			if err != nil {
				return fmt.Sprintf("error: %v", err), nil
			}

			content := string(data)
			count := strings.Count(content, oldText)
			if count == 0 {
				return fmt.Sprintf("edit_file failed: old_text not found in %s", path), nil
			}
			if count > 1 {
				return fmt.Sprintf("edit_file failed: old_text matched %d locations in %s; provide more surrounding context to make the match unique", count, path), nil
			}

			newContent := strings.Replace(content, oldText, newText, 1)
			diff := simpleDiff(content, newContent, path)

			approved, err := approve(Action{
				Tool:    "edit_file",
				Summary: fmt.Sprintf("edit %s", path),
				Detail:  diff,
			})
			if err != nil {
				return "", err
			}
			if !approved {
				return "edit_file denied by user", nil
			}

			if err := os.WriteFile(resolved, []byte(newContent), 0o644); err != nil {
				return fmt.Sprintf("error writing file: %v", err), nil
			}

			return fmt.Sprintf("edited %s", path), nil
		},
	}
}
```

- [ ] **Step 4: Run tests — verify they pass**

Run: `go test ./internal/agent/tools/ -run TestEditFile -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/tools/edit.go internal/agent/tools/edit_test.go
git commit -m "feat(agent): add edit_file tool with search-and-replace"
```

---

### Task 8: list_dir Tool

**Files:**
- Create: `internal/agent/tools/list.go`
- Create: `internal/agent/tools/list_test.go`

- [ ] **Step 1: Write tests**

```go
// internal/agent/tools/list_test.go
package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListDirBasic(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("x"), 0o644)
	os.Mkdir(filepath.Join(dir, "pkg"), 0o755)
	os.WriteFile(filepath.Join(dir, "pkg", "util.go"), []byte("x"), 0o644)

	tool := NewListDir(dir, nil)
	result, err := tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "main.go") {
		t.Error("missing main.go")
	}
	if !strings.Contains(result, "pkg/") {
		t.Error("missing pkg/ dir")
	}
}

func TestListDirRecursive(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755)
	os.WriteFile(filepath.Join(dir, "a", "b", "deep.go"), []byte("x"), 0o644)

	tool := NewListDir(dir, nil)
	result, err := tool.Execute(context.Background(), map[string]any{
		"path": ".", "recursive": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "deep.go") {
		t.Error("missing deep.go in recursive listing")
	}
}

func TestListDirIgnoresDotGit(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".git", "objects"), 0o755)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("x"), 0o644)

	tool := NewListDir(dir, []string{".git"})
	result, err := tool.Execute(context.Background(), map[string]any{"recursive": true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result, ".git") {
		t.Error("should not list .git")
	}
}
```

- [ ] **Step 2: Run tests — verify they fail**

- [ ] **Step 3: Implement list.go**

```go
// internal/agent/tools/list.go
package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func NewListDir(workDir string, ignoreDirs []string) Tool {
	ignoreSet := make(map[string]bool)
	for _, d := range ignoreDirs {
		ignoreSet[d] = true
	}

	return Tool{
		Name:        "list_dir",
		Description: "List directory contents.",
		Parameters: []ParameterDef{
			{Name: "path", Type: "string", Description: "directory path (default \".\")", Required: false},
			{Name: "recursive", Type: "bool", Description: "list recursively (default false)", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			path := "."
			if p, ok := args["path"].(string); ok && p != "" {
				path = p
			}
			recursive, _ := args["recursive"].(bool)

			resolved, err := ResolvePath(workDir, path)
			if err != nil {
				return "", err
			}

			if recursive {
				return listRecursive(resolved, workDir, ignoreSet)
			}
			return listFlat(resolved, ignoreSet)
		},
	}
}

func listFlat(dir string, ignore map[string]bool) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Sprintf("error: %v", err), nil
	}
	var sb strings.Builder
	for _, e := range entries {
		if ignore[e.Name()] {
			continue
		}
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		sb.WriteString(name + "\n")
	}
	return sb.String(), nil
}

func listRecursive(dir, workDir string, ignore map[string]bool) (string, error) {
	var sb strings.Builder
	count := 0
	maxEntries := 500

	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && ignore[d.Name()] {
			return filepath.SkipDir
		}
		if count >= maxEntries {
			return filepath.SkipAll
		}

		rel, _ := filepath.Rel(workDir, path)
		if rel == "." {
			return nil
		}

		name := rel
		if d.IsDir() {
			name += "/"
		}
		sb.WriteString(name + "\n")
		count++
		return nil
	})

	if count >= maxEntries {
		sb.WriteString(fmt.Sprintf("... truncated at %d entries\n", maxEntries))
	}
	return sb.String(), nil
}
```

- [ ] **Step 4: Run tests — verify they pass**

Run: `go test ./internal/agent/tools/ -run TestListDir -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/tools/list.go internal/agent/tools/list_test.go
git commit -m "feat(agent): add list_dir tool with ignore and recursive"
```

---

### Task 9: search Tool

**Files:**
- Create: `internal/agent/tools/search.go`
- Create: `internal/agent/tools/search_test.go`

- [ ] **Step 1: Write tests**

```go
// internal/agent/tools/search_test.go
package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchBasic(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc hello() {}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "other.go"), []byte("package main\n\nfunc world() {}\n"), 0o644)

	tool := NewSearch(dir)
	result, err := tool.Execute(context.Background(), map[string]any{"pattern": "func"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "hello") {
		t.Error("missing hello match")
	}
	if !strings.Contains(result, "world") {
		t.Error("missing world match")
	}
}

func TestSearchWithGlob(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# main package\n"), 0o644)

	tool := NewSearch(dir)
	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "main", "glob": "*.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "main.go") {
		t.Error("should match main.go")
	}
	if strings.Contains(result, "readme.md") {
		t.Error("should not match readme.md with *.go glob")
	}
}

func TestSearchNoResults(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644)

	tool := NewSearch(dir)
	result, err := tool.Execute(context.Background(), map[string]any{"pattern": "zzzzzzz"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "no matches") {
		t.Errorf("expected 'no matches', got: %s", result)
	}
}
```

- [ ] **Step 2: Run tests — verify they fail**

- [ ] **Step 3: Implement search.go**

```go
// internal/agent/tools/search.go
package tools

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func NewSearch(workDir string) Tool {
	return Tool{
		Name:        "search",
		Description: "Search for a pattern across files.",
		Parameters: []ParameterDef{
			{Name: "pattern", Type: "string", Description: "regex pattern", Required: true},
			{Name: "path", Type: "string", Description: "directory to search (default \".\")", Required: false},
			{Name: "glob", Type: "string", Description: "file pattern filter (e.g. \"*.go\")", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			pattern, _ := args["pattern"].(string)
			searchPath := "."
			if p, ok := args["path"].(string); ok && p != "" {
				searchPath = p
			}
			glob, _ := args["glob"].(string)

			// Try ripgrep first, fall back to grep
			result, err := searchRg(ctx, workDir, pattern, searchPath, glob)
			if err != nil {
				result, err = searchGrep(ctx, workDir, pattern, searchPath, glob)
				if err != nil {
					return fmt.Sprintf("search error: %v", err), nil
				}
			}

			if result == "" {
				return "no matches found", nil
			}

			lines := strings.Split(strings.TrimRight(result, "\n"), "\n")
			if len(lines) > 100 {
				truncated := strings.Join(lines[:100], "\n")
				return truncated + fmt.Sprintf("\n... %d more matches", len(lines)-100), nil
			}
			return result, nil
		},
	}
}

func searchRg(ctx context.Context, workDir, pattern, searchPath, glob string) (string, error) {
	args := []string{"-n", "--no-heading", pattern, searchPath}
	if glob != "" {
		args = []string{"-n", "--no-heading", "--glob", glob, pattern, searchPath}
	}
	cmd := exec.CommandContext(ctx, "rg", args...)
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return "", nil // no matches
		}
		return "", err
	}
	return string(out), nil
}

func searchGrep(ctx context.Context, workDir, pattern, searchPath, glob string) (string, error) {
	args := []string{"-rn", pattern, searchPath}
	if glob != "" {
		args = []string{"-rn", "--include", glob, pattern, searchPath}
	}
	cmd := exec.CommandContext(ctx, "grep", args...)
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return "", nil
		}
		return "", err
	}
	return string(out), nil
}
```

- [ ] **Step 4: Run tests — verify they pass**

Run: `go test ./internal/agent/tools/ -run TestSearch -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/tools/search.go internal/agent/tools/search_test.go
git commit -m "feat(agent): add search tool with rg/grep fallback"
```

---

### Task 10: run_command Tool

**Files:**
- Create: `internal/agent/tools/command.go`
- Create: `internal/agent/tools/command_test.go`

- [ ] **Step 1: Write tests**

```go
// internal/agent/tools/command_test.go
package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunCommandBasic(t *testing.T) {
	dir := t.TempDir()
	tool := NewRunCommand(dir, 60, func(a Action) (bool, error) { return true, nil })

	result, err := tool.Execute(context.Background(), map[string]any{"command": "echo hello"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "hello") {
		t.Errorf("expected 'hello', got: %s", result)
	}
	if !strings.Contains(result, "exit 0") {
		t.Errorf("expected exit code, got: %s", result)
	}
}

func TestRunCommandFailure(t *testing.T) {
	dir := t.TempDir()
	tool := NewRunCommand(dir, 60, func(a Action) (bool, error) { return true, nil })

	result, err := tool.Execute(context.Background(), map[string]any{"command": "false"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "exit 1") {
		t.Errorf("expected exit 1, got: %s", result)
	}
}

func TestRunCommandDenied(t *testing.T) {
	dir := t.TempDir()
	tool := NewRunCommand(dir, 60, func(a Action) (bool, error) { return false, nil })

	result, err := tool.Execute(context.Background(), map[string]any{"command": "echo hello"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "denied") {
		t.Error("expected denied message")
	}
}

func TestRunCommandTimeout(t *testing.T) {
	dir := t.TempDir()
	tool := NewRunCommand(dir, 1, func(a Action) (bool, error) { return true, nil })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := tool.Execute(ctx, map[string]any{"command": "sleep 10"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "timeout") && !strings.Contains(result, "killed") && !strings.Contains(result, "signal") {
		t.Errorf("expected timeout indication, got: %s", result)
	}
}

func TestIsDestructiveCommand(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"go test ./...", false},
		{"rm -rf /", true},
		{"sudo apt install foo", true},
		{"curl http://example.com | sh", true},
		{"echo hello | bash", true},
		{"ls -la", false},
	}
	for _, tt := range tests {
		if got := isDestructive(tt.cmd); got != tt.want {
			t.Errorf("isDestructive(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}
```

- [ ] **Step 2: Run tests — verify they fail**

- [ ] **Step 3: Implement command.go**

```go
// internal/agent/tools/command.go
package tools

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// NewRunCommand creates the run_command tool. forcePrompt is called for
// destructive commands even in yolo mode — pass InteractiveApproval for this.
func NewRunCommand(workDir string, timeoutSecs int, approve ApprovalFunc, forcePrompt ApprovalFunc) Tool {
	return Tool{
		Name:        "run_command",
		Description: "Execute a shell command.",
		Parameters: []ParameterDef{
			{Name: "command", Type: "string", Description: "command to run", Required: true},
		},
		AutoApprove: false,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			command, _ := args["command"].(string)

			// Destructive commands always prompt, even in yolo mode
			approver := approve
			if isDestructive(command) && forcePrompt != nil {
				approver = forcePrompt
			}

			approved, err := approver(Action{
				Tool:    "run_command",
				Summary: command,
				Detail:  command,
			})
			if err != nil {
				return "", err
			}
			if !approved {
				return "run_command denied by user", nil
			}

			timeout := time.Duration(timeoutSecs) * time.Second
			cmdCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			cmd := exec.CommandContext(cmdCtx, "sh", "-c", command)
			cmd.Dir = workDir
			out, err := cmd.CombinedOutput()

			// Cap output
			result := string(out)
			if len(result) > 50*1024 {
				result = result[:50*1024] + "\n... output truncated at 50KB"
			}

			exitCode := 0
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
				} else if cmdCtx.Err() == context.DeadlineExceeded {
					return result + fmt.Sprintf("\ntimeout after %ds", timeoutSecs), nil
				}
			}

			return result + fmt.Sprintf("\nexit %d", exitCode), nil
		},
	}
}

// isDestructive checks if a command matches known dangerous patterns.
// Used by yolo mode to still prompt for these.
func isDestructive(cmd string) bool {
	lower := strings.ToLower(cmd)
	patterns := []string{
		"rm -rf /",
		"sudo ",
		"| sh", "| bash", "| zsh",
		"chmod 777",
		"mkfs",
		"> /dev/",
		"dd if=",
	}
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run tests — verify they pass**

Run: `go test ./internal/agent/tools/ -run TestRunCommand -v`
Expected: PASS (TestRunCommandTimeout may take ~1-2s)

Run: `go test ./internal/agent/tools/ -run TestIsDestructive -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/tools/command.go internal/agent/tools/command_test.go
git commit -m "feat(agent): add run_command tool with destructive command detection"
```

---

## Chunk 3: Agent Loop, Rendering, and CLI Wiring

### Task 11: Approval System

**Files:**
- Create: `internal/agent/approval.go`
- Create: `internal/agent/approval_test.go`

- [ ] **Step 1: Write tests**

```go
// internal/agent/approval_test.go
package agent

import (
	"bytes"
	"testing"

	"forge/internal/agent/tools"
)

func TestYoloApproval(t *testing.T) {
	approve := YoloApproval()
	ok, err := approve(tools.Action{Tool: "write_file", Summary: "write main.go"})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("yolo should always approve")
	}
}

func TestYoloApprovalDestructive(t *testing.T) {
	// In real yolo mode, destructive commands should still use the inner approval
	// This is tested via the YoloWithSafetyNet wrapper
	approve := YoloApproval()
	ok, _ := approve(tools.Action{Tool: "run_command", Summary: "rm -rf /"})
	// Pure yolo approves everything — the safety net is applied in the tool itself
	if !ok {
		t.Error("pure yolo approves everything")
	}
}

func TestInteractiveApprovalYes(t *testing.T) {
	input := bytes.NewBufferString("y\n")
	output := &bytes.Buffer{}
	approve := InteractiveApproval(input, output)

	ok, err := approve(tools.Action{
		Tool:    "write_file",
		Summary: "write main.go",
		Detail:  "+package main",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("should approve on 'y'")
	}
}

func TestInteractiveApprovalNo(t *testing.T) {
	input := bytes.NewBufferString("n\n")
	output := &bytes.Buffer{}
	approve := InteractiveApproval(input, output)

	ok, err := approve(tools.Action{Tool: "write_file", Summary: "write main.go"})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("should deny on 'n'")
	}
}
```

- [ ] **Step 2: Run tests — verify they fail**

- [ ] **Step 3: Implement approval.go**

```go
// internal/agent/approval.go
package agent

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"forge/internal/agent/tools"
)

// YoloApproval returns an ApprovalFunc that approves everything.
func YoloApproval() tools.ApprovalFunc {
	return func(action tools.Action) (bool, error) {
		return true, nil
	}
}

// InteractiveApproval returns an ApprovalFunc that prompts the user.
func InteractiveApproval(in io.Reader, out io.Writer) tools.ApprovalFunc {
	scanner := bufio.NewScanner(in)
	return func(action tools.Action) (bool, error) {
		fmt.Fprintf(out, "\n● %s\n", action.Summary)
		if action.Detail != "" {
			for _, line := range strings.Split(action.Detail, "\n") {
				fmt.Fprintf(out, "  %s\n", line)
			}
		}

		prompt := "apply? [y/n] "
		if action.Tool == "run_command" {
			prompt = "run? [y/n] "
		}
		fmt.Fprint(out, prompt)

		if !scanner.Scan() {
			return false, nil
		}
		answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
		return answer == "y" || answer == "yes", nil
	}
}
```

- [ ] **Step 4: Run tests — verify they pass**

Run: `go test ./internal/agent/ -run "TestYolo|TestInteractive" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/approval.go internal/agent/approval_test.go
git commit -m "feat(agent): add interactive and yolo approval functions"
```

---

### Task 12: Terminal Renderer

**Files:**
- Create: `internal/agent/render.go`
- Create: `internal/agent/render_test.go`

- [ ] **Step 1: Write tests**

```go
// internal/agent/render_test.go
package agent

import (
	"bytes"
	"strings"
	"testing"
)

func TestRendererAgentText(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf, 80, true)
	r.AgentText("hello world")
	if !strings.Contains(buf.String(), "hello world") {
		t.Errorf("output = %q", buf.String())
	}
}

func TestRendererToolCall(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf, 80, true)
	r.ToolCall("read_file", "reading main.go")
	out := buf.String()
	if !strings.Contains(out, "read_file") {
		t.Errorf("missing tool name in: %q", out)
	}
}

func TestRendererDiff(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf, 80, true)
	r.Diff("main.go", "-old line\n+new line\n")
	out := buf.String()
	if !strings.Contains(out, "old line") || !strings.Contains(out, "new line") {
		t.Errorf("diff not rendered: %q", out)
	}
}

func TestRendererNoColor(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf, 80, false)
	r.AgentText("plain text")
	out := buf.String()
	if strings.Contains(out, "\033[") {
		t.Error("should not contain ANSI codes when colors disabled")
	}
}
```

- [ ] **Step 2: Run tests — verify they fail**

- [ ] **Step 3: Implement render.go**

```go
// internal/agent/render.go
package agent

import (
	"fmt"
	"io"
	"strings"
)

const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiDim    = "\033[2m"
	ansiRed    = "\033[31m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiBright = "\033[97m"
)

type Renderer struct {
	out    io.Writer
	width  int
	colors bool
}

func NewRenderer(out io.Writer, width int, colors bool) *Renderer {
	return &Renderer{out: out, width: width, colors: colors}
}

func (r *Renderer) AgentText(text string) {
	if r.colors {
		fmt.Fprint(r.out, ansiBright)
	}
	fmt.Fprint(r.out, text)
	if r.colors {
		fmt.Fprint(r.out, ansiReset)
	}
}

func (r *Renderer) ToolCall(name, summary string) {
	if r.colors {
		fmt.Fprintf(r.out, "\n%s● %s%s %s%s\n", ansiDim, ansiYellow, name, summary, ansiReset)
	} else {
		fmt.Fprintf(r.out, "\n● %s %s\n", name, summary)
	}
}

func (r *Renderer) ToolResult(name, output string) {
	lines := strings.Split(output, "\n")
	if len(lines) > 10 {
		output = strings.Join(lines[:10], "\n") + fmt.Sprintf("\n... (%d more lines)", len(lines)-10)
	}
	if r.colors {
		fmt.Fprintf(r.out, "%s  %s%s\n", ansiDim, output, ansiReset)
	} else {
		fmt.Fprintf(r.out, "  %s\n", output)
	}
}

func (r *Renderer) Diff(path, diff string) {
	for _, line := range strings.Split(diff, "\n") {
		if r.colors {
			switch {
			case strings.HasPrefix(line, "+"):
				fmt.Fprintf(r.out, "  %s%s%s\n", ansiGreen, line, ansiReset)
			case strings.HasPrefix(line, "-"):
				fmt.Fprintf(r.out, "  %s%s%s\n", ansiRed, line, ansiReset)
			default:
				fmt.Fprintf(r.out, "  %s%s%s\n", ansiDim, line, ansiReset)
			}
		} else {
			fmt.Fprintf(r.out, "  %s\n", line)
		}
	}
}

func (r *Renderer) Error(msg string) {
	if r.colors {
		fmt.Fprintf(r.out, "%s✗ %s%s\n", ansiRed, msg, ansiReset)
	} else {
		fmt.Fprintf(r.out, "error: %s\n", msg)
	}
}

func (r *Renderer) Done(summary string) {
	if r.colors {
		fmt.Fprintf(r.out, "\n%s✓%s %s\n", ansiGreen, ansiReset, summary)
	} else {
		fmt.Fprintf(r.out, "\ndone: %s\n", summary)
	}
}

func (r *Renderer) Prompt() {
	if r.colors {
		fmt.Fprintf(r.out, "\n%sforge>%s ", ansiGreen, ansiReset)
	} else {
		fmt.Fprint(r.out, "\nforge> ")
	}
}
```

- [ ] **Step 4: Run tests — verify they pass**

Run: `go test ./internal/agent/ -run TestRenderer -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/render.go internal/agent/render_test.go
git commit -m "feat(agent): add terminal renderer with ANSI color support"
```

---

### Task 13: System Prompt Builder

**Files:**
- Create: `internal/agent/system.go`
- Create: `internal/agent/system_test.go`

- [ ] **Step 1: Write tests**

```go
// internal/agent/system_test.go
package agent

import (
	"strings"
	"testing"

	"forge/internal/agent/tools"
)

func TestBuildSystemPrompt(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{Name: "read_file", Description: "Read a file"})

	prompt := BuildSystemPrompt("/home/user/project", reg)

	if !strings.Contains(prompt, "/home/user/project") {
		t.Error("missing workDir")
	}
	if !strings.Contains(prompt, "read_file") {
		t.Error("missing tool description")
	}
	if !strings.Contains(prompt, "edit_file") {
		t.Error("missing edit_file guideline")
	}
	if !strings.Contains(prompt, "tool_call") {
		t.Error("missing tool call format instructions")
	}
}
```

- [ ] **Step 2: Run tests — verify they fail**

- [ ] **Step 3: Implement system.go**

```go
// internal/agent/system.go
package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"forge/internal/agent/tools"
)

func BuildSystemPrompt(workDir string, registry *tools.Registry) string {
	var sb strings.Builder
	sb.WriteString("You are forge, a coding agent. You work in the user's project directory.\n\n")
	sb.WriteString(fmt.Sprintf("Working directory: %s\n", workDir))

	// Detect project info
	info := detectProject(workDir)
	if info != "" {
		sb.WriteString(info + "\n")
	}

	sb.WriteString("\n")
	sb.WriteString(registry.Describe())
	sb.WriteString("\nGuidelines:\n")
	sb.WriteString("- Read files before editing them. Understand what you're changing.\n")
	sb.WriteString("- Use edit_file for surgical changes to existing files. Use write_file only for new files or complete rewrites.\n")
	sb.WriteString("- After making changes, run relevant tests or build commands to verify.\n")
	sb.WriteString("- Explain what you're doing and why before making changes.\n")
	sb.WriteString("- If something fails, read the error, diagnose, and fix. Don't repeat the same failing approach.\n")
	sb.WriteString("- Ask the user for clarification if the request is ambiguous.\n")

	return sb.String()
}

func detectProject(workDir string) string {
	indicators := map[string]string{
		"go.mod":         "Go",
		"package.json":   "JavaScript/TypeScript",
		"Cargo.toml":     "Rust",
		"pyproject.toml": "Python",
		"requirements.txt": "Python",
		"Makefile":       "Make",
		"CMakeLists.txt": "C/C++",
	}

	var detected []string
	for file, lang := range indicators {
		if _, err := os.Stat(filepath.Join(workDir, file)); err == nil {
			detected = append(detected, lang)
		}
	}

	fileCount := 0
	filepath.WalkDir(workDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() && (name == ".git" || name == "node_modules" || name == "vendor" || name == "__pycache__") {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			fileCount++
		}
		if fileCount > 1000 {
			return filepath.SkipAll
		}
		return nil
	})

	parts := []string{fmt.Sprintf("Files: ~%d", fileCount)}
	if len(detected) > 0 {
		parts = append(parts, fmt.Sprintf("Languages: %s", strings.Join(detected, ", ")))
	}
	return strings.Join(parts, "  ")
}
```

- [ ] **Step 4: Run tests — verify they pass**

Run: `go test ./internal/agent/ -run TestBuildSystem -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/system.go internal/agent/system_test.go
git commit -m "feat(agent): add system prompt builder with project detection"
```

---

### Task 14: Agent Loop

**Files:**
- Create: `internal/agent/agent.go`
- Create: `internal/agent/agent_test.go`

- [ ] **Step 1: Write agent loop test**

This is the central integration test. Uses a mock driver that returns predefined responses.

```go
// internal/agent/agent_test.go
package agent

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"forge/internal/agent/tools"
	"forge/internal/llm"
)

// mockDriver returns predefined responses in sequence.
type mockDriver struct {
	responses []string
	callIdx   int
}

func (d *mockDriver) Name() string { return "mock" }

func (d *mockDriver) Stream(ctx context.Context, messages []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	if d.callIdx >= len(d.responses) {
		out <- llm.Token{Text: "done"}
		return nil
	}
	resp := d.responses[d.callIdx]
	d.callIdx++
	out <- llm.Token{Text: resp}
	return nil
}

func TestAgentRunNoTools(t *testing.T) {
	driver := &mockDriver{responses: []string{"Hello! I can help with that."}}
	reg := tools.NewRegistry()
	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)

	agent := NewAgent(driver, reg, YoloApproval(), "/tmp", 10, renderer)
	err := agent.Run(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Hello") {
		t.Errorf("output = %q", output.String())
	}
}

func TestAgentRunWithToolCall(t *testing.T) {
	dir := t.TempDir()
	driver := &mockDriver{responses: []string{
		"Let me read the file.\n\n<tool_call>\n{\"name\": \"list_dir\", \"args\": {}}\n</tool_call>",
		"I see the directory listing. All done.",
	}}

	reg := tools.NewRegistry()
	reg.Register(tools.NewListDir(dir, nil))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)

	agent := NewAgent(driver, reg, YoloApproval(), dir, 10, renderer)
	err := agent.Run(context.Background(), "list files")
	if err != nil {
		t.Fatal(err)
	}
}

func TestAgentMaxTurns(t *testing.T) {
	// Driver always returns a tool call — should hit max turns
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"list_dir\", \"args\": {}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"list_dir\", \"args\": {}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"list_dir\", \"args\": {}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"list_dir\", \"args\": {}}\n</tool_call>",
	}}
	reg := tools.NewRegistry()
	reg.Register(tools.NewListDir(t.TempDir(), nil))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)

	agent := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 3, renderer)
	err := agent.Run(context.Background(), "loop forever")
	// Should not hang — maxTurns should stop it
	if err != nil && !strings.Contains(err.Error(), "max turns") {
		t.Fatalf("unexpected error: %v", err)
	}
}
```

- [ ] **Step 2: Run tests — verify they fail**

- [ ] **Step 3: Implement agent.go**

```go
// internal/agent/agent.go
package agent

import (
	"context"
	"fmt"
	"strings"

	"forge/internal/agent/tools"
	"forge/internal/llm"
)

type Agent struct {
	driver   llm.Driver
	tools    *tools.Registry
	approve  tools.ApprovalFunc
	history  []llm.Message
	system   string
	workDir  string
	maxTurns int
	renderer *Renderer
}

func NewAgent(driver llm.Driver, toolReg *tools.Registry, approve tools.ApprovalFunc, workDir string, maxTurns int, renderer *Renderer) *Agent {
	return &Agent{
		driver:   driver,
		tools:    toolReg,
		approve:  approve,
		workDir:  workDir,
		maxTurns: maxTurns,
		renderer: renderer,
		system:   BuildSystemPrompt(workDir, toolReg),
	}
}

func (a *Agent) Run(ctx context.Context, userMessage string) error {
	a.history = append(a.history, llm.Message{Role: llm.RoleUser, Content: userMessage})

	for turn := 0; turn < a.maxTurns; turn++ {
		messages := make([]llm.Message, 0, len(a.history)+1)
		messages = append(messages, llm.Message{Role: llm.RoleSystem, Content: a.system})
		messages = append(messages, a.history...)

		// Stream response
		out := make(chan llm.Token, 64)
		errCh := make(chan error, 1)
		go func() {
			errCh <- a.driver.Stream(ctx, messages, out)
		}()

		var sb strings.Builder
		for tok := range out {
			sb.WriteString(tok.Text)
		}
		if err := <-errCh; err != nil {
			a.renderer.Error(err.Error())
			return err
		}

		response := sb.String()

		// Parse tool calls
		calls, text := ParseToolCalls(response)

		// Display reasoning text
		text = strings.TrimSpace(text)
		if text != "" {
			a.renderer.AgentText(text + "\n")
		}

		// No tool calls — final answer
		if len(calls) == 0 {
			a.history = append(a.history, llm.Message{Role: llm.RoleAssistant, Content: response})
			return nil
		}

		// Execute tool calls
		var results []string
		for _, call := range calls {
			tool, ok := a.tools.Get(call.Name)
			if !ok {
				result := fmt.Sprintf("error: unknown tool %q", call.Name)
				a.renderer.Error(result)
				results = append(results, fmt.Sprintf("[%s] %s", call.Name, result))
				continue
			}

			a.renderer.ToolCall(call.Name, formatCallSummary(call))

			result, err := tool.Execute(ctx, call.Args)
			if err != nil {
				result = fmt.Sprintf("error: %v", err)
				a.renderer.Error(result)
			} else if !tool.AutoApprove {
				// Approval was handled inside the tool's Execute
				a.renderer.ToolResult(call.Name, truncateResult(result))
			} else {
				a.renderer.ToolResult(call.Name, truncateResult(result))
			}

			results = append(results, fmt.Sprintf("[%s] %s", call.Name, result))
		}

		// Append to history
		a.history = append(a.history, llm.Message{Role: llm.RoleAssistant, Content: response})

		toolResultContent := strings.Join(results, "\n\n")
		if len(toolResultContent) > 30*1024 {
			toolResultContent = toolResultContent[:30*1024] + "\n... (truncated)"
		}
		a.history = append(a.history, llm.Message{Role: llm.RoleUser, Content: "Tool results:\n\n" + toolResultContent})
	}

	return fmt.Errorf("max turns (%d) exceeded", a.maxTurns)
}

func formatCallSummary(call ToolCall) string {
	if path, ok := call.Args["path"].(string); ok {
		return path
	}
	if cmd, ok := call.Args["command"].(string); ok {
		if len(cmd) > 60 {
			return cmd[:60] + "..."
		}
		return cmd
	}
	if pattern, ok := call.Args["pattern"].(string); ok {
		return pattern
	}
	return ""
}

func truncateResult(result string) string {
	lines := strings.Split(result, "\n")
	if len(lines) > 20 {
		return strings.Join(lines[:20], "\n") + fmt.Sprintf("\n... (%d more lines)", len(lines)-20)
	}
	return result
}
```

- [ ] **Step 4: Run tests — verify they pass**

Run: `go test ./internal/agent/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/agent.go internal/agent/agent_test.go
git commit -m "feat(agent): add core agent loop with tool execution"
```

---

### Task 14b: Context Management / History Compression

**Files:**
- Modify: `internal/agent/agent.go`
- Modify: `internal/agent/agent_test.go`

- [ ] **Step 1: Write compression tests**

Add to `agent_test.go`:

```go
func TestCompressHistory(t *testing.T) {
	a := &Agent{maxTurns: 50}

	// Build a history with large tool results
	for i := 0; i < 20; i++ {
		a.history = append(a.history, llm.Message{
			Role:    llm.RoleAssistant,
			Content: fmt.Sprintf("<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"file%d.go\"}}\n</tool_call>", i),
		})
		a.history = append(a.history, llm.Message{
			Role:    llm.RoleUser,
			Content: "Tool results:\n\n[read_file] " + strings.Repeat("x", 5000),
		})
	}

	before := len(a.history)
	a.compressHistory(50000) // compress when over 50K chars

	// Recent messages should be preserved, older tool results summarized
	if len(a.history) != before {
		// Message count stays the same, but content is shorter
	}

	// Check that old tool results are summarized
	totalLen := 0
	for _, m := range a.history {
		totalLen += len(m.Content)
	}
	if totalLen > 55000 {
		t.Errorf("compressed history too large: %d chars", totalLen)
	}
}

func TestEstimateTokens(t *testing.T) {
	if got := estimateTokens("hello world"); got < 2 || got > 4 {
		t.Errorf("estimateTokens('hello world') = %d", got)
	}
}
```

- [ ] **Step 2: Run tests — verify they fail**

Run: `go test ./internal/agent/ -run "TestCompress|TestEstimate" -v`

- [ ] **Step 3: Add compression to agent.go**

Add these methods:

```go
// estimateTokens returns a rough token count (~4 chars per token).
func estimateTokens(text string) int {
	return (len(text) + 3) / 4
}

// compressHistory replaces old tool results with one-line summaries
// when total content exceeds the threshold.
func (a *Agent) compressHistory(charThreshold int) {
	total := 0
	for _, m := range a.history {
		total += len(m.Content)
	}
	if total <= charThreshold {
		return
	}

	// Keep the most recent 4 messages intact
	preserve := 4
	if preserve > len(a.history) {
		preserve = len(a.history)
	}
	cutoff := len(a.history) - preserve

	for i := 0; i < cutoff; i++ {
		m := &a.history[i]
		if m.Role == llm.RoleUser && strings.HasPrefix(m.Content, "Tool results:") {
			// Summarize: keep first line of each tool result
			lines := strings.Split(m.Content, "\n")
			var summary []string
			for _, line := range lines {
				if strings.HasPrefix(line, "[") {
					bracket := strings.Index(line, "]")
					if bracket > 0 && len(line) > bracket+2 {
						toolName := line[1:bracket]
						summary = append(summary, fmt.Sprintf("[%s: result truncated]", toolName))
					}
				}
			}
			if len(summary) > 0 {
				m.Content = "Tool results (summarized):\n" + strings.Join(summary, "\n")
			}
		}
	}
}
```

Then in the `Run()` method, add compression call before building messages (step 2):

```go
// Compress history if growing too large (~100K chars ≈ ~25K tokens)
a.compressHistory(100000)
```

- [ ] **Step 4: Run tests — verify they pass**

Run: `go test ./internal/agent/ -run "TestCompress|TestEstimate" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/agent.go internal/agent/agent_test.go
git commit -m "feat(agent): add history compression to prevent context overflow"
```

---

### Task 15: CLI Wiring — `forge chat` Subcommand

**Files:**
- Modify: `cmd/forge/main.go`

- [ ] **Step 1: Add "chat" case to subcommand router**

In the `switch os.Args[1]` block in `main()`, add:

```go
case "chat":
    runChat()
    return
```

- [ ] **Step 2: Implement `runChat()` function**

Add after the existing helper functions in main.go:

```go
func runChat() {
	fs := flag.NewFlagSet("chat", flag.ExitOnError)
	yolo := fs.Bool("yolo", false, "skip all approval prompts")
	model := fs.String("model", "", "model override")
	workDir := fs.String("C", "", "working directory (default: cwd)")
	live := fs.Bool("live", false, "use split-pane live view")
	fs.Parse(os.Args[2:])

	if *live {
		fmt.Fprintln(os.Stderr, "live mode not yet implemented")
		os.Exit(1)
	}

	cfgPath := filepath.Join(os.Getenv("HOME"), ".config", "forge", "config.toml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	if os.Getenv("FORGE_CHAT_YOLO") == "1" {
		*yolo = true
	}
	if cfg.Chat.Yolo {
		*yolo = true
	}

	// Resolve working directory
	wd := "."
	if *workDir != "" {
		wd = *workDir
	}
	absWd, err := filepath.Abs(wd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error resolving working directory: %v\n", err)
		os.Exit(1)
	}

	// Resolve model
	chatModel := cfg.ChatModel()
	if *model != "" {
		chatModel = *model
	}

	// Load auth tokens
	tokens, err := auth.Load()
	if err != nil {
		tokens = &auth.Tokens{}
	}

	// Create driver
	var driver llm.Driver
	if isAnthropicModel(chatModel) && cfg.AnthropicKey() != "" {
		driver = drivers.NewClaude(cfg.AnthropicKey(), chatModel)
	} else if isOpenAIModel(chatModel) && cfg.OpenAIKey() != "" {
		driver = drivers.NewOpenAI(cfg.OpenAIKey(), chatModel)
	} else if p := findCompatProvider(buildCompatProviders(cfg), chatModel); p != nil {
		driver = drivers.NewOpenAICompatible(p.keyFn(), p.baseURL, chatModel)
	} else if isCopilotModel(chatModel) && tokens.CopilotToken != "" {
		driver = drivers.NewCopilot(tokens.CopilotToken, chatModel, copilotAPIModel(chatModel))
	}

	if driver == nil {
		fmt.Fprintf(os.Stderr, "error: no API key found for model %q\n", chatModel)
		os.Exit(1)
	}

	driver = wrapRetry(driver)

	// Build tool registry
	var approve tools.ApprovalFunc
	if *yolo {
		approve = agent.YoloApproval()
	} else {
		approve = agent.InteractiveApproval(os.Stdin, os.Stdout)
	}

	reg := tools.NewRegistry()
	reg.Register(tools.NewReadFile(absWd))
	reg.Register(tools.NewWriteFile(absWd, approve))
	reg.Register(tools.NewEditFile(absWd, approve))
	reg.Register(tools.NewListDir(absWd, cfg.Chat.IgnoreDirs))
	reg.Register(tools.NewSearch(absWd))
	// For destructive commands in yolo mode, always use interactive prompt
	interactiveApprove := agent.InteractiveApproval(os.Stdin, os.Stdout)
	reg.Register(tools.NewRunCommand(absWd, cfg.Chat.CommandTimeout, approve, interactiveApprove))

	renderer := agent.NewRenderer(os.Stdout, 80, true)
	a := agent.NewAgent(driver, reg, approve, absWd, cfg.Chat.MaxTurns, renderer)

	fmt.Printf("forge chat (%s) — %s\n", chatModel, absWd)
	fmt.Println("type your request, or \"exit\" to quit\n")

	// Signal handling: Ctrl+C cancels current operation, second Ctrl+C exits
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		count := 0
		for range sigCh {
			count++
			if count >= 2 {
				fmt.Println("\ninterrupted")
				os.Exit(0)
			}
			cancel()
			// Reset context for next turn
			ctx, cancel = context.WithCancel(context.Background())
		}
	}()

	scanner := bufio.NewScanner(os.Stdin)

	for {
		renderer.Prompt()
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == "exit" || input == "quit" {
			break
		}

		if err := a.Run(ctx, input); err != nil {
			renderer.Error(err.Error())
		}
	}
	fmt.Println()
}
```

- [ ] **Step 3: Add imports**

Add these imports to main.go (if not already present):

```go
"os/signal"

"forge/internal/agent"
"forge/internal/agent/tools"
```

- [ ] **Step 4: Update printHelp()**

Add chat command to help text:

```
  forge chat [flags]              Start interactive agent session
```

And add chat flags section:

```
Chat flags:
  --yolo            Skip all approval prompts
  --live            Use split-pane live view (not yet implemented)
  --model MODEL     Override chat model
  -C PATH           Set working directory (default: cwd)
```

- [ ] **Step 5: Build and verify**

Run: `go build ./cmd/forge/`
Expected: clean build

Run: `./forge chat --help` (should show flag usage via flag.ExitOnError)

- [ ] **Step 6: Commit**

```bash
git add cmd/forge/main.go
git commit -m "feat: wire forge chat subcommand with agent loop"
```

---

### Task 16: Add Event Types for Live Mode (deferred implementation)

**Files:**
- Modify: `internal/llm/types.go`

- [ ] **Step 1: Add new EventKind constants**

```go
EventToolCall   EventKind = "tool_call"
EventToolResult EventKind = "tool_result"
```

These are needed for the `--live` flag (future task) but define them now so the types are complete.

- [ ] **Step 2: Verify build**

Run: `go vet ./...`
Expected: clean

- [ ] **Step 3: Commit**

```bash
git add internal/llm/types.go
git commit -m "feat(llm): add tool_call and tool_result event kinds"
```

---

### Task 17: Full Integration Test

**Files:**
- Create: `internal/agent/integration_test.go`

- [ ] **Step 1: Write end-to-end test**

```go
// internal/agent/integration_test.go
package agent

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forge/internal/agent/tools"
	"forge/internal/llm"
)

func TestAgentEndToEnd(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)

	// Simulate: agent reads a file, then edits it
	driver := &mockDriver{responses: []string{
		// Turn 1: read the file
		"I'll read main.go first.\n\n<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"main.go\"}}\n</tool_call>",
		// Turn 2: edit the file
		"<tool_call>\n{\"name\": \"edit_file\", \"args\": {\"path\": \"main.go\", \"old_text\": \"func main() {}\", \"new_text\": \"func main() {\\n\\tfmt.Println(\\\"hello\\\")\\n}\"}}\n</tool_call>",
		// Turn 3: done
		"I've added a print statement to main.go.",
	}}

	reg := tools.NewRegistry()
	approve := func(a tools.Action) (bool, error) { return true, nil }
	reg.Register(tools.NewReadFile(dir))
	reg.Register(tools.NewEditFile(dir, approve))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, approve, dir, 10, renderer)

	err := a.Run(context.Background(), "add a hello world print to main.go")
	if err != nil {
		t.Fatal(err)
	}

	// Verify file was edited
	data, _ := os.ReadFile(filepath.Join(dir, "main.go"))
	if !strings.Contains(string(data), "Println") {
		t.Error("file should contain Println after edit")
	}

	// Verify output contains agent reasoning
	out := output.String()
	if !strings.Contains(out, "read") {
		t.Error("output should mention reading")
	}
}

func TestAgentCodeFenceExample(t *testing.T) {
	// Agent explains tool usage with a code fence — should not execute
	driver := &mockDriver{responses: []string{
		"Here's how you'd use the tool:\n```\n<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"secret.txt\"}}\n</tool_call>\n```\nBut I won't actually run it.",
	}}

	reg := tools.NewRegistry()
	readCalled := false
	reg.Register(tools.Tool{
		Name: "read_file",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			readCalled = true
			return "should not reach here", nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer)
	a.Run(context.Background(), "explain how to use read_file")

	if readCalled {
		t.Error("tool inside code fence should NOT be executed")
	}
}
```

- [ ] **Step 2: Run all agent tests**

Run: `go test ./internal/agent/... -v`
Expected: PASS

- [ ] **Step 3: Run full test suite**

Run: `go vet ./... && go test ./internal/... -v`
Expected: clean vet, all tests pass

- [ ] **Step 4: Commit**

```bash
git add internal/agent/integration_test.go
git commit -m "test(agent): add end-to-end integration tests"
```

---

### Task 18: Final Build Verification

- [ ] **Step 1: Full build**

Run: `go build -o /tmp/forge-test ./cmd/forge/`
Expected: clean build

- [ ] **Step 2: Verify help includes chat**

Run: `/tmp/forge-test --help`
Expected: output includes `forge chat` in the command list

- [ ] **Step 3: Verify existing tests still pass**

Run: `go test ./... 2>&1 | tail -20`
Expected: all packages pass

- [ ] **Step 4: Clean up temp build**

Run: `rm /tmp/forge-test`
