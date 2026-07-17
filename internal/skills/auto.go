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

func DetectAuto(loaded []Skill, input string) (Skill, bool) {
	return NewRuntime(loaded).ResolveAuto(input)
}

func containsAny(input string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(input, term) {
			return true
		}
	}
	return false
}
