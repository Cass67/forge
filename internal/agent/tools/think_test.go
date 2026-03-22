package tools

import (
	"context"
	"testing"
)

func TestThink(t *testing.T) {
	tool := NewThink()
	if tool.Name != "think" {
		t.Fatalf("expected name 'think', got %q", tool.Name)
	}
	if !tool.AutoApprove {
		t.Fatal("think should be auto-approved")
	}
	result, err := tool.Execute(context.Background(), map[string]any{
		"thought": "I need to check the database schema first",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != "ok" {
		t.Errorf("expected 'ok', got %q", result)
	}
}

func TestThinkEmpty(t *testing.T) {
	tool := NewThink()
	result, err := tool.Execute(context.Background(), map[string]any{
		"thought": "",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != "ok" {
		t.Errorf("expected 'ok', got %q", result)
	}
}
