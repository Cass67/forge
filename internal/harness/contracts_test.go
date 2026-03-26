package harness

import (
	"strings"
	"testing"

	"forge/internal/agent"
)

func TestValidateWorkerResultRejectsUnknownField(t *testing.T) {
	_, err := ValidateWorkerResult(WorkerReader, `{"status":"complete","evidence":[],"coverage":"repo","gaps":[],"suggested_next":"","extra":"nope"}`)
	if err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestValidateWorkerResultRejectsInvalidStatus(t *testing.T) {
	_, err := ValidateWorkerResult(WorkerResearcher, `{"status":"pending","findings":[{"summary":"doc found"}],"sources":[{"label":"doc","locator":"example"}],"confidence":"high"}`)
	if err == nil {
		t.Fatal("expected invalid status error")
	}
}

func TestValidateWorkerResultRejectsMissingRequiredField(t *testing.T) {
	_, err := ValidateWorkerResult(WorkerReader, `{"status":"complete","evidence":[],"coverage":"repo","gaps":[]}`)
	if err == nil || !strings.Contains(err.Error(), "suggested_next") {
		t.Fatalf("expected missing suggested_next error, got %v", err)
	}
}

func TestValidateWorkerResultRejectsTrailingJSON(t *testing.T) {
	_, err := ValidateWorkerResult(WorkerReader, `{"status":"complete","evidence":[],"coverage":"repo","gaps":[],"suggested_next":""}{"status":"complete"}`)
	if err == nil {
		t.Fatal("expected trailing JSON error")
	}
}

func TestValidateWorkerResultRejectsInvalidReaderEvidenceKind(t *testing.T) {
	_, err := ValidateWorkerResult(WorkerReader, `{"status":"complete","evidence":[{"kind":"url","summary":"read docs"}],"coverage":"repo","gaps":[],"suggested_next":""}`)
	if err == nil || !strings.Contains(err.Error(), "reader evidence kind") {
		t.Fatalf("expected invalid reader evidence kind error, got %v", err)
	}
}

func TestValidateWorkerResultRejectsReaderOutputWithoutConcreteEvidence(t *testing.T) {
	_, err := ValidateWorkerResult(WorkerReader, `{"status":"complete","evidence":[{"kind":"note","summary":"This looks like a Go project."}],"coverage":"ambient context only","gaps":[],"suggested_next":"inspect files"}`)
	if err == nil || !strings.Contains(err.Error(), "concrete evidence") {
		t.Fatalf("expected concrete evidence error, got %v", err)
	}
}

func TestValidateWorkerResultRejectsInvalidVerifierOutcome(t *testing.T) {
	_, err := ValidateWorkerResult(WorkerVerifier, `{"status":"complete","checks":[{"name":"unit","outcome":"maybe"}],"failures":[],"confidence":"high"}`)
	if err == nil || !strings.Contains(err.Error(), "verifier check outcome") {
		t.Fatalf("expected invalid verifier outcome error, got %v", err)
	}
}

func TestValidateWorkerResultRejectsInvalidConfidence(t *testing.T) {
	_, err := ValidateWorkerResult(WorkerResearcher, `{"status":"complete","findings":[{"summary":"doc found"}],"sources":[{"label":"doc","locator":"example"}],"confidence":"certain"}`)
	if err == nil || !strings.Contains(err.Error(), "confidence") {
		t.Fatalf("expected invalid confidence error, got %v", err)
	}
}

func TestValidateWorkerResultParsesReaderPayload(t *testing.T) {
	result, err := ValidateWorkerResult(WorkerReader, `{"status":"complete","evidence":[{"kind":"file","path":"README.md","summary":"README outlines the CLI."}],"coverage":"repo root","gaps":[],"suggested_next":"inspect cmd/forge next"}`)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ObservationComplete {
		t.Fatalf("result = %#v", result)
	}
	if result.Response != "README outlines the CLI." {
		t.Fatalf("response = %q", result.Response)
	}
}

func TestValidateWorkerResultWithToolCallsRejectsUngroundedReaderFileEvidence(t *testing.T) {
	_, err := ValidateWorkerResultWithToolCalls(
		WorkerReader,
		`{"status":"complete","evidence":[{"kind":"file","path":"README.md","summary":"README outlines the CLI."}],"coverage":"repo root","gaps":[],"suggested_next":"inspect cmd/forge next"}`,
		[]agent.ToolCall{
			{Name: "list_dir", Args: map[string]any{"path": ".", "recursive": false}},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "reader file evidence requires a matching read_file call") {
		t.Fatalf("expected grounded file evidence error, got %v", err)
	}
}

func TestValidateWorkerResultWithToolCallsAcceptsGroundedReaderEvidence(t *testing.T) {
	result, err := ValidateWorkerResultWithToolCalls(
		WorkerReader,
		`{"status":"complete","evidence":[{"kind":"command","summary":"Top-level listing shows the repo layout."},{"kind":"file","path":"README.md","summary":"README outlines the CLI."}],"coverage":"repo root","gaps":[],"suggested_next":"inspect cmd/forge next"}`,
		[]agent.ToolCall{
			{Name: "list_dir", Args: map[string]any{"path": ".", "recursive": false}},
			{Name: "read_file", Args: map[string]any{"path": "README.md", "start_line": 1, "end_line": 40}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ObservationComplete {
		t.Fatalf("result = %#v", result)
	}
}

func TestValidateWorkerResultParsesEditorPayload(t *testing.T) {
	result, err := ValidateWorkerResult(WorkerEditor, `{"status":"complete","changes":[{"path":"internal/runtime/chat.go","summary":"Removed the visible agent toggle."}],"verification_attempts":[{"command":"go test ./internal/runtime","outcome":"pass"}],"remaining_issues":[],"suggested_next":"run the full harness suite"}`)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ObservationComplete {
		t.Fatalf("result = %#v", result)
	}
	if result.Response != "Removed the visible agent toggle." {
		t.Fatalf("response = %q", result.Response)
	}
}

func TestValidateWorkerResultParsesVerifierPayload(t *testing.T) {
	result, err := ValidateWorkerResult(WorkerVerifier, `{"status":"complete","checks":[{"name":"unit","outcome":"pass","detail":"go test ./internal/harness"}],"failures":[],"confidence":"high"}`)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ObservationComplete {
		t.Fatalf("result = %#v", result)
	}
	if result.Response != "verified 1 checks" {
		t.Fatalf("response = %q", result.Response)
	}
}

func TestValidateWorkerResultParsesResearcherPayload(t *testing.T) {
	result, err := ValidateWorkerResult(WorkerResearcher, `{"status":"complete","findings":[{"summary":"Latest docs confirm the API exists.","detail":"Checked the official reference."}],"sources":[{"label":"official docs","locator":"docs"}],"confidence":"high"}`)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ObservationComplete {
		t.Fatalf("result = %#v", result)
	}
	if result.Response != "Latest docs confirm the API exists." {
		t.Fatalf("response = %q", result.Response)
	}
}
