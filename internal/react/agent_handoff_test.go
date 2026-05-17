package react

import (
	"strings"
	"testing"
)

func TestParseAgentHandoffExtractsReportAndActions(t *testing.T) {
	raw := "audit findings\n\n```forge_handoff\n{" +
		`"remaining_actions":[{"kind":"write_file","target_path":"docs/audit.md","description":"Save synthesized audit","blocking":true}],` +
		`"incidents":[{"kind":"accidental_write","paths":["README.md"],"description":"Child wrote report into README","blocking":true}]` +
		"}\n```"
	report, handoff, err := ParseAgentHandoff(raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(report, "forge_handoff") {
		t.Fatalf("report leaked handoff block: %q", report)
	}
	if strings.TrimSpace(report) != "audit findings" {
		t.Fatalf("report = %q, want audit findings", report)
	}
	if len(handoff.RemainingActions) != 1 || handoff.RemainingActions[0].Kind != AgentActionWriteFile {
		t.Fatalf("handoff actions = %#v", handoff.RemainingActions)
	}
	if handoff.RemainingActions[0].TargetPath != "docs/audit.md" || !handoff.RemainingActions[0].Blocking {
		t.Fatalf("handoff action detail = %#v", handoff.RemainingActions[0])
	}
	if len(handoff.Incidents) != 1 || handoff.Incidents[0].Paths[0] != "README.md" {
		t.Fatalf("handoff incidents = %#v", handoff.Incidents)
	}
	if !handoff.Blocking() {
		t.Fatal("handoff should be blocking")
	}
}

func TestParseAgentHandoffNoBlockReturnsOriginalReport(t *testing.T) {
	report, handoff, err := ParseAgentHandoff("plain report")
	if err != nil {
		t.Fatal(err)
	}
	if report != "plain report" {
		t.Fatalf("report = %q", report)
	}
	if handoff.Blocking() || len(handoff.RemainingActions) != 0 || len(handoff.Incidents) != 0 {
		t.Fatalf("handoff = %#v, want empty", handoff)
	}
}

func TestParseAgentHandoffInvalidJSONReturnsError(t *testing.T) {
	raw := "report\n```forge_handoff\n{bad json}\n```"
	report, _, err := ParseAgentHandoff(raw)
	if err == nil {
		t.Fatal("expected invalid JSON error")
	}
	if report != raw {
		t.Fatalf("report = %q, want raw output preserved", report)
	}
}

func TestParseAgentHandoffRejectsMultipleBlocks(t *testing.T) {
	raw := "report\n```forge_handoff\n{}\n```\n```forge_handoff\n{}\n```"
	report, _, err := ParseAgentHandoff(raw)
	if err == nil {
		t.Fatal("expected multiple handoff blocks error")
	}
	if report != raw {
		t.Fatalf("report = %q, want raw output preserved", report)
	}
}

func TestParseAgentHandoffUnknownBlockingKind(t *testing.T) {
	raw := "report\n```forge_handoff\n{" +
		`"remaining_actions":[{"kind":"custom_action","blocking":true}],` +
		`"incidents":[{"kind":"custom_incident","blocking":true}]` +
		"}\n```"
	_, handoff, err := ParseAgentHandoff(raw)
	if err != nil {
		t.Fatal(err)
	}
	if handoff.RemainingActions[0].Kind != "custom_action" || handoff.Incidents[0].Kind != "custom_incident" {
		t.Fatalf("handoff = %#v", handoff)
	}
	if !handoff.Blocking() {
		t.Fatal("unknown blocking kinds should remain blocking")
	}
}
