package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"forge/internal/config"
	"forge/internal/llm"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

const defaultTimeout = 15 * time.Second

var ErrNotFound = errors.New("mcp resource not found")

type Server struct {
	Name   string
	Config config.MCPServerConfig
}

type Tool struct {
	ServerName  string
	Name        string
	Description string
	Parameters  []llm.ToolParam
}

type Resource struct {
	ServerName  string
	Name        string
	URI         string
	Description string
	MIMEType    string
	Content     string
}

type ResourceTemplate struct {
	ServerName  string
	Name        string
	URITemplate string
	Description string
	MIMEType    string
}

type ToolResult struct {
	ServerName        string        `json:"server"`
	ToolName          string        `json:"tool"`
	IsError           bool          `json:"is_error,omitempty"`
	Content           []ContentItem `json:"content,omitempty"`
	StructuredContent any           `json:"structured_content,omitempty"`
}

type ContentItem struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	MIMEType string `json:"mime_type,omitempty"`
	URI      string `json:"uri,omitempty"`
}

type Snapshot struct {
	Tools             []Tool
	Resources         []Resource
	ResourceTemplates []ResourceTemplate
}

type EventKind string

const (
	EventRefreshed        EventKind = "refreshed"
	EventToolsChanged     EventKind = "tools_changed"
	EventResourcesChanged EventKind = "resources_changed"
	EventResourceUpdated  EventKind = "resource_updated"
	EventLogMessage       EventKind = "log_message"
	EventProgress         EventKind = "progress"
)

type Event struct {
	ServerName string
	Kind       EventKind
	Message    string
	URI        string
	Snapshot   Snapshot
}

type clientSession interface {
	Close() error
	ListTools(ctx context.Context, params *sdkmcp.ListToolsParams) (*sdkmcp.ListToolsResult, error)
	CallTool(ctx context.Context, params *sdkmcp.CallToolParams) (*sdkmcp.CallToolResult, error)
	ListResources(ctx context.Context, params *sdkmcp.ListResourcesParams) (*sdkmcp.ListResourcesResult, error)
	ListResourceTemplates(ctx context.Context, params *sdkmcp.ListResourceTemplatesParams) (*sdkmcp.ListResourceTemplatesResult, error)
	ReadResource(ctx context.Context, params *sdkmcp.ReadResourceParams) (*sdkmcp.ReadResourceResult, error)
}

type sessionConnector func(ctx context.Context, server Server) (clientSession, error)

type Manager struct {
	mu        sync.RWMutex
	servers   []Server
	sessions  map[string]clientSession
	perServer map[string]Snapshot
	snapshot  Snapshot
	connector sessionConnector
	frozen    bool
	onEvent   func(Event)
}

func NewManager() *Manager {
	return &Manager{
		sessions:  make(map[string]clientSession),
		perServer: make(map[string]Snapshot),
	}
}

func (m *Manager) FreezeForTesting(cfg *config.Config, snapshot Snapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.servers = m.EnabledServers(cfg)
	m.snapshot = Snapshot{
		Tools:             append([]Tool(nil), snapshot.Tools...),
		Resources:         append([]Resource(nil), snapshot.Resources...),
		ResourceTemplates: append([]ResourceTemplate(nil), snapshot.ResourceTemplates...),
	}
	m.perServer = map[string]Snapshot{}
	m.frozen = true
}

func (m *Manager) SetEventHandler(handler func(Event)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onEvent = handler
}

