package skills

import "strings"

const (
	AutoSkillsOff     = "off"
	AutoSkillsSuggest = "suggest"
	AutoSkillsAuto    = "auto"
)

func NormalizeAutoMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", AutoSkillsSuggest:
		return AutoSkillsSuggest
	case AutoSkillsOff:
		return AutoSkillsOff
	case AutoSkillsAuto:
		return AutoSkillsAuto
	default:
		return ""
	}
}

func SkillMessage(s Skill) string {
	return "[Skill: " + s.Name + "]\n\n" + s.Body
}

func SkillMessageWithUserInput(s Skill, input string) string {
	return SkillMessage(s) + "\n\n" + input
}

func DetectAuto(loaded []Skill, input string) (Skill, bool) {
	lower := strings.ToLower(input)
	for _, s := range loaded {
		name := strings.ToLower(s.Name)
		desc := strings.ToLower(s.Description)
		if strings.Contains(lower, name) {
			return s, true
		}
		switch name {
		case "brainstorming":
			if containsAny(lower, "plan", "planning", "brainstorm", "design", "architecture", "approach", "review", "code review", "audit") {
				return s, true
			}
		case "systematic-debugging":
			if containsAny(lower, "debug", "bug", "failing", "failure", "regression", "investigate", "root cause", "broken") {
				return s, true
			}
		case "test-driven-development", "tdd":
			if containsAny(lower, "implement", "implementation", "develop", "development", "build", "feature", "refactor", "add tests", "write tests") {
				return s, true
			}
		default:
			if desc != "" && strings.Contains(lower, desc) {
				return s, true
			}
		}
	}
	return Skill{}, false
}

func containsAny(input string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(input, term) {
			return true
		}
	}
	return false
}
