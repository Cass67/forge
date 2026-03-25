package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type delegateOutcome struct {
	Role         string
	Structured   bool
	Status       string
	Message      string
	ArtifactKind string
	Artifact     string
	NextRole     string
	NextTask     string
	Raw          string
}

type delegateEnvelope struct {
	Status       string `json:"status"`
	Message      string `json:"message"`
	ArtifactKind string `json:"artifact_kind,omitempty"`
	Artifact     string `json:"artifact,omitempty"`
	NextRole     string `json:"next_role,omitempty"`
	NextTask     string `json:"next_task,omitempty"`
}

func parseDelegateOutcome(raw string) delegateOutcome {
	return parseDelegateOutcomeForRole("", raw)
}

func parseDelegateOutcomeForRole(role, raw string) delegateOutcome {
	role = strings.TrimSpace(role)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return delegateOutcome{Role: role, Status: "blocked", Raw: raw}
	}

	if envelope, ok := parseDelegateEnvelope(raw); ok {
		return delegateOutcomeFromEnvelope(role, envelope, raw)
	}

	if envelope, ok := parseDelegateJSONObjectForRole(role, raw); ok {
		return delegateOutcomeFromEnvelope(role, envelope, raw)
	}

	upper := strings.ToUpper(raw)
	switch {
	case raw == "(sub-agent produced no output)":
		return delegateOutcome{Role: role, Status: "blocked", Message: raw, Raw: raw}
	case strings.HasPrefix(upper, "CANCELLED:"):
		return delegateOutcome{Role: role, Status: "blocked", Message: raw, Raw: raw}
	case strings.HasPrefix(upper, "AGENT ERROR"):
		return delegateOutcome{Role: role, Status: "blocked", Message: raw, Raw: raw}
	default:
		return delegateOutcome{Role: role, Status: "complete", Message: raw, Artifact: raw, Raw: raw}
	}
}

func canonicalStructuredDelegateResult(role, raw string) (string, bool) {
	role = strings.TrimSpace(role)
	if !roleRequiresStructuredDelegateResult(role) {
		return raw, false
	}
	var envelope delegateEnvelope
	var ok bool
	if envelope, ok = parseDelegateEnvelope(raw); !ok {
		if envelope, ok = parseDelegateJSONObjectForRole(role, raw); !ok {
			return raw, false
		}
	}
	outcome := delegateOutcomeFromEnvelope(role, envelope, raw)
	if !outcome.Completed() {
		return raw, false
	}
	message := strings.TrimSpace(outcome.DisplayText())
	if message == "" {
		message = strings.TrimSpace(defaultDelegateMessage(role))
	}
	artifactKind := strings.TrimSpace(outcome.ArtifactKind)
	if artifactKind == "" {
		artifactKind = defaultDelegateArtifactKind(role)
	}
	artifact := strings.TrimSpace(outcome.ContextText())
	if artifact == "" {
		artifact = message
	}
	canonical, err := json.Marshal(delegateEnvelope{
		Status:       "complete",
		Message:      message,
		ArtifactKind: artifactKind,
		Artifact:     artifact,
		NextRole:     strings.TrimSpace(outcome.NextRole),
		NextTask:     strings.TrimSpace(outcome.NextTask),
	})
	if err != nil {
		return raw, false
	}
	return string(canonical), true
}

func delegateOutcomeFromEnvelope(role string, envelope delegateEnvelope, raw string) delegateOutcome {
	return delegateOutcome{
		Role:         strings.TrimSpace(role),
		Structured:   true,
		Status:       normalizeDelegateStatus(envelope.Status),
		Message:      strings.TrimSpace(envelope.Message),
		ArtifactKind: strings.TrimSpace(envelope.ArtifactKind),
		Artifact:     strings.TrimSpace(envelope.Artifact),
		NextRole:     strings.TrimSpace(envelope.NextRole),
		NextTask:     strings.TrimSpace(envelope.NextTask),
		Raw:          raw,
	}
}

