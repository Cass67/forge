package cli

import "testing"

func TestDispatchKnownCommandRunsCommand(t *testing.T) {
	ran := false
	Dispatch([]string{"test"}, map[string]Command{
		"test": {Name: "test", Run: func(args []string) { ran = true }},
	}, func() {
		t.Fatal("fallback should not run")
	})
	if !ran {
		t.Fatal("expected command to run")
	}
}

func TestDispatchUnknownCommandFallsBack(t *testing.T) {
	fellBack := false
	Dispatch([]string{"unknown"}, map[string]Command{}, func() {
		fellBack = true
	})
	if !fellBack {
		t.Fatal("expected fallback to run")
	}
}
