package tools

import (
	"context"
	"strings"
	"testing"

	"forge/internal/llm"
)

func TestRegistryRegisterAndGet(t *testing.T) {
	reg := NewRegistry()
	tool := Tool{
		Name:        "test_tool",
		Description: "A test tool",
		Parameters: []ParameterDef{
			{Name: "arg1", Type: "string", Description: "first arg", Required: true},
		},
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			return "ok", nil
		},
	}
	reg.Register(tool)

	got, ok := reg.Get("test_tool")
	if !ok {
		t.Fatal("tool not found")
	}
	if got.Name != "test_tool" {
		t.Errorf("got name %q", got.Name)
	}

	_, ok = reg.Get("nonexistent")
	if ok {
		t.Error("found nonexistent tool")
	}
}

func TestRegistryDescribe(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Tool{
		Name:        "read_file",
		Description: "Read a file",
		Parameters: []ParameterDef{
			{Name: "path", Type: "string", Description: "file path", Required: true},
		},
	})
	desc := reg.Describe()
	if !strings.Contains(desc, "read_file") {
		t.Error("describe missing tool name")
	}
	if !strings.Contains(desc, "path") {
		t.Error("describe missing parameter")
	}
}

func TestRegistryDescribePromptHidesHiddenToolsByDefault(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Tool{Name: "read_file", Description: "Read a file"})
	reg.Register(Tool{Name: "tool_help", Description: "Reveal specialized tools"})
	reg.Register(Tool{Name: "web_search", Description: "Search the web", PromptVisibility: PromptHidden})

	desc := reg.DescribeForPrompt()
	if !strings.Contains(desc, "read_file") {
		t.Fatal("missing core tool")
	}
	if !strings.Contains(desc, "tool_help") {
		t.Fatal("missing tool_help")
	}
	if strings.Contains(desc, "web_search") {
		t.Fatal("hidden tool should not be shown by default")
	}
}

func TestRegistryPromptExamplesUseConcreteToolCalls(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Tool{
		Name:        "read_file",
		Description: "Read a file",
		Parameters: []ParameterDef{
			{Name: "path", Type: "string", Description: "file path", Required: true},
		},
	})

	check := func(name, desc string) {
		t.Helper()
		if strings.Contains(desc, `"name": "tool_name"`) {
			t.Fatalf("%s should not teach a placeholder tool name: %s", name, desc)
		}
		if strings.Contains(desc, `"param": "value"`) {
			t.Fatalf("%s should not teach placeholder args: %s", name, desc)
		}
		if !strings.Contains(desc, `"read_file"`) {
			t.Fatalf("%s should show a concrete registered tool example: %s", name, desc)
		}
		if !strings.Contains(desc, `"path"`) {
			t.Fatalf("%s should show a real parameter name: %s", name, desc)
		}
	}

	check("DescribeForPrompt", reg.DescribeForPrompt())
	check("DescribeForSingleToolPrompt", reg.DescribeForSingleToolPrompt())
}

func TestRegistryRevealTools(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Tool{Name: "tool_help", Description: "Reveal specialized tools"})
	reg.Register(Tool{Name: "git_commit", Description: "Create a git commit", PromptVisibility: PromptHidden})
	reg.Register(Tool{Name: "web_search", Description: "Search the web", PromptVisibility: PromptHidden})

	revealed := reg.RevealMatchingTools("commit changes")
	if len(revealed) != 1 || revealed[0].Name != "git_commit" {
		t.Fatalf("unexpected revealed tools: %#v", revealed)
	}

	desc := reg.DescribeForPrompt()
	if !strings.Contains(desc, "git_commit") {
		t.Fatal("revealed tool missing from prompt")
	}
	if strings.Contains(desc, "web_search") {
		t.Fatal("unrevealed hidden tool should stay hidden")
	}
}

func TestRegistryFilterRebindsToolHelpToFilteredRegistry(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Tool{Name: "read_file", Description: "Read a file"})
	reg.Register(NewToolHelp(reg))
	reg.Register(Tool{Name: "write_file", Description: "Write a file", PromptVisibility: PromptHidden})

	filtered := reg.Filter([]string{"read_file", "tool_help"})
	help, ok := filtered.Get("tool_help")
	if !ok {
		t.Fatal("filtered registry missing tool_help")
	}
	result, err := help.Execute(context.Background(), map[string]any{"query": "write_file"})
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(result, "write_file") {
		t.Fatalf("filtered tool_help revealed unavailable tool: %s", result)
	}
	if _, ok := filtered.Get("write_file"); ok {
		t.Fatal("filtered registry should not contain write_file")
	}
}

