// Package plugin is the native Go SDK for extending forge.
// Plugin authors implement the interfaces in this package and register
// via the global Registry (call Register* in init() functions).
//
// Forge discovers native plugins at build time through blank imports.
// External (JSON-RPC) plugins remain supported via internal/plugins.
package plugin

import (
	"context"
	"fmt"
	"strings"
)

// Tool is one callable tool exposed to the LLM.
type Tool struct {
	Name        string
	Description string   // shown to the model in tool schemas
	Parameters  []Param  // JSON Schema for arguments
	Execute     ToolFunc // called when the model invokes this tool
}

// ToolFunc receives deserialized arguments and returns output the model sees.
// Return an error for tool failures; the error message is shown to the model.
type ToolFunc func(ctx context.Context, args map[string]any) (output string, err error)

// Param describes one parameter in a tool's JSON schema.
type Param struct {
	Name        string
	Type        string // "string", "number", "boolean", "array", "object"
	Description string
	Required    bool
	Default     any      // optional default value
	Enum        []string // optional allowed values
}

// ParameterDef is an alias for Param, used by the plugins bridge.
type ParameterDef = Param

// Hook is one lifecycle hook registration.
type Hook struct {
	Point   HookPoint                                           // which lifecycle event
	Handler func(ctx context.Context, e HookEvent) []HookResult // handler
}

// HookPoint names a lifecycle event. Mirrors hooks.Point.
type HookPoint string

const (
	PointSessionStart      HookPoint = "session_start"
	PointSessionEnd        HookPoint = "session_end"
	PointPermissionRequest HookPoint = "permission_request"
	PointBeforeTool        HookPoint = "before_tool"
	PointAfterTool         HookPoint = "after_tool"
	PointPreCompact        HookPoint = "pre_compact"
	PointPostCompact       HookPoint = "post_compact"
	PointTurnComplete      HookPoint = "turn_complete"
	PointPromptContext     HookPoint = "prompt_context"
	PointChatMessage       HookPoint = "chat_message"
)

// HookEvent carries the hook point and its snapshot data.
type HookEvent struct {
	Point    HookPoint
	Snapshot any // type varies by point; see docs for per-point schemas
}

// HookResult is returned by hook handlers.
type HookResult struct {
	Overlay *HookOverlay // inject content into system prompt
	Note    *HookNote    // info message
	Block   *HookBlock   // block the action
}

// HookOverlay injects content into the system prompt at a named key.
type HookOverlay struct {
	Key      string // overlay slot name
	Content  string // markdown to inject
	Priority int    // 0=normal, 1=high, -1=low
}

// HookNote is an informational message from a hook.
type HookNote struct {
	Message  string
	Priority int
}

// HookBlock blocks an action with a reason message.
type HookBlock struct {
	Message string
}

// Skill is a reusable capability: instructions + optional scripts.
type Skill struct {
	Name        string // machine name (lowercase, hyphens)
	Description string // when to load this skill
	Body        string // markdown instructions (SKILL.md content)
	Dir         string // directory path for relative references to scripts/assets
}

// Agent is a custom agent definition registered by a plugin.
type Agent struct {
	Name         string
	Description  string
	SystemPrompt string
	Model        string   // optional default model override
	Fallbacks    []string // fallback model names (in priority order)
	ModelFamily  string   // e.g. "claude", "gpt"
	Tools        []string // tool names to expose
}

// MCPServer describes a Model Context Protocol server a plugin provides.
type MCPServer struct {
	Name    string
	Command []string          // process-based transport
	Env     map[string]string // environment for the process
	URL     string            // http-based transport (mutually exclusive with Command)
}

// Command is a custom slash command registered by a plugin.
type Command struct {
	Name        string // "/name" in the chat UI
	Description string
	Handler     func(ctx context.Context, args string) (string, error)
}

// Provider adds a custom LLM provider.
type Provider struct {
	Name string
	// Models this provider supports.
	Models []ModelDef
	// ResolveAPIKey returns the API key for the provider.
	ResolveAPIKey func(ctx context.Context) (string, error)
	// BaseURL is the provider's API endpoint.
	BaseURL string
}

