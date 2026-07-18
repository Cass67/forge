package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"sync"

	agenttools "forge/internal/agent/tools"
	"forge/internal/config"
	"forge/internal/hooks"
)

type Manager struct {
	workDir string
	configs []config.PluginConfig

	mu      sync.RWMutex
	plugins map[string]*pluginState
	tools   []pluginTool
}

func NewManager(workDir string, configs []config.PluginConfig) *Manager {
	return &Manager{
		workDir: strings.TrimSpace(workDir),
		configs: append([]config.PluginConfig(nil), configs...),
	}
}

func (m *Manager) Start(ctx context.Context) error {
	if m == nil {
		return nil
	}
	pluginsByID := make(map[string]*pluginState)
	var allTools []pluginTool
	var errs []error
	for _, cfg := range m.configs {
		if !cfg.IsEnabled() {
			continue
		}
		if strings.TrimSpace(cfg.Kind) == "native" {
			continue
		}
		id := strings.TrimSpace(cfg.ID)
		if id == "" {
			errs = append(errs, fmt.Errorf("plugin requires id"))
			continue
		}
		if _, exists := pluginsByID[strings.ToLower(id)]; exists {
			errs = append(errs, fmt.Errorf("plugin %q already started", id))
			continue
		}
		client, initResult, err := startClient(ctx, m.workDir, cfg)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		tools, err := normalizeTools(id, initResult.Tools)
		if err != nil {
			_ = client.Close()
			errs = append(errs, fmt.Errorf("plugin %q tools: %w", id, err))
			continue
		}
		state := &pluginState{
			config: cfg,
			client: client,
			tools:  tools,
			hooks:  normalizeHooks(initResult.Hooks),
			agents: initResult.Agents,
		}
		pluginsByID[strings.ToLower(id)] = state
		allTools = append(allTools, tools...)
	}
	m.mu.Lock()
	m.plugins = pluginsByID
	m.tools = allTools
	m.mu.Unlock()
	return errors.Join(errs...)
}

func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	pluginsByID := m.plugins
	m.plugins = nil
	m.tools = nil
	m.mu.Unlock()
	var errs []error
	for _, state := range pluginsByID {
		if state != nil && state.client != nil {
			errs = append(errs, state.client.Close())
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) HasPlugins() bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.plugins) > 0
}

func (m *Manager) Tools() []pluginTool {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]pluginTool(nil), m.tools...)
}

type AgentDef struct {
	Name         string
	Description  string
	SystemPrompt string
	Model        string
	Fallbacks    []string
	ModelFamily  string
	Tools        []string
}

func (m *Manager) AgentDefs() []AgentDef {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var all []AgentDef
	for _, state := range m.plugins {
		for _, a := range state.agents {
			all = append(all, AgentDef{
				Name:         a.Name,
				Description:  a.Description,
				SystemPrompt: a.SystemPrompt,
				Model:        a.Model,
				Fallbacks:    a.Fallbacks,
				ModelFamily:  a.ModelFamily,
				Tools:        toToolList(a.Tools),
			})
		}
	}
	return all
}

func toToolList(tools any) []string {
	if tools == nil || tools == "*" {
		return nil
	}
	if arr, ok := tools.([]any); ok {
		list := make([]string, 0, len(arr))
		for _, item := range arr {
			if s, ok := item.(string); ok {
				list = append(list, s)
			}
		}
		return list
	}
	return nil
}

func (m *Manager) RegisterTools(reg *agenttools.Registry, approve agenttools.ApprovalFunc) {
	if m == nil || reg == nil {
		return
	}
	for _, tool := range m.Tools() {
		namespacedName := NamespacedToolName(tool.PluginID, tool.Name)
		autoApprove := m.autoApproves(tool.PluginID, tool.Name)
		reg.Register(agenttools.Tool{
			Name:             namespacedName,
			Description:      pluginToolDescription(tool),
			Parameters:       append([]agenttools.ParameterDef(nil), tool.Parameters...),
			PromptVisibility: agenttools.PromptHidden,
			AutoApprove:      autoApprove,
			Execute: func(ctx context.Context, args map[string]any) (string, error) {
				if !autoApprove {
					if approve == nil {
						return "", fmt.Errorf("plugin tool %s requires approval", namespacedName)
					}
					approved, err := approve(agenttools.Action{
						Context: ctx,
						Tool:    namespacedName,
						Summary: "Run plugin tool " + namespacedName,
						Detail:  pluginToolApprovalDetail(args),
					})
					if err != nil {
						return "", err
					}
					if !approved {
						return namespacedName + " denied by user", nil
					}
				}
				if tool.ExecuteFunc != nil {
					return tool.ExecuteFunc(ctx, args)
				}
				return m.CallTool(ctx, tool.PluginID, tool.Name, args)
			},
		})
	}
}

