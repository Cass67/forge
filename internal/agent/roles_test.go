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
		"Choose the next specialist using judgment, not rigid scripts:",
		"Repo review or improvement requests usually start with scout for evidence, then architect for synthesis.",
		`Prefer letting the current specialist request the next specialist through its structured JSON result ("next_role", "next_task")`,
		"You may delegate to the same role again when the task calls for a narrower retry, another pass, or follow-up work.",
		"Take one orchestration action per turn.",
		"scratchpad_write may only persist raw sub-agent output or raw scratchpad content.",
	} {
		if !strings.Contains(dispatchPrompt, want) {
			t.Fatalf("dispatch prompt missing %q", want)
		}
	}
}

func TestScoutPromptKeepsRecommendationsOutOfScoutResponses(t *testing.T) {
	for _, want := range []string{
		"Do not recommend code changes, plans, or prioritization.",
		"For repo-review tasks, gather a bounded evidence set and then stop.",
		"Never inspect runtime-generated conversation artifacts such as debug logs, scratchpad files, session histories, or session logs unless the task explicitly asks for them.",
		"Your first working turn for an evidence-gathering task must contain tool calls, not a search plan.",
		"Do not return a blocked or \"I couldn't verify\" answer before using the relevant search/read tools available to you.",
		"If a delegated search is yours, own it to completion.",
		`{"status":"complete|blocked","message":"concise user-visible summary","artifact_kind":"evidence","artifact":"detailed evidence with file paths and line numbers","next_role":"","next_task":""}`,
		`Set next_role and next_task only when another role must act immediately in the same user turn.`,
	} {
		if !strings.Contains(scoutPrompt, want) {
			t.Fatalf("scout prompt missing %q", want)
		}
	}
	if strings.Contains(scoutPrompt, "RECOMMENDATION: [what to do next, if applicable]") {
		t.Fatalf("scout prompt still asks for recommendations: %q", scoutPrompt)
	}
}

func TestScoutPromptRequiresWrappedFirstToolCall(t *testing.T) {
	if !strings.Contains(scoutPrompt, "exactly one valid <tool_call>...</tool_call> block and nothing else") {
		t.Fatal("scout prompt missing exact wrapped first tool call rule")
	}
	if !strings.Contains(scoutPrompt, "Never emit a bare JSON tool call") {
		t.Fatal("scout prompt missing bare JSON tool call prohibition")
	}
}

func TestBuilderPromptRequiresActionFirstAndVerificationDiscipline(t *testing.T) {
	for _, want := range []string{
		"Your first working turn must contain tool calls or edits, not a plan.",
		"If task context already contains scout or architect findings, use that context as your starting point instead of re-running the same broad discovery.",
		"End-to-end verification is part of the task, not optional cleanup.",
		"Do not claim a fix without evidence from commands or test output.",
		"Do not restart a repo-wide search if scout or architect already handed you the relevant files or findings.",
		`{"status":"complete|blocked","message":"concise user-visible summary","artifact_kind":"implementation","artifact":"files changed, verification run, and any details the next role needs","next_role":"","next_task":""}`,
	} {
		if !strings.Contains(builderPrompt, want) {
			t.Fatalf("builder prompt missing %q", want)
		}
	}
}

func TestDoctorPromptRequiresEvidenceBeforeDiagnosis(t *testing.T) {
	for _, want := range []string{
		"Core rule: no diagnosis without evidence.",
		"Your first working turn for a debugging task must contain tool calls, not a theory.",
		"Do not hand back a debugging plan when you still have read-only tools available to investigate.",
		"Recommend a fix only after you can state a root cause tied to concrete evidence.",
		"If you cannot reach root cause, return a blocked diagnostic result that says exactly what evidence is missing.",
		`{"status":"complete|blocked","message":"concise user-visible diagnosis","artifact_kind":"diagnosis","artifact":"ROOT CAUSE, EVIDENCE, FIX, and RISK in a compact markdown block","next_role":"","next_task":""}`,
		`Set next_role:"builder" and a concrete next_task only when a builder should act immediately in the same user turn.`,
	} {
		if !strings.Contains(doctorPrompt, want) {
			t.Fatalf("doctor prompt missing %q", want)
		}
	}
}

func TestArchitectPromptRequiresBlockedResultWhenEvidenceIsIncomplete(t *testing.T) {
	for _, want := range []string{
		"Your first working turn should use read/search tools unless the provided context is already sufficient to plan from directly.",
		"If scout or doctor already produced the relevant evidence, synthesize from that evidence instead of reopening the investigation.",
		"Do not turn gathered findings into generic user-facing prose; your job is plan structure, prioritization, and decision framing.",
		"Produce the minimum viable plan that gets the job done.",
		"If the evidence is incomplete, stale, placeholder-only, or derived from an agent error, do not synthesize recommendations.",
		"Return a short blocked result that states exactly what evidence is missing.",
		`{"status":"complete|blocked","message":"concise user-visible summary","artifact_kind":"plan","artifact":"GOAL, ASSUMPTIONS, STEPS, PARALLEL, and RISKS as markdown","next_role":"","next_task":""}`,
		`Set next_role and next_task only when the current task explicitly requires immediate builder follow-through.`,
	} {
		if !strings.Contains(architectPrompt, want) {
			t.Fatalf("architect prompt missing %q", want)
		}
	}
}
