package tui

import "strings"

// NudgeSuggestion is a non-intrusive hint driven by runtime policy and task state.
// Each suggestion has a short label shown in the status header and an optional
// hint shown as a flash message when the nudge first fires.
type NudgeSuggestion struct {
	// Label is shown as a compact badge in the status header (e.g. "[plan]").
	// Empty means no active nudge.
	Label string
	// Flash is shown once as a transient flash message when the nudge first arrives.
	// Empty means no flash.
	Flash string
	// Kind categorises the nudge for filtering and tests.
	Kind NudgeKind
}

// NudgeKind identifies what type of nudge this is.
type NudgeKind string

const (
	NudgeNone         NudgeKind = ""
	NudgeMode         NudgeKind = "mode"
	NudgeSkill        NudgeKind = "skill"
	NudgePlanMode     NudgeKind = "plan_mode"
	NudgeVerification NudgeKind = "verification"
)

// SelectNudge returns the highest-priority nudge for the current runtime state.
// It is a pure function so it can be tested independently of the TUI.
//
// mode is the current session mode (plan, implement, review, validate, preview, chat, inspect).
// taskOp is the inferred task operation from DetectTaskStateFromInput (same set).
// suggestedSkill is the name of the skill recommended by the skills policy, or empty.
func SelectNudge(mode, taskOp, suggestedSkill string) NudgeSuggestion {
	mode = strings.TrimSpace(strings.ToLower(mode))
	taskOp = strings.TrimSpace(strings.ToLower(taskOp))
	suggestedSkill = strings.TrimSpace(suggestedSkill)

	// Non-default session mode is the highest priority nudge — always show it.
	switch mode {
	case "plan":
		return NudgeSuggestion{Kind: NudgeMode, Label: "[plan]", Flash: ""}
	case "implement":
		return NudgeSuggestion{Kind: NudgeMode, Label: "[implementing]", Flash: ""}
	case "review":
		return NudgeSuggestion{Kind: NudgeMode, Label: "[review]", Flash: ""}
	case "validate":
		return NudgeSuggestion{Kind: NudgeMode, Label: "[validate]", Flash: ""}
	case "preview":
		return NudgeSuggestion{Kind: NudgeMode, Label: "[preview]", Flash: ""}
	}

	// Task operation can suggest entering plan mode on ambiguous/large work.
	if taskOp == "plan" && mode != "plan" {
		return NudgeSuggestion{
			Kind:  NudgePlanMode,
			Label: "[plan?]",
			Flash: "Hint: use /enter_plan_mode for structured planning on this task",
		}
	}

	// Verification nudge when in implement mode with active validation need.
	if taskOp == "validate" {
		return NudgeSuggestion{
			Kind:  NudgeVerification,
			Label: "[verify?]",
			Flash: "Hint: run tests or checks to verify the change before finishing",
		}
	}

	// Skill suggestion is shown when the skills policy has a recommended skill.
	if suggestedSkill != "" {
		return NudgeSuggestion{
			Kind:  NudgeSkill,
			Label: "",
			Flash: "suggested skill: /" + suggestedSkill,
		}
	}

	return NudgeSuggestion{}
}
