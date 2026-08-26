package tools

import (
	"context"
	"fmt"
	"strings"

	"forge/internal/skills"
)

// NewSkillTool returns the "Skill" tool: it loads a skill's instructions
// (its SKILL.md body) by name on demand, so the model can follow a procedure
// without every skill body sitting in the prompt. This is the standard
// progressive-disclosure Skill tool used by agent runtimes: skill names and
// descriptions are advertised cheaply (see skills.Describe) and the full body
// is pulled in only when a task matches one.
//
// load returns the currently available skills; a lazy loader keeps the tool
// fresh across skill reloads.
func NewSkillTool(load func() []skills.Skill) Tool {
	return Tool{
		Name:        "Skill",
		Description: "Loads a skill's instructions (its SKILL.md body) by name so you can follow its procedure. Use when a task matches a skill's description.",
		Parameters: []ParameterDef{
			{Name: "name", Type: "string", Description: "Name of the skill to load, e.g. \"grilling\" or \"tdd\". Prefixes and abbreviations are matched.", Required: true},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			_ = ctx
			name, _ := args["name"].(string)
			name = strings.TrimSpace(name)
			if name == "" {
				return fmt.Sprintf("No skill name given. Available skills:\n%s", skills.Describe(load())), nil
			}
			all := load()
			s, ok := skills.Get(all, name)
			if !ok {
				return fmt.Sprintf("No skill named %q found. Available skills:\n%s", name, skills.Describe(all)), nil
			}
			return fmt.Sprintf("[Skill: %s]\n\n%s", s.Name, s.ResolveBody()), nil
		},
	}
}
