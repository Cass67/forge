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

func TestScannerDetectsExpandedSecretMatrix(t *testing.T) {
	scanner := NewDefaultScanner()
	cases := []struct {
		name string
		text string
		rule string
	}{
		{name: "bearer token", text: "Authorization: Bearer " + strings.Repeat("b", 32), rule: "bearer-token"},
		{name: "generic token assignment", text: "TOKEN=" + strings.Repeat("x", 24), rule: "generic-token"},
		{name: "private key block", text: "-----BEGIN PRIVATE KEY-----\n" + strings.Repeat("a", 64) + "\n-----END PRIVATE KEY-----", rule: "private-key"},
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
		})
	}
}

func TestScannerPrefersSpecificSecretInsideGenericAssignment(t *testing.T) {
	text := "TOKEN=" + "ghp_" + strings.Repeat("a", 36)
	matches := NewDefaultScanner().Scan(text)
	if len(matches) != 1 {
		t.Fatalf("matches = %d, want 1", len(matches))
	}
	if matches[0].RuleID != "github-pat" {
		t.Fatalf("RuleID = %q, want github-pat", matches[0].RuleID)
	}
	if redacted := Redact(text, matches); redacted != "TOKEN=<REDACTED:github-pat>" {
		t.Fatalf("redacted = %q", redacted)
	}
}

func TestScannerDetectsLowercaseGenericAssignments(t *testing.T) {
	cases := []string{
		"api_key=" + strings.Repeat("x", 24),
		"password: " + strings.Repeat("y", 24),
		"secret=" + strings.Repeat("z", 24),
	}

	for _, text := range cases {
		matches := NewDefaultScanner().Scan(text)
		if len(matches) != 1 {
			t.Fatalf("matches = %d, want 1 for %q", len(matches), text)
		}
		if matches[0].RuleID != "generic-token" {
			t.Fatalf("RuleID = %q, want generic-token", matches[0].RuleID)
		}
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

func TestScannerPublicFormattingOmitsExpandedMatchedValues(t *testing.T) {
	secrets := []string{
		"Authorization: Bearer " + strings.Repeat("b", 32),
		"OPENAI_API_KEY=" + "sk-proj-" + strings.Repeat("c", 48),
		"ANTHROPIC_API_KEY=" + "sk-ant-api03-" + strings.Repeat("d", 84),
		"AWS_ACCESS_KEY_ID=" + "AKIA" + strings.Repeat("A", 16),
		"TOKEN=" + strings.Repeat("e", 24),
		"-----BEGIN PRIVATE KEY-----\n" + strings.Repeat("f", 64) + "\n-----END PRIVATE KEY-----",
	}

	for _, secret := range secrets {
		matches := NewDefaultScanner().Scan(secret)
		if len(matches) == 0 {
			t.Fatalf("expected match for dummy secret shape")
		}
		redacted := Redact(secret, matches)
		summary := Summary(matches)
		if strings.Contains(redacted, secret) || strings.Contains(summary, secret) {
			t.Fatalf("public formatting exposed matched value: redacted=%q summary=%q", redacted, summary)
		}
		if !strings.Contains(redacted, "<REDACTED:") {
			t.Fatalf("redacted output missing marker: %q", redacted)
		}
	}
}
