package tools

import (
	"context"
	"strings"
	"testing"

	"forge/internal/llm"
	"forge/internal/mcp"
)

type fakeMCPManager struct {
	tools             []mcp.Tool
	resources         []mcp.Resource
	resourceTemplates []mcp.ResourceTemplate
	callResult        mcp.ToolResult
	status            []mcp.ServerStatus
}

func (f fakeMCPManager) Status() []mcp.ServerStatus { return f.status }

func (f fakeMCPManager) Tools() []mcp.Tool                         { return f.tools }
func (f fakeMCPManager) Resources() []mcp.Resource                 { return f.resources }
func (f fakeMCPManager) ResourceTemplates() []mcp.ResourceTemplate { return f.resourceTemplates }
func (f fakeMCPManager) CallTool(ctx context.Context, serverName, toolName string, args map[string]any) (mcp.ToolResult, error) {
	_ = ctx
	_ = serverName
	_ = toolName
	_ = args
	return f.callResult, nil
}
func (f fakeMCPManager) ReadResource(ctx context.Context, serverName, uri string) (mcp.Resource, error) {
	_ = ctx
	for _, resource := range f.resources {
		if resource.ServerName == serverName && resource.URI == uri {
			return resource, nil
		}
	}
	return mcp.Resource{}, mcp.ErrNotFound
}

func TestNewListMCPResources(t *testing.T) {
	tool := NewListMCPResources(fakeMCPManager{
		resources: []mcp.Resource{{ServerName: "context7", URI: "context7://docs", Name: "docs"}},
	})
	result, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "context7://docs") {
		t.Fatalf("result = %q", result)
	}
}

func TestNewReadMCPResource(t *testing.T) {
	tool := NewReadMCPResource(fakeMCPManager{
		resources: []mcp.Resource{{ServerName: "context7", URI: "context7://docs", Name: "docs", Content: "hello"}},
	})
	result, err := tool.Execute(context.Background(), map[string]any{
		"server": "context7",
		"uri":    "context7://docs",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "\"Content\":\"hello\"") {
		t.Fatalf("result = %q", result)
	}
}

func TestNewMCPDynamicToolUsesNamespacedName(t *testing.T) {
	schema := &llm.ToolSchema{
		Type: "object",
		Properties: map[string]*llm.ToolSchema{
			"payload": {Type: "object"},
		},
	}
	tool := NewMCPDynamicTool(mcp.Tool{
		ServerName:  "context7",
		Name:        "resolve_library_id",
		Description: "Resolve a library identifier.",
		Schema:      schema,
		Parameters: []llm.ToolParam{
			{Name: "library_name", Type: "string", Required: true},
		},
	}, fakeMCPManager{})
	if tool.Name != "mcp__context7__resolve_library_id" {
		t.Fatalf("tool.Name = %q", tool.Name)
	}
	if len(tool.Parameters) != 1 || tool.Parameters[0].Name != "library_name" {
		t.Fatalf("tool.Parameters = %#v", tool.Parameters)
	}
	if tool.Schema != schema {
		t.Fatalf("tool.Schema = %#v, want original MCP schema", tool.Schema)
	}
}

func TestNewMCPDynamicToolExecutesCall(t *testing.T) {
	tool := NewMCPDynamicTool(mcp.Tool{
		ServerName: "context7",
		Name:       "resolve_library_id",
	}, fakeMCPManager{
		callResult: mcp.ToolResult{
			ServerName: "context7",
			ToolName:   "resolve_library_id",
			Content:    []mcp.ContentItem{{Type: "text", Text: "ok"}},
		},
	})
	result, err := tool.Execute(context.Background(), map[string]any{"library_name": "react"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "\"tool\":\"resolve_library_id\"") {
		t.Fatalf("result = %q", result)
	}
}

func TestListMCPResourcesExplainsEmptyResults(t *testing.T) {
	connected := NewListMCPResources(fakeMCPManager{
		status: []mcp.ServerStatus{{Name: "jira", Connected: true, Tools: 12}},
	})
	result, err := connected.Execute(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(result) == "null" {
		t.Fatal("empty listing still reports a bare null")
	}
	for _, want := range []string{`"items":[]`, `"connected":true`, "jira", "mcp__"} {
		if !strings.Contains(result, want) {
			t.Fatalf("missing %q in %s", want, result)
		}
	}

	failed := NewListMCPResources(fakeMCPManager{
		status: []mcp.ServerStatus{{Name: "jira", Error: "connect timed out after 1m30s"}},
	})
	result, err = failed.Execute(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "connect timed out") {
		t.Fatalf("connection failure hidden from the model: %s", result)
	}

	none := NewListMCPResources(fakeMCPManager{})
	result, err = none.Execute(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "no MCP servers configured") {
		t.Fatalf("unconfigured case not distinguished: %s", result)
	}
}
