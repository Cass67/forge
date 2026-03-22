package agent

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forge/internal/agent/tools"
)

func TestAgentEndToEnd(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)

	driver := &mockDriver{responses: []string{
		"I'll read main.go first.\n\n<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"main.go\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"edit_file\", \"args\": {\"path\": \"main.go\", \"old_text\": \"func main() {}\", \"new_text\": \"func main() {\\n\\tfmt.Println(\\\"hello\\\")\\n}\"}}\n</tool_call>",
		"I've added a print statement to main.go.",
	}}

	reg := tools.NewRegistry()
	approve := func(a tools.Action) (bool, error) { return true, nil }
	reg.Register(tools.NewReadFile(dir))
	reg.Register(tools.NewEditFile(dir, approve))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, approve, dir, 10, renderer, nil)

	err := a.Run(context.Background(), "add a hello world print to main.go")
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "main.go"))
	if !strings.Contains(string(data), "Println") {
		t.Error("file should contain Println after edit")
	}

	out := output.String()
	if !strings.Contains(out, "read") {
		t.Error("output should mention reading")
	}
}

func TestAgentCodeFenceExample(t *testing.T) {
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
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil)
	a.Run(context.Background(), "explain how to use read_file")

	if readCalled {
		t.Error("tool inside code fence should NOT be executed")
	}
}
