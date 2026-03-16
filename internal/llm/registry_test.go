package llm_test

import (
	"context"
	"testing"
	"forge/internal/llm"
)

type mockDriver struct{ name string }

func (m *mockDriver) Name() string { return m.name }
func (m *mockDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	close(out)
	return nil
}

func TestRegisterAndLookup(t *testing.T) {
	reg := llm.NewRegistry()
	d := &mockDriver{name: "mock-model"}
	reg.Register(d)

	got, err := reg.Lookup("mock-model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name() != "mock-model" {
		t.Fatalf("expected mock-model, got %s", got.Name())
	}
}

func TestLookupMissing(t *testing.T) {
	reg := llm.NewRegistry()
	_, err := reg.Lookup("nonexistent")
	if err == nil {
		t.Fatal("expected error for missing driver")
	}
}

func TestListNames(t *testing.T) {
	reg := llm.NewRegistry()
	reg.Register(&mockDriver{name: "a"})
	reg.Register(&mockDriver{name: "b"})
	names := reg.Names()
	if len(names) != 2 {
		t.Fatalf("expected 2 names, got %d", len(names))
	}
}