func parseDelegateEnvelope(raw string) (delegateEnvelope, bool) {
	trimmed := strings.TrimSpace(stripJSONFence(raw))
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
		return delegateEnvelope{}, false
	}
	var envelope struct {
		Status       string          `json:"status"`
		Message      string          `json:"message"`
		ArtifactKind string          `json:"artifact_kind,omitempty"`
		Artifact     json.RawMessage `json:"artifact,omitempty"`
		NextRole     string          `json:"next_role,omitempty"`
		NextTask     string          `json:"next_task,omitempty"`
	}
	if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
		return delegateEnvelope{}, false
	}
	if normalizeDelegateStatus(envelope.Status) == "" {
		return delegateEnvelope{}, false
	}
	artifact, ok := normalizeDelegateArtifact(envelope.Artifact)
	if !ok {
		return delegateEnvelope{}, false
	}
	nextRole := strings.TrimSpace(envelope.NextRole)
	nextTask := strings.TrimSpace(envelope.NextTask)
	if _, ok := Roles[nextRole]; !ok {
		nextRole = ""
		nextTask = ""
	}
	return delegateEnvelope{
		Status:       envelope.Status,
		Message:      envelope.Message,
		ArtifactKind: envelope.ArtifactKind,
		Artifact:     artifact,
		NextRole:     nextRole,
		NextTask:     nextTask,
	}, true
}

func parseDelegateJSONObjectForRole(role, raw string) (delegateEnvelope, bool) {
	trimmed := strings.TrimSpace(stripJSONFence(raw))
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
		return delegateEnvelope{}, false
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &object); err != nil {
		return delegateEnvelope{}, false
	}

	if len(object) == 0 {
		return delegateEnvelope{}, false
	}

	if _, hasStatus := object["status"]; hasStatus {
		return delegateEnvelope{}, false
	}

	message := strings.TrimSpace(decodeDelegateStringField(object, "message"))
	if message == "" {
		message = defaultDelegateMessage(role)
	}

	artifactKind := strings.TrimSpace(decodeDelegateStringField(object, "artifact_kind"))
	if artifactKind == "" {
		artifactKind = defaultDelegateArtifactKind(role)
	}

	artifact := trimmed
	if rawArtifact, ok := object["artifact"]; ok {
		normalizedArtifact, ok := normalizeDelegateArtifact(rawArtifact)
		if !ok {
			return delegateEnvelope{}, false
		}
		if strings.TrimSpace(normalizedArtifact) != "" {
			artifact = normalizedArtifact
		}
	}

	nextRole := strings.TrimSpace(decodeDelegateStringField(object, "next_role"))
	nextTask := strings.TrimSpace(decodeDelegateStringField(object, "next_task"))
	if _, ok := Roles[nextRole]; !ok {
		nextRole = ""
		nextTask = ""
	}

	return delegateEnvelope{
		Status:       "complete",
		Message:      message,
		ArtifactKind: artifactKind,
		Artifact:     artifact,
		NextRole:     nextRole,
		NextTask:     nextTask,
	}, true
}

func decodeDelegateStringField(object map[string]json.RawMessage, key string) string {
	raw, ok := object[key]
	if !ok {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func defaultDelegateArtifactKind(role string) string {
	switch strings.TrimSpace(role) {
	case "scout":
		return "evidence"
	case "builder":
		return "implementation"
	case "doctor":
		return "diagnosis"
	case "architect":
		return "plan"
	default:
		return ""
	}
}

func defaultDelegateMessage(role string) string {
	switch strings.TrimSpace(role) {
	case "scout":
		return "Evidence gathered."
	case "builder":
		return "Implementation complete."
	case "doctor":
		return "Diagnosis ready."
	case "architect":
		return "Architect output ready."
	default:
		return ""
	}
}

func normalizeDelegateArtifact(raw json.RawMessage) (string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", true
	}
	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		return strings.TrimSpace(text), true
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, trimmed); err != nil {
		return "", false
	}
	return strings.TrimSpace(compact.String()), true
}

