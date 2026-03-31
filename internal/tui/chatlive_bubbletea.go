package tui

import (
	"forge/internal/llm"

	tea "github.com/charmbracelet/bubbletea"
)

// RunChatLiveBubbleTea runs the chat interface using Bubble Tea.
// It has the same signature as RunChatLive for drop-in replacement.
func RunChatLiveBubbleTea(events <-chan llm.Event, cfg ChatLiveConfig, inputCh chan<- string, doneCh <-chan struct{}) ChatLiveResult {
	m := NewChatModel(cfg)
	m.inputCh = inputCh
	m.responseCh = cfg.ResponseCh

	p := tea.NewProgram(m, programOptionsForSurfaceMode(cfg.SurfaceMode())...)

	// Wire NotifyNudge so the runtime can push structured nudges to the TUI.
	if cfg.NotifyNudge != nil {
		// NotifyNudge is set by the caller — wrap it to also forward to the program.
		origNotify := cfg.NotifyNudge
		wrapped := func(mode, taskOp, suggestedSkill string) {
			origNotify(mode, taskOp, suggestedSkill)
			nudge := SelectNudge(mode, taskOp, suggestedSkill)
			if nudge.Kind != NudgeNone {
				p.Send(agentNudgeMsg(nudge))
			}
		}
		cfg.NotifyNudge = wrapped
		// Publish the wrapped function so the runtime goroutine can call it
		// directly and also trigger p.Send.
		if cfg.NotifyNudgeSink != nil {
			*cfg.NotifyNudgeSink = wrapped
		}
	}

	// Feed LLM events into the program
	go func() {
		for ev := range events {
			p.Send(ev)
		}
		p.Send(llm.Event{Kind: llm.EventDone})
	}()

	// Feed approval requests
	if cfg.ApprovalCh != nil {
		go func() {
			for action := range cfg.ApprovalCh {
				p.Send(chatApprovalMsg(action))
			}
		}()
	}

	// Feed done signals
	go func() {
		for range doneCh {
			p.Send(llm.Event{Kind: llm.EventDone})
		}
	}()

	finalModel, _ := p.Run()
	fm := finalModel.(ChatModel)

	return ChatLiveResult{
		Aborted: fm.status == "aborted",
	}
}
