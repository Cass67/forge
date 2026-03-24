package agent

import (
	"strings"
	"testing"
)

func TestDispatchPromptDoesNotTellDispatchToPresentResults(t *testing.T) {
	if strings.Contains(dispatchPrompt, "Present the sub-agent's result to the user") {
		t.Fatalf("dispatch prompt still instructs result presentation: %q", dispatchPrompt)
	}
	for _, want := range []string{
		"Do not present, summarize, rewrite, or analyze sub-agent results yourself.",
		"If no further delegation is needed, stop. Do not add a prose answer.",
	} {
		if !strings.Contains(dispatchPrompt, want) {
			t.Fatalf("dispatch prompt missing %q", want)
		}
	}
}