func (m *Manager) RegisterHooks(reg *hooks.Registry) {
	if m == nil || reg == nil {
		return
	}
	m.mu.RLock()
	registrations := make([]hookRegistration, 0)
	for _, cfg := range m.configs {
		if !cfg.IsEnabled() {
			continue
		}
		id := strings.ToLower(strings.TrimSpace(cfg.ID))
		state := m.plugins[id]
		if state == nil {
			continue
		}
		for _, point := range hookRegistrationPoints() {
			if _, ok := state.hooks[point]; ok {
				registrations = append(registrations, hookRegistration{pluginID: id, point: point})
			}
		}
	}
	m.mu.RUnlock()
	for _, registration := range registrations {
		pluginID := registration.pluginID
		point := registration.point
		reg.Register(point, "plugin:"+pluginID+":"+string(point), func(ctx context.Context, event hooks.Event) []hooks.Result {
			return m.CallHook(ctx, pluginID, point, event)
		})
	}
}

type hookRegistration struct {
	pluginID string
	point    hooks.Point
}

func hookRegistrationPoints() []hooks.Point {
	return []hooks.Point{
		hooks.PointPromptContext,
		hooks.PointBeforeTool,
		hooks.PointAfterTool,
		hooks.PointChatMessage,
		hooks.PointChatParams,
		hooks.PointChatHeaders,
		hooks.PointPermissionRequest,
		hooks.PointSessionStart,
		hooks.PointSessionEnd,
		hooks.PointPreCompact,
		hooks.PointPostCompact,
		hooks.PointTurnComplete,
		hooks.PointEvent,
	}
}

func (m *Manager) CallTool(ctx context.Context, pluginID, toolName string, args map[string]any) (string, error) {
	state, err := m.state(pluginID)
	if err != nil {
		return "", err
	}
	callCtx, cancel := withRequestTimeout(ctx, requestTimeout(state.config))
	defer cancel()
	var result toolCallResult
	err = state.client.call(callCtx, "tool_call", toolCallParams{Name: toolName, Arguments: cloneMap(args)}, &result)
	if err != nil {
		return "", err
	}
	if result.IsError {
		message := strings.TrimSpace(result.Content)
		if message == "" {
			message = "plugin tool returned an error"
		}
		return "", errors.New(message)
	}
	return result.Content, nil
}

func (m *Manager) CallHook(ctx context.Context, pluginID string, point hooks.Point, event hooks.Event) []hooks.Result {
	state, err := m.state(pluginID)
	if err != nil || state.client == nil {
		return nil
	}
	if _, ok := state.hooks[point]; !ok {
		return nil
	}
	callCtx, cancel := withRequestTimeout(ctx, requestTimeout(state.config))
	defer cancel()
	var result hookCallResult
	if err := state.client.call(callCtx, "hook", hookParams(point, event), &result); err != nil {
		return nil
	}
	return hookResults(pluginID, result)
}

func (m *Manager) state(pluginID string) (*pluginState, error) {
	if m == nil {
		return nil, fmt.Errorf("plugin manager is not running")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.plugins[strings.ToLower(strings.TrimSpace(pluginID))]
	if state == nil || state.client == nil {
		return nil, fmt.Errorf("plugin not connected: %s", pluginID)
	}
	return state, nil
}

func NamespacedToolName(pluginID, toolName string) string {
	return "plugin__" + strings.TrimSpace(pluginID) + "__" + strings.TrimSpace(toolName)
}

func pluginToolDescription(tool pluginTool) string {
	description := strings.TrimSpace(tool.Description)
	if description == "" {
		description = "Plugin tool " + tool.Name + "."
	}
	return description + " (plugin: " + tool.PluginID + ")"
}

func pluginToolApprovalDetail(args map[string]any) string {
	if len(args) == 0 {
		return "{}"
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return fmt.Sprint(args)
	}
	return string(encoded)
}

func (m *Manager) autoApproves(pluginID, toolName string) bool {
	state, err := m.state(pluginID)
	if err != nil {
		return false
	}
	namespaced := NamespacedToolName(pluginID, toolName)
	for _, approved := range state.config.AutoApproveTools {
		approved = strings.TrimSpace(approved)
		if approved == toolName || approved == namespaced {
			return true
		}
	}
	return false
}

func normalizeTools(pluginID string, defs []toolDef) ([]pluginTool, error) {
	seen := map[string]struct{}{}
	tools := make([]pluginTool, 0, len(defs))
	for _, def := range defs {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			return nil, fmt.Errorf("tool name must not be empty")
		}
		if !validIdentifier(name) {
			return nil, fmt.Errorf("tool %q must contain only letters, digits, underscores, or hyphens", name)
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("duplicate tool %q", name)
		}
		seen[key] = struct{}{}
		params := make([]agenttools.ParameterDef, 0, len(def.Parameters))
		for _, param := range def.Parameters {
			paramName := strings.TrimSpace(param.Name)
			if paramName == "" {
				return nil, fmt.Errorf("tool %q parameter name must not be empty", name)
			}
			params = append(params, agenttools.ParameterDef{
				Name:        paramName,
				Type:        normalizeParameterType(param.Type),
				Description: strings.TrimSpace(param.Description),
				Required:    param.Required,
			})
		}
		tools = append(tools, pluginTool{
			PluginID:    pluginID,
			Name:        name,
			Description: strings.TrimSpace(def.Description),
			Parameters:  params,
		})
	}
	return tools, nil
}

