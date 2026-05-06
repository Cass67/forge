package plugins

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	agenttools "forge/internal/agent/tools"
	"forge/internal/config"
	"forge/internal/hooks"
)

func TestManagerRegistersPluginToolsAndHooks(t *testing.T) {
	m := newTestManager(t, config.PluginConfig{
		ID:               "demo",
		Command:          pluginHelperCommand(),
		Env:              map[string]string{"FORGE_PLUGIN_HELPER": "1"},
		AutoApproveTools: []string{"echo"},
		StartupTimeoutMS: 1000,
		RequestTimeoutMS: 1000,
	})
	defer func() { _ = m.Close() }()

	reg := agenttools.NewRegistry()
	m.RegisterTools(reg, func(action agenttools.Action) (bool, error) {
		t.Fatalf("auto-approved plugin tool should not prompt, got %#v", action)
		return false, nil
	})
	tool, ok := reg.Get("plugin__demo__echo")
	if !ok {
		t.Fatal("expected plugin echo tool to be registered")
	}
	if !tool.AutoApprove {
		t.Fatal("expected configured plugin tool to be auto-approved")
	}
	result, err := tool.Execute(context.Background(), map[string]any{"message": "hello"})
	if err != nil {
		t.Fatalf("plugin tool call: %v", err)
	}
	if result != "echo:hello" {
		t.Fatalf("plugin tool result = %q", result)
	}

	hookReg := hooks.NewRegistry()
	m.RegisterHooks(hookReg)
	promptOutput := hookReg.Dispatch(context.Background(), hooks.Event{Point: hooks.PointPromptContext})
	if len(promptOutput.Overlays) != 1 || promptOutput.Overlays[0].Content != "plugin prompt" {
		t.Fatalf("prompt overlays = %#v", promptOutput.Overlays)
	}
	if promptOutput.Overlays[0].Provenance != "plugin:demo" {
		t.Fatalf("overlay provenance = %q", promptOutput.Overlays[0].Provenance)
	}

	type toolPayload struct {
		ToolName string
		Args     map[string]any
		IsError  bool
		Error    string
	}
	beforeOutput := hookReg.Dispatch(context.Background(), hooks.Event{
		Point:     hooks.PointBeforeTool,
		Transient: toolPayload{ToolName: "blocked", Args: map[string]any{"path": "README.md"}},
	})
	if beforeOutput.Block == nil || beforeOutput.Block.Message != "blocked by plugin" {
		t.Fatalf("before block = %#v", beforeOutput.Block)
	}
	afterOutput := hookReg.Dispatch(context.Background(), hooks.Event{
		Point:     hooks.PointAfterTool,
		Transient: toolPayload{ToolName: "echo"},
	})
	if afterOutput.Note == nil || afterOutput.Note.Message != "after echo ok" {
		t.Fatalf("after note = %#v", afterOutput.Note)
	}
}

func TestManagerPromptsForPluginToolApproval(t *testing.T) {
	m := newTestManager(t, config.PluginConfig{
		ID:               "demo",
		Command:          pluginHelperCommand(),
		Env:              map[string]string{"FORGE_PLUGIN_HELPER": "1"},
		StartupTimeoutMS: 1000,
		RequestTimeoutMS: 1000,
	})
	defer func() { _ = m.Close() }()

	reg := agenttools.NewRegistry()
	var actions []agenttools.Action
	m.RegisterTools(reg, func(action agenttools.Action) (bool, error) {
		actions = append(actions, action)
		return false, nil
	})
	tool, ok := reg.Get("plugin__demo__echo")
	if !ok {
		t.Fatal("expected plugin echo tool to be registered")
	}
	if tool.AutoApprove {
		t.Fatal("tool without auto_approve_tools entry should not be auto-approved")
	}
	result, err := tool.Execute(context.Background(), map[string]any{"message": "hello"})
	if err != nil {
		t.Fatalf("denied plugin tool should return a denial message, got error: %v", err)
	}
	if result != "plugin__demo__echo denied by user" {
		t.Fatalf("denied result = %q", result)
	}
	if len(actions) != 1 {
		t.Fatalf("approval prompts = %d, want 1", len(actions))
	}
	if actions[0].Tool != "plugin__demo__echo" || !strings.Contains(actions[0].Detail, `"message":"hello"`) {
		t.Fatalf("approval action = %#v", actions[0])
	}
}

func TestManagerExecutesApprovedPluginTool(t *testing.T) {
	m := newTestManager(t, config.PluginConfig{
		ID:               "demo",
		Command:          pluginHelperCommand(),
		Env:              map[string]string{"FORGE_PLUGIN_HELPER": "1"},
		StartupTimeoutMS: 1000,
		RequestTimeoutMS: 1000,
	})
	defer func() { _ = m.Close() }()

	reg := agenttools.NewRegistry()
	m.RegisterTools(reg, func(action agenttools.Action) (bool, error) {
		if action.Tool != "plugin__demo__echo" {
			t.Fatalf("approval action tool = %q", action.Tool)
		}
		return true, nil
	})
	tool, ok := reg.Get("plugin__demo__echo")
	if !ok {
		t.Fatal("expected plugin echo tool to be registered")
	}
	result, err := tool.Execute(context.Background(), map[string]any{"message": "hello"})
	if err != nil {
		t.Fatalf("approved plugin tool call: %v", err)
	}
	if result != "echo:hello" {
		t.Fatalf("plugin tool result = %q", result)
	}
}

