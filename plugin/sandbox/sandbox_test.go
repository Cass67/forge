package sandbox

import (
	"context"
	"forge/internal/plugin"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	// Reset state before each test
	resetState()
	m.Run()
}

func TestPluginRegistration(t *testing.T) {
	p := Plugin{}
	if p.Name() != "sandbox" {
		t.Errorf("name = %q, want %q", p.Name(), "sandbox")
	}
	if p.Version() == "" {
		t.Error("version empty")
	}

	tools := p.Tools()
	if len(tools) != 3 {
		t.Fatalf("got %d tools, want 3", len(tools))
	}
	names := []string{"sandbox_run", "sandbox_status", "sandbox_stop"}
	for i, n := range names {
		if tools[i].Name != n {
			t.Errorf("tool %d = %q, want %q", i, tools[i].Name, n)
		}
		if tools[i].Execute == nil {
			t.Errorf("tool %s has nil Execute", n)
		}
	}

	skills := p.Skills()
	if len(skills) != 1 || skills[0].Name != "docker-sandbox" {
		t.Errorf("skills = %v", skills)
	}

	cmds := p.Commands()
	if len(cmds) != 1 || cmds[0].Name != "/sandbox" {
		t.Errorf("commands = %v", cmds)
	}

	agents := p.Agents()
	if len(agents) != 1 || agents[0].Name != "sandbox-dev" {
		t.Errorf("agents = %v", agents)
	}
}

func TestSandboxRunTool(t *testing.T) {
	p := Plugin{}
	var runTool plugin.Tool
	for _, tool := range p.Tools() {
		if tool.Name == "sandbox_run" {
			runTool = tool
			break
		}
	}
	if runTool.Name == "" {
		t.Fatal("sandbox_run tool not found")
	}

	output, err := runTool.Execute(context.Background(), map[string]any{
		"command": "echo hello",
		"image":   "alpine:3.20",
		"shell":   "sh",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, "hello") {
		t.Errorf("expected output to contain 'hello', got: %s", output)
	}
}

func TestSandboxStatusTool(t *testing.T) {
	resetState()
	p := Plugin{}
	var statusTool plugin.Tool
	for _, tool := range p.Tools() {
		if tool.Name == "sandbox_status" {
			statusTool = tool
			break
		}
	}
	if statusTool.Name == "" {
		t.Fatal("sandbox_status tool not found")
	}

	output, err := statusTool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, "No active sandboxes") {
		t.Errorf("expected 'No active sandboxes', got: %s", output)
	}
}

func TestSandboxStopTool(t *testing.T) {
	p := Plugin{}
	var stopTool plugin.Tool
	for _, tool := range p.Tools() {
		if tool.Name == "sandbox_stop" {
			stopTool = tool
			break
		}
	}
	if stopTool.Name == "" {
		t.Fatal("sandbox_stop tool not found")
	}

	output, err := stopTool.Execute(context.Background(), map[string]any{
		"container_id": "fake-container",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, "fake-container") {
		t.Errorf("expected output to contain 'fake-container', got: %s", output)
	}
}

func TestSlashCommand(t *testing.T) {
	resetState()
	p := Plugin{}
	var cmd plugin.Command
	for _, c := range p.Commands() {
		if c.Name == "/sandbox" || c.Name == "sandbox" {
			cmd = c
			break
		}
	}
	if cmd.Name == "" {
		t.Fatal("sandbox command not found")
	}

	output, err := cmd.Handler(context.Background(), "echo slash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, "slash") {
		t.Errorf("expected output to contain 'slash', got: %s", output)
	}
}

func TestContainerName(t *testing.T) {
	tests := []struct {
		dir string
	}{
		{"/Users/cass/git/forge"},
		{"/home/user/project"},
		{"/tmp/test"},
	}
	for _, tt := range tests {
		name := containerName(tt.dir)
		if !strings.HasPrefix(name, "forge-") {
			t.Errorf("containerName(%q) = %q, want prefix 'forge-'", tt.dir, name)
		}
		if len(name) > 64 {
			t.Errorf("containerName(%q) = %q, length %d > 64", tt.dir, name, len(name))
		}
		if strings.Contains(name, "--") {
			t.Errorf("containerName(%q) = %q, contains consecutive dashes", tt.dir, name)
		}
	}
}