func (m *Manager) EnabledServers(cfg *config.Config) []Server {
	if cfg == nil || len(cfg.MCPServers) == 0 {
		return nil
	}
	names := make([]string, 0, len(cfg.MCPServers))
	for name, server := range cfg.MCPServers {
		if !server.IsEnabled() {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	servers := make([]Server, 0, len(names))
	for _, name := range names {
		servers = append(servers, Server{
			Name:   name,
			Config: cfg.MCPServers[name],
		})
	}
	return servers
}

func (m *Manager) RefreshFromConfig(cfg *config.Config) {
	_ = m.Refresh(context.Background(), cfg)
}

func (m *Manager) Refresh(ctx context.Context, cfg *config.Config) error {
	m.mu.RLock()
	frozen := m.frozen
	m.mu.RUnlock()
	if frozen {
		m.mu.Lock()
		m.servers = m.EnabledServers(cfg)
		m.mu.Unlock()
		return nil
	}
	servers := m.EnabledServers(cfg)
	if len(servers) == 0 {
		m.reset()
		return nil
	}

	m.mu.RLock()
	existing := make(map[string]clientSession, len(m.sessions))
	for name, session := range m.sessions {
		existing[name] = session
	}
	m.mu.RUnlock()

	nextSessions := make(map[string]clientSession, len(servers))
	perServer := make(map[string]Snapshot, len(servers))
	var errs []error

	for _, server := range servers {
		session := existing[server.Name]
		delete(existing, server.Name)
		if session == nil {
			var err error
			connectCtx, cancel := context.WithTimeout(withParent(ctx), timeoutForConfig(server.Config))
			session, err = m.connect(connectCtx, server)
			cancel()
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: connect: %w", server.Name, err))
				continue
			}
		}

		refreshCtx, cancel := context.WithTimeout(withParent(ctx), timeoutForConfig(server.Config))
		serverSnapshot, err := buildSnapshot(refreshCtx, server.Name, session)
		cancel()
		if err != nil {
			_ = session.Close()
			errs = append(errs, fmt.Errorf("%s: refresh: %w", server.Name, err))
			continue
		}

		nextSessions[server.Name] = session
		perServer[server.Name] = serverSnapshot
	}

	for _, session := range existing {
		_ = session.Close()
	}

	m.mu.Lock()
	m.servers = append([]Server(nil), servers...)
	m.sessions = nextSessions
	m.perServer = perServer
	m.snapshot = flattenSnapshots(perServer)
	eventHandler := m.onEvent
	snapshot := m.snapshot
	m.mu.Unlock()
	if eventHandler != nil {
		eventHandler(Event{Kind: EventRefreshed, Snapshot: snapshot, Message: fmt.Sprintf("refreshed %d MCP server(s)", len(nextSessions))})
	}

	return errors.Join(errs...)
}

func (m *Manager) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, session := range m.sessions {
		_ = session.Close()
	}
	m.servers = nil
	m.sessions = make(map[string]clientSession)
	m.perServer = make(map[string]Snapshot)
	m.snapshot = Snapshot{}
}

func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var errs []error
	for _, session := range m.sessions {
		if err := session.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	m.sessions = make(map[string]clientSession)
	m.perServer = make(map[string]Snapshot)
	m.snapshot = Snapshot{}
	return errors.Join(errs...)
}

func (m *Manager) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return Snapshot{
		Tools:             append([]Tool(nil), m.snapshot.Tools...),
		Resources:         append([]Resource(nil), m.snapshot.Resources...),
		ResourceTemplates: append([]ResourceTemplate(nil), m.snapshot.ResourceTemplates...),
	}
}

func (m *Manager) SetSnapshot(snapshot Snapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snapshot = Snapshot{
		Tools:             append([]Tool(nil), snapshot.Tools...),
		Resources:         append([]Resource(nil), snapshot.Resources...),
		ResourceTemplates: append([]ResourceTemplate(nil), snapshot.ResourceTemplates...),
	}
}

func (m *Manager) HasServers() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.servers) > 0
}

// ConnectedServers returns server names with live MCP sessions.
func (m *Manager) ConnectedServers() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.sessions))
	for name := range m.sessions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (m *Manager) Tools() []Tool {
	return m.Snapshot().Tools
}

func (m *Manager) Resources() []Resource {
	return m.Snapshot().Resources
}

func (m *Manager) ResourceTemplates() []ResourceTemplate {
	return m.Snapshot().ResourceTemplates
}

func (m *Manager) CallTool(ctx context.Context, serverName, toolName string, args map[string]any) (ToolResult, error) {
	session, err := m.session(serverName)
	if err != nil {
		return ToolResult{}, err
	}
	callCtx, cancel := context.WithTimeout(withParent(ctx), timeoutForConfig(serverConfig(m, serverName)))
	defer cancel()
	res, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name:      toolName,
		Arguments: args,
	})
	if err != nil {
		return ToolResult{}, err
	}
	return normalizeToolResult(serverName, toolName, res), nil
}

func (m *Manager) ReadResource(ctx context.Context, serverName, uri string) (Resource, error) {
	session, err := m.session(serverName)
	if err != nil {
		return Resource{}, err
	}
	readCtx, cancel := context.WithTimeout(withParent(ctx), timeoutForConfig(serverConfig(m, serverName)))
	defer cancel()
	res, err := session.ReadResource(readCtx, &sdkmcp.ReadResourceParams{URI: uri})
	if err != nil {
		return Resource{}, err
	}
	return normalizeReadResource(serverName, uri, res), nil
}

