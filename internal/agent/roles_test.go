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
		"REPO REVIEW / IMPROVEMENT REQUESTS → delegate to scout for evidence gathering first, then architect for synthesis.",
	} {
		if !strings.Contains(dispatchPrompt, want) {
			t.Fatalf("dispatch prompt missing %q", want)
		}
	}
}

func TestScoutPromptKeepsRecommendationsOutOfScoutResponses(t *testing.T) {
	for _, want := range []string{
		"Do not recommend code changes, plans, or prioritization.",
		"FOLLOW-UP: [what information is still needed or which role should handle synthesis]",
	} {
		if !strings.Contains(scoutPrompt, want) {
			t.Fatalf("scout prompt missing %q", want)
		}
	}
	if strings.Contains(scoutPrompt, "RECOMMENDATION: [what to do next, if applicable]") {
		t.Fatalf("scout prompt still asks for recommendations: %q", scoutPrompt)
	}
}
