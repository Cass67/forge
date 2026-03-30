package tools

import (
	"context"
	"testing"
)

type fakeLSPService struct{}

func (fakeLSPService) Definition(_ context.Context, _, path string, line, column int) (string, error) {
	return path + ":definition", nil
}
func (fakeLSPService) References(_ context.Context, _, path string, line, column int, includeDeclaration bool) (string, error) {
	return path + ":references", nil
}
func (fakeLSPService) Hover(_ context.Context, _, path string, line, column int) (string, error) {
	return "hover", nil
}
func (fakeLSPService) DocumentSymbols(_ context.Context, _, path string) (string, error) {
	return "symbols", nil
}

func TestLSPDefinitionToolUsesService(t *testing.T) {
	oldFactory := newLSPService
	newLSPService = func() lspService { return fakeLSPService{} }
	defer func() { newLSPService = oldFactory }()

	dir := t.TempDir()
	tool := NewLSPDefinition(dir)
	result, err := tool.Execute(context.Background(), map[string]any{
		"path":   "main.go",
		"line":   3.0,
		"column": 6.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == "" || result == "hover" {
		t.Fatalf("unexpected result: %q", result)
	}
}

func TestLSPHoverToolUsesService(t *testing.T) {
	oldFactory := newLSPService
	newLSPService = func() lspService { return fakeLSPService{} }
	defer func() { newLSPService = oldFactory }()

	dir := t.TempDir()
	tool := NewLSPHover(dir)
	result, err := tool.Execute(context.Background(), map[string]any{
		"path":   "main.go",
		"line":   3.0,
		"column": 6.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != "hover" {
		t.Fatalf("result = %q", result)
	}
}
