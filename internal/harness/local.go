package harness

import (
	"context"
	"strings"

	"forge/internal/agent/tools"
)

type AgentRunner interface {
	Run(ctx context.Context, userMessage string) error
	LastResponse() string
}

type ScopedAgentRunner interface {
	AgentRunner
	SetTools(reg *tools.Registry)
}

type LocalExecutor interface {
	Execute(ctx context.Context, turn UserTurn, class Classification) (Observation, error)
}

type AgentExecutor struct {
	Agent        ScopedAgentRunner
	DefaultTools *tools.Registry
	InspectTools *tools.Registry
}

func (e AgentExecutor) Execute(ctx context.Context, turn UserTurn, class Classification) (Observation, error) {
	userMessage := turn.Text
	if useReadOnlyInspectScope(class) {
		userMessage = buildInspectTurnPrompt(class, turn.Text)
		if e.InspectTools != nil {
			e.Agent.SetTools(e.InspectTools)
			if e.DefaultTools != nil {
				defer e.Agent.SetTools(e.DefaultTools)
			}
		}
	} else if e.DefaultTools != nil {
		e.Agent.SetTools(e.DefaultTools)
	}

	if err := e.Agent.Run(ctx, userMessage); err != nil {
		return Observation{
			Status:   ObservationBlocked,
			Response: "",
			Summary:  err.Error(),
			TopicKey: class.TopicKey,
			Err:      err,
		}, err
	}

	response := strings.TrimSpace(e.Agent.LastResponse())
	return Observation{
		Status:   ObservationComplete,
		Response: response,
		Summary:  response,
		TopicKey: class.TopicKey,
	}, nil
}

func useReadOnlyInspectScope(class Classification) bool {
	return class.Family == FamilyInspect && !class.WantsAction
}

func buildInspectTurnPrompt(class Classification, userMessage string) string {
	userMessage = strings.TrimSpace(userMessage)
	scope := inspectPromptScope(class)
	lines := []string{
		"HARNESS MODE: inspect",
		"INSPECT SCOPE: " + scope,
		"This is a read-only inspection turn.",
		"Rules for this turn:",
		"- inspect the actual workspace before answering",
		"- each working turn must emit exactly one tool call and no prose",
		"- start with the smallest discovery step that reduces uncertainty",
		"- prefer list_dir, read_file, glob, search, git_status, git_log, and git_diff",
		"- do not ask the user to choose between multiple summary formats; provide the most useful walkthrough directly",
		"- ground claims in tool results from this turn or prior evidence already in the conversation",
		"- keep the scope tight and stop once you have enough evidence",
	}
	if scope == "focused-files" {
		lines = append(lines,
			"- start by discovering candidate files before reading contents",
			"- sample a small representative set of matching files; do not read every matching file unless the user explicitly asks for exhaustive coverage",
			"- make it clear when the answer is based on sampled files rather than exhaustive coverage",
		)
	}
	if class.WantsEvaluation {
		lines = append(lines,
			"- lead with the highest-value improvements or findings instead of a neutral walkthrough",
			"- distinguish observed facts from recommendations so the user can see what you saw versus what you advise",
			"- keep the answer actionable: explain why each issue matters and what to do next",
		)
		if scope == "repository" || scope == "directory" {
			lines = append(lines,
				"- inspect at least one representative implementation file when one is present; do not stop at README or directory listings alone",
			)
		}
	}
	lines = append(lines, "", "USER REQUEST:", userMessage)
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func inspectPromptScope(class Classification) string {
	switch {
	case strings.HasPrefix(class.TopicKey, "files:"):
		return "focused-files"
	case strings.HasPrefix(class.TopicKey, "workspace:repository"):
		return "repository"
	case strings.HasPrefix(class.TopicKey, "workspace:directory"):
		return "directory"
	default:
		return "generic"
	}
}
