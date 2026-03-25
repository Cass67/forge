package agent

import "testing"

func TestParseDelegateOutcomeStructuredEnvelope(t *testing.T) {
	outcome := parseDelegateOutcome(`{"status":"complete","message":"done","artifact_kind":"plan","artifact":"plan body","next_role":"builder","next_task":"implement it"}`)
	if !outcome.Structured {
		t.Fatalf("expected structured outcome")
	}
	if !outcome.Completed() {
		t.Fatalf("expected completed outcome")
	}
	if outcome.DisplayText() != "done" {
		t.Fatalf("display text = %q, want %q", outcome.DisplayText(), "done")
	}
	if outcome.ContextText() != "plan body" {
		t.Fatalf("context text = %q, want %q", outcome.ContextText(), "plan body")
	}
	if outcome.NextRole != "builder" || outcome.NextTask != "implement it" {
		t.Fatalf("unexpected next step: role=%q task=%q", outcome.NextRole, outcome.NextTask)
	}
}

func TestParseDelegateOutcomePlainTextIsTerminalComplete(t *testing.T) {
	outcome := parseDelegateOutcome("No code change is indicated.")
	if outcome.Structured {
		t.Fatalf("plain text outcome should not be structured")
	}
	if !outcome.Completed() {
		t.Fatalf("plain text outcome should be complete")
	}
	if outcome.Blocked() {
		t.Fatalf("plain text outcome should not be blocked")
	}
	if outcome.ContextText() != "No code change is indicated." {
		t.Fatalf("context text = %q", outcome.ContextText())
	}
}

func TestParseDelegateOutcomeAgentErrorIsBlocked(t *testing.T) {
	outcome := parseDelegateOutcome("AGENT ERROR (scout): malformed tool call")
	if !outcome.Blocked() {
		t.Fatalf("agent error should be blocked")
	}
	if outcome.Completed() {
		t.Fatalf("agent error should not be complete")
	}
}

func TestParseDelegateOutcomeStructuredEnvelopeStringifiesObjectArtifact(t *testing.T) {
	outcome := parseDelegateOutcome(`{"status":"complete","message":"Collected evidence.","artifact_kind":"evidence_summary","artifact":{"source_file":"util-rancid/update_cerner_daily.sh","source_lines":"743-753"},"next_role":"","next_task":""}`)
	if !outcome.Structured {
		t.Fatalf("expected structured outcome")
	}
	if !outcome.Completed() {
		t.Fatalf("expected completed outcome")
	}
	if outcome.DisplayText() != "Collected evidence." {
		t.Fatalf("display text = %q", outcome.DisplayText())
	}
	if want := `{"source_file":"util-rancid/update_cerner_daily.sh","source_lines":"743-753"}`; outcome.ContextText() != want {
		t.Fatalf("context text = %q, want %q", outcome.ContextText(), want)
	}
}

func TestParseDelegateOutcomeStructuredEnvelopeDropsInvalidNextRole(t *testing.T) {
	outcome := parseDelegateOutcome(`{"status":"complete","message":"Collected evidence.","artifact_kind":"evidence_summary","artifact":{"subject":"Rancid f5 objstor verify script missing"},"next_role":"user","next_task":"inspect repository scheduling files"}`)
	if !outcome.Structured {
		t.Fatalf("expected structured outcome")
	}
	if outcome.NextRole != "" || outcome.NextTask != "" {
		t.Fatalf("expected invalid next step to be dropped, got role=%q task=%q", outcome.NextRole, outcome.NextTask)
	}
}

func TestParseDelegateOutcomeStructuredEnvelopeNormalizesCompletedStatus(t *testing.T) {
	outcome := parseDelegateOutcome(`{"status":"completed","message":"Collected evidence.","artifact_kind":"evidence_summary","artifact":{"subject":"Rancid f5 objstor verify script missing"},"next_role":"none","next_task":"none"}`)
	if !outcome.Structured {
		t.Fatalf("expected structured outcome")
	}
	if !outcome.Completed() {
		t.Fatalf("expected completed outcome")
	}
	if outcome.NextRole != "" || outcome.NextTask != "" {
		t.Fatalf("expected invalid next step to be dropped, got role=%q task=%q", outcome.NextRole, outcome.NextTask)
	}
	if want := `{"subject":"Rancid f5 objstor verify script missing"}`; outcome.ContextText() != want {
		t.Fatalf("context text = %q, want %q", outcome.ContextText(), want)
	}
}