func (m *Manager) session(serverName string) (clientSession, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	session := m.sessions[serverName]
	if session == nil {
		return nil, fmt.Errorf("mcp server not connected: %s", serverName)
	}
	return session, nil
}

func (m *Manager) connect(ctx context.Context, server Server) (clientSession, error) {
	if m.connector != nil {
		return m.connector(ctx, server)
	}
	return connectServer(ctx, server, m.handleEvent)
}

func serverConfig(m *Manager, serverName string) config.MCPServerConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, server := range m.servers {
		if server.Name == serverName {
			return server.Config
		}
	}
	return config.MCPServerConfig{}
}

func withParent(parent context.Context) context.Context {
	if parent == nil {
		return context.Background()
	}
	return parent
}

func timeoutForConfig(cfg config.MCPServerConfig) time.Duration {
	timeout := defaultTimeout
	if cfg.TimeoutMS > 0 {
		timeout = time.Duration(cfg.TimeoutMS) * time.Millisecond
	}
	return timeout
}

func connectServer(ctx context.Context, server Server, notify func(Event)) (clientSession, error) {
	client := sdkmcp.NewClient(&sdkmcp.Implementation{
		Name:    "forge",
		Version: "dev",
	}, &sdkmcp.ClientOptions{
		ToolListChangedHandler: func(ctx context.Context, req *sdkmcp.ToolListChangedRequest) {
			_ = req
			if notify != nil {
				notify(Event{ServerName: server.Name, Kind: EventToolsChanged, Message: "MCP tools changed"})
			}
		},
		ResourceListChangedHandler: func(ctx context.Context, req *sdkmcp.ResourceListChangedRequest) {
			_ = req
			if notify != nil {
				notify(Event{ServerName: server.Name, Kind: EventResourcesChanged, Message: "MCP resources changed"})
			}
		},
		ResourceUpdatedHandler: func(ctx context.Context, req *sdkmcp.ResourceUpdatedNotificationRequest) {
			if notify != nil && req != nil && req.Params != nil {
				notify(Event{ServerName: server.Name, Kind: EventResourceUpdated, URI: req.Params.URI, Message: "MCP resource updated"})
			}
		},
		LoggingMessageHandler: func(ctx context.Context, req *sdkmcp.LoggingMessageRequest) {
			if notify != nil && req != nil && req.Params != nil {
				notify(Event{ServerName: server.Name, Kind: EventLogMessage, Message: fmt.Sprintf("MCP log [%s]: %v", req.Params.Level, req.Params.Data)})
			}
		},
		ProgressNotificationHandler: func(ctx context.Context, req *sdkmcp.ProgressNotificationClientRequest) {
			if notify != nil && req != nil && req.Params != nil {
				msg := strings.TrimSpace(req.Params.Message)
				if msg == "" {
					msg = fmt.Sprintf("MCP progress %.0f/%.0f", req.Params.Progress, req.Params.Total)
				}
				notify(Event{ServerName: server.Name, Kind: EventProgress, Message: msg})
			}
		},
	})

	transport, err := transportForServer(server)
	if err != nil {
		return nil, err
	}
	return client.Connect(ctx, transport, nil)
}