func TestManagerDoesNotInheritParentEnvironmentByDefault(t *testing.T) {
	t.Setenv("FORGE_PLUGIN_SECRET", "hidden")
	m := newTestManager(t, config.PluginConfig{
		ID:               "demo",
		Command:          pluginHelperCommand(),
		Env:              map[string]string{"FORGE_PLUGIN_HELPER": "1", "VISIBLE_TO_PLUGIN": "ok"},
		StartupTimeoutMS: 1000,
		RequestTimeoutMS: 1000,
	})
	defer func() { _ = m.Close() }()

	secret, err := m.CallTool(context.Background(), "demo", "env", map[string]any{"name": "FORGE_PLUGIN_SECRET"})
	if err != nil {
		t.Fatalf("plugin env call: %v", err)
	}
	if secret != "" {
		t.Fatalf("expected parent secret env to be absent, got %q", secret)
	}
	visible, err := m.CallTool(context.Background(), "demo", "env", map[string]any{"name": "VISIBLE_TO_PLUGIN"})
	if err != nil {
		t.Fatalf("plugin env call: %v", err)
	}
	if visible != "ok" {
		t.Fatalf("explicit plugin env = %q, want ok", visible)
	}
}

func newTestManager(t *testing.T, cfg config.PluginConfig) *Manager {
	t.Helper()
	m := NewManager(t.TempDir(), []config.PluginConfig{cfg})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := m.Start(ctx); err != nil {
		t.Fatalf("start plugin manager: %v", err)
	}
	if !m.HasPlugins() {
		t.Fatal("expected plugin manager to have a connected plugin")
	}
	return m
}

func pluginHelperCommand() []string {
	return []string{os.Args[0], "-test.run=TestPluginHelperProcess", "--"}
}

func TestPluginHelperProcess(t *testing.T) {
	if os.Getenv("FORGE_PLUGIN_HELPER") != "1" {
		return
	}
	servePluginHelper(os.Stdin, os.Stdout)
	os.Exit(0)
}

func servePluginHelper(in io.Reader, out io.Writer) {
	dec := json.NewDecoder(in)
	enc := json.NewEncoder(out)
	for {
		var req struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := dec.Decode(&req); err != nil {
			return
		}
		switch req.Method {
		case "initialize":
			writePluginResult(enc, req.ID, initializeResult{
				Tools: []toolDef{
					{
						Name:        "echo",
						Description: "Echo a message",
						Parameters:  []paramDef{{Name: "message", Type: "string", Required: true}},
					},
					{
						Name:        "env",
						Description: "Read an environment variable",
						Parameters:  []paramDef{{Name: "name", Type: "string", Required: true}},
					},
				},
				Hooks: []string{"prompt_context", "before_tool", "after_tool"},
			})
		case "tool_call":
			var params toolCallParams
			_ = json.Unmarshal(req.Params, &params)
			switch params.Name {
			case "echo":
				message, _ := params.Arguments["message"].(string)
				writePluginResult(enc, req.ID, toolCallResult{Content: "echo:" + message})
			case "env":
				name, _ := params.Arguments["name"].(string)
				writePluginResult(enc, req.ID, toolCallResult{Content: os.Getenv(name)})
			default:
				writePluginError(enc, req.ID, "unknown helper tool")
			}
		case "hook":
			var params hookCallParams
			_ = json.Unmarshal(req.Params, &params)
			switch params.Point {
			case "prompt_context":
				writePluginResult(enc, req.ID, hookCallResult{
					Overlays: []hookOverlay{{Key: "context", Content: "plugin prompt", Priority: "high"}},
				})
			case "before_tool":
				if params.ToolName == "blocked" {
					writePluginResult(enc, req.ID, hookCallResult{Block: &hookBlock{Message: "blocked by plugin"}})
					continue
				}
				writePluginResult(enc, req.ID, hookCallResult{})
			case "after_tool":
				status := strings.TrimSpace(params.Status)
				if status == "" {
					status = "ok"
				}
				writePluginResult(enc, req.ID, hookCallResult{Note: &hookNote{Message: "after " + params.ToolName + " " + status}})
			default:
				writePluginResult(enc, req.ID, hookCallResult{})
			}
		default:
			writePluginError(enc, req.ID, "unknown method")
		}
	}
}

func writePluginResult(enc *json.Encoder, id int64, result any) {
	_ = enc.Encode(map[string]any{"id": id, "result": result})
}

func writePluginError(enc *json.Encoder, id int64, message string) {
	_ = enc.Encode(map[string]any{"id": id, "error": responseError{Message: message}})
}
