package tools

import (
	"context"
	"strings"
	"testing"
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
