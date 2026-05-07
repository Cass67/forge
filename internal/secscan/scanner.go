package secscan

import (
	"regexp"
	"sort"
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
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Start == matches[j].Start {
			return matches[i].End < matches[j].End
		}
		return matches[i].Start < matches[j].Start
	})
	return coalesceMatches(matches)
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
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Start == matches[j].Start {
			return matches[i].End < matches[j].End
		}
		return matches[i].Start < matches[j].Start
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
