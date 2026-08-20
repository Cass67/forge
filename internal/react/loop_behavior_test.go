package react

import (
	"context"
	"fmt"
	"strings"
	"testing"

	agenttools "forge/internal/agent/tools"
	"forge/internal/llm"
)

// A model that never gives up on one failing call: does the loop nudge it,
// block it, and still terminate rather than burning the whole step budget?
func TestLoopHandlesModelThatIgnoresGuidance(t *testing.T) {
	const steps = 40
	seq := make([][]llm.Token, steps)
	for i := range seq {
		seq[i] = []llm.Token{{ToolCall: &llm.NativeToolCall{
			ID:       fmt.Sprintf("c%d", i+1),
			Name:     "run_command",
			ArgsJSON: `{"command":"npm exec -- node --version"}`,
		}}}
	}
	driver := &nativeSequenceDriver{steps: seq}

	execCalls := 0
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "run_command",
		Description: "run",
		Parameters:  []agenttools.ParameterDef{{Name: "command", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			execCalls++
			return "same unchanging output", nil
		},
	})

	session := NewSession()
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session, MaxSteps: steps})
	_ = r.Run(context.Background(), "check node")

	snap := session.Snapshot()
	var blocks int
	for _, msg := range snap.History {
		if strings.Contains(msg.Content, "blocked: identical") {
			blocks++
		}
	}
	// The nudge reaches the model as a system overlay, not as history.
	nudge := r.repeatWorkflow.overlayContent(r.toolThrashCircuitBreaker)
	t.Logf("executions=%d blocks=%d history=%d nudge=%q", execCalls, blocks, len(snap.History), nudge)

	if execCalls > repeatToolCallBlockThreshold {
		t.Errorf("tool executed %d times; the block should cap real executions at %d", execCalls, repeatToolCallBlockThreshold)
	}
	if blocks == 0 {
		t.Error("model never blocked despite repeating one call 40 times")
	}
	if !strings.Contains(nudge, "Loop detection") {
		t.Errorf("no loop-detection overlay offered to the model: %q", nudge)
	}
}