func stripJSONFence(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) < 3 {
		return trimmed
	}
	if !strings.HasPrefix(strings.TrimSpace(lines[0]), "```") || strings.TrimSpace(lines[len(lines)-1]) != "```" {
		return trimmed
	}
	return strings.Join(lines[1:len(lines)-1], "\n")
}

func normalizeDelegateStatus(status string) string {
	normalized := strings.TrimSpace(strings.ToLower(status))
	switch normalized {
	case "":
		return ""
	case "blocked", "block", "error", "failed", "failure", "cancelled", "canceled":
		return "blocked"
	default:
		return "complete"
	}
}

func (o delegateOutcome) Completed() bool {
	return o.Status == "complete"
}

func (o delegateOutcome) Blocked() bool {
	return o.Status == "blocked"
}

func (o delegateOutcome) DisplayText() string {
	if o.Structured {
		message := strings.TrimSpace(o.Message)
		if summary := extractDelegateArtifactSummary(o.Role, o.Artifact); summary != "" {
			if message == "" || isGenericDelegateMessage(message) || delegateArtifactSummaryShouldReplaceMessage(summary, message) {
				return summary
			}
		}
		if message != "" {
			return message
		}
		if fallback := strings.TrimSpace(defaultDelegateMessage(o.Role)); fallback != "" {
			return fallback
		}
	}
	switch {
	case strings.TrimSpace(o.Message) != "":
		return strings.TrimSpace(o.Message)
	case strings.TrimSpace(o.Artifact) != "":
		return strings.TrimSpace(o.Artifact)
	default:
		return strings.TrimSpace(o.Raw)
	}
}

var genericDelegateMessages = map[string]struct{}{
	"evidence gathered":       {},
	"architect output ready":  {},
	"diagnosis ready":         {},
	"implementation complete": {},
	"recommendations ready":   {},
	"plan ready":              {},
}

func isGenericDelegateMessage(message string) bool {
	normalized := strings.TrimSpace(strings.ToLower(message))
	normalized = strings.TrimSuffix(normalized, ".")
	_, ok := genericDelegateMessages[normalized]
	return ok
}

func extractDelegateArtifactSummary(role, artifact string) string {
	object, ok := parseDelegateArtifactObject(artifact)
	if !ok {
		return ""
	}
	switch strings.TrimSpace(role) {
	case "scout":
		return summarizeScoutArtifact(object)
	case "architect":
		return summarizeArchitectArtifact(object)
	case "doctor":
		return summarizeDoctorArtifact(object)
	case "builder":
		return summarizeBuilderArtifact(object)
	default:
		return ""
	}
}

func parseDelegateArtifactObject(artifact string) (map[string]any, bool) {
	artifact = strings.TrimSpace(artifact)
	if artifact == "" || !strings.HasPrefix(artifact, "{") {
		return nil, false
	}
	var object map[string]any
	if err := json.Unmarshal([]byte(artifact), &object); err != nil {
		return nil, false
	}
	if len(object) == 0 {
		return nil, false
	}
	return object, true
}

func summarizeScoutArtifact(object map[string]any) string {
	location := scoutSourceLocation(object)
	trigger := firstNonEmptyString(object, "trigger", "trigger_condition", "triggering_condition", "most_likely_trigger", "why_it_was_sent")
	whySent := firstNonEmptyString(object, "why_sent")
	reason := firstNonEmptyString(object, "reason")
	source := firstNonEmptyString(object, "source")
	explanation := firstNonEmptyString(object, "explanation")
	evidence := firstUsefulEntry(object["evidence"])

	var parts []string
	if location != "" {
		parts = append(parts, labelSentence("Source: ", location))
	}
	if location == "" && source != "" {
		parts = append(parts, sentence(source))
	}
	if trigger != "" {
		parts = append(parts, labelSentence("Likely trigger: ", trigger))
	} else if whySent != "" {
		parts = append(parts, labelSentence("Likely trigger: ", whySent))
	} else if reason != "" {
		parts = append(parts, labelSentence("Likely trigger: ", reason))
	}
	if len(parts) == 0 && explanation != "" {
		parts = append(parts, sentence(explanation))
	}
	if len(parts) == 0 && evidence != "" {
		parts = append(parts, sentence(evidence))
	}
	return strings.Join(parts, " ")
}

