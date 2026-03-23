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
	LastDiff    func() string // optional: returns diff from last execution, nil if not applicable
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

// Filter returns a new registry containing only the named tools.
// If allowed is nil, a copy of the full registry is returned.
func (r *Registry) Filter(allowed []string) *Registry {
	if allowed == nil {
		filtered := NewRegistry()
		for _, name := range r.order {
			filtered.Register(r.tools[name])
		}
		return filtered
	}
	allowSet := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		allowSet[name] = true
	}
	filtered := NewRegistry()
	for _, name := range r.order {
		if allowSet[name] {
			filtered.Register(r.tools[name])
		}
	}
	return filtered
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
