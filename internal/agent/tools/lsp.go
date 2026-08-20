package tools

import (
	"context"
	"fmt"

	"forge/internal/lsp"
)

type lspService interface {
	Definition(ctx context.Context, workDir, path string, line, column int) (string, error)
	References(ctx context.Context, workDir, path string, line, column int, includeDeclaration bool) (string, error)
	Hover(ctx context.Context, workDir, path string, line, column int) (string, error)
	DocumentSymbols(ctx context.Context, workDir, path string) (string, error)
}

// The shared service pools language servers; a per-call service would spawn and
// kill a fresh gopls every time, paying the cold index on every request.
var newLSPService = func() lspService { return lsp.Shared() }

func NewLSPDefinition(workDir string) Tool {
	return Tool{
		Name:        "lsp_definition",
		Description: "Resolve the symbol definition at a file position using an external language server when available.",
		Parameters: []ParameterDef{
			{Name: "path", Type: "string", Description: "source file path", Required: true},
			{Name: "line", Type: "int", Description: "1-based line number", Required: true},
			{Name: "column", Type: "int", Description: "1-based column number", Required: true},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			return runLSPPositionTool(ctx, workDir, args, func(svc lspService, path string, line, column int) (string, error) {
				return svc.Definition(ctx, workDir, path, line, column)
			})
		},
	}
}

func NewLSPReferences(workDir string) Tool {
	return Tool{
		Name:        "lsp_references",
		Description: "Find references to the symbol at a file position using an external language server when available.",
		Parameters: []ParameterDef{
			{Name: "path", Type: "string", Description: "source file path", Required: true},
			{Name: "line", Type: "int", Description: "1-based line number", Required: true},
			{Name: "column", Type: "int", Description: "1-based column number", Required: true},
			{Name: "include_declaration", Type: "bool", Description: "include the defining location in results", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			includeDeclaration := false
			if value, ok := args["include_declaration"].(bool); ok {
				includeDeclaration = value
			}
			return runLSPPositionTool(ctx, workDir, args, func(svc lspService, path string, line, column int) (string, error) {
				return svc.References(ctx, workDir, path, line, column, includeDeclaration)
			})
		},
	}
}

func NewLSPHover(workDir string) Tool {
	return Tool{
		Name:        "lsp_hover",
		Description: "Show hover information for the symbol at a file position using an external language server when available.",
		Parameters: []ParameterDef{
			{Name: "path", Type: "string", Description: "source file path", Required: true},
			{Name: "line", Type: "int", Description: "1-based line number", Required: true},
			{Name: "column", Type: "int", Description: "1-based column number", Required: true},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			return runLSPPositionTool(ctx, workDir, args, func(svc lspService, path string, line, column int) (string, error) {
				return svc.Hover(ctx, workDir, path, line, column)
			})
		},
	}
}

func NewLSPDocumentSymbols(workDir string) Tool {
	return Tool{
		Name:        "lsp_document_symbols",
		Description: "List document symbols for a source file using an external language server when available.",
		Parameters: []ParameterDef{
			{Name: "path", Type: "string", Description: "source file path", Required: true},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			path, _ := args["path"].(string)
			resolved, err := ResolvePath(workDir, path)
			if err != nil {
				return "", err
			}
			out, err := newLSPService().DocumentSymbols(ctx, workDir, resolved)
			if err != nil {
				return fmt.Sprintf("lsp_document_symbols failed: %v", err), nil
			}
			return out, nil
		},
	}
}

func runLSPPositionTool(ctx context.Context, workDir string, args map[string]any, run func(lspService, string, int, int) (string, error)) (string, error) {
	path, _ := args["path"].(string)
	line, okLine := numericArg(args["line"])
	column, okColumn := numericArg(args["column"])
	if path == "" || !okLine || !okColumn {
		return "lsp tool failed: path, line, and column are required", nil
	}
	resolved, err := ResolvePath(workDir, path)
	if err != nil {
		return "", err
	}
	out, err := run(newLSPService(), resolved, line, column)
	if err != nil {
		return fmt.Sprintf("lsp tool failed: %v", err), nil
	}
	return out, nil
}

func numericArg(value any) (int, bool) {
	switch typed := value.(type) {
	case float64:
		return int(typed), true
	case int:
		return typed, true
	default:
		return 0, false
	}
}
