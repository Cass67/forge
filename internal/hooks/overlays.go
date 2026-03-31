package hooks

import (
	"strings"

	"forge/internal/agent/promptcomposer"
)

type Priority int

const (
	PriorityLow Priority = iota
	PriorityNormal
	PriorityHigh
)

type Overlay struct {
	Key        string
	Content    string
	Priority   Priority
	Provenance string
}

func ToPromptOverlays(overlays []Overlay) []promptcomposer.Overlay {
	out := make([]promptcomposer.Overlay, 0, len(overlays))
	for _, overlay := range overlays {
		content := strings.TrimSpace(overlay.Content)
		if content == "" {
			continue
		}
		key := strings.TrimSpace(overlay.Key)
		if key == "" {
			key = "overlay"
		}
		provenance := strings.TrimSpace(overlay.Provenance)
		if provenance == "" {
			provenance = "runtime"
		}
		out = append(out, promptcomposer.Overlay{
			Key:      "hook:" + key,
			Priority: toPromptPriority(overlay.Priority),
			Content:  "[hook:" + provenance + "]\n" + content,
		})
	}
	return out
}

func toPromptPriority(priority Priority) promptcomposer.Priority {
	switch priority {
	case PriorityHigh:
		return promptcomposer.PriorityHigh
	case PriorityLow:
		return promptcomposer.PriorityLow
	default:
		return promptcomposer.PriorityNormal
	}
}
