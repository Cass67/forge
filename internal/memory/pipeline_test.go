package memory

import (
	"strings"
	"testing"

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
