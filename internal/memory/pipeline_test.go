package memory

import (
	"fmt"
	"strings"
	"testing"

	"forge/internal/hooks"
	reactruntime "forge/internal/react"
)

func TestExtractSessionMemoryRedactsSensitiveText(t *testing.T) {
	snapshot := reactruntime.SessionSnapshot{
		Mode: reactruntime.ModeImplement,
		TaskState: &reactruntime.TaskState{
			Objective: "rotate token sk-SECRETSECRETSECRET and fix runtime flow",
		},
		Turns: []reactruntime.TurnRecord{
			{FinalResponse: "Updated runtime flow and replaced token sk-SECRETSECRETSECRET with an env var reference."},
		},
	}

	record, ok := ExtractSessionMemory(snapshot)
	if !ok {
		t.Fatal("expected memory record")
	}
	if strings.Contains(record.Objective, "sk-SECRET") {
		t.Fatalf("objective not redacted: %q", record.Objective)
	}
	if strings.Contains(record.Summary, "sk-SECRET") {
		t.Fatalf("summary not redacted: %q", record.Summary)
	}
}

func TestExtractSessionMemorySkipsBlockedSnapshot(t *testing.T) {
	snapshot := reactruntime.SessionSnapshot{
		LastInput: "rotate credentials",
		HookOutput: hooks.ExecutionOutput{
			Block: &hooks.BlockResult{Message: "blocked: approval required"},
		},
		HookOutputSet: true,
		Turns: []reactruntime.TurnRecord{
			{FinalResponse: "I can help once approval is granted."},
		},
	}

	if record, ok := ExtractSessionMemory(snapshot); ok {
		t.Fatalf("expected blocked snapshot to be skipped, got %#v", record)
	}
}

func TestExtractSessionMemorySkipsErroredTurn(t *testing.T) {
	snapshot := reactruntime.SessionSnapshot{
		LastInput: "fix runtime flow",
		Turns: []reactruntime.TurnRecord{
			{
				FinalResponse: "Updated runtime flow.",
				Error:         "tool execution failed",
			},
		},
	}

	if record, ok := ExtractSessionMemory(snapshot); ok {
		t.Fatalf("expected errored turn to be skipped, got %#v", record)
	}
}

func TestExtractSessionMemoryTrimsAndRedactsRetainedText(t *testing.T) {
	longObjective := "review " + strings.Repeat("token ghp_exampleSECRET1234567890 ", 20)
	longSummary := "Updated plan with bearer abcdefghijklmnopqrstuvwxyz1234567890 and " + strings.Repeat("extra summary words ", 30)
	snapshot := reactruntime.SessionSnapshot{
		Mode: reactruntime.ModeReview,
		TaskState: &reactruntime.TaskState{
			Objective: longObjective,
		},
		Turns: []reactruntime.TurnRecord{
			{FinalResponse: longSummary},
		},
	}

	record, ok := ExtractSessionMemory(snapshot)
	if !ok {
		t.Fatal("expected memory record")
	}
	if strings.Contains(record.Objective, "ghp_") {
		t.Fatalf("objective not redacted: %q", record.Objective)
	}
	if strings.Contains(record.Summary, "bearer abcdef") {
		t.Fatalf("summary not redacted: %q", record.Summary)
	}
	if len(record.Objective) > 160 {
		t.Fatalf("objective too long: %d %q", len(record.Objective), record.Objective)
	}
	if len(record.Summary) > 220 {
		t.Fatalf("summary too long: %d %q", len(record.Summary), record.Summary)
	}
}

func TestRedactTextCoversCommonSecretShapes(t *testing.T) {
	input := strings.Join([]string{
		"Authorization: Bearer abcdefghijklmnopqrstuvwxyz1234567890",
		"github token ghp_abcdefghijklmnopqrstuvwxyz123456",
		"api_key=supersecretvalue1234567890",
		"-----BEGIN PRIVATE KEY-----",
	}, "\n")

	redacted := RedactText(input)
	if strings.Contains(redacted, "Bearer abcdef") {
		t.Fatalf("bearer token not redacted: %q", redacted)
	}
	if strings.Contains(redacted, "ghp_abcdefghijklmnopqrstuvwxyz123456") {
		t.Fatalf("github token not redacted: %q", redacted)
	}
	if strings.Contains(redacted, "supersecretvalue1234567890") {
		t.Fatalf("assignment secret not redacted: %q", redacted)
	}
	if strings.Contains(redacted, "BEGIN PRIVATE KEY") {
		t.Fatalf("private key marker not redacted: %q", redacted)
	}
}

