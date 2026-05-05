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
	return NewRuntime([]Skill{s}).InjectableMessage(s)
}

func SkillMessageWithUserInput(s Skill, input string) string {
	msg := SkillMessage(s)
	if strings.TrimSpace(input) == "" {
		return msg
	}
	return msg + "\n\n" + input
}

func DetectAuto(loaded []Skill, input string) (Skill, bool) {
	return NewRuntime(loaded).ResolveAuto(input)
}

// AutoModeWhenSuperpowers upgrades the auto-skills mode to "auto" when the
// using-superpowers skill is installed and the configured mode is the default
// "suggest". This ensures superpowers skills are actually activated.
func AutoModeWhenSuperpowers(configuredMode string, loaded []Skill) string {
	if configuredMode != "" && configuredMode != AutoSkillsSuggest {
		return configuredMode
	}
	if _, ok := Get(loaded, "using-superpowers"); ok {
		return AutoSkillsAuto
	}
	return AutoSkillsSuggest
}

func containsAny(input string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(input, term) {
			return true
		}
	}
	return false
}
