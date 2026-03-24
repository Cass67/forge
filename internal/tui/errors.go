package tui

import (
	"strings"

	"forge/internal/llm"
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
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "429") && strings.Contains(lower, "too many requests") {
		return "429 Too Many Requests"
	}
	if strings.Contains(lower, "rate limit exceeded") {
		return "Rate limit exceeded"
	}
	return strings.TrimSpace(msg)
}