func TestConsolidateRecordsKeepsNewestBoundedSet(t *testing.T) {
	records := []Record{
		{Mode: "inspect", Objective: "one", Summary: "summary one"},
		{Mode: "plan", Objective: "two", Summary: "summary two"},
		{Mode: "implement", Objective: "three", Summary: "summary three"},
	}

	state := ConsolidateRecords(records, 2)
	if len(state.Records) != 2 {
		t.Fatalf("records = %#v", state.Records)
	}
	if state.Records[0].Objective != "two" || state.Records[1].Objective != "three" {
		t.Fatalf("bounded records = %#v", state.Records)
	}
	if !strings.Contains(state.Summary, "summary two") || !strings.Contains(state.Summary, "summary three") {
		t.Fatalf("summary = %q", state.Summary)
	}
}

func TestConsolidateRecordsNormalizesAndSummarizesCompactly(t *testing.T) {
	records := []Record{
		{
			Mode:      "review",
			Objective: " investigate runtime drift ",
			Summary:   "  normalized summary with spacing   ",
		},
		{
			Mode:      "review",
			Objective: "investigate runtime drift",
			Summary:   "normalized summary with spacing",
		},
		{
			Mode:      "plan",
			Objective: "ship phase 4",
			Summary:   strings.Repeat("long summary words ", 30),
		},
	}

	state := ConsolidateRecords(records, 5)
	if len(state.Records) != 2 {
		t.Fatalf("records = %#v", state.Records)
	}
	if state.Records[0].Summary != "normalized summary with spacing" {
		t.Fatalf("first record = %#v", state.Records[0])
	}
	lines := strings.Split(strings.TrimSpace(state.Summary), "\n")
	if len(lines) != 2 {
		t.Fatalf("summary lines = %#v", lines)
	}
	for _, line := range lines {
		if len(line) > 160 {
			t.Fatalf("summary line too long: %d %q", len(line), line)
		}
		if !strings.HasPrefix(line, "- ") {
			t.Fatalf("summary line missing bullet: %q", line)
		}
	}
}

func TestPipelineProcessesSnapshotIntoBoundedState(t *testing.T) {
	p := Pipeline{MaxRecords: 2}
	state := State{
		Records: []Record{
			{Mode: "inspect", Objective: "existing", Summary: "existing summary"},
		},
	}
	snapshot := reactruntime.SessionSnapshot{
		Mode: reactruntime.ModeReview,
		TaskState: &reactruntime.TaskState{
			Objective: "review the runtime behavior",
		},
		Turns: []reactruntime.TurnRecord{
			{FinalResponse: "- Finding: runtime note coverage is incomplete."},
		},
	}

	next, ok := p.Process(state, snapshot)
	if !ok {
		t.Fatal("expected updated state")
	}
	if len(next.Records) != 2 {
		t.Fatalf("records = %#v", next.Records)
	}
	if next.Records[1].Mode != "review" {
		t.Fatalf("records = %#v", next.Records)
	}
}

func TestPipelineSkipsUnsafeRetentionAndKeepsUsefulSummary(t *testing.T) {
	p := Pipeline{MaxRecords: 3}
	state := State{
		Records: []Record{
			{Mode: "inspect", Objective: "existing", Summary: "existing summary"},
		},
	}
	blocked := reactruntime.SessionSnapshot{
		LastInput:     "rotate credentials",
		HookOutputSet: true,
		HookOutput:    hooks.ExecutionOutput{Block: &hooks.BlockResult{Message: "blocked"}},
		Turns:         []reactruntime.TurnRecord{{FinalResponse: "blocked response"}},
	}

	next, ok := p.Process(state, blocked)
	if ok {
		t.Fatalf("expected blocked snapshot to be ignored, got %#v", next)
	}

	success := reactruntime.SessionSnapshot{
		Mode: reactruntime.ModeImplement,
		TaskState: &reactruntime.TaskState{
			Objective: "finish memory hardening",
		},
		Turns: []reactruntime.TurnRecord{
			{FinalResponse: fmt.Sprintf("Finalized memory summary with api_key=%s.", strings.Repeat("x", 24))},
		},
	}
	next, ok = p.Process(state, success)
	if !ok {
		t.Fatal("expected successful snapshot to update state")
	}
	if len(next.Records) != 2 {
		t.Fatalf("records = %#v", next.Records)
	}
	if strings.Contains(next.Summary, strings.Repeat("x", 24)) {
		t.Fatalf("summary not redacted: %q", next.Summary)
	}
	if len(strings.Split(strings.TrimSpace(next.Summary), "\n")) != 2 {
		t.Fatalf("summary = %q", next.Summary)
	}
}