func summarizeArchitectArtifact(object map[string]any) string {
	assessment := firstNonEmptyString(object, "assessment")
	severity := humanizeDelegateText(firstNonEmptyString(object, "severity", "worry_level"), " ")
	actionabilityRaw := strings.TrimSpace(strings.ToLower(firstNonEmptyString(object, "actionability")))
	actionability := humanizeDelegateText(actionabilityRaw, " ")
	nextCheck := firstNonEmptyString(object, "recommended_next_check")
	impact := firstNonEmptyString(object, "likely_impact")
	suggested := firstUsefulEntry(object["suggested_next_checks"])
	prioritySummary := summarizeArchitectList(object["priorities"])
	themeSummary := summarizeArchitectList(object["themes"])
	findingSummary := summarizeArchitectList(object["prioritized_findings"])
	riskPrioritySummary := summarizeArchitectList(object["risk_prioritization"])
	actionSummary := summarizeArchitectList(object["next_actions"])
	riskSummary := summarizeArchitectList(object["top_risks"])
	metricSummary := summarizeArchitectList(object["success_metrics"])

	switch {
	case prioritySummary != "":
		return prioritySummary
	case themeSummary != "":
		return themeSummary
	case riskPrioritySummary != "" && actionSummary != "":
		return riskPrioritySummary + "\n" + actionSummary
	case riskPrioritySummary != "":
		return riskPrioritySummary
	case findingSummary != "" && actionSummary != "":
		return findingSummary + "\n" + actionSummary
	case findingSummary != "":
		return findingSummary
	case actionSummary != "":
		return actionSummary
	case riskSummary != "":
		return riskSummary
	case metricSummary != "":
		return metricSummary
	}

	if assessment != "" {
		parts := []string{sentence(assessment)}
		if nextCheck != "" {
			parts = append(parts, labelSentence("Next check: ", nextCheck))
		}
		return strings.Join(parts, " ")
	}

	var parts []string
	if severity != "" {
		parts = append(parts, labelSentence("Severity: ", severity))
	}
	if actionability != "" {
		switch {
		case actionabilityRaw == "actionable" && nextCheck != "":
			// The explicit next check already conveys actionability.
		case actionabilityRaw == "actionable":
			parts = append(parts, sentence("Action needed"))
		default:
			parts = append(parts, labelSentence("Actionability: ", actionability))
		}
	}
	if severity == "" && impact != "" {
		parts = append(parts, labelSentence("Likely impact: ", impact))
	}
	if nextCheck != "" {
		parts = append(parts, labelSentence("Next check: ", nextCheck))
	}
	if nextCheck == "" && suggested != "" {
		parts = append(parts, labelSentence("Next check: ", suggested))
	}
	if len(parts) > 0 {
		return strings.Join(parts, " ")
	}
	return ""
}

