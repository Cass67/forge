package skills

import "strings"

type Suggestion struct {
	Name   string
	Reason string
}

func Suggest(loaded []Skill, mode, input string, active map[string]bool) (Suggestion, bool) {
	candidates := []Suggestion{
		suggestionForMode(mode),
		suggestionForInput(input),
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.Name) == "" {
			continue
		}
		if active[strings.TrimSpace(candidate.Name)] {
			continue
		}
		if _, ok := Get(loaded, candidate.Name); ok {
			return candidate, true
		}
	}
	return Suggestion{}, false
}

func suggestionForMode(mode string) Suggestion {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "plan":
		return Suggestion{Name: "brainstorming", Reason: "planning work benefits from explicit design before implementation"}
	case "implement":
		return Suggestion{Name: "test-driven-development", Reason: "implementation work should start from a failing test"}
	case "review":
		return Suggestion{Name: "requesting-code-review", Reason: "review-mode work benefits from a findings-first code review workflow"}
	default:
		return Suggestion{}
	}
}

func suggestionForInput(input string) Suggestion {
	lower := strings.ToLower(strings.TrimSpace(input))
	switch {
	case looksLikeCrossRepoGapReport(lower):
		return Suggestion{}
	case containsAny(lower, "debug", "bug", "failing", "failure", "regression", "root cause", "broken"):
		return Suggestion{Name: "systematic-debugging", Reason: "the request looks like debugging work"}
	case looksLikeReviewOrStatusAudit(lower):
		return Suggestion{Name: "requesting-code-review", Reason: "the request looks like review work"}
	case looksLikePlanningRequest(lower):
		return Suggestion{Name: "brainstorming", Reason: "the request looks like planning or design work"}
	case containsAny(lower, "implement", "implementation", "build", "feature", "refactor", "fix", "add"):
		return Suggestion{Name: "test-driven-development", Reason: "the request looks like implementation work"}
	default:
		return Suggestion{}
	}
}
