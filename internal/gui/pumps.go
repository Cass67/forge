package gui

import (
	"forge/internal/agent/tools"
	"forge/internal/llm"
)

func (s *server) pumpEvents(events <-chan llm.Event) {
	for ev := range events {
		s.send(eventFrame{Type: "event", Event: toWireEvent(ev)})
	}
	s.send(doneFrame{Type: "done"})
}

func (s *server) pumpApprovals(ch <-chan tools.Action) {
	for a := range ch {
		s.send(approvalFrame{Type: "approval", Action: wireAction{
			Tool: a.Tool, Summary: a.Summary, Detail: a.Detail, Path: a.Path,
		}})
	}
}
