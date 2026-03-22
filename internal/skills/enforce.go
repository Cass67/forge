package skills

import "strings"

// RequiredForInput returns the name of a skill that should be activated before
// handling the given user input, if any.
func RequiredForInput(input string) string {
	lower := strings.ToLower(strings.TrimSpace(input))
	switch {
	case containsAny(lower, "plan", "planning", "brainstorm", "design", "architecture", "approach"):
		return "brainstorming"
	case containsAny(lower, "debug", "bug", "failing", "failure", "regression", "investigate", "root cause", "broken"):
		return "systematic-debugging"
	case containsAny(lower, "implement", "implementation", "develop", "development", "build", "feature", "refactor", "add tests", "write tests"):
		return "test-driven-development"
	default:
		return ""
	}
}