func normalizeHooks(names []string) map[hooks.Point]struct{} {
	out := make(map[hooks.Point]struct{})
	for _, name := range names {
		if point, ok := hookPoint(name); ok {
			out[point] = struct{}{}
		}
	}
	return out
}

func hookParams(point hooks.Point, event hooks.Event) hookCallParams {
	params := hookCallParams{Point: string(point)}
	if point == hooks.PointBeforeTool || point == hooks.PointAfterTool {
		params.ToolName = transientString(event.Transient, "ToolName")
		params.Args = cloneMap(transientMap(event.Transient, "Args"))
	}
	if point == hooks.PointAfterTool {
		if transientBool(event.Transient, "IsError") {
			params.Status = "error"
			params.Error = transientString(event.Transient, "Error")
		} else {
			params.Status = "ok"
		}
	}
	if point == hooks.PointEvent || point == hooks.PointSessionStart || point == hooks.PointSessionEnd ||
		point == hooks.PointTurnComplete || point == hooks.PointPreCompact || point == hooks.PointPostCompact ||
		point == hooks.PointChatMessage || point == hooks.PointChatParams || point == hooks.PointChatHeaders ||
		point == hooks.PointPermissionRequest {
		params.Event = eventToMap(event)
	}
	return params
}

func eventToMap(event hooks.Event) map[string]any {
	result := map[string]any{
		"type": string(event.Point),
	}
	if event.Snapshot != nil {
		result["snapshot"] = event.Snapshot
	}
	if event.Transient != nil {
		result["transient"] = event.Transient
	}
	return result
}

func hookResults(pluginID string, result hookCallResult) []hooks.Result {
	out := make([]hooks.Result, 0, len(result.Overlays)+2)
	for _, overlay := range result.Overlays {
		content := strings.TrimSpace(overlay.Content)
		if content == "" {
			continue
		}
		key := strings.TrimSpace(overlay.Key)
		if key == "" {
			key = pluginID
		}
		out = append(out, hooks.OverlayResult{
			Key:        "plugin_" + pluginID + "_" + key,
			Content:    content,
			Priority:   priorityFromAny(overlay.Priority),
			Provenance: "plugin:" + pluginID,
		})
	}
	if result.Note != nil && strings.TrimSpace(result.Note.Message) != "" {
		out = append(out, hooks.NoteResult{
			Message:    strings.TrimSpace(result.Note.Message),
			Priority:   priorityFromAny(result.Note.Priority),
			Provenance: "plugin:" + pluginID,
		})
	}
	if result.Block != nil && strings.TrimSpace(result.Block.Message) != "" {
		out = append(out, hooks.BlockResult{
			Message:    strings.TrimSpace(result.Block.Message),
			Provenance: "plugin:" + pluginID,
		})
	}
	return out
}

func transientString(value any, field string) string {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return ""
	}
	if reflected.Kind() == reflect.Pointer {
		if reflected.IsNil() {
			return ""
		}
		reflected = reflected.Elem()
	}
	if reflected.Kind() != reflect.Struct {
		return ""
	}
	fieldValue := reflected.FieldByName(field)
	if !fieldValue.IsValid() || fieldValue.Kind() != reflect.String {
		return ""
	}
	return strings.TrimSpace(fieldValue.String())
}

func transientBool(value any, field string) bool {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return false
	}
	if reflected.Kind() == reflect.Pointer {
		if reflected.IsNil() {
			return false
		}
		reflected = reflected.Elem()
	}
	if reflected.Kind() != reflect.Struct {
		return false
	}
	fieldValue := reflected.FieldByName(field)
	return fieldValue.IsValid() && fieldValue.Kind() == reflect.Bool && fieldValue.Bool()
}

func transientMap(value any, field string) map[string]any {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return nil
	}
	if reflected.Kind() == reflect.Pointer {
		if reflected.IsNil() {
			return nil
		}
		reflected = reflected.Elem()
	}
	if reflected.Kind() != reflect.Struct {
		return nil
	}
	fieldValue := reflected.FieldByName(field)
	if !fieldValue.IsValid() || fieldValue.IsNil() || !fieldValue.CanInterface() {
		return nil
	}
	if args, ok := fieldValue.Interface().(map[string]any); ok {
		return args
	}
	return nil
}

func cloneMap(args map[string]any) map[string]any {
	if len(args) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(args))
	maps.Copy(cloned, args)
	return cloned
}

func validIdentifier(value string) bool {
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}