// ModelDef describes one model from a custom provider.
type ModelDef struct {
	ID            string
	Name          string
	ContextWindow int
	MaxTokens     int
	Reasoning     bool
}

// Plugin is the top-level interface. A plugin may implement any subset
// of the capability interfaces below. The registry inspects which
// interfaces the plugin satisfies.
type Plugin interface {
	// Name returns a stable, unique identifier for this plugin.
	Name() string
}

// ToolProvider: plugin exposes tools to the LLM.
type ToolProvider interface {
	Tools() []Tool
}

// HookProvider: plugin subscribes to lifecycle hooks.
type HookProvider interface {
	Hooks() []Hook
}

// SkillProvider: plugin provides skills.
type SkillProvider interface {
	Skills() []Skill
}

// AgentProvider: plugin defines custom agents.
type AgentProvider interface {
	Agents() []Agent
}

// MCPServerProvider: plugin provides MCP servers.
type MCPServerProvider interface {
	MCPServers() []MCPServer
}

// CommandProvider: plugin adds CLI slash commands.
type CommandProvider interface {
	Commands() []Command
}

// ProviderProvider: plugin adds a custom LLM provider.
type ProviderProvider interface {
	Providers() []Provider
}

var global = &Registry{}

// HookHandler is a function that handles a hook event and returns results.
// Used by the plugins bridge to dispatch native hook calls.
type HookHandler = func(context.Context, HookEvent) []HookResult

// PluginInfo is the snapshot of a registered plugin's capabilities.
// Returned by GetPlugin for the plugins bridge to consume.
type PluginInfo struct {
	Name         string
	Tools        []Tool
	HookPoints   []HookPoint
	HookHandlers map[string]HookHandler
	Agents       []Agent
	Skills       []Skill
	Commands     []Command
}

// Register registers a plugin. Call from init().
func Register(p Plugin) {
	global.Register(p)
}

// RegisterTool registers a standalone tool.
func RegisterTool(t Tool) []Tool { global.RegisterTool(t); return nil }

// RegisterHook registers a standalone hook.
func RegisterHook(h Hook) []Hook { global.RegisterHook(h); return nil }

// Registry holds all registered plugins and their capabilities.
type Registry struct {
	plugins   []Plugin
	byName    map[string]*PluginInfo // for GetPlugin lookups
	tools     []Tool
	hooks     []Hook
	skills    []Skill
	agents    []Agent
	mcps      []MCPServer
	commands  []Command
	providers []Provider
}

// Register adds a plugin, introspecting which interfaces it implements.
func (r *Registry) Register(p Plugin) {
	name := p.Name()
	r.plugins = append(r.plugins, p)
	if r.byName == nil {
		r.byName = make(map[string]*PluginInfo)
	}
	info := &PluginInfo{Name: name}

	if tp, ok := p.(ToolProvider); ok {
		tools := tp.Tools()
		r.tools = append(r.tools, tools...)
		info.Tools = tools
	}
	if hp, ok := p.(HookProvider); ok {
		hooks := hp.Hooks()
		r.hooks = append(r.hooks, hooks...)
		for _, h := range hooks {
			info.HookPoints = append(info.HookPoints, h.Point)
		}
	}
	if sp, ok := p.(SkillProvider); ok {
		skills := sp.Skills()
		r.skills = append(r.skills, skills...)
		info.Skills = skills
	}
	if ap, ok := p.(AgentProvider); ok {
		agents := ap.Agents()
		r.agents = append(r.agents, agents...)
		info.Agents = agents
	}
	if mp, ok := p.(MCPServerProvider); ok {
		r.mcps = append(r.mcps, mp.MCPServers()...)
	}
	if cp, ok := p.(CommandProvider); ok {
		cmds := cp.Commands()
		r.commands = append(r.commands, cmds...)
		info.Commands = cmds
	}
	if pp, ok := p.(ProviderProvider); ok {
		r.providers = append(r.providers, pp.Providers()...)
	}
	r.byName[name] = info
}

