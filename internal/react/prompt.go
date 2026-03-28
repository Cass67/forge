package react

import "strings"

// BuildPrompt normalizes per-turn user input for the ReAct loop.
func BuildPrompt(input string) string {
	return strings.TrimSpace(input)
}