func summarizeArchitectList(value any) string {
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return ""
	}
	lines := make([]string, 0, min(len(items), 6)+1)
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		title := firstNonEmptyString(object, "title", "name", "theme", "action", "risk", "id")
		if title == "" {
			continue
		}
		summary := firstNonEmptyString(object, "summary", "assessment", "result", "why_it_matters", "impact", "expected_outcome", "definition")
		nextCheck := firstArchitectRecommendation(object["recommendations"])
		if nextCheck == "" {
			nextCheck = firstArchitectRecommendation(object["details"])
		}
		if nextCheck == "" {
			nextCheck = firstArchitectRecommendation(object["drivers"])
		}
		if summary == "" || nextCheck == "" {
			findingSummary, findingNext := summarizeArchitectFindings(object["findings"])
			if summary == "" {
				summary = findingSummary
			}
			if nextCheck == "" {
				nextCheck = findingNext
			}
		}
		risk := humanizeDelegateText(firstNonEmptyString(object, "risk", "severity"), " ")
		switch {
		case summary != "" && nextCheck != "":
			lines = append(lines, fmt.Sprintf("- %s: %s. Next: %s.", title, trimSentenceEnding(summary), trimSentenceEnding(nextCheck)))
		case summary != "" && risk != "":
			lines = append(lines, fmt.Sprintf("- %s (%s): %s.", title, risk, trimSentenceEnding(summary)))
		case summary != "":
			lines = append(lines, fmt.Sprintf("- %s: %s.", title, trimSentenceEnding(summary)))
		case risk != "" && nextCheck != "":
			lines = append(lines, fmt.Sprintf("- %s (%s). Next: %s.", title, risk, trimSentenceEnding(nextCheck)))
		case risk != "":
			lines = append(lines, fmt.Sprintf("- %s (%s).", title, risk))
		case nextCheck != "":
			lines = append(lines, fmt.Sprintf("- %s. Next: %s.", title, trimSentenceEnding(nextCheck)))
		default:
			lines = append(lines, fmt.Sprintf("- %s.", title))
		}
		if len(lines) == 6 {
			break
		}
	}
	if len(lines) == 0 {
		return ""
	}
	if extra := len(items) - len(lines); extra > 0 {
		lines = append(lines, fmt.Sprintf("- %d more.", extra))
	}
	return strings.Join(lines, "\n")
}

func summarizeArchitectFindings(value any) (string, string) {
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return "", ""
	}
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		summary := firstNonEmptyString(object, "summary", "assessment", "result", "why_it_matters", "risk")
		nextCheck := firstArchitectRecommendation(object["recommendations"])
		if summary != "" || nextCheck != "" {
			return summary, nextCheck
		}
	}
	return "", ""
}

func firstArchitectRecommendation(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []any:
		for _, entry := range typed {
			if text := firstArchitectRecommendation(entry); text != "" {
				return text
			}
		}
	case map[string]any:
		for _, key := range []string{"summary", "text", "message", "action", "recommendation", "title", "name"} {
			if text := firstNonEmptyString(typed, key); text != "" {
				return text
			}
		}
	}
	return ""
}

func summarizeDoctorArtifact(object map[string]any) string {
	rootCause := firstNonEmptyString(object, "root_cause")
	fix := firstNonEmptyString(object, "fix")

	switch {
	case rootCause != "" && fix != "":
		return strings.Join([]string{
			labelSentence("Root cause: ", rootCause),
			labelSentence("Fix: ", fix),
		}, " ")
	case rootCause != "":
		return labelSentence("Root cause: ", rootCause)
	case fix != "":
		return labelSentence("Fix: ", fix)
	default:
		return ""
	}
}

func summarizeBuilderArtifact(object map[string]any) string {
	summary := firstNonEmptyString(object, "summary")
	result := firstNonEmptyString(object, "result")
	verification := firstNonEmptyString(object, "verification")
	filesChanged := firstUsefulEntry(object["files_changed"])

	switch {
	case summary != "":
		if verification != "" {
			return strings.Join([]string{
				sentence(summary),
				labelSentence("Verification: ", verification),
			}, " ")
		}
		return sentence(summary)
	case result != "":
		if verification != "" {
			return strings.Join([]string{
				sentence(result),
				labelSentence("Verification: ", verification),
			}, " ")
		}
		return sentence(result)
	case filesChanged != "":
		parts := []string{labelSentence("Changed: ", filesChanged)}
		if verification != "" {
			parts = append(parts, labelSentence("Verification: ", verification))
		}
		return strings.Join(parts, " ")
	default:
		return ""
	}
}

func scoutSourceLocation(object map[string]any) string {
	sourceFile := firstNonEmptyString(object, "source_file", "origin_file", "file", "path")
	line := firstNonEmptyString(object, "source_line", "source_lines", "line", "lines")
	evidenceFile, evidenceLine := firstEvidenceFileLine(object["evidence"])
	if sourceFile == "" {
		sourceFile = evidenceFile
	}
	if sourceFile == "" {
		return ""
	}
	if line == "" && evidenceLine != "" && (evidenceFile == "" || evidenceFile == sourceFile) {
		line = evidenceLine
	}
	if line == "" {
		return sourceFile
	}
	return sourceFile + ":" + line
}

