package skills

import "strings"

// Descriptor is the stable skill catalog entry shared with the primary
// assistant and hidden workers.
type Descriptor struct {
	Name        string
	Description string
	Source      string
}

// UseRecord captures one runtime skill-use decision for later debugging.
type UseRecord struct {
	Name     string
	WorkerID string
	Outcome  string
}

// Runtime exposes the non-shell skill adapter shared by Forge and its workers.
type Runtime struct {
	loaded []Skill
	uses   []UseRecord
}

func NewRuntime(loaded []Skill) *Runtime {
	copied := append([]Skill(nil), loaded...)
	return &Runtime{loaded: copied}
}

func (r *Runtime) ListSkills() []Descriptor {
	return Descriptors(r.loaded)
}

func (r *Runtime) LoadByName(name string) (Skill, bool) {
	return Get(r.loaded, name)
}

func (r *Runtime) ResolveRequired(input string) (Skill, bool) {
	name := resolveRequiredSkillName(input)
	if name == "" {
		return Skill{}, false
	}
	return r.LoadByName(name)
}

func (r *Runtime) ResolveAuto(input string) (Skill, bool) {
	return resolveAutoSkill(r.loaded, input)
}

func (r *Runtime) InjectableMessage(skill Skill) string {
	return "[Skill: " + skill.Name + "]\n\n" + skill.Body
}

func (r *Runtime) RecordSkillUse(name, workerID, outcome string) {
	r.uses = append(r.uses, UseRecord{
		Name:     strings.TrimSpace(name),
		WorkerID: strings.TrimSpace(workerID),
		Outcome:  strings.TrimSpace(outcome),
	})
}

func (r *Runtime) UseRecords() []UseRecord {
	return append([]UseRecord(nil), r.uses...)
}

func resolveRequiredSkillName(input string) string {
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

func resolveAutoSkill(loaded []Skill, input string) (Skill, bool) {
	lower := strings.ToLower(input)
	for _, s := range loaded {
		name := strings.ToLower(s.Name)
		desc := strings.ToLower(s.Description)
		if strings.Contains(lower, name) {
			return s, true
		}
		if desc != "" && strings.Contains(lower, desc) {
			return s, true
		}
	}
	return Skill{}, false
}
