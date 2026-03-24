package agent

import (
	"encoding/json"
	"strings"
)

type delegateOutcome struct {
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
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return delegateOutcome{Status: "blocked", Raw: raw}
	}

	if envelope, ok := parseDelegateEnvelope(raw); ok {
		return delegateOutcome{
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

	upper := strings.ToUpper(raw)
	switch {
	case raw == "(sub-agent produced no output)":
		return delegateOutcome{Status: "blocked", Message: raw, Raw: raw}
	case strings.HasPrefix(upper, "CANCELLED:"):
		return delegateOutcome{Status: "blocked", Message: raw, Raw: raw}
	case strings.HasPrefix(upper, "AGENT ERROR"):
		return delegateOutcome{Status: "blocked", Message: raw, Raw: raw}
	default:
		return delegateOutcome{Status: "complete", Message: raw, Artifact: raw, Raw: raw}
	}
}

func parseDelegateEnvelope(raw string) (delegateEnvelope, bool) {
	trimmed := strings.TrimSpace(stripJSONFence(raw))
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
		return delegateEnvelope{}, false
	}
	var envelope delegateEnvelope
	if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
		return delegateEnvelope{}, false
	}
	if normalizeDelegateStatus(envelope.Status) == "" {
		return delegateEnvelope{}, false
	}
	return envelope, true
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
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "complete":
		return "complete"
	case "blocked":
		return "blocked"
	default:
		return ""
	}
}

func (o delegateOutcome) Completed() bool {
	return o.Status == "complete"
}

func (o delegateOutcome) Blocked() bool {
	return o.Status == "blocked"
}

func (o delegateOutcome) DisplayText() string {
	switch {
	case strings.TrimSpace(o.Message) != "":
		return strings.TrimSpace(o.Message)
	case strings.TrimSpace(o.Artifact) != "":
		return strings.TrimSpace(o.Artifact)
	default:
		return strings.TrimSpace(o.Raw)
	}
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
