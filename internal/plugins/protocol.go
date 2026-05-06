package plugins

import (
	"encoding/json"
	"strings"
	"time"

	"forge/internal/agent/tools"
	"forge/internal/config"
	"forge/internal/hooks"
)

const protocolVersion = 1

const (
	defaultStartupTimeout = 3 * time.Second
	defaultRequestTimeout = 10 * time.Second
)

type requestEnvelope struct {
	ID     int64  `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type responseEnvelope struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *responseError  `json:"error,omitempty"`
}

type responseError struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message"`
}

type initializeParams struct {
	ProtocolVersion int      `json:"protocol_version"`
	PluginID        string   `json:"plugin_id"`
	CWD             string   `json:"cwd"`
	Capabilities    []string `json:"capabilities"`
	ForgeTools      []string `json:"forge_tools,omitempty"`
}

type initializeResult struct {
	Tools []toolDef `json:"tools,omitempty"`
	Hooks []string  `json:"hooks,omitempty"`
}

type toolDef struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Parameters  []paramDef `json:"parameters,omitempty"`
}

type paramDef struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type toolCallResult struct {
	Content string `json:"content,omitempty"`
	IsError bool   `json:"is_error,omitempty"`
}

type hookCallParams struct {
	Point    string         `json:"point"`
	ToolName string         `json:"tool_name,omitempty"`
	Args     map[string]any `json:"args,omitempty"`
	Status   string         `json:"status,omitempty"`
	Error    string         `json:"error,omitempty"`
}

type hookCallResult struct {
	Overlays []hookOverlay `json:"overlays,omitempty"`
	Note     *hookNote     `json:"note,omitempty"`
	Block    *hookBlock    `json:"block,omitempty"`
}

type hookOverlay struct {
	Key      string `json:"key,omitempty"`
	Content  string `json:"content,omitempty"`
	Priority any    `json:"priority,omitempty"`
}

type hookNote struct {
	Message  string `json:"message,omitempty"`
	Priority any    `json:"priority,omitempty"`
}

type hookBlock struct {
	Message string `json:"message,omitempty"`
}

type pluginTool struct {
	PluginID    string
	Name        string
	Description string
	Parameters  []tools.ParameterDef
}

type pluginState struct {
	config config.PluginConfig
	client *client
	tools  []pluginTool
	hooks  map[hooks.Point]struct{}
}

func startupTimeout(cfg config.PluginConfig) time.Duration {
	if cfg.StartupTimeoutMS > 0 {
		return time.Duration(cfg.StartupTimeoutMS) * time.Millisecond
	}
	return defaultStartupTimeout
}

func requestTimeout(cfg config.PluginConfig) time.Duration {
	if cfg.RequestTimeoutMS > 0 {
		return time.Duration(cfg.RequestTimeoutMS) * time.Millisecond
	}
	return defaultRequestTimeout
}

func hookPoint(name string) (hooks.Point, bool) {
	switch hooks.Point(strings.TrimSpace(name)) {
	case hooks.PointPromptContext:
		return hooks.PointPromptContext, true
	case hooks.PointBeforeTool:
		return hooks.PointBeforeTool, true
	case hooks.PointAfterTool:
		return hooks.PointAfterTool, true
	default:
		return "", false
	}
}

func normalizeParameterType(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "integer", "number", "int":
		return "int"
	case "boolean", "bool":
		return "bool"
	default:
		return "string"
	}
}

func priorityFromString(value string) hooks.Priority {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "high":
		return hooks.PriorityHigh
	case "low":
		return hooks.PriorityLow
	default:
		return hooks.PriorityNormal
	}
}

func priorityFromAny(value any) hooks.Priority {
	switch typed := value.(type) {
	case string:
		return priorityFromString(typed)
	case float64:
		return hooks.Priority(typed)
	case int:
		return hooks.Priority(typed)
	default:
		return hooks.PriorityNormal
	}
}