func TestParseDelegateOutcomeForRoleCoercesBareScoutJSONObject(t *testing.T) {
	raw := `{"source_file":"util-rancid/update_cerner_daily.sh","source_line":753,"message":"Found the alert source.","evidence":["mailx subject matches"]}`
	outcome := parseDelegateOutcomeForRole("scout", raw)
	if !outcome.Structured {
		t.Fatalf("expected structured outcome")
	}
	if !outcome.Completed() {
		t.Fatalf("expected completed outcome")
	}
	if outcome.DisplayText() != "Found the alert source." {
		t.Fatalf("display text = %q", outcome.DisplayText())
	}
	if outcome.ArtifactKind != "evidence" {
		t.Fatalf("artifact kind = %q", outcome.ArtifactKind)
	}
	if outcome.ContextText() != raw {
		t.Fatalf("context text = %q, want %q", outcome.ContextText(), raw)
	}
}

func TestParseDelegateOutcomeForRoleCoercesBareArchitectJSONObject(t *testing.T) {
	raw := `{"worry_level":"low_to_medium","actionability":"actionable","recommended_next_check":"Verify the expected verify script path exists and is executable."}`
	outcome := parseDelegateOutcomeForRole("architect", raw)
	if !outcome.Structured {
		t.Fatalf("expected structured outcome")
	}
	if !outcome.Completed() {
		t.Fatalf("expected completed outcome")
	}
	if outcome.DisplayText() != "Low-to-medium severity. Actionable. Next check: Verify the expected verify script path exists and is executable." {
		t.Fatalf("display text = %q", outcome.DisplayText())
	}
	if outcome.ArtifactKind != "plan" {
		t.Fatalf("artifact kind = %q", outcome.ArtifactKind)
	}
	if outcome.ContextText() != raw {
		t.Fatalf("context text = %q, want %q", outcome.ContextText(), raw)
	}
}

func TestParseDelegateOutcomeForRoleUsesScoutArtifactSummaryWhenMessageIsGeneric(t *testing.T) {
	outcome := parseDelegateOutcomeForRole("scout", `{"source_file":"util-rancid/update_cerner_daily.sh","source_line":753,"most_likely_trigger":"missing verify script at runtime"}`)
	if got := outcome.DisplayText(); got != "Source: util-rancid/update_cerner_daily.sh:753. Likely trigger: missing verify script at runtime." {
		t.Fatalf("display text = %q", got)
	}
}

func TestParseDelegateOutcomeStructuredEnvelopeKeepsSpecificMessageOverArtifactSummary(t *testing.T) {
	raw := `{"status":"complete","message":"The email comes from util-rancid/update_cerner_daily.sh:753.","artifact_kind":"evidence","artifact":{"source_file":"util-rancid/update_cerner_daily.sh","source_line":753,"most_likely_trigger":"missing verify script at runtime"}}`
	outcome := parseDelegateOutcomeForRole("scout", raw)
	if got := outcome.DisplayText(); got != "The email comes from util-rancid/update_cerner_daily.sh:753." {
		t.Fatalf("display text = %q", got)
	}
	if want := `{"source_file":"util-rancid/update_cerner_daily.sh","source_line":753,"most_likely_trigger":"missing verify script at runtime"}`; outcome.ContextText() != want {
		t.Fatalf("context text = %q, want %q", outcome.ContextText(), want)
	}
}

func TestParseDelegateOutcomeForRoleUsesDoctorArtifactSummaryWhenMessageIsGeneric(t *testing.T) {
	outcome := parseDelegateOutcomeForRole("doctor", `{"root_cause":"delegate parser rejected a bare JSON object","fix":"coerce bare JSON objects into typed delegate outcomes"}`)
	if got := outcome.DisplayText(); got != "Root cause: delegate parser rejected a bare JSON object. Fix: coerce bare JSON objects into typed delegate outcomes." {
		t.Fatalf("display text = %q", got)
	}
}

func TestParseDelegateOutcomeForRoleUsesBuilderArtifactSummaryWhenMessageIsGeneric(t *testing.T) {
	outcome := parseDelegateOutcomeForRole("builder", `{"summary":"Removed the tools pane and switched to a transcript-first layout.","verification":"go test ./internal/tui && go build ./cmd/forge"}`)
	if got := outcome.DisplayText(); got != "Removed the tools pane and switched to a transcript-first layout. Verification: go test ./internal/tui && go build ./cmd/forge." {
		t.Fatalf("display text = %q", got)
	}
}
