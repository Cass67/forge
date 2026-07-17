package tui

import (
	"testing"

	"forge/internal/llm"

	tea "github.com/charmbracelet/bubbletea"
)

// After a pipeline completes, the TUI must return to normal chat — Enter and
// 'q' previously quit the whole app, losing the session.
func TestPipelineDoneReturnsToChatAndDoesNotQuit(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 120
	m.height = 30
	m.pipelineActive = true
	m.pipelineViewActive = true
	m.pipelinePhase = "auditor"

	consumed, _ := m.handlePipelineLLMEvent(llm.Event{Kind: llm.EventDone, Text: "output: /tmp/out"})
	if !consumed {
		t.Fatal("done event should be consumed by pipeline handler")
	}
	if m.pipelineActive || m.pipelineViewActive {
		t.Fatal("pipeline should deactivate and return to chat view on completion")
	}

	m.inputBuf = "you did not write my file"
	m.inputPos = len(m.inputBuf)
	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(ChatModel)
	if cmd != nil {
		if msg := cmd(); msg != nil {
			if _, isQuit := msg.(tea.QuitMsg); isQuit {
				t.Fatal("Enter after pipeline completion must not quit the app")
			}
		}
	}
}

func TestPipelineAbortReturnsToChat(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 120
	m.height = 30
	m.pipelineActive = true
	m.pipelineViewActive = true

	m.handlePipelineLLMEvent(llm.Event{Kind: llm.EventError, Err: errFake})
	if m.pipelineActive || m.pipelineViewActive {
		t.Fatal("pipeline should deactivate and return to chat view on abort")
	}
}

var errFake = errTest("boom")

type errTest string

func (e errTest) Error() string { return string(e) }
