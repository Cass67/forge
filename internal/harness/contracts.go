package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type ReaderEvidence struct {
	Kind    string `json:"kind"`
	Path    string `json:"path,omitempty"`
	Summary string `json:"summary"`
}

type ReaderResult struct {
	Status        string           `json:"status"`
	Evidence      []ReaderEvidence `json:"evidence"`
	Coverage      string           `json:"coverage"`
	Gaps          []string         `json:"gaps"`
	SuggestedNext string           `json:"suggested_next"`
}

type ChangeRecord struct {
	Path    string `json:"path"`
	Summary string `json:"summary"`
}

type VerificationAttempt struct {
	Command string `json:"command"`
	Outcome string `json:"outcome"`
}

type EditorResult struct {
	Status               string                `json:"status"`
	Changes              []ChangeRecord        `json:"changes"`
	VerificationAttempts []VerificationAttempt `json:"verification_attempts"`
	RemainingIssues      []string              `json:"remaining_issues"`
	SuggestedNext        string                `json:"suggested_next"`
}

type VerificationCheck struct {
	Name    string `json:"name"`
	Outcome string `json:"outcome"`
	Detail  string `json:"detail,omitempty"`
}

type VerifierResult struct {
	Status     string              `json:"status"`
	Checks     []VerificationCheck `json:"checks"`
	Failures   []string            `json:"failures"`
	Confidence string              `json:"confidence"`
}

type ResearchFinding struct {
	Summary string `json:"summary"`
	Detail  string `json:"detail,omitempty"`
}

type ResearchSource struct {
	Label   string `json:"label"`
	Locator string `json:"locator"`
}

type ResearcherResult struct {
	Status     string            `json:"status"`
	Findings   []ResearchFinding `json:"findings"`
	Sources    []ResearchSource  `json:"sources"`
	Confidence string            `json:"confidence"`
}

func ValidateWorkerResult(kind WorkerKind, raw string) (ValidatedWorkerResult, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ValidatedWorkerResult{}, fmt.Errorf("%s produced empty output", kind)
	}

	switch kind {
	case WorkerReader:
		var result ReaderResult
		if err := decodeStrictJSON(raw, &result, "status", "evidence", "coverage", "gaps", "suggested_next"); err != nil {
			return ValidatedWorkerResult{}, err
		}
		if err := validateReaderResult(result); err != nil {
			return ValidatedWorkerResult{}, err
		}
		return ValidatedWorkerResult{
			Parsed:   result,
			Response: summarizeReaderResult(result),
			Summary:  result.Coverage,
			Status:   workerStatus(result.Status),
		}, nil
	case WorkerEditor:
		var result EditorResult
		if err := decodeStrictJSON(raw, &result, "status", "changes", "verification_attempts", "remaining_issues", "suggested_next"); err != nil {
			return ValidatedWorkerResult{}, err
		}
		if err := validateEditorResult(result); err != nil {
			return ValidatedWorkerResult{}, err
		}
		return ValidatedWorkerResult{
			Parsed:   result,
			Response: summarizeEditorResult(result),
			Summary:  summarizeEditorResult(result),
			Status:   workerStatus(result.Status),
		}, nil
	case WorkerVerifier:
		var result VerifierResult
		if err := decodeStrictJSON(raw, &result, "status", "checks", "failures", "confidence"); err != nil {
			return ValidatedWorkerResult{}, err
		}
		if err := validateVerifierResult(result); err != nil {
			return ValidatedWorkerResult{}, err
		}
		return ValidatedWorkerResult{
			Parsed:   result,
			Response: summarizeVerifierResult(result),
			Summary:  summarizeVerifierResult(result),
			Status:   workerStatus(result.Status),
		}, nil
	case WorkerResearcher:
		var result ResearcherResult
		if err := decodeStrictJSON(raw, &result, "status", "findings", "sources", "confidence"); err != nil {
			return ValidatedWorkerResult{}, err
		}
		if err := validateResearcherResult(result); err != nil {
			return ValidatedWorkerResult{}, err
		}
		return ValidatedWorkerResult{
			Parsed:   result,
			Response: summarizeResearcherResult(result),
			Summary:  summarizeResearcherResult(result),
			Status:   workerStatus(result.Status),
		}, nil
	default:
		return ValidatedWorkerResult{}, fmt.Errorf("unsupported worker kind %q", kind)
	}
}

