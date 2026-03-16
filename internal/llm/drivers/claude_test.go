package drivers_test

import (
	"testing"

	"forge/internal/llm/drivers"
)

func TestClaudeDriverName(t *testing.T) {
	d := drivers.NewClaude("sk-test", "claude-sonnet-4-6")
	if d.Name() != "claude-sonnet-4-6" {
		t.Fatalf("unexpected name: %s", d.Name())
	}
}
