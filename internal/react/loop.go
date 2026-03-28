package react

import (
	"context"
	"fmt"
)

type TurnRunner interface {
	Run(context.Context, string) error
}

type Config struct {
	Agent           TurnRunner
	Session         *Session
	Progress        func(string)
	MaxSessionTurns int
}

type Runner struct {
	agent           TurnRunner
	session         *Session
	progress        func(string)
	maxSessionTurns int
}

func NewRunner(cfg Config) *Runner {
	session := cfg.Session
	if session == nil {
		session = NewSession()
	}
	return &Runner{
		agent:           cfg.Agent,
		session:         session,
		progress:        cfg.Progress,
		maxSessionTurns: maxSessionTurns(cfg.MaxSessionTurns),
	}
}

func (r *Runner) Run(ctx context.Context, input string) error {
	if r == nil || r.agent == nil {
		return fmt.Errorf("react runner: agent is nil")
	}
	prompt := BuildPrompt(input)
	if prompt == "" {
		return nil
	}
	turn := r.session.RecordInput(prompt)
	if r.progress != nil {
		r.progress(fmt.Sprintf("react runtime: executing turn %d", turn))
	}
	if CompactSessionHistory(r.session, r.maxSessionTurns) && r.progress != nil {
		r.progress("react runtime: compacted session context")
	}
	return r.agent.Run(ctx, prompt)
}

func maxSessionTurns(value int) int {
	if value < 1 {
		return 20
	}
	return value
}