func TestRegistryNeedsApproval(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Tool{Name: "read_file", AutoApprove: true})
	reg.Register(Tool{Name: "write_file", AutoApprove: false})

	r, _ := reg.Get("read_file")
	if !r.AutoApprove {
		t.Error("read_file should auto-approve")
	}
	w, _ := reg.Get("write_file")
	if w.AutoApprove {
		t.Error("write_file should not auto-approve")
	}
}

func TestRegistryAll(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Tool{Name: "first"})
	reg.Register(Tool{Name: "second"})
	reg.Register(Tool{Name: "third"})

	all := reg.All()
	if len(all) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(all))
	}
	if all[0].Name != "first" || all[1].Name != "second" || all[2].Name != "third" {
		t.Errorf("tools not in registration order: %v", []string{all[0].Name, all[1].Name, all[2].Name})
	}
}

func TestToLLMToolDefsBasic(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Tool{
		Name:        "read_file",
		Description: "Read a file",
		Parameters: []ParameterDef{
			{Name: "path", Type: "string", Description: "file path", Required: true},
			{Name: "start_line", Type: "int", Description: "start line", Required: false},
		},
	})

	defs := reg.ToLLMToolDefs()
	if len(defs) != 1 {
		t.Fatalf("want 1 def, got %d", len(defs))
	}
	d := defs[0]
	if d.Name != "read_file" {
		t.Fatalf("name = %q, want read_file", d.Name)
	}
	if len(d.Parameters) != 2 {
		t.Fatalf("params = %d, want 2", len(d.Parameters))
	}
	if d.Parameters[0].Name != "path" || !d.Parameters[0].Required {
		t.Fatal("first param should be path (required)")
	}
	if d.Parameters[1].Name != "start_line" || d.Parameters[1].Required {
		t.Fatal("second param should be start_line (optional)")
	}
}

func TestToLLMToolDefsPreservesStructuredSchema(t *testing.T) {
	additional := false
	reg := NewRegistry()
	reg.Register(Tool{
		Name: "update_plan",
		Schema: &llm.ToolSchema{
			Type: "object",
			Properties: map[string]*llm.ToolSchema{
				"steps": {Type: "array", Items: &llm.ToolSchema{Type: "object"}},
			},
			Required:             []string{"steps"},
			AdditionalProperties: &additional,
		},
	})

	defs := reg.ToLLMToolDefs()
	if len(defs) != 1 {
		t.Fatalf("want 1 def, got %d", len(defs))
	}
	if defs[0].Schema == nil || defs[0].Schema.Properties["steps"].Type != "array" {
		t.Fatalf("schema not preserved: %#v", defs[0].Schema)
	}
}

func TestToLLMToolDefsIncludesToolHelp(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Tool{Name: "read_file", Description: "read"})
	reg.Register(Tool{Name: "tool_help", Description: "meta"})

	defs := reg.ToLLMToolDefs()
	foundToolHelp := false
	for _, d := range defs {
		if d.Name == "tool_help" {
			foundToolHelp = true
		}
	}
	if !foundToolHelp {
		t.Fatal("tool_help should be included in native tool defs")
	}
	if len(defs) != 2 {
		t.Fatalf("want 2 defs including tool_help, got %d", len(defs))
	}
}

func TestToLLMToolDefsTypeMapping(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Tool{
		Name: "write_file",
		Parameters: []ParameterDef{
			{Name: "path", Type: "string", Required: true},
			{Name: "line_count", Type: "int", Required: false},
			{Name: "overwrite", Type: "bool", Required: false},
		},
	})
	defs := reg.ToLLMToolDefs()
	if len(defs) != 1 {
		t.Fatalf("want 1 def, got %d", len(defs))
	}
	params := map[string]string{}
	for _, p := range defs[0].Parameters {
		params[p.Name] = p.Type
	}
	if params["path"] != "string" {
		t.Fatalf("string type mismatch, got %q", params["path"])
	}
	if params["line_count"] != "integer" {
		t.Fatalf("int should map to integer, got %q", params["line_count"])
	}
	if params["overwrite"] != "boolean" {
		t.Fatalf("bool should map to boolean, got %q", params["overwrite"])
	}
}
