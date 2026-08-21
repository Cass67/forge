package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"forge/internal/llm"
	"forge/internal/protocol"
)

type PromptVisibility int

const (
	PromptCore PromptVisibility = iota
	PromptHidden
)

type ToolConcurrency string

const (
	ToolConcurrencyParallel ToolConcurrency = "parallel"
	ToolConcurrencySerial   ToolConcurrency = "serial"
)

// Tool defines a single tool the agent can call.
type Tool struct {
	Name             string
	Description      string
	Parameters       []ParameterDef
	Schema           *llm.ToolSchema
	PromptVisibility PromptVisibility
	AutoApprove      bool
	Concurrency      ToolConcurrency
	Timeout          time.Duration
	Detached         bool
	MutatesWorkspace bool
	Execute          func(ctx context.Context, args map[string]any) (string, error)
	LastDiff         func() string // optional: returns diff from last execution, nil if not applicable
}

func (t Tool) ParallelSafe() bool {
	return t.Concurrency == "" || t.Concurrency == ToolConcurrencyParallel
}

func (t Tool) EffectiveTimeout() time.Duration {
	return t.Timeout
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
	Context context.Context
	Tool    string
	Summary string
	Detail  string // diff content, command text, or file content
	Path    string
}

// ApprovalFunc asks the user to approve an action. Returns true if approved.
type ApprovalFunc func(action Action) (bool, error)

// Registry holds available tools.
type Registry struct {
	mu        sync.RWMutex
	tools     map[string]Tool
	order     []string
	disclosed map[string]bool
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools:     make(map[string]Tool),
		disclosed: make(map[string]bool),
	}
}

// Register adds a tool to the registry.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[t.Name]; !exists {
		r.order = append(r.order, t.Name)
	}
	r.tools[t.Name] = t
}

// Get retrieves a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// All returns all registered tools in registration order.
func (r *Registry) All() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Tool, 0, len(r.order))
	for _, name := range r.order {
		result = append(result, r.tools[name])
	}
	return result
}

// Filter returns a new registry containing only the named tools.
// If allowed is nil, a copy of the full registry is returned.
func (r *Registry) Filter(allowed []string) *Registry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if allowed == nil {
		filtered := NewRegistry()
		for _, name := range r.order {
			filtered.Register(r.tools[name])
		}
		filtered.rebindToolHelp()
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
	filtered.rebindToolHelp()
	return filtered
}

func (r *Registry) rebindToolHelp() {
	if r == nil {
		return
	}
	if _, ok := r.Get("tool_help"); !ok {
		return
	}
	r.Register(NewToolHelp(r))
}

// Describe formats all tools for injection into the system prompt.
func (r *Registry) Describe() string {
	return r.describeTools(r.All(), true, false)
}

func (r *Registry) DescribeForPrompt() string {
	return r.describeForPrompt(false)
}

func (r *Registry) DescribeForSingleToolPrompt() string {
	return r.describeForPrompt(true)
}

func (r *Registry) describeForPrompt(singleToolTurns bool) string {
	r.mu.RLock()
	tools := make([]Tool, 0, len(r.order))
	for _, name := range r.order {
		tool := r.tools[name]
		if tool.PromptVisibility == PromptHidden && !r.disclosed[name] {
			continue
		}
		tools = append(tools, tool)
	}
	hiddenNames := r.hiddenToolNamesLocked()
	r.mu.RUnlock()

	var sb strings.Builder
	sb.WriteString(r.describeTools(tools, false, singleToolTurns))
	if len(hiddenNames) > 0 {
		sb.WriteString("\nSpecialized tools are hidden by default to save context. Use tool_help(query) to reveal only what you need.\n")
	}
	return sb.String()
}

func (r *Registry) RevealMatchingTools(query string) []Tool {
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	var matched []Tool
	for _, name := range r.order {
		tool := r.tools[name]
		if tool.PromptVisibility != PromptHidden {
			continue
		}
		if hiddenToolMatches(tool, query) {
			r.disclosed[tool.Name] = true
			matched = append(matched, tool)
		}
	}
	return matched
}

func (r *Registry) ResetDisclosure() {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.disclosed)
}

func (r *Registry) DescribeNamedTools(names []string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tools := make([]Tool, 0, len(names))
	for _, name := range names {
		if tool, ok := r.tools[name]; ok {
			tools = append(tools, tool)
		}
	}
	return r.describeTools(tools, true, false)
}

func (r *Registry) hiddenToolNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.hiddenToolNamesLocked()
}

func (r *Registry) hiddenToolNamesLocked() []string {
	names := make([]string, 0, len(r.tools))
	for _, name := range r.order {
		if r.tools[name].PromptVisibility == PromptHidden && !r.disclosed[name] {
			names = append(names, name)
		}
	}
	return names
}

func (r *Registry) lookupVisible(query string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return nil
	}
	terms := strings.Fields(query)
	var matches []string
	for _, name := range r.order {
		tool := r.tools[name]
		if tool.PromptVisibility == PromptHidden && !r.disclosed[name] {
			continue
		}
		lowerName := strings.ToLower(name)
		for _, term := range terms {
			if len(term) > 2 && strings.Contains(lowerName, term) {
				matches = append(matches, name)
				break
			}
		}
	}
	if len(matches) == 0 {
		// Fallback: match the full query as a single substring
		for _, name := range r.order {
			tool := r.tools[name]
			if tool.PromptVisibility == PromptHidden && !r.disclosed[name] {
				continue
			}
			if strings.Contains(strings.ToLower(name), query) {
				matches = append(matches, name)
			}
		}
	}
	return matches
}

