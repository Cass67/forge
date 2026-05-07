package promptcomposer

import (
	"strings"
)

type StaticInput struct {
	Identity       string
	System         string
	Responsiveness string
	Planning       string
	Delegation     string
	Validation     string
	Progress       string
	Autonomy       string
	FinalAnswer    string
}

type Priority int

const (
	PriorityLow Priority = iota
	PriorityNormal
	PriorityHigh
)

type Overlay struct {
	Key      string
	Priority Priority
	Content  string
}

type composeOptions struct {
	maxBytes int
}

type Option func(*composeOptions)

func WithMaxBytes(max int) Option {
	return func(cfg *composeOptions) {
		cfg.maxBytes = max
	}
}

func Compose(static StaticInput, overlays []Overlay, opts ...Option) string {
	cfg := composeOptions{}
	for _, opt := range opts {
		opt(&cfg)
	}

	sections := staticSections(static)
	out := joinSections(sections)
	if cfg.maxBytes > 0 && len(out) > cfg.maxBytes {
		out = truncateToBudget(sections, nil, cfg.maxBytes)
	}

	filtered := make([]Overlay, 0, len(overlays))
	for _, overlay := range overlays {
		if strings.TrimSpace(overlay.Content) == "" {
			continue
		}
		filtered = append(filtered, Overlay{
			Key:      strings.TrimSpace(overlay.Key),
			Priority: overlay.Priority,
			Content:  strings.TrimSpace(overlay.Content),
		})
	}

	if len(filtered) == 0 {
		return out
	}

	for _, overlay := range sortOverlays(filtered) {
		candidate := joinSections(append(sections, overlay.Content))
		if cfg.maxBytes > 0 && len(candidate) > cfg.maxBytes {
			continue
		}
		sections = append(sections, overlay.Content)
		out = candidate
	}

	if out == "" {
		return truncateToBudget(staticSections(static), nil, cfg.maxBytes)
	}
	return out
}

func staticSections(in StaticInput) []string {
	var sections []string
	for _, part := range []string{
		in.Identity,
		in.System,
		in.Responsiveness,
		in.Planning,
		in.Delegation,
		in.Validation,
		in.Progress,
		in.Autonomy,
		in.FinalAnswer,
	} {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		sections = append(sections, part)
	}
	return sections
}

func joinSections(parts []string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		filtered = append(filtered, part)
	}
	return strings.Join(filtered, "\n\n")
}

func sortOverlays(overlays []Overlay) []Overlay {
	sorted := append([]Overlay(nil), overlays...)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].Priority > sorted[i].Priority {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	return sorted
}

func truncateToBudget(static []string, overlays []Overlay, maxBytes int) string {
	if maxBytes <= 0 {
		return joinSections(static)
	}
	sections := append([]string(nil), static...)
	for _, overlay := range sortOverlays(overlays) {
		candidate := joinSections(append(sections, overlay.Content))
		if len(candidate) > maxBytes {
			continue
		}
		sections = append(sections, overlay.Content)
	}
	if joined := joinSections(sections); len(joined) <= maxBytes {
		return joined
	}
	joined := joinSections(static)
	if len(joined) <= maxBytes {
		return joined
	}
	return joined[:maxBytes]
}
