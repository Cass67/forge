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
