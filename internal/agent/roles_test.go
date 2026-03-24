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
		"SEARCH / TRACE / ORIGIN QUESTIONS → delegate to scout",
		"Do not turn scout search results into a new architect or builder task unless the flow explicitly requires it.",
		"Never use builder to gather repo-review evidence, reconstruct missing repo-review scratchpad context, or format repo-review recommendations.",
		"Take one orchestration action per turn.",
		"scratchpad_write may only persist raw sub-agent output or raw scratchpad content.",
		"Never delegate architect for repo-review synthesis if the latest scout result was blocked, incomplete, cancelled, or errored.",
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
		"For repo-review tasks, gather a bounded evidence set and then stop.",
		"Never inspect runtime-generated conversation artifacts such as debug logs, scratchpad files, session histories, or session logs unless the task explicitly asks for them.",
		"Your first working turn for an evidence-gathering task must contain tool calls, not a search plan.",
		"Do not return a blocked or \"I couldn't verify\" answer before using the relevant search/read tools available to you.",
		"If a delegated search is yours, own it to completion.",
	} {
		if !strings.Contains(scoutPrompt, want) {
			t.Fatalf("scout prompt missing %q", want)
		}
	}
	if strings.Contains(scoutPrompt, "RECOMMENDATION: [what to do next, if applicable]") {
		t.Fatalf("scout prompt still asks for recommendations: %q", scoutPrompt)
	}
}

func TestArchitectPromptRequiresBlockedResultWhenEvidenceIsIncomplete(t *testing.T) {
	for _, want := range []string{
		"If the evidence is incomplete, stale, placeholder-only, or derived from an agent error, do not synthesize recommendations.",
		"Return a short blocked result that states exactly what evidence is missing.",
	} {
		if !strings.Contains(architectPrompt, want) {
			t.Fatalf("architect prompt missing %q", want)
		}
	}
}
