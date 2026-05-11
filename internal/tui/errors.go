package tui

import (
	"forge/internal/llm"
	"forge/internal/resilience/errors"
)

func eventErrorMessage(ev llm.Event) string {
	msg := ""
	if ev.Err != nil {
		msg = ev.Err.Error()
	}
	if ev.Text != "" {
		msg = ev.Text
	}
	if msg == "" {
		return "unknown error"
	}
	return distillErrorMessage(msg)
}

func distillErrorMessage(msg string) string {
	fe := errors.ClassifyError(nil)
	_ = fe
	exhausted := containsLower(msg, "attempts failed")
	if exhausted {
		for _, check := range []struct {
			pattern string
			result  string
		}{
			{"500", "Server error after retries"},
			{"502", "Bad gateway after retries"},
			{"503", "Service unavailable after retries"},
			{"timeout", "Request timed out after retries"},
			{"connection reset", "Connection reset after retries"},
		} {
			if containsLower(msg, check.pattern) {
				return check.result
			}
		}
	}
	// Use the resilience taxonomy for classification, but keep user-friendly formatting
	// Check against known patterns
	for _, check := range []struct {
		pattern string
		result  string
	}{
		{"403", "403 Forbidden — check authentication"},
		{"429", "429 Too Many Requests — rate limited"},
		{"rate limit exceeded", "Rate limit exceeded"},
		{"context_length_exceeded", "Context window exceeded — session will be compacted"},
		{"insufficient_quota", "Billing/quota error — check your account"},
		{"500", "Server error — retrying"},
		{"502", "Bad gateway — retrying"},
		{"503", "Service unavailable — retrying"},
		{"timeout", "Request timed out — retrying"},
		{"connection reset", "Connection reset — retrying"},
	} {
		if containsLower(msg, check.pattern) {
			return check.result
		}
	}
	return msg
}

func containsLower(haystack, needle string) bool {
	for i := 0; i <= len(haystack)-len(needle); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			c := haystack[i+j]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

//nolint:unused // placeholder for future TUI error display wiring
func extractModelName(msg string) string {
	const marker = "model: "
	idx := indexLower(msg, marker)
	if idx < 0 {
		return ""
	}
	rest := msg[idx+len(marker):]
	for _, stop := range []string{")", ":", ","} {
		if cut := indexLower(rest, stop); cut >= 0 {
			rest = rest[:cut]
			break
		}
	}
	return rest
}

//nolint:unused // placeholder for future TUI error display wiring
func indexLower(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			c := s[i+j]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != substr[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