func firstEvidenceFileLine(value any) (string, string) {
	switch typed := value.(type) {
	case []any:
		for _, entry := range typed {
			if file, line := firstEvidenceFileLine(entry); file != "" {
				return file, line
			}
		}
	case map[string]any:
		file := firstNonEmptyString(typed, "file", "path", "source_file", "origin_file")
		line := firstNonEmptyString(typed, "line", "lines", "source_line", "source_lines")
		if file != "" {
			return file, line
		}
	}
	return "", ""
}

func firstNonEmptyString(object map[string]any, keys ...string) string {
	for _, key := range keys {
		if key == "" {
			continue
		}
		if value, ok := object[key]; ok {
			if text := stringifyDelegateValue(value); text != "" {
				return text
			}
		}
	}
	return ""
}

func firstUsefulEntry(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []any:
		for _, entry := range typed {
			if text := firstUsefulEntry(entry); text != "" {
				return text
			}
		}
	case map[string]any:
		for _, key := range []string{"summary", "text", "message", "path"} {
			if text := firstNonEmptyString(typed, key); text != "" {
				return text
			}
		}
	}
	return ""
}

func stringifyDelegateValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		if typed == float64(int64(typed)) {
			return strings.TrimSpace(strconv.FormatInt(int64(typed), 10))
		}
		return strings.TrimSpace(strconv.FormatFloat(typed, 'f', -1, 64))
	case int:
		return strings.TrimSpace(strconv.Itoa(typed))
	case int64:
		return strings.TrimSpace(strconv.FormatInt(typed, 10))
	case json.Number:
		return strings.TrimSpace(typed.String())
	default:
		return ""
	}
}

func humanizeDelegateText(text, separator string) string {
	text = strings.TrimSpace(strings.ToLower(text))
	if text == "" {
		return ""
	}
	switch separator {
	case "-":
		text = strings.ReplaceAll(text, "_", "-")
	default:
		text = strings.ReplaceAll(text, "_", " ")
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

func labelSentence(label, text string) string {
	label = strings.TrimSpace(label)
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	text = trimSentenceEnding(text)
	if label == "" {
		return text + "."
	}
	if !strings.HasSuffix(label, ":") {
		label = strings.TrimRight(label, " ")
	}
	return label + " " + text + "."
}

func sentence(text string) string {
	return labelSentence("", text)
}

func trimSentenceEnding(text string) string {
	return strings.TrimRight(strings.TrimSpace(text), ".!?")
}

func (o delegateOutcome) ContextText() string {
	switch {
	case strings.TrimSpace(o.Artifact) != "":
		return strings.TrimSpace(o.Artifact)
	case strings.TrimSpace(o.Message) != "":
		return strings.TrimSpace(o.Message)
	default:
		return strings.TrimSpace(o.Raw)
	}
}

func (o delegateOutcome) CarryContextText() string {
	message := strings.TrimSpace(o.Message)
	artifact := strings.TrimSpace(o.Artifact)
	switch {
	case artifact == "" && message == "":
		return strings.TrimSpace(o.Raw)
	case artifact == "":
		return message
	case message == "":
		return artifact
	case !o.Structured:
		return artifact
	case isGenericDelegateMessage(message):
		return artifact
	case sameDelegateContext(message, artifact):
		return artifact
	default:
		return "MESSAGE:\n" + message + "\n\nARTIFACT:\n" + artifact
	}
}

func sameDelegateContext(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" {
		return left == right
	}
	if left == right {
		return true
	}
	return trimSentenceEnding(left) == trimSentenceEnding(right)
}

func delegateArtifactSummaryShouldReplaceMessage(summary, message string) bool {
	summary = strings.TrimSpace(summary)
	message = strings.TrimSpace(message)
	if summary == "" || message == "" || sameDelegateContext(summary, message) {
		return false
	}
	if strings.Count(summary, "\n") > strings.Count(message, "\n") {
		return true
	}
	return len(summary) > len(message)+40
}