func transportForServer(server Server) (sdkmcp.Transport, error) {
	cfg := server.Config
	switch inferServerType(cfg) {
	case "stdio":
		if len(cfg.Command) == 0 {
			return nil, fmt.Errorf("stdio MCP server %q requires command", server.Name)
		}
		cmd := exec.Command(cfg.Command[0], cfg.Command[1:]...)
		if len(cfg.Env) > 0 {
			cmd.Env = append(os.Environ(), flattenEnv(cfg.Env)...)
		}
		return &sdkmcp.CommandTransport{Command: cmd}, nil
	case "remote":
		if strings.TrimSpace(cfg.URL) == "" {
			return nil, fmt.Errorf("remote MCP server %q requires url", server.Name)
		}
		client := http.DefaultClient
		headers := make(map[string]string, len(cfg.Headers)+1)
		for key, value := range cfg.Headers {
			headers[key] = value
		}
		if token, ok, err := BearerToken(server.Name); err == nil && ok {
			headers["Authorization"] = "Bearer " + token
		}
		if len(headers) > 0 {
			client = &http.Client{Transport: &headerRoundTripper{
				base:    http.DefaultTransport,
				headers: headers,
			}}
		}
		return &sdkmcp.StreamableClientTransport{
			Endpoint:             cfg.URL,
			HTTPClient:           client,
			DisableStandaloneSSE: true,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported MCP server type %q", cfg.Type)
	}
}

func inferServerType(cfg config.MCPServerConfig) string {
	kind := strings.TrimSpace(strings.ToLower(cfg.Type))
	switch kind {
	case "stdio", "remote":
		return kind
	}
	if len(cfg.Command) > 0 {
		return "stdio"
	}
	if strings.TrimSpace(cfg.URL) != "" {
		return "remote"
	}
	return kind
}

func flattenEnv(env map[string]string) []string {
	base := make([]string, 0, len(env))
	for key, value := range env {
		base = append(base, key+"="+value)
	}
	return base
}

type headerRoundTripper struct {
	base    http.RoundTripper
	headers map[string]string
}

func (r *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	base := r.base
	if base == nil {
		base = http.DefaultTransport
	}
	cloned := req.Clone(req.Context())
	for key, value := range r.headers {
		cloned.Header.Set(key, value)
	}
	return base.RoundTrip(cloned)
}

func buildSnapshot(ctx context.Context, serverName string, session clientSession) (Snapshot, error) {
	var errs []error
	snapshot := Snapshot{}

	toolsRes, err := session.ListTools(ctx, nil)
	if err == nil {
		snapshot.Tools = make([]Tool, 0, len(toolsRes.Tools))
		for _, tool := range toolsRes.Tools {
			snapshot.Tools = append(snapshot.Tools, normalizeTool(serverName, tool))
		}
	} else {
		errs = append(errs, fmt.Errorf("list tools: %w", err))
	}

	resourcesRes, err := session.ListResources(ctx, nil)
	if err == nil {
		snapshot.Resources = make([]Resource, 0, len(resourcesRes.Resources))
		for _, resource := range resourcesRes.Resources {
			snapshot.Resources = append(snapshot.Resources, normalizeResource(serverName, resource))
		}
	} else {
		errs = append(errs, fmt.Errorf("list resources: %w", err))
	}

	templatesRes, err := session.ListResourceTemplates(ctx, nil)
	if err == nil {
		snapshot.ResourceTemplates = make([]ResourceTemplate, 0, len(templatesRes.ResourceTemplates))
		for _, template := range templatesRes.ResourceTemplates {
			snapshot.ResourceTemplates = append(snapshot.ResourceTemplates, normalizeResourceTemplate(serverName, template))
		}
	} else {
		errs = append(errs, fmt.Errorf("list resource templates: %w", err))
	}
	if len(snapshot.Tools) == 0 && len(snapshot.Resources) == 0 && len(snapshot.ResourceTemplates) == 0 {
		return Snapshot{}, errors.Join(errs...)
	}
	return snapshot, nil
}

func flattenSnapshots(perServer map[string]Snapshot) Snapshot {
	names := make([]string, 0, len(perServer))
	for name := range perServer {
		names = append(names, name)
	}
	sort.Strings(names)
	flat := Snapshot{}
	for _, name := range names {
		snapshot := perServer[name]
		flat.Tools = append(flat.Tools, snapshot.Tools...)
		flat.Resources = append(flat.Resources, snapshot.Resources...)
		flat.ResourceTemplates = append(flat.ResourceTemplates, snapshot.ResourceTemplates...)
	}
	return flat
}

func (m *Manager) handleEvent(event Event) {
	switch event.Kind {
	case EventToolsChanged, EventResourcesChanged:
		session, err := m.session(event.ServerName)
		if err != nil {
			m.emit(Event{
				ServerName: event.ServerName,
				Kind:       EventLogMessage,
				Message:    fmt.Sprintf("MCP refresh failed: %v", err),
			})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeoutForConfig(serverConfig(m, event.ServerName)))
		snapshot, err := buildSnapshot(ctx, event.ServerName, session)
		cancel()
		if err != nil {
			m.emit(Event{
				ServerName: event.ServerName,
				Kind:       EventLogMessage,
				Message:    fmt.Sprintf("MCP refresh failed: %v", err),
			})
			return
		}
		m.mu.Lock()
		if m.perServer == nil {
			m.perServer = make(map[string]Snapshot)
		}
		m.perServer[event.ServerName] = snapshot
		m.snapshot = flattenSnapshots(m.perServer)
		event.Snapshot = m.snapshot
		m.mu.Unlock()
	case EventResourceUpdated, EventLogMessage, EventProgress:
	default:
	}
	m.emit(event)
}

func (m *Manager) emit(event Event) {
	m.mu.RLock()
	handler := m.onEvent
	m.mu.RUnlock()
	if handler != nil {
		handler(event)
	}
}

func normalizeTool(serverName string, tool *sdkmcp.Tool) Tool {
	if tool == nil {
		return Tool{ServerName: serverName}
	}
	return Tool{
		ServerName:  serverName,
		Name:        tool.Name,
		Description: tool.Description,
		Parameters:  paramsFromSchema(tool.InputSchema),
	}
}

func normalizeResource(serverName string, resource *sdkmcp.Resource) Resource {
	if resource == nil {
		return Resource{ServerName: serverName}
	}
	return Resource{
		ServerName:  serverName,
		Name:        firstNonEmpty(resource.Title, resource.Name),
		URI:         resource.URI,
		Description: resource.Description,
		MIMEType:    resource.MIMEType,
	}
}

func normalizeResourceTemplate(serverName string, template *sdkmcp.ResourceTemplate) ResourceTemplate {
	if template == nil {
		return ResourceTemplate{ServerName: serverName}
	}
	return ResourceTemplate{
		ServerName:  serverName,
		Name:        firstNonEmpty(template.Title, template.Name),
		URITemplate: template.URITemplate,
		Description: template.Description,
		MIMEType:    template.MIMEType,
	}
}

func normalizeReadResource(serverName, uri string, result *sdkmcp.ReadResourceResult) Resource {
	resource := Resource{ServerName: serverName, URI: uri}
	if result == nil {
		return resource
	}
	var parts []string
	for _, content := range result.Contents {
		if content == nil {
			continue
		}
		if resource.URI == "" {
			resource.URI = content.URI
		}
		if resource.MIMEType == "" {
			resource.MIMEType = content.MIMEType
		}
		switch {
		case content.Text != "":
			parts = append(parts, content.Text)
		case len(content.Blob) > 0:
			parts = append(parts, string(content.Blob))
		}
	}
	resource.Content = strings.Join(parts, "\n")
	return resource
}

func normalizeToolResult(serverName, toolName string, result *sdkmcp.CallToolResult) ToolResult {
	out := ToolResult{
		ServerName: serverName,
		ToolName:   toolName,
	}
	if result == nil {
		return out
	}
	out.IsError = result.IsError
	out.StructuredContent = result.StructuredContent
	out.Content = normalizeContent(result.Content)
	return out
}

func normalizeContent(items []sdkmcp.Content) []ContentItem {
	out := make([]ContentItem, 0, len(items))
	for _, item := range items {
		switch c := item.(type) {
		case *sdkmcp.TextContent:
			out = append(out, ContentItem{Type: "text", Text: c.Text})
		case *sdkmcp.ResourceLink:
			out = append(out, ContentItem{Type: "resource_link", URI: c.URI, MIMEType: c.MIMEType, Text: firstNonEmpty(c.Title, c.Name, c.Description)})
		case *sdkmcp.EmbeddedResource:
			if c.Resource != nil {
				text := c.Resource.Text
				if text == "" && len(c.Resource.Blob) > 0 {
					text = string(c.Resource.Blob)
				}
				out = append(out, ContentItem{Type: "resource", URI: c.Resource.URI, MIMEType: c.Resource.MIMEType, Text: text})
			}
		default:
			raw, err := json.Marshal(c)
			if err != nil {
				out = append(out, ContentItem{Type: "unknown"})
				continue
			}
			out = append(out, ContentItem{Type: "json", Text: string(raw)})
		}
	}
	return out
}

func paramsFromSchema(schema any) []llm.ToolParam {
	schemaMap, ok := schema.(map[string]any)
	if !ok || schemaMap == nil {
		return nil
	}
	properties, ok := schemaMap["properties"].(map[string]any)
	if !ok || len(properties) == 0 {
		return nil
	}
	required := requiredSet(schemaMap["required"])
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	params := make([]llm.ToolParam, 0, len(names))
	for _, name := range names {
		property, _ := properties[name].(map[string]any)
		params = append(params, llm.ToolParam{
			Name:        name,
			Type:        schemaType(property["type"]),
			Description: stringValue(property["description"]),
			Required:    required[name],
		})
	}
	return params
}

func requiredSet(raw any) map[string]bool {
	out := map[string]bool{}
	switch v := raw.(type) {
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				out[s] = true
			}
		}
	case []string:
		for _, item := range v {
			out[item] = true
		}
	}
	return out
}

func schemaType(raw any) string {
	value := strings.TrimSpace(strings.ToLower(stringValue(raw)))
	switch value {
	case "integer":
		return "integer"
	case "boolean":
		return "boolean"
	default:
		return "string"
	}
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
