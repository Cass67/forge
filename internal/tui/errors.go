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
	if strings.Contains(lower, "403") && strings.Contains(lower, "forbidden") {
		if model := extractModelName(msg); model != "" && strings.Contains(lower, "chatgpt stream") {
			return "403 Forbidden — ChatGPT auth is not authorized for " + model
		}
		return "403 Forbidden"
	}
	if strings.Contains(lower, "429") && strings.Contains(lower, "too many requests") {
		return "429 Too Many Requests"
	}
	if strings.Contains(lower, "rate limit exceeded") {
		return "Rate limit exceeded"
	}
	return strings.TrimSpace(msg)
}

func extractModelName(msg string) string {
	const marker = "model: "
	idx := strings.Index(msg, marker)
	if idx < 0 {
		return ""
	}
	rest := msg[idx+len(marker):]
	for _, stop := range []string{")", ":", ","} {
		if cut := strings.Index(rest, stop); cut >= 0 {
			rest = rest[:cut]
			break
		}
	}
	return strings.TrimSpace(rest)
}
