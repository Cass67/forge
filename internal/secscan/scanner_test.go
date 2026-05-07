package secscan

import (
	"strings"
	"testing"
)

func TestScannerDetectsHighConfidenceDummyTokens(t *testing.T) {
	scanner := NewDefaultScanner()
	cases := []struct {
		name string
		text string
		rule string
	}{
		{name: "github pat", text: "token=" + "ghp_" + strings.Repeat("a", 36), rule: "github-pat"},
		{name: "aws access key", text: "id=" + "AKIA" + strings.Repeat("A", 16), rule: "aws-access-token"},
		{name: "anthropic key", text: "key=" + "sk-ant-api03-" + strings.Repeat("a", 84), rule: "anthropic-api-key"},
		{name: "openai key", text: "key=" + "sk-proj-" + strings.Repeat("a", 48), rule: "openai-api-key"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matches := scanner.Scan(tc.text)
			if len(matches) != 1 {
				t.Fatalf("matches = %d, want 1", len(matches))
			}
			if matches[0].RuleID != tc.rule {
				t.Fatalf("RuleID = %q, want %q", matches[0].RuleID, tc.rule)
			}
			if matches[0].Start <= 0 || matches[0].End <= matches[0].Start {
				t.Fatalf("invalid range: %#v", matches[0])
			}
		})
	}
}

func TestScannerIgnoresRandomUUID(t *testing.T) {
	matches := NewDefaultScanner().Scan("id=123e4567-e89b-12d3-a456-426614174000")
	if len(matches) != 0 {
		t.Fatalf("matches = %#v, want none", matches)
	}
}

func TestRedactReplacesMatchedValues(t *testing.T) {
	text := "token=" + "ghp_" + strings.Repeat("a", 36)
	matches := NewDefaultScanner().Scan(text)
	redacted := Redact(text, matches)
	if redacted == text {
		t.Fatal("expected redacted text to change")
	}
	if redacted != "token=<REDACTED:github-pat>" {
		t.Fatalf("redacted = %q", redacted)
	}
}

func TestMatchSummaryDoesNotIncludeMatchedValue(t *testing.T) {
	text := "token=" + "ghp_" + strings.Repeat("a", 36)
	matches := NewDefaultScanner().Scan(text)
	summary := Summary(matches)
	if summary != "github-pat" {
		t.Fatalf("summary = %q", summary)
	}
	if summary == text {
		t.Fatal("summary included raw input")
	}
}
