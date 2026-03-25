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

func TestParseDelegateOutcomeStructuredEnvelopeNormalizesDoneStatus(t *testing.T) {
	outcome := parseDelegateOutcome(`{"status":"done","message":"Found the alert source.","artifact_kind":"evidence","artifact":"./util-rancid/update_cerner_daily.sh","next_role":"developer","next_task":"inspect more"}`)
	if !outcome.Structured {
		t.Fatalf("expected structured outcome")
	}
	if !outcome.Completed() {
		t.Fatalf("expected completed outcome")
	}
	if outcome.NextRole != "" || outcome.NextTask != "" {
		t.Fatalf("expected invalid next step to be dropped, got role=%q task=%q", outcome.NextRole, outcome.NextTask)
	}
	if got := outcome.DisplayText(); got != "Found the alert source." {
		t.Fatalf("display text = %q", got)
	}
}

func TestParseDelegateOutcomeStructuredEnvelopeNormalizesPartialStatusAndKeepsValidNextRole(t *testing.T) {
	outcome := parseDelegateOutcome(`{"status":"partial","message":"Need one more scout pass.","artifact_kind":"evidence","artifact":"more evidence needed","next_role":"scout","next_task":"capture the exact run_or_warn block"}`)
	if !outcome.Structured {
		t.Fatalf("expected structured outcome")
	}
	if !outcome.Completed() {
		t.Fatalf("expected completed outcome")
	}
	if outcome.NextRole != "scout" || outcome.NextTask != "capture the exact run_or_warn block" {
		t.Fatalf("expected valid next step to be kept, got role=%q task=%q", outcome.NextRole, outcome.NextTask)
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
	if outcome.DisplayText() != "Severity: Low to medium. Next check: Verify the expected verify script path exists and is executable." {
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

func TestParseDelegateOutcomeForRoleUsesScoutArtifactSummaryForCurrentScoutContract(t *testing.T) {
	raw := `{"origin_file":"util-rancid/update_cerner_daily.sh","evidence":[{"file":"util-rancid/update_cerner_daily.sh","line":753,"match":"run_or_warn \"f5 objstor verify missing-script alert email\" mailx -s \"Rancid f5 objstor verify script missing\" martin.cassidy@oracle.com </dev/null"}],"explanation":"The email subject/body text is hard-coded in a mailx command inside the daily RANCID update script.","triggering_condition":"Missing-script alert path for the f5 objstor verify step in the daily update workflow","related_job_or_config":"Likely invoked by a cron/scheduled daily job that runs util-rancid/update_cerner_daily.sh","confidence":"high"}`
	outcome := parseDelegateOutcomeForRole("scout", raw)
	if got := outcome.DisplayText(); got != "Source: util-rancid/update_cerner_daily.sh:753. Likely trigger: Missing-script alert path for the f5 objstor verify step in the daily update workflow." {
		t.Fatalf("display text = %q", got)
	}
}

func TestParseDelegateOutcomeForRoleUsesScoutArtifactSummaryForRuntimeScoutPayload(t *testing.T) {
	raw := `{"source_file":"util-rancid/update_cerner_daily.sh","source_line":753,"message_subject":"Rancid f5 objstor verify script missing","emitter":"mailx invocation inside the RANCID daily update script","trigger_condition":"The f5 objstor verify step expected a verify script to exist, but the script was missing or unavailable when the job ran","why_sent":"To alert the configured recipient that the automated RANCID verification for the f5 objstor target could not run due to the missing verify script","recipient":"martin.cassidy@oracle.com","context":"This appears to be a maintenance/monitoring alert from a scheduled RANCID update workflow, not a user-initiated email.","evidence":"run_or_warn \"f5 objstor verify missing-script alert email\" mailx -s \"Rancid f5 objstor verify script missing\" martin.cassidy@oracle.com </dev/null"}`
	outcome := parseDelegateOutcomeForRole("scout", raw)
	if got := outcome.DisplayText(); got != "Source: util-rancid/update_cerner_daily.sh:753. Likely trigger: The f5 objstor verify step expected a verify script to exist, but the script was missing or unavailable when the job ran." {
		t.Fatalf("display text = %q", got)
	}
}

func TestParseDelegateOutcomeForRoleUsesScoutArtifactSummaryForLiveRetryPayload(t *testing.T) {
	raw := `{"file":"util-rancid/update_cerner_daily.sh","line":753,"subject":"Rancid f5 objstor verify script missing","process":"Daily RANCID update/maintenance workflow driven by update_cerner_daily.sh (likely cron-scheduled)","trigger":"The script sends this alert when the expected F5 objstor verify helper/script is missing or unavailable during the update run.","reason":"This is a missing-script failure notification, not a normal report; it warns the recipient that the RANCID F5 objstor verify step could not be performed because the script was not present.","recipient":"martin.cassidy@oracle.com"}`
	outcome := parseDelegateOutcomeForRole("scout", raw)
	if got := outcome.DisplayText(); got != "Source: util-rancid/update_cerner_daily.sh:753. Likely trigger: The script sends this alert when the expected F5 objstor verify helper/script is missing or unavailable during the update run." {
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

func TestParseDelegateOutcomeForRoleUsesLabeledArchitectSummaryForSeverityAndActionability(t *testing.T) {
	outcome := parseDelegateOutcomeForRole("architect", `{"severity":"medium","actionability":"high"}`)
	if got := outcome.DisplayText(); got != "Severity: Medium. Actionability: High." {
		t.Fatalf("display text = %q", got)
	}
}
