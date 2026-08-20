package tools

import (
	"context"
	"errors"
	"fmt"

	"forge/internal/llm"
	"forge/internal/mcp"
)

type mcpManager interface {
	Tools() []mcp.Tool
	Resources() []mcp.Resource
	ResourceTemplates() []mcp.ResourceTemplate
	Status() []mcp.ServerStatus
	CallTool(ctx context.Context, serverName, toolName string, args map[string]any) (mcp.ToolResult, error)
	ReadResource(ctx context.Context, serverName, uri string) (mcp.Resource, error)
}

func NewMCPDynamicTool(def mcp.Tool, manager mcpManager) Tool {
	return Tool{
		Name:        namespacedMCPToolName(def.ServerName, def.Name),
		Description: def.Description,
		Parameters:  toolParamsFromLLM(def.Parameters),
		Schema:      def.Schema,
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			result, err := manager.CallTool(ctx, def.ServerName, def.Name, args)
			if err != nil {
				return "", err
			}
			return encodeToolJSON(result)
		},
	}
}

// mcpListing wraps a listing with server status. A bare "null" reply told the
// model nothing was configured when the truth might be a connected server that
// simply exposes no resources, or one that failed to start.
type mcpListing struct {
	Servers []mcp.ServerStatus `json:"servers"`
	Note    string             `json:"note,omitempty"`
	Items   any                `json:"items"`
}

func newMCPListing(manager mcpManager, items any, count int, kind string) (string, error) {
	listing := mcpListing{Servers: manager.Status(), Items: items}
	switch {
	case len(listing.Servers) == 0:
		listing.Note = "no MCP servers configured; add one under [mcp_servers] in the forge config"
	case count == 0:
		listing.Note = fmt.Sprintf("no %s exposed; check the per-server status above, and note that a server can expose tools (called directly as mcp__<server>__<tool>) while exposing no %s", kind, kind)
	}
	return encodeToolJSON(listing)
}

func NewListMCPResources(manager mcpManager) Tool {
	return Tool{
		Name:        "list_mcp_resources",
		Description: "List resources exposed by configured MCP servers, with per-server connection status.",
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			_ = ctx
			_ = args
			resources := manager.Resources()
			if resources == nil {
				resources = []mcp.Resource{}
			}
			return newMCPListing(manager, resources, len(resources), "resources")
		},
	}
}

func NewListMCPResourceTemplates(manager mcpManager) Tool {
	return Tool{
		Name:        "list_mcp_resource_templates",
		Description: "List parameterized resource templates exposed by configured MCP servers, with per-server connection status.",
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			_ = ctx
			_ = args
			templates := manager.ResourceTemplates()
			if templates == nil {
				templates = []mcp.ResourceTemplate{}
			}
			return newMCPListing(manager, templates, len(templates), "resource templates")
		},
	}
}

func NewReadMCPResource(manager mcpManager) Tool {
	return Tool{
		Name:        "read_mcp_resource",
		Description: "Read a resource exposed by a configured MCP server.",
		Parameters: []ParameterDef{
			{Name: "server", Type: "string", Description: "configured MCP server name", Required: true},
			{Name: "uri", Type: "string", Description: "resource URI", Required: true},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			server, _ := args["server"].(string)
			uri, _ := args["uri"].(string)
			resource, err := manager.ReadResource(ctx, server, uri)
			if err != nil {
				if errors.Is(err, mcp.ErrNotFound) {
					return fmt.Sprintf("MCP resource not found: server=%s uri=%s", server, uri), nil
				}
				return "", err
			}
			return encodeToolJSON(resource)
		},
	}
}

func namespacedMCPToolName(server, tool string) string {
	return "mcp__" + server + "__" + tool
}

func toolParamsFromLLM(params []llm.ToolParam) []ParameterDef {
	out := make([]ParameterDef, 0, len(params))
	for _, p := range params {
		out = append(out, ParameterDef{
			Name:        p.Name,
			Type:        reverseMapParamType(p.Type),
			Description: p.Description,
			Required:    p.Required,
		})
	}
	return out
}

func reverseMapParamType(t string) string {
	switch t {
	case "integer":
		return "int"
	case "boolean":
		return "bool"
	default:
		return "string"
	}
}
