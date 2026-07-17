package react

import (
	"context"
	"testing"

	agenttools "forge/internal/agent/tools"
	"forge/internal/llm"
)

func newLeanTestRunner() *Runner {
	reg := agenttools.NewRegistry()
	noop := func(context.Context, map[string]any) (string, error) { return "ok", nil }
	for _, name := range []string{"read_file", "run_command", "lsp_hover", "web_search"} {
		reg.Register(agenttools.Tool{Name: name, Execute: noop})
	}
	return NewRunner(Config{Session: NewSession(), Tools: reg, LeanToolExposure: true})
}

func TestLeanExposureFiltersToolDefs(t *testing.T) {
	r := newLeanTestRunner()
	defs, decision := r.selectToolDefsWithDecision(r.session.Snapshot())
	if decision.Reason != "lean" {
		t.Fatalf("reason = %q, want lean", decision.Reason)
	}
	names := toolNamesFromDefs(defs)
	for _, name := range names {
		if name == "lsp_hover" || name == "web_search" {
			t.Fatalf("deferred tool %q exposed in lean defs %v", name, names)
		}
	}
	if len(names) != 2 {
		t.Fatalf("lean defs = %v, want read_file and run_command only", names)
	}
}

func TestLeanExposureAllowsDeferredRegisteredCalls(t *testing.T) {
	r := newLeanTestRunner()
	defs, _ := r.selectToolDefsWithDecision(r.session.Snapshot())
	calls := []llm.NativeToolCall{{Name: "lsp_hover"}}
	if err := r.rejectUnknownNativeToolCalls(context.Background(), 1, calls, defs); err != nil {
		t.Fatalf("deferred registered tool rejected: %v", err)
	}
	if err := r.rejectUnknownNativeToolCalls(context.Background(), 1, []llm.NativeToolCall{{Name: "no_such_tool"}}, defs); err == nil {
		t.Fatal("unknown tool accepted")
	}
}
