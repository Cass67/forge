package drivers_test

import (
	"testing"

	"forge/internal/llm/drivers"
)

func TestOpenAIDriverName(t *testing.T) {
	d := drivers.NewOpenAI("sk-test", "gpt-4o")
	if d.Name() != "gpt-4o" {
		t.Fatalf("unexpected name: %s", d.Name())
	}
}
