package plugins

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"forge/internal/config"
	"forge/internal/hooks"
)

func TestOpenCodeHostCompatShim(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required for OpenCode host compat shim test")
	}
	dir := t.TempDir()
	pluginPath := filepath.Join(dir, "compat-plugin.mjs")
	if err := os.WriteFile(pluginPath, []byte(`
export default {
  server: async () => ({
    tool: {
      echo: {
        description: "Echo back input",
        args: { message: { type: "string", description: "Message to echo" } },
        execute: async (args) => {
          return { output: "echo: " + (args.message || "nothing") };
        },
      },
    },
    agents: [
      {
        name: "test_agent",
        description: "A test agent",
        system_prompt: "You are a test agent.",
        model: "test/model",
        fallbacks: ["test/fallback"],
        model_family: "claude",
        tools: ["echo"],
      },
    ],
    "tool.execute.before": async (ctx, output) => {
      // no-op for test
    },
  })
}
`), 0o600); err != nil {
		t.Fatalf("write plugin: %v", err)
	}
	hostPath := filepath.Join(dir, OpenCodeHostFileName)
	if err := WriteOpenCodeHost(hostPath); err != nil {
		t.Fatalf("write host: %v", err)
	}

	m := NewManager(dir, []config.PluginConfig{{
		ID:               "oc",
		Kind:             "opencode",
		Source:           pluginPath,
		Command:          []string{"node", hostPath, "--module", pluginPath},
		AutoApproveTools: []string{"echo"},
		StartupTimeoutMS: 1000,
		RequestTimeoutMS: 1000,
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := m.Start(ctx); err != nil {
		t.Fatalf("start opencode host: %v", err)
	}
	defer func() { _ = m.Close() }()

	tools := m.Tools()
	if len(tools) != 1 || tools[0].Name != "echo" || tools[0].Parameters[0].Name != "message" {
		t.Fatalf("tools = %#v", tools)
	}
	result, err := m.CallTool(context.Background(), "oc", "echo", map[string]any{"message": "hello"})
	if err != nil {
		t.Fatalf("call opencode tool: %v", err)
	}
	if result != "echo: hello" {
		t.Fatalf("result = %q", result)
	}

	output := m.CallHook(context.Background(), "oc", hooks.PointBeforeTool, hooks.Event{
		Point: hooks.PointBeforeTool,
		Transient: struct {
			ToolName string
			Args     map[string]any
		}{ToolName: "echo"},
	})
	if len(output) != 0 {
		t.Fatalf("unexpected hook output = %#v", output)
	}

	agents := m.AgentDefs()
	if len(agents) != 1 {
		t.Fatalf("agents = %#v", agents)
	}
	if agents[0].Name != "test_agent" || agents[0].Description != "A test agent" || agents[0].SystemPrompt != "You are a test agent." {
		t.Fatalf("agent = %#v", agents[0])
	}
	if agents[0].Model != "test/model" || len(agents[0].Fallbacks) != 1 || agents[0].Fallbacks[0] != "test/fallback" {
		t.Fatalf("agent model/fallbacks = %#v", agents[0])
	}
	if agents[0].ModelFamily != "claude" || len(agents[0].Tools) != 1 || agents[0].Tools[0] != "echo" {
		t.Fatalf("agent family/tools = %#v", agents[0])
	}
}

func TestOpenCodeHostRunsSimplePlugin(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required for OpenCode host compatibility test")
	}
	dir := t.TempDir()
	pluginPath := filepath.Join(dir, "simple-plugin.mjs")
	if err := os.WriteFile(pluginPath, []byte(`
export default {
  id: "simple",
  server: async () => ({
    tool: {
      echo: {
        description: "Echo a message through an OpenCode plugin",
        args: {
          message: { description: "Message to echo", _def: { type: "string" } },
          loud: { description: "Uppercase output", _def: { type: "boolean" } },
        },
        execute: async (args) => args.loud ? String(args.message).toUpperCase() : "echo:" + args.message,
      },
    },
    "tool.execute.before": async (input) => {
      if (input.tool === "blocked") throw new Error("blocked by OpenCode hook")
    },
    "tool.execute.after": async () => {},
  }),
}
`), 0o600); err != nil {
		t.Fatalf("write plugin: %v", err)
	}
	hostPath := filepath.Join(dir, OpenCodeHostFileName)
	if err := WriteOpenCodeHost(hostPath); err != nil {
		t.Fatalf("write host: %v", err)
	}

	m := NewManager(dir, []config.PluginConfig{{
		ID:               "oc",
		Kind:             "opencode",
		Source:           pluginPath,
		Command:          []string{"node", hostPath, "--module", pluginPath},
		AutoApproveTools: []string{"echo"},
		StartupTimeoutMS: 1000,
		RequestTimeoutMS: 1000,
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := m.Start(ctx); err != nil {
		t.Fatalf("start opencode host: %v", err)
	}
	defer func() { _ = m.Close() }()

	tools := m.Tools()
	if len(tools) != 1 || tools[0].Name != "echo" || tools[0].Parameters[0].Name != "message" {
		t.Fatalf("tools = %#v", tools)
	}
	result, err := m.CallTool(context.Background(), "oc", "echo", map[string]any{"message": "hello"})
	if err != nil {
		t.Fatalf("call opencode tool: %v", err)
	}
	if result != "echo:hello" {
		t.Fatalf("result = %q", result)
	}

	output := m.CallHook(context.Background(), "oc", hooks.PointBeforeTool, hooks.Event{
		Point: hooks.PointBeforeTool,
		Transient: struct {
			ToolName string
			Args     map[string]any
		}{ToolName: "blocked"},
	})
	if len(output) != 1 {
		t.Fatalf("hook output = %#v", output)
	}
	block, ok := output[0].(hooks.BlockResult)
	if !ok || block.Message != "blocked by OpenCode hook" {
		t.Fatalf("block = %#v", output[0])
	}
}

func TestOpenCodeHostProvidesMetadataFunction(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required for OpenCode host compatibility test")
	}
	dir := t.TempDir()
	pluginPath := filepath.Join(dir, "metadata-plugin.mjs")
	if err := os.WriteFile(pluginPath, []byte(`
export default {
  server: async () => ({
    tool: {
      task: {
        description: "Call metadata like oh-my-openagent task tools",
        args: { description: { type: "string", description: "Task title" } },
        execute: async (args, ctx) => {
          await ctx.metadata?.({ title: args.description || "task" })
          return "metadata ok"
        },
      },
    },
  }),
}
`), 0o600); err != nil {
		t.Fatalf("write plugin: %v", err)
	}
	hostPath := filepath.Join(dir, OpenCodeHostFileName)
	if err := WriteOpenCodeHost(hostPath); err != nil {
		t.Fatalf("write host: %v", err)
	}

	m := NewManager(dir, []config.PluginConfig{{
		ID:               "oc",
		Kind:             "opencode",
		Source:           pluginPath,
		Command:          []string{"node", hostPath, "--module", pluginPath},
		AutoApproveTools: []string{"task"},
		StartupTimeoutMS: 1000,
		RequestTimeoutMS: 1000,
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := m.Start(ctx); err != nil {
		t.Fatalf("start opencode host: %v", err)
	}
	defer func() { _ = m.Close() }()

	result, err := m.CallTool(context.Background(), "oc", "task", map[string]any{"description": "audit repo"})
	if err != nil {
		t.Fatalf("call opencode tool: %v", err)
	}
	if result != "metadata ok" {
		t.Fatalf("result = %q", result)
	}
}
