package mcp

import (
	"context"
	"sync"
	"testing"

	"forge/internal/config"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestManagerEnabledServersOmitsDisabledEntries(t *testing.T) {
	manager := NewManager()
	cfg := &config.Config{
		MCPServers: map[string]config.MCPServerConfig{
			"context7": {
				Type: "remote",
				URL:  "https://mcp.context7.com/mcp",
			},
			"disabled": {
				Type:    "stdio",
				Command: []string{"node", "server.js"},
				Enabled: boolPtr(false),
			},
		},
	}

	servers := manager.EnabledServers(cfg)
	if len(servers) != 1 {
		t.Fatalf("len(servers) = %d, want 1", len(servers))
	}
	if servers[0].Name != "context7" {
		t.Fatalf("servers[0].Name = %q", servers[0].Name)
	}
}

func TestManagerEnabledServersSortsByName(t *testing.T) {
	manager := NewManager()
	cfg := &config.Config{
		MCPServers: map[string]config.MCPServerConfig{
			"zeta":  {Type: "remote", URL: "https://example.com/zeta"},
			"alpha": {Type: "remote", URL: "https://example.com/alpha"},
		},
	}

	servers := manager.EnabledServers(cfg)
	if len(servers) != 2 {
		t.Fatalf("len(servers) = %d, want 2", len(servers))
	}
	if servers[0].Name != "alpha" || servers[1].Name != "zeta" {
		t.Fatalf("server order = %#v", servers)
	}
}

func TestManagerSnapshotStartsEmpty(t *testing.T) {
	manager := NewManager()
	snapshot := manager.Snapshot()

	if len(snapshot.Tools) != 0 {
		t.Fatalf("snapshot.Tools = %#v, want empty", snapshot.Tools)
	}
	if len(snapshot.Resources) != 0 {
		t.Fatalf("snapshot.Resources = %#v, want empty", snapshot.Resources)
	}
	if len(snapshot.ResourceTemplates) != 0 {
		t.Fatalf("snapshot.ResourceTemplates = %#v, want empty", snapshot.ResourceTemplates)
	}
}

func TestManagerHasServersAfterRefresh(t *testing.T) {
	manager := NewManager()
	manager.connector = func(ctx context.Context, server Server) (clientSession, error) {
		_ = ctx
		_ = server
		return stubSession{}, nil
	}
	cfg := &config.Config{
		MCPServers: map[string]config.MCPServerConfig{
			"context7": {Type: "remote", URL: "https://mcp.context7.com/mcp"},
		},
	}

	manager.RefreshFromConfig(cfg)
	if !manager.HasServers() {
		t.Fatal("HasServers() = false, want true")
	}
	connected := manager.ConnectedServers()
	if len(connected) != 1 || connected[0] != "context7" {
		t.Fatalf("ConnectedServers() = %#v", connected)
	}
}

func TestManagerRefreshCallToolAndReadResource(t *testing.T) {
	manager := NewManager()
	manager.connector = func(ctx context.Context, server Server) (clientSession, error) {
		_ = server
		return newTestClientSession(t, ctx), nil
	}

	cfg := &config.Config{
		MCPServers: map[string]config.MCPServerConfig{
			"context7": {Type: "stdio", Command: []string{"ignored"}},
		},
	}
	if err := manager.Refresh(context.Background(), cfg); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	snapshot := manager.Snapshot()
	if len(snapshot.Tools) != 1 || snapshot.Tools[0].Name != "resolve_library_id" {
		t.Fatalf("snapshot.Tools = %#v", snapshot.Tools)
	}
	if len(snapshot.Resources) != 1 || snapshot.Resources[0].URI != "context7://docs" {
		t.Fatalf("snapshot.Resources = %#v", snapshot.Resources)
	}
	if len(snapshot.ResourceTemplates) != 1 || snapshot.ResourceTemplates[0].URITemplate != "context7://library/{name}" {
		t.Fatalf("snapshot.ResourceTemplates = %#v", snapshot.ResourceTemplates)
	}
	if got := snapshot.Tools[0].Parameters[0]; got.Name != "library_name" || got.Type != "string" || !got.Required {
		t.Fatalf("snapshot.Tools[0].Parameters = %#v", snapshot.Tools[0].Parameters)
	}

	toolResult, err := manager.CallTool(context.Background(), "context7", "resolve_library_id", map[string]any{"library_name": "react"})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if len(toolResult.Content) != 1 || toolResult.Content[0].Text != "react-id" {
		t.Fatalf("toolResult.Content = %#v", toolResult.Content)
	}

	resource, err := manager.ReadResource(context.Background(), "context7", "context7://docs")
	if err != nil {
		t.Fatalf("ReadResource() error = %v", err)
	}
	if resource.Content != "forge docs" {
		t.Fatalf("resource.Content = %q", resource.Content)
	}
}

func TestBuildSnapshotAllowsToolsOnlyServers(t *testing.T) {
	snapshot, err := buildSnapshot(context.Background(), "context7", toolsOnlySession{})
	if err != nil {
		t.Fatalf("buildSnapshot() error = %v", err)
	}
	if len(snapshot.Tools) != 1 {
		t.Fatalf("len(snapshot.Tools) = %d, want 1", len(snapshot.Tools))
	}
	if len(snapshot.Resources) != 0 || len(snapshot.ResourceTemplates) != 0 {
		t.Fatalf("unexpected snapshot = %#v", snapshot)
	}
}

func TestManagerHandleEventRefreshesSnapshotAndEmitsEvent(t *testing.T) {
	manager := NewManager()
	session := &mutableToolSession{
		tools: [][]*sdkmcp.Tool{
			{{Name: "resolve_library_id"}},
			{{Name: "resolve_library_id"}, {Name: "get_library_docs"}},
		},
		calls: 1,
	}
	manager.sessions["context7"] = session
	manager.servers = []Server{{Name: "context7", Config: config.MCPServerConfig{Type: "stdio", Command: []string{"ignored"}}}}
	manager.perServer = map[string]Snapshot{
		"context7": {
			Tools: []Tool{{ServerName: "context7", Name: "resolve_library_id"}},
		},
	}
	manager.snapshot = flattenSnapshots(manager.perServer)

	var events []Event
	manager.SetEventHandler(func(ev Event) {
		events = append(events, ev)
	})

	manager.handleEvent(Event{ServerName: "context7", Kind: EventToolsChanged, Message: "MCP tools changed"})

	snapshot := manager.Snapshot()
	if len(snapshot.Tools) != 2 {
		t.Fatalf("len(snapshot.Tools) = %d, want 2", len(snapshot.Tools))
	}
	if snapshot.Tools[1].Name != "get_library_docs" {
		t.Fatalf("snapshot.Tools = %#v", snapshot.Tools)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	if len(events[0].Snapshot.Tools) != 2 {
		t.Fatalf("events[0].Snapshot.Tools = %#v", events[0].Snapshot.Tools)
	}
}

type stubSession struct{}

func (stubSession) Close() error { return nil }
func (stubSession) ListTools(context.Context, *sdkmcp.ListToolsParams) (*sdkmcp.ListToolsResult, error) {
	return &sdkmcp.ListToolsResult{Tools: []*sdkmcp.Tool{{Name: "tool"}}}, nil
}
func (stubSession) CallTool(context.Context, *sdkmcp.CallToolParams) (*sdkmcp.CallToolResult, error) {
	return &sdkmcp.CallToolResult{}, nil
}
func (stubSession) ListResources(context.Context, *sdkmcp.ListResourcesParams) (*sdkmcp.ListResourcesResult, error) {
	return &sdkmcp.ListResourcesResult{}, nil
}
func (stubSession) ListResourceTemplates(context.Context, *sdkmcp.ListResourceTemplatesParams) (*sdkmcp.ListResourceTemplatesResult, error) {
	return &sdkmcp.ListResourceTemplatesResult{}, nil
}
func (stubSession) ReadResource(context.Context, *sdkmcp.ReadResourceParams) (*sdkmcp.ReadResourceResult, error) {
	return &sdkmcp.ReadResourceResult{}, nil
}

type toolsOnlySession struct {
	stubSession
}

type mutableToolSession struct {
	stubSession
	mu    sync.Mutex
	tools [][]*sdkmcp.Tool
	calls int
}

func (s *mutableToolSession) ListTools(context.Context, *sdkmcp.ListToolsParams) (*sdkmcp.ListToolsResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.calls
	if index >= len(s.tools) {
		index = len(s.tools) - 1
	}
	s.calls++
	return &sdkmcp.ListToolsResult{Tools: s.tools[index]}, nil
}

func (toolsOnlySession) ListTools(context.Context, *sdkmcp.ListToolsParams) (*sdkmcp.ListToolsResult, error) {
	return &sdkmcp.ListToolsResult{Tools: []*sdkmcp.Tool{{Name: "resolve_library_id"}}}, nil
}
func (toolsOnlySession) ListResources(context.Context, *sdkmcp.ListResourcesParams) (*sdkmcp.ListResourcesResult, error) {
	return nil, context.DeadlineExceeded
}
func (toolsOnlySession) ListResourceTemplates(context.Context, *sdkmcp.ListResourceTemplatesParams) (*sdkmcp.ListResourceTemplatesResult, error) {
	return nil, context.DeadlineExceeded
}

func newTestClientSession(t *testing.T, ctx context.Context) clientSession {
	t.Helper()

	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "context7", Version: "v1.0.0"}, nil)
	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name:        "resolve_library_id",
		Description: "Resolve a Context7 library id.",
	}, func(_ context.Context, _ *sdkmcp.CallToolRequest, in struct {
		LibraryName string `json:"library_name" jsonschema:"Library name to resolve"`
	}) (*sdkmcp.CallToolResult, any, error) {
		return &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "react-id"}},
		}, nil, nil
	})
	server.AddResource(&sdkmcp.Resource{
		Name:        "docs",
		URI:         "context7://docs",
		Description: "Forge docs",
		MIMEType:    "text/plain",
	}, func(context.Context, *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
		return &sdkmcp.ReadResourceResult{
			Contents: []*sdkmcp.ResourceContents{{
				URI:      "context7://docs",
				MIMEType: "text/plain",
				Text:     "forge docs",
			}},
		}, nil
	})
	server.AddResourceTemplate(&sdkmcp.ResourceTemplate{
		Name:        "library-docs",
		URITemplate: "context7://library/{name}",
		Description: "Library docs",
		MIMEType:    "text/plain",
	}, func(context.Context, *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
		return &sdkmcp.ReadResourceResult{}, nil
	})

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "forge", Version: "test"}, nil)
	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect() error = %v", err)
	}
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect() error = %v", err)
	}
	return &wrappedSession{ClientSession: clientSession, serverSession: serverSession}
}

type wrappedSession struct {
	*sdkmcp.ClientSession
	serverSession *sdkmcp.ServerSession
}

func (s *wrappedSession) Close() error {
	err := s.ClientSession.Close()
	_ = s.serverSession.Close()
	return err
}

func boolPtr(v bool) *bool { return &v }