func (r *Registry) describeTools(tools []Tool, detailed bool, singleToolTurns bool) string {
	var sb strings.Builder
	sb.WriteString("You have access to the following tools:\n\n")
	for _, t := range sortToolsByName(tools) {
		if detailed {
			_, _ = fmt.Fprintf(&sb, "## %s\n%s\n", t.Name, t.Description)
			if len(t.Parameters) > 0 {
				sb.WriteString("Parameters:\n")
				for _, p := range t.Parameters {
					req := "optional"
					if p.Required {
						req = "required"
					}
					_, _ = fmt.Fprintf(&sb, "  - %s (%s, %s): %s\n", p.Name, p.Type, req, p.Description)
				}
			}
			sb.WriteString("\n")
			continue
		}
		_, _ = fmt.Fprintf(&sb, "- %s: %s\n", formatToolSignature(t), t.Description)
	}
	sb.WriteString("To call a tool, use this exact format:\n\n")
	sb.WriteString(toolCallExample(tools))
	sb.WriteString("\n\n")
	if singleToolTurns {
		sb.WriteString("Call at most one tool in a single response. After tool results are returned, continue your work.\n")
	} else {
		sb.WriteString("You may call multiple tools. After tool results are returned, continue your work.\n")
	}
	sb.WriteString("Wait for results before making decisions based on them.\n")
	return sb.String()
}

func toolCallExample(tools []Tool) string {
	if example, ok := marshalToolCallExample(preferredExampleTool(tools)); ok {
		return example
	}
	return "<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\"README.md\"}}\n</tool_call>"
}

func preferredExampleTool(tools []Tool) Tool {
	preferred := []string{"read_file", "list_dir", "search", "glob", "git_status"}
	for _, name := range preferred {
		for _, tool := range tools {
			if tool.Name == name {
				return tool
			}
		}
	}
	sorted := sortToolsByName(tools)
	if len(sorted) == 0 {
		return Tool{}
	}
	return sorted[0]
}

func marshalToolCallExample(tool Tool) (string, bool) {
	if strings.TrimSpace(tool.Name) == "" {
		return "", false
	}
	payload := map[string]any{
		"name": tool.Name,
		"args": toolExampleArgs(tool),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", false
	}
	return "<tool_call>\n" + string(encoded) + "\n</tool_call>", true
}

func toolExampleArgs(tool Tool) map[string]any {
	args := make(map[string]any)
	for _, param := range tool.Parameters {
		if !param.Required {
			continue
		}
		args[param.Name] = exampleValueForParameter(param)
	}
	return args
}

func exampleValueForParameter(param ParameterDef) any {
	switch param.Type {
	case "int":
		return 1
	case "bool":
		return true
	}

	name := strings.ToLower(strings.TrimSpace(param.Name))
	switch {
	case strings.Contains(name, "path"):
		return "README.md"
	case strings.Contains(name, "pattern"):
		return "*.go"
	case strings.Contains(name, "query"):
		return "inspect repository"
	case strings.Contains(name, "command"):
		return "go test ./..."
	case strings.Contains(name, "ref"):
		return "HEAD~1"
	default:
		return "example"
	}
}

func sortToolsByName(tools []Tool) []Tool {
	out := append([]Tool(nil), tools...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func formatToolSignature(t Tool) string {
	if len(t.Parameters) == 0 {
		return t.Name
	}
	params := make([]string, 0, len(t.Parameters))
	for _, p := range t.Parameters {
		name := p.Name
		if !p.Required {
			name = "[" + name + "]"
		}
		params = append(params, name)
	}
	return fmt.Sprintf("%s(%s)", t.Name, strings.Join(params, ", "))
}

// ToLLMToolDefs converts registered tools to llm.ToolDef for native tool calling.
func (r *Registry) ToLLMToolDefs() []llm.ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	defs := make([]llm.ToolDef, 0, len(r.order))
	for _, name := range r.order {
		t := r.tools[name]
		params := make([]llm.ToolParam, 0, len(t.Parameters))
		for _, p := range t.Parameters {
			params = append(params, llm.ToolParam{
				Name:        p.Name,
				Type:        mapParamType(p.Type),
				Description: p.Description,
				Required:    p.Required,
			})
		}
		defs = append(defs, llm.ToolDef{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  params,
			Schema:      protocol.SanitizeToolSchema(t.Schema),
		})
	}
	return defs
}

// mapParamType converts forge parameter type strings to JSON Schema type names.
func mapParamType(t string) string {
	switch t {
	case "int":
		return "integer"
	case "bool":
		return "boolean"
	default:
		return "string"
	}
}

func hiddenToolMatches(tool Tool, query string) bool {
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return false
	}
	if strings.Contains(strings.ToLower(tool.Name), query) {
		return true
	}
	if strings.Contains(strings.ToLower(tool.Description), query) {
		return true
	}

	matchesAny := func(terms ...string) bool {
		for _, term := range terms {
			if strings.Contains(query, term) {
				return true
			}
		}
		return false
	}

	switch tool.Name {
	case "git_commit":
		return matchesAny("commit", "checkpoint", "save changes")
	case "web_fetch":
		return matchesAny("fetch", "url", "http", "https", "web page", "website", "download")
	case "web_search":
		return matchesAny("search", "lookup", "research", "internet", "web")
	default:
		return false
	}
}
