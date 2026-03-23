package tui

import "forge/internal/llm"

func eventErrorMessage(ev llm.Event) string {
	if ev.Err != nil {
		return ev.Err.Error()
	}
	if ev.Text != "" {
		return ev.Text
	}
	return "unknown error"
}