func (r *Registry) RegisterTool(t Tool) {
	r.tools = append(r.tools, t)
}

func (r *Registry) RegisterHook(h Hook) {
	r.hooks = append(r.hooks, h)
}

// GetAllTools returns all registered tools.
func (r *Registry) GetAllTools() []Tool { return r.tools }

// GetAllHooks returns all registered hooks.
func (r *Registry) GetAllHooks() []Hook { return r.hooks }

// GetAllSkills returns all registered skills.
func (r *Registry) GetAllSkills() []Skill { return r.skills }

// GetAllAgents returns all registered agents.
func (r *Registry) GetAllAgents() []Agent { return r.agents }

// GetAllMCPServers returns all registered MCP servers.
func (r *Registry) GetAllMCPServers() []MCPServer { return r.mcps }

// GetAllCommands returns all registered commands.
func (r *Registry) GetAllCommands() []Command { return r.commands }

// GetAllProviders returns all registered providers.
func (r *Registry) GetAllProviders() []Provider { return r.providers }

// GetPlugin returns plugin info by name, or nil if not found.
func (r *Registry) GetPlugin(name string) *PluginInfo {
	info, ok := r.byName[name]
	if !ok {
		return nil
	}
	copied := *info
	return &copied
}

// Count returns the number of registered plugins.
func (r *Registry) Count() int { return len(r.plugins) }

// Global returns the global plugin registry.
func Global() *Registry { return global }

// GetPlugin returns the plugin info for a registered plugin by name.
func GetPlugin(name string) *PluginInfo { return global.GetPlugin(name) }

// RegisteredCount returns the number of registered plugins.
func RegisteredCount() int { return global.Count() }

// --- helpers for building tools ---

// StringParam is a shorthand for a required string parameter.
func StringParam(name, desc string) Param {
	return Param{Name: name, Type: "string", Description: desc, Required: true}
}

// OptStringParam is a shorthand for an optional string parameter.
func OptStringParam(name, desc string) Param {
	return Param{Name: name, Type: "string", Description: desc, Required: false}
}

// BoolParam is a shorthand for a required boolean parameter.
func BoolParam(name, desc string) Param {
	return Param{Name: name, Type: "boolean", Description: desc, Required: true}
}

// OptBoolParam is a shorthand for an optional boolean parameter.
func OptBoolParam(name, desc string) Param {
	return Param{Name: name, Type: "boolean", Description: desc, Required: false}
}

// IntParam is a shorthand for a required integer parameter.
func IntParam(name, desc string) Param {
	return Param{Name: name, Type: "integer", Description: desc, Required: true}
}

// OptIntParam is a shorthand for an optional integer parameter.
func OptIntParam(name, desc string) Param {
	return Param{Name: name, Type: "integer", Description: desc, Required: false}
}

// ValidateSkills checks skill name rules and returns errors for violations.
// Rules: lowercase, hyphens, 1-64 chars, no consecutive/leading/trailing hyphens.
func ValidateSkills(skills []Skill) error {
	var errs []string
	for _, s := range skills {
		if len(s.Name) < 1 || len(s.Name) > 64 {
			errs = append(errs, fmt.Sprintf("skill %q: name must be 1-64 characters", s.Name))
		}
		if s.Name != strings.ToLower(s.Name) {
			errs = append(errs, fmt.Sprintf("skill %q: name must be lowercase", s.Name))
		}
		if strings.Contains(s.Name, "--") {
			errs = append(errs, fmt.Sprintf("skill %q: no consecutive hyphens allowed", s.Name))
		}
		if strings.HasPrefix(s.Name, "-") || strings.HasSuffix(s.Name, "-") {
			errs = append(errs, fmt.Sprintf("skill %q: no leading/trailing hyphens", s.Name))
		}
		if strings.TrimSpace(s.Description) == "" {
			errs = append(errs, fmt.Sprintf("skill %q: description is required", s.Name))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("skill validation:\n%s", strings.Join(errs, "\n"))
	}
	return nil
}
