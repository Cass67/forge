package secscan

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
)

type Match struct {
	RuleID string
	Start  int
	End    int
}

type Rule struct {
	ID      string
	Pattern *regexp.Regexp
}

type Scanner struct {
	rules []Rule
}

func NewDefaultScanner() *Scanner {
	return &Scanner{rules: []Rule{
		{ID: "github-pat", Pattern: regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9_]{30,}\b`)},
		{ID: "aws-access-token", Pattern: regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`)},
		{ID: "anthropic-api-key", Pattern: regexp.MustCompile(`\bsk-ant-api[0-9]{2}-[A-Za-z0-9_-]{80,}\b`)},
		{ID: "openai-api-key", Pattern: regexp.MustCompile(`\bsk-(?:proj-)?[A-Za-z0-9_-]{40,}\b`)},
		{ID: "bearer-token", Pattern: regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._\-]{16,}\b`)},
		{ID: "generic-token", Pattern: regexp.MustCompile(`(?i)\b[A-Z0-9_]*(?:TOKEN|API[_-]?KEY|PASSWORD|SECRET)[A-Z0-9_]*\s*[:=]\s*["']?[A-Za-z0-9._+\-/=]{12,}["']?`)},
		{ID: "private-key", Pattern: regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)},
	}}
}

func (s *Scanner) Scan(text string) []Match {
	if s == nil || text == "" {
		return nil
	}
	var matches []Match
	for _, rule := range s.rules {
		if rule.Pattern == nil || strings.TrimSpace(rule.ID) == "" {
			continue
		}
		for _, loc := range rule.Pattern.FindAllStringIndex(text, -1) {
			if len(loc) != 2 || loc[1] <= loc[0] {
				continue
			}
			matches = append(matches, Match{RuleID: rule.ID, Start: loc[0], End: loc[1]})
		}
	}
	matches = dropGenericTokenOverlaps(matches)
	slices.SortStableFunc(matches, func(a, b Match) int {
		if a.Start == b.Start {
			return cmp.Compare(a.End, b.End)
		}
		return cmp.Compare(a.Start, b.Start)
	})
	return coalesceMatches(matches)
}

func dropGenericTokenOverlaps(matches []Match) []Match {
	if len(matches) == 0 {
		return nil
	}
	out := make([]Match, 0, len(matches))
	for i, match := range matches {
		if match.RuleID == "generic-token" {
			overlapsSpecific := false
			for j, other := range matches {
				if i == j || other.RuleID == "generic-token" {
					continue
				}
				if rangesOverlap(match, other) {
					overlapsSpecific = true
					break
				}
			}
			if overlapsSpecific {
				continue
			}
		}
		out = append(out, match)
	}
	return out
}

func rangesOverlap(a, b Match) bool {
	return a.Start < b.End && b.Start < a.End
}

func Redact(text string, matches []Match) string {
	if text == "" || len(matches) == 0 {
		return text
	}
	matches = coalesceMatches(matches)
	var out strings.Builder
	pos := 0
	for _, match := range matches {
		if match.Start < pos || match.Start < 0 || match.End > len(text) || match.End <= match.Start {
			continue
		}
		out.WriteString(text[pos:match.Start])
		out.WriteString("<REDACTED:" + match.RuleID + ">")
		pos = match.End
	}
	out.WriteString(text[pos:])
	return out.String()
}

func Summary(matches []Match) string {
	matches = coalesceMatches(matches)
	seen := map[string]struct{}{}
	ids := make([]string, 0, len(matches))
	for _, match := range matches {
		id := strings.TrimSpace(match.RuleID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return strings.Join(ids, ", ")
}

func coalesceMatches(matches []Match) []Match {
	if len(matches) == 0 {
		return nil
	}
	slices.SortStableFunc(matches, func(a, b Match) int {
		if a.Start == b.Start {
			return cmp.Compare(a.End, b.End)
		}
		return cmp.Compare(a.Start, b.Start)
	})
	out := make([]Match, 0, len(matches))
	for _, match := range matches {
		if match.Start < 0 || match.End <= match.Start || strings.TrimSpace(match.RuleID) == "" {
			continue
		}
		if len(out) > 0 && match.Start < out[len(out)-1].End {
			if match.End > out[len(out)-1].End {
				out[len(out)-1].End = match.End
			}
			continue
		}
		out = append(out, match)
	}
	return out
}