func decodeStrictJSON(raw string, target any, requiredFields ...string) error {
	if err := decodeSingleJSON(raw, target); err != nil {
		return err
	}
	if len(requiredFields) == 0 {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := decodeSingleJSON(raw, &fields); err != nil {
		return err
	}
	for _, field := range requiredFields {
		if _, ok := fields[field]; !ok {
			return fmt.Errorf("%s is required", field)
		}
	}
	return nil
}

func decodeSingleJSON(raw string, target any) error {
	dec := json.NewDecoder(bytes.NewBufferString(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	var trailing struct{}
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON content")
		}
		return fmt.Errorf("unexpected trailing JSON content: %w", err)
	}
	return nil
}

func validateReaderResult(result ReaderResult) error {
	if err := validateStatus(result.Status); err != nil {
		return err
	}
	if strings.TrimSpace(result.Coverage) == "" {
		return fmt.Errorf("reader coverage is required")
	}
	hasConcreteEvidence := false
	for _, evidence := range result.Evidence {
		if strings.TrimSpace(evidence.Kind) == "" || strings.TrimSpace(evidence.Summary) == "" {
			return fmt.Errorf("reader evidence entries require kind and summary")
		}
		if err := validateReaderEvidenceKind(evidence.Kind); err != nil {
			return err
		}
		if evidence.Kind == "file" && strings.TrimSpace(evidence.Path) == "" {
			return fmt.Errorf("reader file evidence requires path")
		}
		if evidence.Kind == "file" || evidence.Kind == "command" {
			hasConcreteEvidence = true
		}
	}
	if strings.TrimSpace(result.Status) == "complete" && !hasConcreteEvidence {
		return fmt.Errorf("reader complete result must include concrete evidence from at least one file or command")
	}
	return nil
}

func validateEditorResult(result EditorResult) error {
	if err := validateStatus(result.Status); err != nil {
		return err
	}
	for _, change := range result.Changes {
		if strings.TrimSpace(change.Path) == "" || strings.TrimSpace(change.Summary) == "" {
			return fmt.Errorf("editor changes require path and summary")
		}
	}
	for _, attempt := range result.VerificationAttempts {
		if strings.TrimSpace(attempt.Command) == "" || strings.TrimSpace(attempt.Outcome) == "" {
			return fmt.Errorf("editor verification attempts require command and outcome")
		}
	}
	return nil
}

func validateVerifierResult(result VerifierResult) error {
	if err := validateStatus(result.Status); err != nil {
		return err
	}
	if err := validateConfidence("verifier", result.Confidence); err != nil {
		return err
	}
	for _, check := range result.Checks {
		if strings.TrimSpace(check.Name) == "" || strings.TrimSpace(check.Outcome) == "" {
			return fmt.Errorf("verifier checks require name and outcome")
		}
		if err := validateVerifierOutcome(check.Outcome); err != nil {
			return err
		}
	}
	return nil
}

func validateResearcherResult(result ResearcherResult) error {
	if err := validateStatus(result.Status); err != nil {
		return err
	}
	if err := validateConfidence("researcher", result.Confidence); err != nil {
		return err
	}
	for _, finding := range result.Findings {
		if strings.TrimSpace(finding.Summary) == "" {
			return fmt.Errorf("researcher findings require summary")
		}
	}
	for _, source := range result.Sources {
		if strings.TrimSpace(source.Label) == "" || strings.TrimSpace(source.Locator) == "" {
			return fmt.Errorf("researcher sources require label and locator")
		}
	}
	return nil
}

func validateStatus(status string) error {
	switch strings.TrimSpace(status) {
	case "complete", "blocked":
		return nil
	default:
		return fmt.Errorf("invalid worker status %q", status)
	}
}

func validateReaderEvidenceKind(kind string) error {
	switch strings.TrimSpace(kind) {
	case "file", "command", "note":
		return nil
	default:
		return fmt.Errorf("reader evidence kind must be file, command, or note")
	}
}

func validateVerifierOutcome(outcome string) error {
	switch strings.TrimSpace(outcome) {
	case "pass", "fail":
		return nil
	default:
		return fmt.Errorf("verifier check outcome must be pass or fail")
	}
}

func validateConfidence(worker, confidence string) error {
	switch strings.TrimSpace(confidence) {
	case "low", "medium", "high":
		return nil
	case "":
		return fmt.Errorf("%s confidence is required", worker)
	default:
		return fmt.Errorf("%s confidence must be low, medium, or high", worker)
	}
}

func workerStatus(status string) ObservationStatus {
	if strings.TrimSpace(status) == "blocked" {
		return ObservationBlocked
	}
	return ObservationComplete
}

func summarizeReaderResult(result ReaderResult) string {
	if len(result.Evidence) > 0 {
		return result.Evidence[0].Summary
	}
	return result.Coverage
}

func summarizeEditorResult(result EditorResult) string {
	if len(result.Changes) > 0 {
		return result.Changes[0].Summary
	}
	if len(result.RemainingIssues) > 0 {
		return result.RemainingIssues[0]
	}
	return "editor worker completed"
}

func summarizeVerifierResult(result VerifierResult) string {
	if len(result.Failures) > 0 {
		return result.Failures[0]
	}
	if len(result.Checks) > 0 {
		return fmt.Sprintf("verified %d checks", len(result.Checks))
	}
	return "verifier worker completed"
}

func summarizeResearcherResult(result ResearcherResult) string {
	if len(result.Findings) > 0 {
		return result.Findings[0].Summary
	}
	return fmt.Sprintf("research confidence: %s", result.Confidence)
}
