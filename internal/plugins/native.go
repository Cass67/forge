package plugins

import (
	"strings"

	agenttools "forge/internal/agent/tools"
	"forge/internal/hooks"
	"forge/internal/plugin"
)

// CollectNativePlugins scans the configuration for plugins with Kind="native"
// and injects them from the global init()-based plugin registry into the
// Manager's active plugin state. It is called by the runtime after Start().
func (m *Manager) CollectNativePlugins() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, cfg := range m.configs {
		if !cfg.IsEnabled() {
			continue
		}
		kind := strings.TrimSpace(cfg.Kind)
		if kind != "native" {
			continue
		}
		id := strings.TrimSpace(cfg.ID)
		if id == "" {
			continue
		}
		info := plugin.GetPlugin(id)
		if info == nil {
			continue
		}
		state := &pluginState{config: cfg, hooks: make(map[hooks.Point]struct{})}
		for _, t := range info.Tools {
			pt := pluginTool{
				PluginID:    id,
				Name:        t.Name,
				Description: t.Description,
				Parameters:  convertParams(t.Parameters),
				ExecuteFunc: t.Execute,
			}
			state.tools = append(state.tools, pt)
			m.tools = append(m.tools, pt)
		}
		for _, hp := range info.HookPoints {
			state.hooks[hooks.Point(hp)] = struct{}{}
		}
		for _, a := range info.Agents {
			state.agents = append(state.agents, agentDef{
				Name:         a.Name,
				Description:  a.Description,
				SystemPrompt: a.SystemPrompt,
				Model:        a.Model,
				Fallbacks:    a.Fallbacks,
				ModelFamily:  a.ModelFamily,
				Tools:        a.Tools,
			})
		}
		m.plugins[strings.ToLower(id)] = state
	}
}

// HasNativePlugins reports whether the global registry has any registered plugins.
func HasNativePlugins() bool {
	return plugin.RegisteredCount() > 0
}

// convertParams converts plugin.ParameterDef to tools.ParameterDef.
func convertParams(params []plugin.ParameterDef) []agenttools.ParameterDef {
	if len(params) == 0 {
		return nil
	}
	out := make([]agenttools.ParameterDef, 0, len(params))
	for _, p := range params {
		out = append(out, agenttools.ParameterDef{
			Name:        p.Name,
			Type:        p.Type,
			Description: p.Description,
			Required:    p.Required,
		})
	}
	return out
}
