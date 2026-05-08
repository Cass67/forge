package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"forge/internal/auth"
	"forge/internal/chatgptauth"
	"forge/internal/chatstate"
	"forge/internal/claudeauth"
	"forge/internal/codexusage"
	"forge/internal/copilot"
	"forge/internal/llm"
	"forge/internal/modelcatalog"
	"forge/internal/skills"
)

func setToolsContent(m *ChatModel, content string) {
	m.toolsSections = nil
	if content != "" {
		m.toolsSections = []toolsSection{{buf: content}}
	}
}

func TestChatModelInit(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test-model", WorkDir: "/tmp"})
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init should return a command")
	}
}

func TestChatModelAddMessage(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test-model", WorkDir: "/tmp"})
	m.AddMessage(ChatMessage{Kind: MsgUser, Header: "You • 12:00:00", Content: "hello"})
	if len(m.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.messages))
	}
	if m.messages[0].Content != "hello" {
		t.Fatalf("message content = %q", m.messages[0].Content)
	}
}

func TestChatModelBlocksPromptWithoutConfiguredModel(t *testing.T) {
	inputCh := make(chan string, 1)
	m := NewChatModel(ChatLiveConfig{Model: "", WorkDir: "/tmp"})
	m.inputCh = inputCh
	m.inputBuf = "help me set this up"
	m.inputPos = len([]rune(m.inputBuf))

	updated, cmd := m.submitInput()
	m = updated.(ChatModel)

	if cmd != nil {
		t.Fatal("expected no command when no model is configured")
	}
	if m.busy {
		t.Fatal("expected busy=false when no model is configured")
	}
	if !strings.Contains(strings.ToLower(m.flash), "/provider") {
		t.Fatalf("flash = %q, want provider guidance", m.flash)
	}
	select {
	case msg := <-inputCh:
		t.Fatalf("unexpected queued input: %q", msg)
	default:
	}
}

func TestChatModelViewNotEmpty(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test-model", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24
	m.AddMessage(ChatMessage{Kind: MsgUser, Header: "You • 12:00:00", Content: "hello"})
	v := m.View()
	if v == "" {
		t.Fatal("View() should not be empty")
	}
}

func TestChatModelHandlesTokenEvent(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	ev := llm.Event{Kind: llm.EventToken, Text: "Hello "}
	updated, _ := m.Update(ev)
	m = updated.(ChatModel)

	ev2 := llm.Event{Kind: llm.EventToken, Text: "world"}
	updated, _ = m.Update(ev2)
	m = updated.(ChatModel)

	if len(m.messages) != 1 {
		t.Fatalf("expected 1 agent message, got %d", len(m.messages))
	}
	if m.messages[0].Content != "Hello world" {
		t.Fatalf("content = %q, want %q", m.messages[0].Content, "Hello world")
	}
}

func TestChatModelKeepsAssistantTextLiveDuringToolCalls(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	updated, _ := m.Update(llm.Event{Kind: llm.EventToken, Text: "I will trace the TUI event flow."})
	m = updated.(ChatModel)
	updated, _ = m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "read_file", Text: "internal/tui/chatmodel.go"})
	m = updated.(ChatModel)

	if len(m.messages) == 0 {
		t.Fatal("expected messages")
	}
	last := m.messages[len(m.messages)-1]
	if last.Kind != MsgAgent || !strings.Contains(last.Content, "trace the TUI event flow") {
		t.Fatalf("expected assistant text to remain live, got %#v", m.messages)
	}
	for _, msg := range m.messages {
		if msg.Kind == MsgWorking && strings.Contains(msg.Content, "Reading chatmodel.go") {
			t.Fatalf("tool call should not replace live assistant text with working status: %#v", m.messages)
		}
	}
}

func TestChatModelDoesNotPersistExplorationToolCallsAsStatusBullets(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	for _, ev := range []llm.Event{
		{Kind: llm.EventToolCall, Agent: "list_dir", Text: "."},
		{Kind: llm.EventToolCall, Agent: "read_file", Text: "AGENTS.md"},
		{Kind: llm.EventToolCall, Agent: "glob", Text: "."},
		{Kind: llm.EventToolCall, Agent: "code_search", Text: "internal"},
	} {
		updated, _ := m.Update(ev)
		m = updated.(ChatModel)
	}

	for _, msg := range m.messages {
		if msg.Kind == MsgStatus && strings.HasPrefix(strings.TrimSpace(msg.Content), "• ") {
			t.Fatalf("exploration tool calls should stay out of main transcript bullets, got %#v", m.messages)
		}
	}
	if len(m.messages) == 0 || m.messages[len(m.messages)-1].Kind != MsgWorking {
		t.Fatalf("expected current live activity to remain available, got %#v", m.messages)
	}
}

func TestChatModelRetryClearsPendingAssistantDraft(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	updated, _ := m.Update(llm.Event{Kind: llm.EventToken, Text: "First draft"})
	m = updated.(ChatModel)
	if len(m.messages) != 1 || m.messages[0].Kind != MsgAgent {
		t.Fatalf("expected agent draft, got %#v", m.messages)
	}

	updated, _ = m.Update(llm.Event{Kind: llm.EventRetry, Text: "Revising answer..."})
	m = updated.(ChatModel)

	if len(m.messages) != 1 || m.messages[0].Kind != MsgWorking {
		t.Fatalf("expected working retry message, got %#v", m.messages)
	}
	if strings.Contains(m.messages[0].Content, "First draft") {
		t.Fatalf("expected pending draft to be cleared, got %#v", m.messages)
	}
	if len(m.records) != 0 {
		t.Fatalf("expected retry message to remain live-only until finalized, got %#v", m.records)
	}
}

func TestChatModelSuppressesRawToolMarkupToken(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	updated, _ := m.Update(llm.Event{
		Kind: llm.EventToken,
		Text: "```xml\n<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\"README.md\"}}\n</tool_call>\n```",
	})
	m = updated.(ChatModel)

	if len(m.messages) != 0 {
		t.Fatalf("expected raw tool markup token to be suppressed, got %#v", m.messages)
	}
}

func TestChatModelSuppressesSelfClosingRawToolMarkupToken(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	updated, _ := m.Update(llm.Event{
		Kind: llm.EventToken,
		Text: `<tool_call name="shell.exec" arguments='{"cmd":"ls"}' />`,
	})
	m = updated.(ChatModel)

	if len(m.messages) != 0 {
		t.Fatalf("expected self-closing raw tool markup token to be suppressed, got %#v", m.messages)
	}
}

func TestChatModelSuppressesMalformedRawToolMarkupToken(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	updated, _ := m.Update(llm.Event{
		Kind: llm.EventToken,
		Text: `<tool_call>{"cmd":"ls"}</tool_call>`,
	})
	m = updated.(ChatModel)

	if len(m.messages) != 0 {
		t.Fatalf("expected malformed raw tool markup token to be suppressed, got %#v", m.messages)
	}
}

func TestChatModelHandlesDoneEvent(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24
	m.busy = true

	ev := llm.Event{Kind: llm.EventDone}
	updated, _ := m.Update(ev)
	m = updated.(ChatModel)

	if m.busy {
		t.Fatal("expected busy=false after done event")
	}
	for _, msg := range m.messages {
		if msg.Kind == MsgStatus && strings.Contains(msg.Content, "Agent complete") {
			t.Fatalf("unexpected completion banner: %#v", msg)
		}
	}
}

func TestChatModelHandlesAbortClearsLiveProgress(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24
	m.busy = true

	updated, _ := m.Update(llm.Event{Kind: llm.EventProgress, Agent: "scout", Text: "reading README.md"})
	m = updated.(ChatModel)
	if len(m.messages) != 1 || m.messages[0].Kind != MsgWorking {
		t.Fatalf("expected progress status row before abort, got %#v", m.messages)
	}
	if !strings.Contains(m.messages[0].Content, "reading README.md") {
		t.Fatalf("unexpected progress status row before abort: %#v", m.messages[0])
	}

	updated, _ = m.Update(llm.Event{Kind: llm.EventAbort})
	m = updated.(ChatModel)

	if m.busy {
		t.Fatal("expected busy=false after abort")
	}
	if len(m.messages) != 0 {
		t.Fatalf("expected abort to clear transient progress row, got %#v", m.messages)
	}
	if !m.liveProgress.IsZero() {
		t.Fatalf("expected abort to clear live progress, got %#v", m.liveProgress)
	}
	if len(m.records) != 1 || m.records[0].Kind != RecordSystem {
		t.Fatalf("expected abort to keep prior persisted progress entry, got %#v", m.records)
	}
}

func TestChatModelHandlesErrorEventFromText(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	ev := llm.Event{Kind: llm.EventError, Text: "max turns (3) exceeded"}
	updated, _ := m.Update(ev)
	m = updated.(ChatModel)

	if !strings.Contains(m.renderedToolsBuf(), "max turns (3) exceeded") {
		t.Fatalf("tools = %q", m.renderedToolsBuf())
	}
	if len(m.messages) == 0 || !strings.Contains(m.messages[len(m.messages)-1].Content, "max turns (3) exceeded") {
		t.Fatalf("messages = %#v", m.messages)
	}
}

func TestChatModelHandlesToolCallEvent(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp", DebugEnabled: true})
	m.width = 80
	m.height = 24

	ev := llm.Event{Kind: llm.EventToolCall, Agent: "read_file", Text: "main.go"}
	updated, _ := m.Update(ev)
	m = updated.(ChatModel)

	if m.renderedToolsBuf() == "" {
		t.Fatal("expected tools buffer to have content")
	}
	if len(m.messages) == 0 || m.messages[len(m.messages)-1].Kind != MsgWorking {
		t.Fatalf("expected inline working message for tool call: %#v", m.messages)
	}
	if !strings.Contains(m.messages[len(m.messages)-1].Content, "Reading main.go") {
		t.Fatalf("unexpected inline working message for tool call: %#v", m.messages[len(m.messages)-1])
	}
}

func TestChatModelShowsPreviewStatusInTranscript(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test-model", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	updated, _ := m.Update(llm.Event{
		Kind:  llm.EventToolResult,
		Agent: "preview_server_ensure",
		Text:  `{"status":"live","url":"http://127.0.0.1:8765/index.html","reused":false}`,
	})
	m = updated.(ChatModel)

	if len(m.messages) == 0 {
		t.Fatal("expected preview status message")
	}
	if got := m.messages[len(m.messages)-1].Content; got != "preview live at http://127.0.0.1:8765/index.html" {
		t.Fatalf("preview status = %q", got)
	}
}

func TestChatModelShowsCommandSessionStatusInTranscript(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test-model", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	updated, _ := m.Update(llm.Event{
		Kind:  llm.EventToolResult,
		Agent: "run_command",
		Text:  `{"status":"running","session_id":7,"command":"sleep 1"}`,
	})
	m = updated.(ChatModel)

	if len(m.messages) == 0 {
		t.Fatal("expected command session status message")
	}
	if got := m.messages[len(m.messages)-1].Content; got != "command session 7 running: sleep 1" {
		t.Fatalf("command status = %q", got)
	}
}

func TestChatModelShowsCommandSessionExitOutputInTranscript(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test-model", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	updated, _ := m.Update(llm.Event{
		Kind:  llm.EventToolResult,
		Agent: "command_status",
		Text:  `{"status":"exited","session_id":7,"command":"npm run dev","exit_code":0,"output":"ready on http://127.0.0.1:3000\nwatching for changes"}`,
	})
	m = updated.(ChatModel)

	if len(m.messages) == 0 {
		t.Fatal("expected command exit status message")
	}
	got := m.messages[len(m.messages)-1].Content
	if !strings.Contains(got, "command session 7 exited with code 0: npm run dev") {
		t.Fatalf("exit status = %q", got)
	}
	if !strings.Contains(got, "ready on http://127.0.0.1:3000") {
		t.Fatalf("exit output summary missing from %q", got)
	}
}

func TestChatModelShowsExecSessionStatusInTranscript(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test-model", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	updated, _ := m.Update(llm.Event{
		Kind:  llm.EventToolResult,
		Agent: "exec_session_start",
		Text:  `{"status":"running","session_id":9,"command":"npm run dev","pty":true,"cols":120,"rows":40}`,
	})
	m = updated.(ChatModel)

	if len(m.messages) == 0 {
		t.Fatal("expected exec session status message")
	}
	if got := m.messages[len(m.messages)-1].Content; got != "terminal session 9 running: npm run dev" {
		t.Fatalf("exec session status = %q", got)
	}
}

func TestChatModelShowsExecSessionOutputPreviewInTranscript(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test-model", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	updated, _ := m.Update(llm.Event{
		Kind:  llm.EventToolResult,
		Agent: "exec_session_status",
		Text:  `{"status":"running","session_id":9,"command":"npm run dev","pty":true,"cols":120,"rows":40,"output":"ready on http://127.0.0.1:4173\nwatching for changes"}`,
	})
	m = updated.(ChatModel)

	got := m.messages[len(m.messages)-1].Content
	if !strings.Contains(got, "terminal session 9 running: npm run dev") {
		t.Fatalf("exec session status = %q", got)
	}
	if !strings.Contains(got, "ready on http://127.0.0.1:4173") {
		t.Fatalf("expected running output preview in %q", got)
	}
}

func TestChatModelShowsExecSessionResizeInTranscript(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test-model", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	updated, _ := m.Update(llm.Event{
		Kind:  llm.EventToolResult,
		Agent: "exec_session_resize",
		Text:  `{"status":"running","session_id":9,"command":"npm run dev","pty":true,"cols":132,"rows":44}`,
	})
	m = updated.(ChatModel)

	if got := m.messages[len(m.messages)-1].Content; got != "terminal session 9 resized to 132x44: npm run dev" {
		t.Fatalf("resize status = %q", got)
	}
}

func TestChatModelIgnoresSplitMouseTrackingSequence(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test-model", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24
	m.inputBuf = "draft"
	m.inputPos = len([]rune(m.inputBuf))

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Alt: true, Runes: []rune{'['}})
	m = updated.(ChatModel)
	if got := m.inputBuf; got != "draft" {
		t.Fatalf("after CSI start, inputBuf = %q", got)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("<64;50;33M")})
	m = updated.(ChatModel)
	if got := m.inputBuf; got != "draft" {
		t.Fatalf("after mouse fragment, inputBuf = %q", got)
	}
}

func TestChatModelShowsPersistentPlanAfterUpdatePlanResult(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 90
	m.height = 28

	updated, _ := m.Update(llm.Event{Kind: llm.EventToolResult, Agent: "update_plan", Text: "Explanation: Runtime alignment\nPlan:\n- [completed] Inspect loop\n- [in_progress] Tighten prompt", IsError: false})
	m = updated.(ChatModel)

	var foundPlan bool
	for _, msg := range m.messages {
		if msg.Kind == MsgPlan {
			foundPlan = true
			if !strings.Contains(msg.Content, "[in_progress] Tighten prompt") {
				t.Fatalf("plan message = %#v", msg)
			}
		}
	}
	if !foundPlan {
		t.Fatalf("expected persistent plan message, got %#v", m.messages)
	}
	if got := strippedLine(m.View()); !strings.Contains(got, "[in_progress] Tighten prompt") {
		t.Fatalf("expected plan visible in main pane, got:\n%s", got)
	}
}

func TestChatModelToolCallEventWithoutAgentSkipsWorkingActivity(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp", DebugEnabled: true})
	m.width = 80
	m.height = 24

	updated, _ := m.Update(llm.Event{Kind: llm.EventToolCall, Text: "read_file"})
	m = updated.(ChatModel)

	for _, msg := range m.messages {
		if msg.Kind == MsgWorking {
			t.Fatalf("unexpected inline working message without tool agent: %#v", msg)
		}
	}
}

func TestChatModelToolCallUpdatesCurrentMilestone(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	updated, _ := m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "read_file", Text: "README.md"})
	m = updated.(ChatModel)
	updated, _ = m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "read_file", Text: "AGENTS.md"})
	m = updated.(ChatModel)

	var sawWorking bool
	for _, msg := range m.messages {
		if msg.Kind == MsgWorking && strings.Contains(msg.Content, "Reading AGENTS.md") {
			sawWorking = true
		}
		if msg.Kind == MsgStatus && strings.HasPrefix(strings.TrimSpace(msg.Content), "• ") {
			t.Fatalf("exploration tool call should not persist status checkpoint, got %#v", m.messages)
		}
	}
	if !sawWorking {
		t.Fatalf("expected AGENTS working milestone, got %#v", m.messages)
	}
}

func TestChatModelExplorationToolCallStaysLiveOnly(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	updated, _ := m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "read_file", Text: "README.md"})
	m = updated.(ChatModel)

	for _, msg := range m.messages {
		if msg.Kind == MsgStatus && strings.HasPrefix(strings.TrimSpace(msg.Content), "• ") {
			t.Fatalf("exploration tool call should not persist status checkpoint, got %#v", m.messages)
		}
	}
	if len(m.messages) != 1 || m.messages[0].Kind != MsgWorking || !strings.Contains(m.messages[0].Content, "Reading README.md") {
		t.Fatalf("expected live working message only, got %#v", m.messages)
	}
}

func TestChatModelExplorationToolCallUsesSemanticLiveNarrative(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	updated, _ := m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "code_search", Text: "internal/tui"})
	m = updated.(ChatModel)

	if len(m.messages) != 1 || m.messages[0].Kind != MsgWorking {
		t.Fatalf("expected live working message only, got %#v", m.messages)
	}
	if !strings.Contains(m.messages[0].Content, "Searching code in internal/tui") {
		t.Fatalf("expected semantic live narrative, got %#v", m.messages)
	}
	if strings.Contains(m.messages[0].Content, "code_search") || strings.Contains(m.messages[0].Content, "└ internal/tui") {
		t.Fatalf("live narrative should not render raw tool tree: %#v", m.messages)
	}
}

func TestChatModelExplorationToolCallDedupesRepeatedLiveSteps(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	updated, _ := m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "read_file", Text: "README.md"})
	m = updated.(ChatModel)
	updated, _ = m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "read_file", Text: "README.md"})
	m = updated.(ChatModel)

	var count int
	for _, msg := range m.messages {
		if strings.Contains(msg.Content, "Reading README.md") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected deduped checkpoint count = 1, got %d in %#v", count, m.messages)
	}
}

func TestChatModelToolResultEmitsRanCheckpointWithSnippet(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24

	updated, _ := m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "run_command", Text: "git status --short"})
	m = updated.(ChatModel)
	updated, _ = m.Update(llm.Event{
		Kind:    llm.EventToolResult,
		Agent:   "run_command",
		Text:    " M internal/tui/chatmodel.go\n?? scratch.txt\nexit 0",
		IsError: false,
	})
	m = updated.(ChatModel)

	var sawCheckpoint bool
	for _, msg := range m.messages {
		if msg.Kind != MsgStatus {
			continue
		}
		if strings.Contains(msg.Content, "• Ran git status --short") &&
			strings.Contains(msg.Content, "└ M internal/tui/chatmodel.go") &&
			strings.Contains(msg.Content, "scratch.txt") {
			sawCheckpoint = true
			break
		}
	}
	if !sawCheckpoint {
		t.Fatalf("expected ran checkpoint with output snippet, got %#v", m.messages)
	}
}

func TestChatModelShowsInlineWorkingMessageForRuntimeInfo(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	updated, _ := m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "runtime", Text: "Inspecting repository structure"})
	m = updated.(ChatModel)

	if len(m.messages) == 0 {
		t.Fatal("expected working message")
	}
	last := m.messages[len(m.messages)-1]
	if last.Kind != MsgWorking || !strings.Contains(last.Content, "Inspecting repository structure") {
		t.Fatalf("unexpected last message: %#v", last)
	}
}

func TestChatModelProgressAccumulatesActiveSubAgentMilestones(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	for _, text := range []string{
		"reading README.md",
		"looking for \"**/*.go\"",
		"reading main.go",
		"reading app.go",
	} {
		updated, _ := m.Update(llm.Event{Kind: llm.EventProgress, Agent: "scout", Text: text})
		m = updated.(ChatModel)
	}

	if len(m.messages) != 1 {
		t.Fatalf("expected in-place progress row, got %#v", m.messages)
	}
	if m.messages[0].Kind != MsgWorking {
		t.Fatalf("message kind = %#v, want MsgWorking", m.messages[0])
	}
	if !strings.Contains(m.messages[0].Content, "reading app.go") {
		t.Fatalf("message content = %q, want latest milestone", m.messages[0].Content)
	}
}

func TestChatModelProgressHandoffAppendsPreviousWorkingLine(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	updated, _ := m.Update(llm.Event{Kind: llm.EventProgress, Agent: "scout", Text: "reading README.md"})
	m = updated.(ChatModel)
	updated, _ = m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "runtime", Text: "delegating to builder"})
	m = updated.(ChatModel)
	updated, _ = m.Update(llm.Event{Kind: llm.EventProgress, Agent: "builder", Text: "editing main.go"})
	m = updated.(ChatModel)

	if len(m.messages) != 1 {
		t.Fatalf("messages = %#v", m.messages)
	}
	if m.messages[0].Kind != MsgWorking || !strings.Contains(m.messages[0].Content, "editing main.go") {
		t.Fatalf("unexpected handoff working status: %#v", m.messages[0])
	}
}

func TestChatModelProgressIgnoresGenericHeartbeatWhenSpecificStepExists(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	updated, _ := m.Update(llm.Event{Kind: llm.EventProgress, Agent: "scout", Text: "Reading README to understand the repository intent"})
	m = updated.(ChatModel)
	updated, _ = m.Update(llm.Event{Kind: llm.EventProgress, Agent: "forge", Text: "Connecting findings from the repository scan results"})
	m = updated.(ChatModel)

	if len(m.messages) != 1 || m.messages[0].Kind != MsgWorking {
		t.Fatalf("messages = %#v", m.messages)
	}
	if !strings.Contains(strings.ToLower(m.messages[0].Content), "reading readme") {
		t.Fatalf("expected specific step to remain visible, got %#v", m.messages[0])
	}
	if strings.Contains(strings.ToLower(m.messages[0].Content), "connecting findings") {
		t.Fatalf("expected generic heartbeat to be suppressed, got %#v", m.messages[0])
	}
}

func TestChatModelProgressDoesNotMergeEquivalentMilestones(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	updated, _ := m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "glob", Text: "**/*.py"})
	m = updated.(ChatModel)
	updated, _ = m.Update(llm.Event{Kind: llm.EventProgress, Agent: "forge", Text: `Finding files that match "**/*.py"`})
	m = updated.(ChatModel)

	var working ChatMessage
	var foundWorking bool
	for _, msg := range m.messages {
		if msg.Kind == MsgWorking {
			working = msg
			foundWorking = true
			break
		}
	}
	if !foundWorking {
		t.Fatalf("expected active working milestone, got %#v", m.messages)
	}
	if strings.Contains(strings.ToLower(working.Content), ". now ") {
		t.Fatalf("expected equivalent milestones to avoid narrative chaining, got %#v", working)
	}
}

func TestTranscriptRecordChatModelTracksDurableKindsAndAssistantSegments(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	m.AddMessage(ChatMessage{Kind: MsgUser, Header: "You • 12:00:00", Content: "hello"})
	m.AppendToLastAgentLabeled("Before\n```go\nfmt.Println(\"hi\")\n```\nAfter", "Agent")
	m.AddMessage(ChatMessage{Kind: MsgStatus, Content: "Error: boom"})
	m.AddMessage(ChatMessage{Kind: MsgStatus, Content: "status: ready"})

	if len(m.records) != 4 {
		t.Fatalf("records = %#v", m.records)
	}
	if m.records[0].Kind != RecordUser {
		t.Fatalf("user record = %#v", m.records[0])
	}
	if m.records[1].Kind != RecordAssistant {
		t.Fatalf("assistant record = %#v", m.records[1])
	}
	if got := m.records[1].Segments; len(got) != 3 || got[0].Kind != SegmentText || got[1].Kind != SegmentCode || got[2].Kind != SegmentText {
		t.Fatalf("assistant segments = %#v", got)
	}
	if m.records[2].Kind != RecordError {
		t.Fatalf("error record = %#v", m.records[2])
	}
	if m.records[3].Kind != RecordSystem {
		t.Fatalf("system record = %#v", m.records[3])
	}
}

func TestLiveProgressChatModelKeepsProgressVisibleAfterDone(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	updated, _ := m.Update(llm.Event{Kind: llm.EventProgress, Agent: "scout", Text: "reading README.md"})
	m = updated.(ChatModel)
	updated, _ = m.Update(llm.Event{Kind: llm.EventProgress, Agent: "scout", Text: "reading app.go"})
	m = updated.(ChatModel)

	if len(m.messages) != 1 || m.messages[0].Kind != MsgWorking {
		t.Fatalf("messages = %#v", m.messages)
	}
	if !strings.Contains(m.messages[0].Content, "reading app.go") {
		t.Fatalf("unexpected progress messages before done = %#v", m.messages)
	}
	if len(m.records) != 0 {
		t.Fatalf("records = %#v", m.records)
	}
	if got := m.liveProgress.LatestMessage(); !strings.Contains(got, "reading app.go") {
		t.Fatalf("live progress = %#v", m.liveProgress)
	}

	updated, _ = m.Update(llm.Event{Kind: llm.EventDone})
	m = updated.(ChatModel)

	if len(m.messages) != 0 {
		t.Fatalf("expected done to clear transient progress row, got %#v", m.messages)
	}
	if !m.liveProgress.IsZero() {
		t.Fatalf("live progress after done = %#v", m.liveProgress)
	}
	if len(m.records) != 1 {
		t.Fatalf("records after done = %#v", m.records)
	}
	if m.records[0].Kind != RecordSystem || len(m.records[0].Segments) != 1 || !strings.Contains(m.records[0].Segments[0].Text, "reading app.go") {
		t.Fatalf("finalized live-progress record after done = %#v", m.records[0])
	}
}

func TestChatModelDoneFinalizesAssistantRecordBeforeProgressNote(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	m.AppendToLastAgentLabeled("hello", "Agent")
	updated, _ := m.Update(llm.Event{Kind: llm.EventProgress, Agent: "scout", Text: "reading app.go"})
	m = updated.(ChatModel)

	if len(m.records) != 1 || m.records[0].Kind != RecordAssistant || m.records[0].Final {
		t.Fatalf("records state before done = %#v", m.records)
	}

	updated, _ = m.Update(llm.Event{Kind: llm.EventDone})
	m = updated.(ChatModel)

	if len(m.records) != 2 {
		t.Fatalf("records after done = %#v", m.records)
	}
	if m.records[0].Kind != RecordAssistant || !m.records[0].Final {
		t.Fatalf("assistant record after done = %#v", m.records[0])
	}
	if m.records[1].Kind != RecordSystem {
		t.Fatalf("finalized progress note after done = %#v", m.records[1])
	}
}

func TestChatModelToolCallFinalizesAssistantPreambleRecord(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	m.AppendToLastAgentLabeled("I'll inspect the README first.", "forge")
	updated, _ := m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "read_file", Text: "README.md"})
	m = updated.(ChatModel)

	if len(m.records) == 0 || m.records[0].Kind != RecordAssistant {
		t.Fatalf("records = %#v", m.records)
	}
	if !m.records[0].Final {
		t.Fatalf("assistant preamble should be finalized once tool execution starts: %#v", m.records[0])
	}
}

func TestChatModelDelegatingRuntimeEventUsesWorkingLine(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	updated, _ := m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "runtime", Text: "delegating to scout"})
	m = updated.(ChatModel)

	if len(m.messages) != 1 {
		t.Fatalf("messages = %#v", m.messages)
	}
	got := m.messages[0]
	if got.Kind != MsgWorking {
		t.Fatalf("kind = %#v", got)
	}
	if !strings.Contains(got.Content, "delegating to scout") {
		t.Fatalf("content = %q", got.Content)
	}
}

func TestChatModelShowsSubAgentProgressInMainPane(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24

	updated, _ := m.Update(llm.Event{Kind: llm.EventProgress, Agent: "code researcher", Text: "Reading README for context", SubAgent: "code researcher"})
	m = updated.(ChatModel)

	if len(m.messages) != 1 {
		t.Fatalf("messages = %#v", m.messages)
	}
	got := m.messages[0]
	if got.Kind != MsgWorking {
		t.Fatalf("kind = %#v", got.Kind)
	}
	if !strings.Contains(got.Content, "Reading README for context") {
		t.Fatalf("content = %q", got.Content)
	}
}

func TestChatModelDelegateResultAddsCompactSubAgentSummaryToChat(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24

	updated, _ := m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "runtime", Text: "[scout] starting", SubAgent: "scout"})
	m = updated.(ChatModel)
	updated, _ = m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "read_file", Text: "README.md", SubAgent: "scout"})
	m = updated.(ChatModel)
	updated, _ = m.Update(llm.Event{Kind: llm.EventStats, Duration: time.Second, Usage: llm.Usage{InputTokens: 1}, SubAgent: "scout"})
	m = updated.(ChatModel)
	updated, _ = m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "runtime", Text: "[scout] done", SubAgent: "scout"})
	m = updated.(ChatModel)
	updated, _ = m.Update(llm.Event{
		Kind:    llm.EventToolResult,
		Agent:   "delegate",
		Text:    "Now let me examine the Python files to understand the codebase structure and identify potential issues.",
		IsError: false,
	})
	m = updated.(ChatModel)

	var agentMsgs []ChatMessage
	for _, msg := range m.messages {
		if msg.Kind == MsgAgent {
			agentMsgs = append(agentMsgs, msg)
		}
		if msg.Kind == MsgStatus && strings.Contains(msg.Content, "complete") {
			t.Fatalf("unexpected completion status message: %#v", msg)
		}
	}
	if len(agentMsgs) == 0 {
		t.Fatalf("expected sub-agent result in chat, got %#v", m.messages)
	}
	last := agentMsgs[len(agentMsgs)-1]
	if !strings.HasPrefix(last.Header, "Scout • ") {
		t.Fatalf("agent header = %q", last.Header)
	}
	if !strings.Contains(last.Content, "Now let me examine the Python files") {
		t.Fatalf("agent content = %q", last.Content)
	}
}

func TestChatModelDelegateResultStillShowsWhenPendingSummaryWasLost(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24

	updated, _ := m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "runtime", Text: "[scout] starting", SubAgent: "scout"})
	m = updated.(ChatModel)
	updated, _ = m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "read_file", Text: "README.md", SubAgent: "scout"})
	m = updated.(ChatModel)
	updated, _ = m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "runtime", Text: "[scout] done", SubAgent: "scout"})
	m = updated.(ChatModel)

	// Simulate the brittle state loss path: the delegate result should still surface.
	m.pendingSubAgentSummary = nil

	updated, _ = m.Update(llm.Event{
		Kind:    llm.EventToolResult,
		Agent:   "delegate",
		Text:    "Source: /repo/main.go:12.",
		Content: "{\"source_file\":\"/repo/main.go\",\"source_line\":12}",
		IsError: false,
	})
	m = updated.(ChatModel)

	var agentMsgs []ChatMessage
	for _, msg := range m.messages {
		if msg.Kind == MsgAgent {
			agentMsgs = append(agentMsgs, msg)
		}
	}
	if len(agentMsgs) == 0 {
		t.Fatalf("expected scout result in chat even without pending summary, got %#v", m.messages)
	}
	last := agentMsgs[len(agentMsgs)-1]
	if !strings.HasPrefix(last.Header, "Scout • ") {
		t.Fatalf("agent header = %q", last.Header)
	}
	if !strings.Contains(last.Content, "Source: /repo/main.go:12.") {
		t.Fatalf("agent content = %q", last.Content)
	}
}

func TestChatModelMirrorsRichSubAgentProseIntoChat(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24

	updated, _ := m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "runtime", Text: "[architect] starting", SubAgent: "architect"})
	m = updated.(ChatModel)
	updated, _ = m.Update(llm.Event{Kind: llm.EventToken, Text: "Here is the plain-language version.\n\nThis repo is a toolbox of scripts for recurring data work.", SubAgent: "architect"})
	m = updated.(ChatModel)
	updated, _ = m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "runtime", Text: "[architect] done", SubAgent: "architect"})
	m = updated.(ChatModel)
	updated, _ = m.Update(llm.Event{
		Kind:    llm.EventToolResult,
		Agent:   "delegate",
		Text:    "Provided a plain-language explanation of the repository.",
		Content: "This repository is a practical toolbox of scripts used for routine data operations.\n\n1) Create one Start Here guide.\n2) Add safety checks.",
		IsError: false,
	})
	m = updated.(ChatModel)

	var agentMsgs []ChatMessage
	for _, msg := range m.messages {
		if msg.Kind == MsgAgent {
			agentMsgs = append(agentMsgs, msg)
		}
	}
	if len(agentMsgs) != 1 {
		t.Fatalf("expected one visible architect answer, got %#v", agentMsgs)
	}
	if got := agentMsgs[0].Header; !strings.HasPrefix(got, "Architect • ") {
		t.Fatalf("header = %q", got)
	}
	if got := agentMsgs[0].Content; !strings.Contains(got, "Here is the plain-language version.") {
		t.Fatalf("content = %q", got)
	}
	if got := agentMsgs[0].Content; strings.Contains(got, "Provided a plain-language explanation of the repository.") {
		t.Fatalf("unexpected delegate summary leaked into transcript: %q", got)
	}
}

func TestChatModelMirroredSubAgentProseStaysSeparatedByRole(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24

	updated, _ := m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "runtime", Text: "[scout] starting", SubAgent: "scout"})
	m = updated.(ChatModel)
	updated, _ = m.Update(llm.Event{Kind: llm.EventToken, Text: "Scout found the source file.", SubAgent: "scout"})
	m = updated.(ChatModel)
	updated, _ = m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "runtime", Text: "[scout] done", SubAgent: "scout"})
	m = updated.(ChatModel)
	updated, _ = m.Update(llm.Event{Kind: llm.EventToolResult, Agent: "delegate", Text: "Source: /repo/main.go:12.", Content: `{"source_file":"/repo/main.go","source_line":12}`})
	m = updated.(ChatModel)

	updated, _ = m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "runtime", Text: "[architect] starting", SubAgent: "architect"})
	m = updated.(ChatModel)
	updated, _ = m.Update(llm.Event{Kind: llm.EventToken, Text: "Architect recommends a simpler explanation.", SubAgent: "architect"})
	m = updated.(ChatModel)

	var agentMsgs []ChatMessage
	for _, msg := range m.messages {
		if msg.Kind == MsgAgent {
			agentMsgs = append(agentMsgs, msg)
		}
	}
	if len(agentMsgs) != 2 {
		t.Fatalf("expected separate scout and architect messages, got %#v", agentMsgs)
	}
	if got := agentMsgs[0].Header; !strings.HasPrefix(got, "Scout • ") {
		t.Fatalf("first header = %q", got)
	}
	if got := agentMsgs[1].Header; !strings.HasPrefix(got, "Architect • ") {
		t.Fatalf("second header = %q", got)
	}
}

func TestChatModelDelegateResultUsesRichArtifactWhenNoVisibleSubAgentTranscript(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24
	m.pendingSubAgentSummary = &subAgentSummary{role: "architect", turns: 1, tools: 1}

	updated, _ := m.Update(llm.Event{
		Kind:    llm.EventToolResult,
		Agent:   "delegate",
		Text:    "Provided a plain-language explanation of the repository.",
		Content: "This repository is a practical toolbox of scripts used for routine data operations.\n\n1) Create one Start Here guide.\n2) Add safety checks.",
		IsError: false,
	})
	m = updated.(ChatModel)

	var agentMsgs []ChatMessage
	for _, msg := range m.messages {
		if msg.Kind == MsgAgent {
			agentMsgs = append(agentMsgs, msg)
		}
	}
	if len(agentMsgs) != 1 {
		t.Fatalf("expected one visible architect answer, got %#v", agentMsgs)
	}
	if got := agentMsgs[0].Header; !strings.HasPrefix(got, "Architect • ") {
		t.Fatalf("header = %q", got)
	}
	if got := agentMsgs[0].Content; !strings.Contains(got, "This repository is a practical toolbox of scripts used for routine data operations.") {
		t.Fatalf("expected artifact body in transcript, got %q", got)
	}
	if got := agentMsgs[0].Content; strings.Contains(got, "Provided a plain-language explanation of the repository.") {
		t.Fatalf("expected transcript to prefer artifact over summary, got %q", got)
	}
}

func TestChatModelStructuredSubAgentEnvelopeDoesNotLeakIntoChat(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24

	updated, _ := m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "runtime", Text: "[architect] starting", SubAgent: "architect"})
	m = updated.(ChatModel)
	updated, _ = m.Update(llm.Event{
		Kind:     llm.EventToken,
		Text:     `{"status":"complete","message":"Provided a plain-language explanation.","artifact_kind":"summary","artifact":"full body","next_role":"","next_task":""}`,
		SubAgent: "architect",
	})
	m = updated.(ChatModel)

	for _, msg := range m.messages {
		if msg.Kind == MsgAgent {
			t.Fatalf("unexpected structured envelope in transcript: %#v", msg)
		}
	}
}

func TestChatModelSlashClear(t *testing.T) {
	clearCalls := 0
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp", ClearHistory: func() { clearCalls++ }})
	m.width = 80
	m.height = 24
	m.AddMessage(ChatMessage{Kind: MsgUser, Header: "You", Content: "hello"})
	m.AddMessage(ChatMessage{Kind: MsgAgent, Content: "hi"})

	m.inputBuf = "/clear"
	m.inputPos = 6
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	if len(m.messages) != 0 {
		t.Fatalf("expected 0 messages after /clear, got %d", len(m.messages))
	}
	if clearCalls != 1 {
		t.Fatalf("ClearHistory calls = %d, want 1", clearCalls)
	}
}

func TestChatModelSlashNewStartsCleanSession(t *testing.T) {
	clearCalls := 0
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp", ClearHistory: func() { clearCalls++ }})
	m.width = 80
	m.height = 24
	m.AddMessage(ChatMessage{Kind: MsgUser, Header: "You", Content: "hello"})
	setToolsContent(&m, "tool output")

	m.inputBuf = "/new"
	m.inputPos = len(m.inputBuf)
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	if len(m.messages) != 0 {
		t.Fatalf("expected 0 messages after /new, got %d", len(m.messages))
	}
	if m.renderedToolsBuf() != "" {
		t.Fatalf("tools = %q, want empty after /new", m.renderedToolsBuf())
	}
	if clearCalls != 1 {
		t.Fatalf("ClearHistory calls = %d, want 1", clearCalls)
	}
	if m.flash != "new session started" {
		t.Fatalf("flash = %q", m.flash)
	}
}

func TestChatModelHelpMentionsNewSessionCommand(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.helpTab = 1

	if !strings.Contains(strings.Join(m.helpLines(), "\n"), "/new") {
		t.Fatalf("help lines missing /new: %#v", m.helpLines())
	}
}

func TestChatModelSlashExit(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.inputBuf = "/exit"
	m.inputPos = 5
	_, cmd := m.submitInput()
	if cmd == nil {
		t.Fatal("expected quit command from /exit")
	}
}

func TestChatModelSlashThemeSelectsLight(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	m.inputBuf = "/theme light"
	m.inputPos = len(m.inputBuf)
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	if m.themeID != "light" {
		t.Fatalf("themeID = %q, want %q", m.themeID, "light")
	}
}

func TestChatModelSlashThemeVariants(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	m.inputBuf = "/theme low"
	m.inputPos = len(m.inputBuf)
	updated, _ := m.submitInput()
	m = updated.(ChatModel)
	if m.themeID != "low" {
		t.Fatalf("themeID = %q, want %q", m.themeID, "low")
	}

	m.inputBuf = "/theme default"
	m.inputPos = len(m.inputBuf)
	updated, _ = m.submitInput()
	m = updated.(ChatModel)
	if m.themeID != "default" {
		t.Fatalf("themeID = %q, want %q", m.themeID, "default")
	}
}

func TestChatModelSlashThemeChangesRenderedViewportStyle(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
	})

	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	m.AddMessage(ChatMessage{Kind: MsgUser, Header: "You • 12:00:00", Content: "hello world"})
	defaultRendered := m.chatViewport.View()
	if defaultRendered == "" {
		t.Fatal("expected rendered viewport content")
	}

	m.inputBuf = "/theme light"
	m.inputPos = len(m.inputBuf)
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	lightRendered := m.chatViewport.View()
	if lightRendered == "" {
		t.Fatal("expected rendered viewport content after theme change")
	}
	if defaultRendered == lightRendered {
		t.Fatal("expected viewport rendering to change across themes")
	}
	if !strings.Contains(defaultRendered, "hello world") || !strings.Contains(lightRendered, "hello world") {
		t.Fatal("expected message content to remain visible")
	}
}

func TestChatModelViewHeaderUsesCardLayout(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test-model", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24
	m.themeID = "dusk"
	m.AddMessage(ChatMessage{Kind: MsgUser, Header: "You • 12:00:00", Content: "hello"})

	v := m.View()
	lines := strings.Split(v, "\n")
	if len(lines) < 5 {
		t.Fatalf("view missing header: %q", v)
	}
	header := strings.Join(lines[:5], "\n")
	if !strings.Contains(header, "FORGE") || !strings.Contains(header, "test-model") || !strings.Contains(header, "/tmp") {
		t.Fatalf("view header missing card content:\n%s", header)
	}
	if strings.Contains(header, "theme:") {
		t.Fatalf("view header should omit theme chrome:\n%s", header)
	}
}

func TestChatModelViewUsesRenderedHeaderHeightForLayout(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "copilot/gpt-5", WorkDir: "/tmp"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(ChatModel)

	wantHeaderHeight := strings.Count(renderStatusHeaderForHeight(m.theme(), m.statusSnapshot(), m.width, m.height), "\n") + 1
	wantChatY := wantHeaderHeight + chatHeaderGapHeight
	if got := m.mouseContext().chatY; got != wantChatY {
		t.Fatalf("chatY = %d, want rendered header height plus gap %d", got, wantChatY)
	}
}

func TestChatModelViewFitsWithinWindowHeight(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "copilot/gpt-5", WorkDir: "/tmp"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = updated.(ChatModel)

	lines := strings.Split(m.View(), "\n")
	if len(lines) > 24 {
		t.Fatalf("view has %d lines, want <= 24", len(lines))
	}
}

func TestChatModelViewEmptyStateRemovesOldHelperCopy(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "copilot/gpt-5", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	view := strippedLine(m.View())
	if !strings.Contains(view, "Welcome to Forge.") {
		t.Fatalf("expected ready copy in empty state:\n%s", view)
	}
	for _, unwanted := range []string{
		"Ask for a code change, bugfix, or investigation.",
		"Use /help for commands, /find to search.",
	} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("empty view contains unwanted copy %q", unwanted)
		}
	}
}

func TestChatModelViewKeepsTurnStatsOutOfCompactHeader(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "copilot/gpt-5", WorkDir: "/tmp"})
	m.width = 120
	m.height = 30
	m.statsUsage = llm.Usage{InputTokens: 100, OutputTokens: 20}
	m.sessionUsage = llm.Usage{InputTokens: 100, OutputTokens: 20}

	got := m.View()
	lines := strings.Split(got, "\n")
	if len(lines) < 1 {
		t.Fatalf("view missing header: %q", got)
	}
	if strings.Contains(lines[0], "last 100 in / 20 out") || strings.Contains(lines[0], "session 120 tok") {
		t.Fatalf("compact header should omit turn/session stats: %q", lines[0])
	}
}

func TestChatModelViewKeepsContextSummaryOutOfCompactHeader(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{
		Model:   "openai/gpt-5",
		WorkDir: "/tmp",
		ModelInfo: func(model string) *modelcatalog.ModelInfo {
			return &modelcatalog.ModelInfo{ContextWindow: 8000}
		},
	})
	m.width = 120
	m.height = 30
	m.sessionUsage = llm.Usage{InputTokens: 100, OutputTokens: 20}

	got := m.View()
	lines := strings.Split(got, "\n")
	if len(lines) < 1 {
		t.Fatalf("view missing header: %q", got)
	}
	if strings.Contains(lines[0], "session 120 tok") || strings.Contains(lines[0], "est ctx 120/8000") {
		t.Fatalf("compact header should omit context summary: %q", lines[0])
	}
}

func TestChatModelViewKeepsBaselineSessionStatsOutOfCompactHeader(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{
		Model:   "openai/gpt-5",
		WorkDir: "/tmp",
		ModelInfo: func(model string) *modelcatalog.ModelInfo {
			return &modelcatalog.ModelInfo{ContextWindow: 8000}
		},
	})
	m.width = 120
	m.height = 30

	got := m.View()
	lines := strings.Split(got, "\n")
	if len(lines) < 1 {
		t.Fatalf("view missing header: %q", got)
	}
	if strings.Contains(lines[0], "session 0 tok") || strings.Contains(lines[0], "est ctx 0/8000") {
		t.Fatalf("compact header should omit baseline session stats: %q", lines[0])
	}
}

func TestChatModelEventStatsFetchesProviderDiagnosticsWithoutExpandingHeader(t *testing.T) {
	codexCalls := 0
	m := NewChatModel(ChatLiveConfig{
		Model:   "openai/gpt-5",
		WorkDir: "/tmp",
		FetchCodexUsage: func(ctx context.Context) (*codexusage.Snapshot, error) {
			codexCalls++
			return &codexusage.Snapshot{
				Plan: "pro",
				Primary: &codexusage.Window{
					UsedPercent: 20,
					ResetIn:     "5h",
				},
			}, nil
		},
	})
	m.width = 120
	m.height = 30

	updated, cmd := m.Update(llm.Event{
		Kind:     llm.EventStats,
		Duration: time.Second,
		Usage:    llm.Usage{InputTokens: 100, OutputTokens: 20},
	})
	m = updated.(ChatModel)
	if cmd == nil {
		t.Fatal("expected stats event to trigger provider diagnostics fetch")
	}
	if msg := cmd(); msg != nil {
		updated, _ = m.Update(msg)
		m = updated.(ChatModel)
	}

	if codexCalls != 1 {
		t.Fatalf("codexCalls = %d, want 1", codexCalls)
	}
	if m.statusData.CodexUsage == nil {
		t.Fatal("expected codex usage snapshot to be stored")
	}
	lines := strings.Split(m.View(), "\n")
	if len(lines) < 1 {
		t.Fatalf("view missing header: %q", strings.Join(lines, "\n"))
	}
	if strings.Contains(lines[0], "Codex") {
		t.Fatalf("compact header should omit provider diagnostics: %q", lines[0])
	}
}

func TestChatModelViewKeepsProviderDiagnosticsOutOfCompactHeader(t *testing.T) {
	quota := &copilot.UserQuota{
		Windows: map[string]llm.CopilotQuota{
			"premium": {Type: "premium", Remaining: 3, Included: 10},
		},
	}

	copilotModel := NewChatModel(ChatLiveConfig{Model: "copilot/gpt-5", WorkDir: "/tmp"})
	copilotModel.width = 120
	copilotModel.height = 30
	copilotModel.statusData.CopilotLive = quota
	copilotLines := strings.Split(copilotModel.View(), "\n")
	if len(copilotLines) < 1 {
		t.Fatalf("copilot view missing header: %q", copilotModel.View())
	}
	if strings.Contains(copilotLines[0], "Copilot") {
		t.Fatalf("compact header should omit Copilot diagnostics: %q", copilotLines[0])
	}

	otherModel := NewChatModel(ChatLiveConfig{Model: "anthropic/claude-sonnet-4-6", WorkDir: "/tmp"})
	otherModel.width = 120
	otherModel.height = 30
	otherModel.statusData.CopilotLive = quota
	otherLines := strings.Split(otherModel.View(), "\n")
	if len(otherLines) < 1 {
		t.Fatalf("other view missing header: %q", otherModel.View())
	}
	if strings.Contains(otherLines[0], "Copilot") {
		t.Fatalf("compact header should omit Copilot diagnostics for all models: %q", otherLines[0])
	}
}

func TestChatModelHelpOverlayChangesAcrossThemes(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
	})

	m := NewChatModel(ChatLiveConfig{Model: "test-model", WorkDir: "/tmp"})
	m.width = 100
	m.height = 30
	m.helpVisible = true

	defaultOverlay := m.View()
	m.themeID = "light"
	lightOverlay := m.View()
	if defaultOverlay == lightOverlay {
		t.Fatal("expected help overlay rendering to change across themes")
	}
	if !strings.Contains(defaultOverlay, "Help") || !strings.Contains(lightOverlay, "Help") {
		t.Fatal("expected help overlay content to remain visible")
	}
}

func TestChatModelSlashHelpOpensOverlay(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 30

	m.inputBuf = "/help"
	m.inputPos = len("/help")
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	if !m.helpVisible {
		t.Fatal("expected help overlay to be visible after /help")
	}
	if len(m.messages) != 0 {
		t.Fatalf("expected /help not to append chat messages, got %d", len(m.messages))
	}
	v := m.View()
	if !strings.Contains(v, "Chat Commands") || !strings.Contains(v, "CLI Skills") {
		t.Fatalf("help overlay missing tabs: %s", v)
	}
}

func TestChatModelSlashStatsShowsSectionedOverlay(t *testing.T) {
	copilotCalls := 0
	m := NewChatModel(ChatLiveConfig{
		Model:   "copilot/gpt-5",
		WorkDir: "/tmp",
		FetchLiveCopilotQuota: func(ctx context.Context) (*copilot.UserQuota, error) {
			copilotCalls++
			return &copilot.UserQuota{
				Windows: map[string]llm.CopilotQuota{
					"premium": {Type: "premium_interactions", Remaining: 143},
				},
			}, nil
		},
		RequestMode: func() string { return "responses" },
		ModelInfo: func(model string) *modelcatalog.ModelInfo {
			return &modelcatalog.ModelInfo{Reasoning: true, ToolCall: true}
		},
	})
	m.width = 100
	m.height = 24

	updated, cmd := m.Update(llm.Event{Kind: llm.EventStats, Duration: time.Second, Usage: llm.Usage{InputTokens: 120, OutputTokens: 30}})
	m = updated.(ChatModel)
	if cmd != nil {
		if msg := cmd(); msg != nil {
			updated, _ = m.Update(msg)
			m = updated.(ChatModel)
		}
	}

	m.inputBuf = "/stats"
	m.inputPos = len("/stats")
	updated, cmd = m.submitInput()
	m = updated.(ChatModel)
	if cmd != nil {
		if msg := cmd(); msg != nil {
			updated, _ = m.Update(msg)
			m = updated.(ChatModel)
		}
	}

	if got := m.flash; got != "stats opened" {
		t.Fatalf("flash = %q, want %q", got, "stats opened")
	}
	if !m.statsVisible {
		t.Fatal("expected stats overlay after /stats")
	}
	if copilotCalls != 1 {
		t.Fatalf("copilotCalls = %d, want 1", copilotCalls)
	}
	got := m.View()
	for _, want := range []string{"Turn", "Session", "Provider", "Model", "Diagnostics", "143", "responses", "reasoning"} {
		if !strings.Contains(got, want) {
			t.Fatalf("view missing %q: %s", want, got)
		}
	}
}

func TestChatModelSlashStatsFetchesCodexUsageLazily(t *testing.T) {
	codexCalls := 0
	m := NewChatModel(ChatLiveConfig{
		Model:   "openai/gpt-5",
		WorkDir: "/tmp",
		FetchCodexUsage: func(ctx context.Context) (*codexusage.Snapshot, error) {
			codexCalls++
			return &codexusage.Snapshot{
				Plan: "pro",
				Primary: &codexusage.Window{
					UsedPercent: 20,
					ResetIn:     "5h",
				},
			}, nil
		},
		RequestMode: func() string { return "responses" },
	})
	m.width = 100
	m.height = 24

	m.inputBuf = "/stats"
	m.inputPos = len("/stats")
	updated, cmd := m.submitInput()
	m = updated.(ChatModel)
	if cmd == nil {
		t.Fatal("expected /stats to return fetch command")
	}
	if msg := cmd(); msg != nil {
		updated, _ = m.Update(msg)
		m = updated.(ChatModel)
	}

	if codexCalls != 1 {
		t.Fatalf("codexCalls = %d, want 1", codexCalls)
	}
	got := m.View()
	if !strings.Contains(got, "OpenAI/Codex") || !strings.Contains(got, "pro") || !strings.Contains(got, "5h") {
		t.Fatalf("view missing codex usage overlay content: %s", got)
	}
}

func TestChatModelSlashStatsReusesCachedProviderDiagnostics(t *testing.T) {
	copilotCalls := 0
	m := NewChatModel(ChatLiveConfig{
		Model:   "copilot/gpt-5",
		WorkDir: "/tmp",
		FetchLiveCopilotQuota: func(ctx context.Context) (*copilot.UserQuota, error) {
			copilotCalls++
			return &copilot.UserQuota{
				Windows: map[string]llm.CopilotQuota{
					"premium": {Type: "premium_interactions", Remaining: 143},
				},
			}, nil
		},
	})
	m.width = 100
	m.height = 24

	m.inputBuf = "/stats"
	m.inputPos = len("/stats")
	updated, cmd := m.submitInput()
	m = updated.(ChatModel)
	if cmd == nil {
		t.Fatal("expected first /stats to return fetch command")
	}
	if msg := cmd(); msg != nil {
		updated, _ = m.Update(msg)
		m = updated.(ChatModel)
	}
	if copilotCalls != 1 {
		t.Fatalf("copilotCalls after first open = %d, want 1", copilotCalls)
	}

	m.statsVisible = false
	m.inputBuf = "/stats"
	m.inputPos = len("/stats")
	updated, cmd = m.submitInput()
	m = updated.(ChatModel)
	if cmd != nil {
		if msg := cmd(); msg != nil {
			updated, _ = m.Update(msg)
			m = updated.(ChatModel)
		}
	}
	if copilotCalls != 1 {
		t.Fatalf("copilotCalls after second open = %d, want cached 1", copilotCalls)
	}
}

func TestChatModelApplySnapshotClearsProviderDiagnostics(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "copilot/gpt-5", WorkDir: "/tmp"})
	m.statusData.CopilotLive = &copilot.UserQuota{
		Windows: map[string]llm.CopilotQuota{
			"premium": {Type: "premium_interactions", Remaining: 143},
		},
	}
	m.statusData.CodexUsage = &codexusage.Snapshot{Plan: "pro"}
	m.statsCopilotErr = "temporary failure"
	m.statsCodexErr = "temporary failure"

	m.applySnapshot(chatSessionSnapshot{
		Model:        "anthropic/claude-sonnet-4-6",
		WorkDir:      "/tmp/restored",
		SessionUsage: llm.Usage{InputTokens: 20, OutputTokens: 10},
	})

	if m.statusData.CopilotLive != nil || m.statusData.CodexUsage != nil {
		t.Fatalf("expected restored snapshot to clear provider diagnostics: %#v", m.statusData)
	}
	if m.statsCopilotErr != "" || m.statsCodexErr != "" {
		t.Fatalf("expected restored snapshot to clear provider errors: copilot=%q codex=%q", m.statsCopilotErr, m.statsCodexErr)
	}
}

func TestChatModelF1OpensAndEscClosesHelpOverlay(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 30

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyF1})
	m = updated.(ChatModel)
	if !m.helpVisible {
		t.Fatal("expected F1 to open help overlay")
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = updated.(ChatModel)
	if m.helpVisible {
		t.Fatal("expected Esc to close help overlay")
	}
}

func TestChatModelHelpOverlayTabNavigation(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 30
	m.helpVisible = true

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(ChatModel)
	if m.helpTab != 1 {
		t.Fatalf("expected helpTab=1, got %d", m.helpTab)
	}
	if !strings.Contains(m.View(), "/model <name>") {
		t.Fatal("expected chat commands help content on second tab")
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(ChatModel)
	if m.helpTab != 2 {
		t.Fatalf("expected helpTab=2, got %d", m.helpTab)
	}
	if !strings.Contains(strings.Join(m.helpLines(), "\n"), "forge skills install") {
		t.Fatal("expected CLI skills help content on third tab")
	}
}

func TestChatModelHelpOverlayAvoidsHiddenToolLogTerminology(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 30
	m.helpTab = 1

	lines := strings.Join(m.helpLines(), "\n")
	if strings.Contains(lines, "hidden tool log") {
		t.Fatalf("expected help overlay to avoid hidden tool log wording, got:\n%s", lines)
	}
}

func TestChatModelApprovalFlow(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	// Simulate approval request arriving
	updated, _ := m.Update(chatApprovalMsg{Tool: "write_file", Summary: "Write test.go"})
	m = updated.(ChatModel)

	if m.pendingApproval == nil {
		t.Fatal("expected pending approval")
	}

	v := m.View()
	if !strings.Contains(v, "write_file") {
		t.Fatalf("view should show pending approval tool name, got: %s", v)
	}

	// Approve with 'y'
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = updated.(ChatModel)
	if m.pendingApproval != nil {
		t.Fatal("approval should be cleared after y")
	}
}

func TestChatModelApprovalDeny(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	updated, _ := m.Update(chatApprovalMsg{Tool: "write_file", Summary: "Write test.go"})
	m = updated.(ChatModel)

	// Deny with 'n'
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = updated.(ChatModel)
	if m.pendingApproval != nil {
		t.Fatal("approval should be cleared after n")
	}
}

func TestChatModelHiddenToolsBufferDoesNotRenderByDefault(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 120
	m.height = 24
	setToolsContent(&m, "● read_file {\"path\":\"main.go\"}\nstatus: ok\n")

	v := m.View()
	if strings.Contains(v, "read_file") {
		t.Fatal("hidden tools buffer should not render in the transcript")
	}
}

func TestChatModelOpensSidePanelForSubAgentWork(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 120
	m.height = 24

	updated, _ := m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "runtime", Text: "[repo doc editor] starting", SubAgent: "repo doc editor"})
	m = updated.(ChatModel)
	updated, _ = m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "read_file", Text: "docs/forge-competitive-gap-findings.md", SubAgent: "repo doc editor"})
	m = updated.(ChatModel)

	if !m.toolsVisible {
		t.Fatal("expected sub-agent work to open side panel")
	}
	view := m.View()
	if !strings.Contains(view, "repo doc editor") || !strings.Contains(view, "read_file") {
		t.Fatalf("expected side panel to show sub-agent work, view = %q", view)
	}
}

func TestChatModelAgentPanelSkipsLegacyToolSections(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 120
	m.height = 24
	m.toolsVisible = true
	m.toolsSections = []toolsSection{
		{buf: "legacy raw tool output\n"},
		{role: "repo doc editor", buf: "read_file docs/file.md\n"},
	}

	view := m.View()
	if strings.Contains(view, "legacy raw tool output") {
		t.Fatalf("legacy tool output leaked into agent panel: %s", view)
	}
	if !strings.Contains(view, "read_file docs/file.md") {
		t.Fatalf("agent work missing from panel: %s", view)
	}
}

func TestChatModelToolsPaneToggleShowsRemovedMessage(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 120
	m.height = 24

	if m.toolsVisible {
		t.Fatal("tools pane should start hidden")
	}

	m.inputBuf = "/tools"
	m.inputPos = len("/tools")
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	if m.toolsVisible {
		t.Fatal("tools pane should stay hidden")
	}
	if !strings.Contains(m.flash, "tools pane removed") {
		t.Fatalf("flash = %q", m.flash)
	}
}

func TestChatModelSlashToggleToolsAlias(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 120
	m.height = 24

	m.inputBuf = "/toggle tools"
	m.inputPos = len("/toggle tools")
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	if m.toolsVisible {
		t.Fatal("tools pane should remain hidden after /toggle tools")
	}
	if !strings.Contains(m.flash, "tools pane removed") {
		t.Fatalf("flash = %q", m.flash)
	}
}

func TestChatModelSlashToggleToolsOnOffStaysDisabled(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 120
	m.height = 24

	m.inputBuf = "/toggle tools on"
	m.inputPos = len(m.inputBuf)
	updated, _ := m.submitInput()
	m = updated.(ChatModel)
	if m.toolsVisible {
		t.Fatal("tools pane should remain hidden after /toggle tools on")
	}

	m.inputBuf = "/toggle tools off"
	m.inputPos = len(m.inputBuf)
	updated, _ = m.submitInput()
	m = updated.(ChatModel)
	if m.toolsVisible {
		t.Fatal("tools pane should remain hidden after /toggle tools off")
	}
}

func TestChatModelSlashPanelArmsAutoOpen(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 120
	m.height = 24

	m.inputBuf = "/panel"
	m.inputPos = len(m.inputBuf)
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	if m.toolsVisible {
		t.Fatal("panel should not render until agent work exists")
	}
	if m.agentPanelHiddenByUser {
		t.Fatal("/panel should allow future auto-open")
	}
	if !strings.Contains(m.flash, "panel will open when agent work starts") {
		t.Fatalf("flash = %q", m.flash)
	}
}

func TestChatModelSlashPanelOffSuppressesSubAgentAutoOpen(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 120
	m.height = 24

	m.inputBuf = "/panel off"
	m.inputPos = len(m.inputBuf)
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	updated, _ = m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "runtime", Text: "[repo doc editor] starting", SubAgent: "repo doc editor"})
	m = updated.(ChatModel)

	if m.toolsVisible {
		t.Fatal("panel should stay hidden after /panel off")
	}
}

func TestChatModelSlashPanelOnShowsExistingAgentWork(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 120
	m.height = 24
	m.agentPanelHiddenByUser = true
	m.appendTools("repo doc editor", "read_file docs/file.md\n")

	m.inputBuf = "/panel on"
	m.inputPos = len(m.inputBuf)
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	if !m.toolsVisible {
		t.Fatal("panel should show existing agent work after /panel on")
	}
	if m.agentPanelHiddenByUser {
		t.Fatal("/panel on should clear explicit hide")
	}
}

func TestChatModelSlashProviderShowsProviders(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{
		Model:   "test",
		WorkDir: "/tmp",
		Providers: []ProviderOption{
			{ID: "openai", Label: "OpenAI", Status: "ready", DefaultModel: "openai/gpt-5"},
		},
	})
	m.width = 100
	m.height = 24

	m.inputBuf = "/provider"
	m.inputPos = len("/provider")
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	if got := m.flash; got != "providers opened" {
		t.Fatalf("flash = %q, want providers opened", got)
	}
	if !m.providersVisible {
		t.Fatal("expected provider overlay to be visible")
	}
	if got := m.View(); !strings.Contains(got, "Providers") || !strings.Contains(got, "OpenAI") {
		t.Fatalf("view missing provider overlay output: %s", got)
	}
}

func TestChatModelSlashModelsShowsVisibleOutput(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{
		Model:           "openai/gpt-5",
		WorkDir:         "/tmp",
		AvailableModels: []string{"openai/gpt-5", "anthropic/claude-sonnet-4-6"},
	})
	m.width = 100
	m.height = 24

	m.inputBuf = "/models"
	m.inputPos = len("/models")
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	got := m.View()
	if !strings.Contains(got, "openai/gpt-5") || !strings.Contains(got, "anthropic/claude-sonnet-4-6") {
		t.Fatalf("view missing models output: %s", got)
	}
}

func TestChatModelSlashModelsOpensOverlay(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{
		Model:           "openai/gpt-5",
		WorkDir:         "/tmp",
		AvailableModels: []string{"openai/gpt-5", "anthropic/claude-sonnet-4-6"},
		DescribeModel: func(model string) string {
			switch model {
			case "openai/gpt-5":
				return "gpt-5 [openai]"
			case "anthropic/claude-sonnet-4-6":
				return "claude-sonnet-4-6 [anthropic]"
			default:
				return model
			}
		},
	})
	m.width = 100
	m.height = 24

	m.inputBuf = "/models"
	m.inputPos = len("/models")
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	if !m.modelsVisible {
		t.Fatal("expected models overlay to be visible")
	}
	if got := m.View(); !strings.Contains(got, "Models") || !strings.Contains(got, "claude-sonnet-4-6 [anthropic]") || !strings.Contains(got, "gpt-5 [openai]") {
		t.Fatalf("models overlay missing content: %s", got)
	}
}

func TestChatModelModelFilterMatchesDisplayLabel(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{
		Model:           "gpt-5.4",
		WorkDir:         "/tmp",
		AvailableModels: []string{"gpt-5.4", "openai/gpt-5.4"},
		DescribeModel: func(model string) string {
			switch model {
			case "gpt-5.4":
				return "gpt-5.4 [chatgpt]"
			case "openai/gpt-5.4":
				return "gpt-5.4 [openai]"
			default:
				return model
			}
		},
	})
	m.width = 100
	m.height = 24
	m.openModelPicker()

	updated, _ := m.handleModelsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("chatgpt")})
	m = updated.(ChatModel)
	if len(m.modelsFiltered) != 1 || m.modelsFiltered[0] != "gpt-5.4" {
		t.Fatalf("modelsFiltered = %#v", m.modelsFiltered)
	}
}

func TestChatModelSlashAgentsIsUnknown(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{
		Model:   "openai/gpt-5",
		WorkDir: "/tmp",
	})
	m.width = 100
	m.height = 24

	m.inputBuf = "/agents"
	m.inputPos = len(m.inputBuf)
	updated, _ := m.submitInput()
	m = updated.(ChatModel)
	if got := m.flash; got != "unknown command: /agents" {
		t.Fatalf("flash = %q", got)
	}
}

func TestChatModelSubmitsAbsolutePathAsPrompt(t *testing.T) {
	inputCh := make(chan string, 1)
	path := filepath.Join(t.TempDir(), "2026-05-07-best-of-claude-forge.md")
	if err := os.WriteFile(path, []byte("plan"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewChatModel(ChatLiveConfig{
		Model:   "openai/gpt-5",
		WorkDir: "/tmp",
	})
	m.inputCh = inputCh
	m.inputBuf = path
	m.inputPos = len(m.inputBuf)

	updated, cmd := m.submitInput()
	m = updated.(ChatModel)

	if cmd == nil {
		t.Fatal("expected absolute path to submit to chat")
	}
	if len(m.messages) != 1 || m.messages[0].Kind != MsgUser || m.messages[0].Content != path {
		t.Fatalf("messages = %#v", m.messages)
	}
	cmd()
	select {
	case raw := <-inputCh:
		if !strings.Contains(raw, path) {
			t.Fatalf("queued input = %q", raw)
		}
	default:
		t.Fatal("expected queued chat input")
	}
}

func TestChatModelSubmitsPromptContainingAbsolutePath(t *testing.T) {
	inputCh := make(chan string, 1)
	path := filepath.Join(t.TempDir(), "2026-05-07-best-of-claude-forge.md")
	m := NewChatModel(ChatLiveConfig{
		Model:   "openai/gpt-5",
		WorkDir: "/tmp",
	})
	m.inputCh = inputCh
	m.inputBuf = "read " + path
	m.inputPos = len(m.inputBuf)

	updated, cmd := m.submitInput()
	m = updated.(ChatModel)

	if cmd == nil {
		t.Fatal("expected path-containing prompt to submit to chat")
	}
	if len(m.messages) != 1 || m.messages[0].Kind != MsgUser || !strings.Contains(m.messages[0].Content, "best-of-claude-forge.md") {
		t.Fatalf("messages = %#v", m.messages)
	}
}

func TestChatModelSubmitsTildePathAsPrompt(t *testing.T) {
	inputCh := make(chan string, 1)
	m := NewChatModel(ChatLiveConfig{
		Model:   "openai/gpt-5",
		WorkDir: "/tmp",
	})
	m.inputCh = inputCh
	m.inputBuf = "~/git/forge/docs/plans/2026-05-07-best-of-claude-forge.md"
	m.inputPos = len(m.inputBuf)

	updated, cmd := m.submitInput()
	m = updated.(ChatModel)

	if cmd == nil {
		t.Fatal("expected tilde path to submit to chat")
	}
	if len(m.messages) != 1 || m.messages[0].Kind != MsgUser || m.messages[0].Content != "~/git/forge/docs/plans/2026-05-07-best-of-claude-forge.md" {
		t.Fatalf("messages = %#v", m.messages)
	}
}

func TestChatModelHelpOmitsLegacyAgentCommands(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{
		Model:   "openai/gpt-5",
		WorkDir: "/tmp",
	})
	m.width = 100
	m.height = 24
	m.helpVisible = true
	m.helpTab = 1

	got := m.View()
	if strings.Contains(got, "/agents") || strings.Contains(got, "Agent Models") {
		t.Fatalf("legacy agent commands should be hidden from help: %s", got)
	}
}

func TestChatModelToolCallShowsWorkingActivityWithoutDebug(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "openai/gpt-5", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24

	updated, _ := m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "read_file", Text: "inspect main.go"})
	m = updated.(ChatModel)

	if len(m.toolsSections) != 0 {
		t.Fatalf("expected no default trace buffer, got %#v", m.toolsSections)
	}
	if len(m.messages) == 0 || m.messages[len(m.messages)-1].Kind != MsgWorking {
		t.Fatalf("expected progress status activity row, got %#v", m.messages)
	}
	if !strings.Contains(m.messages[len(m.messages)-1].Content, "Reading inspect main.go") {
		t.Fatalf("unexpected working activity: %#v", m.messages[len(m.messages)-1])
	}
}

func TestChatModelTraceCommandRequiresDebug(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "openai/gpt-5", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24

	m.inputBuf = "/trace"
	m.inputPos = len(m.inputBuf)
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	if m.traceVisible {
		t.Fatal("trace overlay should stay hidden without debug")
	}
	if got := m.flash; got != "trace unavailable without -d" {
		t.Fatalf("flash = %q", got)
	}
}

func TestChatModelTraceCommandOpensOverlayInDebugMode(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "openai/gpt-5", WorkDir: "/tmp", DebugEnabled: true})
	m.width = 100
	m.height = 24
	m.toolsSections = []toolsSection{{buf: "tool_call read_file\nobserve complete\n"}}

	m.inputBuf = "/trace"
	m.inputPos = len(m.inputBuf)
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	if !m.traceVisible {
		t.Fatal("expected trace overlay to open in debug mode")
	}
	if got := m.View(); !strings.Contains(got, "Debug trace") || !strings.Contains(got, "tool_call read_file") {
		t.Fatalf("trace overlay missing content: %s", got)
	}
}

func TestChatModelModelsOverlayIsSearchable(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{
		Model:           "openai/gpt-5",
		WorkDir:         "/tmp",
		AvailableModels: []string{"openai/gpt-5", "anthropic/claude-sonnet-4-6", "groq/llama"},
	})
	m.width = 100
	m.height = 24
	m.openModelPicker()

	updated, _ := m.handleModelsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("anth")})
	m = updated.(ChatModel)

	if len(m.modelsFiltered) != 1 || m.modelsFiltered[0] != "anthropic/claude-sonnet-4-6" {
		t.Fatalf("modelsFiltered = %#v", m.modelsFiltered)
	}
	if got := m.View(); !strings.Contains(got, "Query: anth") || !strings.Contains(got, "anthropic/claude-sonnet-4-6") || strings.Contains(got, "groq/llama") {
		t.Fatalf("searchable models overlay wrong output: %s", got)
	}
}

func TestChatModelModelsOverlaySelectsFilteredResult(t *testing.T) {
	switched := ""
	m := NewChatModel(ChatLiveConfig{
		Model:           "openai/gpt-5",
		WorkDir:         "/tmp",
		AvailableModels: []string{"openai/gpt-5", "anthropic/claude-sonnet-4-6", "groq/llama"},
		SwitchModel: func(name string) (string, error) {
			switched = name
			return name, nil
		},
	})
	m.width = 100
	m.height = 24
	m.openModelPicker()

	updated, _ := m.handleModelsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("groq")})
	m = updated.(ChatModel)
	updated, _ = m.handleModelsKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(ChatModel)

	if switched != "groq/llama" {
		t.Fatalf("switched = %q", switched)
	}
	if m.modelsVisible {
		t.Fatal("models overlay should close after selection")
	}
}

func TestChatModelModelsOverlayMouseSelectsModel(t *testing.T) {
	switched := ""
	m := NewChatModel(ChatLiveConfig{
		Model:           "openai/gpt-5",
		WorkDir:         "/tmp",
		AvailableModels: []string{"openai/gpt-5", "anthropic/claude-sonnet-4-6"},
		SwitchModel: func(name string) (string, error) {
			switched = name
			return name, nil
		},
	})
	m.width = 100
	m.height = 24
	m.openModelPicker()
	x0, _, _, _, listY, _, _ := m.modelsOverlayLayout()

	updated, _ := m.Update(tea.MouseMsg{X: x0 + 2, Y: listY + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = updated.(ChatModel)

	if switched != "anthropic/claude-sonnet-4-6" {
		t.Fatalf("switched = %q", switched)
	}
}

func TestChatModelOpenModelPickerRefreshesWhenCacheEmpty(t *testing.T) {
	refreshCalls := 0
	m := NewChatModel(ChatLiveConfig{
		Model:   "openai/gpt-5",
		WorkDir: "/tmp",
		RefreshModels: func() []string {
			refreshCalls++
			return []string{"openai/gpt-5", "openrouter/moonshotai/kimi-k2-0905"}
		},
	})
	m.width = 100
	m.height = 24
	m.modelsList = nil
	m.modelsFiltered = nil
	m.openModelPicker()

	if refreshCalls != 1 {
		t.Fatalf("refreshCalls = %d, want 1", refreshCalls)
	}
	if len(m.modelsFiltered) != 2 {
		t.Fatalf("modelsFiltered = %#v", m.modelsFiltered)
	}
	if got := m.View(); !strings.Contains(got, "openrouter/moonshotai/kimi-k2-0905") {
		t.Fatalf("models overlay missing refreshed provider model: %s", got)
	}
}

func TestChatModelOpenModelPickerUsesCachedModelsWithoutRefresh(t *testing.T) {
	refreshCalls := 0
	m := NewChatModel(ChatLiveConfig{
		Model:           "openai/gpt-5",
		WorkDir:         "/tmp",
		AvailableModels: []string{"openai/gpt-5", "anthropic/claude-sonnet-4-6"},
		RefreshModels: func() []string {
			refreshCalls++
			return []string{"live/provider-model"}
		},
	})
	m.width = 100
	m.height = 24

	m.openModelPicker()

	if refreshCalls != 0 {
		t.Fatalf("refreshCalls = %d, want 0", refreshCalls)
	}
	if len(m.modelsFiltered) != 2 {
		t.Fatalf("modelsFiltered = %#v", m.modelsFiltered)
	}
	if m.modelsFiltered[1] != "anthropic/claude-sonnet-4-6" {
		t.Fatalf("modelsFiltered = %#v", m.modelsFiltered)
	}
}

func TestChatModelOpenModelPickerStartsAtFirstRow(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{
		Model:           "anthropic/claude-sonnet-4-6",
		WorkDir:         "/tmp",
		AvailableModels: []string{"openai/gpt-5", "anthropic/claude-sonnet-4-6", "copilot/gpt-5"},
	})
	m.width = 100
	m.height = 24

	m.openModelPicker()

	if m.modelsCursor != 0 {
		t.Fatalf("modelsCursor = %d, want 0", m.modelsCursor)
	}
}

func TestChatModelModelsOverlayDedupesDuplicateEntries(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{
		Model:           "openai/gpt-5",
		WorkDir:         "/tmp",
		AvailableModels: []string{"openai/gpt-5", "anthropic/claude-sonnet-4-6", "openai/gpt-5"},
	})
	m.width = 100
	m.height = 24

	m.openModelPicker()

	if len(m.modelsFiltered) != 2 {
		t.Fatalf("modelsFiltered = %#v", m.modelsFiltered)
	}
}

func TestChatModelModelsOverlayDigitsExtendQuery(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{
		Model:   "openai/gpt-5",
		WorkDir: "/tmp",
		AvailableModels: []string{
			"openrouter/provider-model-03",
			"openrouter/provider-model-30",
		},
		SwitchModel: func(name string) (string, error) {
			t.Fatalf("SwitchModel should not be called while typing query, got %q", name)
			return name, nil
		},
	})
	m.width = 100
	m.height = 24
	m.openModelPicker()

	updated, _ := m.handleModelsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("0")})
	m = updated.(ChatModel)
	updated, _ = m.handleModelsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")})
	m = updated.(ChatModel)

	if m.modelsQuery != "03" {
		t.Fatalf("modelsQuery = %q, want %q", m.modelsQuery, "03")
	}
	if !m.modelsVisible {
		t.Fatal("models overlay should remain open while typing query")
	}
	if len(m.modelsFiltered) != 1 || m.modelsFiltered[0] != "openrouter/provider-model-03" {
		t.Fatalf("modelsFiltered = %#v", m.modelsFiltered)
	}
}

func TestChatModelModelsOverlayLeadingDigitStartsSearch(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{
		Model:   "openai/gpt-5",
		WorkDir: "/tmp",
		AvailableModels: []string{
			"anthropic/claude-opus-4-6",
			"anthropic/claude-sonnet-4-6",
			"anthropic/claude-haiku-4-5",
		},
		SwitchModel: func(name string) (string, error) {
			t.Fatalf("SwitchModel should not be called while typing numeric query, got %q", name)
			return name, nil
		},
	})
	m.width = 100
	m.height = 24
	m.openModelPicker()

	updated, _ := m.handleModelsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("4")})
	m = updated.(ChatModel)

	if m.modelsQuery != "4" {
		t.Fatalf("modelsQuery = %q, want %q", m.modelsQuery, "4")
	}
	if !m.modelsVisible {
		t.Fatal("models overlay should remain open while typing query")
	}
	if len(m.modelsFiltered) != 3 {
		t.Fatalf("modelsFiltered = %#v", m.modelsFiltered)
	}
}

func TestChatModelModelsOverlayDedupesEquivalentLabels(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{
		Model:   "claude/claude-sonnet-4-6",
		WorkDir: "/tmp",
		AvailableModels: []string{
			"claude-sonnet-4-6",
			"claude/claude-sonnet-4-6",
			"claude-opus-4-6",
			"claude/claude-opus-4-6",
		},
		DescribeModel: func(model string) string {
			switch model {
			case "claude-sonnet-4-6", "claude/claude-sonnet-4-6":
				return "claude-sonnet-4-6 [claude]"
			case "claude-opus-4-6", "claude/claude-opus-4-6":
				return "claude-opus-4-6 [claude]"
			default:
				return model
			}
		},
	})
	m.width = 100
	m.height = 24

	m.openModelPicker()

	if len(m.modelsFiltered) != 2 {
		t.Fatalf("modelsFiltered = %#v", m.modelsFiltered)
	}
	if m.modelsFiltered[0] != "claude/claude-sonnet-4-6" || m.modelsFiltered[1] != "claude/claude-opus-4-6" {
		t.Fatalf("modelsFiltered = %#v", m.modelsFiltered)
	}
}

func TestChatModelModelsOverlayShowsVisibleRange(t *testing.T) {
	models := make([]string, 0, 30)
	for i := 1; i <= 30; i++ {
		models = append(models, fmt.Sprintf("provider/model-%02d", i))
	}
	m := NewChatModel(ChatLiveConfig{
		Model:           "provider/model-01",
		WorkDir:         "/tmp",
		AvailableModels: models,
	})
	m.width = 100
	m.height = 24
	m.openModelPicker()

	got := m.View()
	if !strings.Contains(got, "/30") {
		t.Fatalf("models overlay missing visible range footer: %s", got)
	}
}

func TestChatModelProviderOverlaySaveRefreshesModelCache(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	m := NewChatModel(ChatLiveConfig{
		Model:   "test",
		WorkDir: "/tmp",
		Providers: []ProviderOption{
			{ID: "openai", Label: "OpenAI", Status: "configure API key", DefaultModel: "openai/gpt-5"},
		},
		RefreshProviders: func() []ProviderOption {
			return []ProviderOption{{ID: "openai", Label: "OpenAI", Status: "ready", DefaultModel: "openai/gpt-5"}}
		},
		RefreshModels: func() []string {
			return []string{"openai/gpt-5", "openai/o3"}
		},
		SwitchModel: func(name string) (string, error) {
			return name, nil
		},
	})
	m.width = 100
	m.height = 24
	m.openProviderPicker()

	updated, _ := m.handleProvidersKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(ChatModel)
	m.providerKeyInput = "sk-test"
	m.providerKeyPos = len(m.providerKeyInput)
	updated, _ = m.handleProvidersKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(ChatModel)

	if len(m.modelsList) != 2 || m.modelsList[1] != "openai/o3" {
		t.Fatalf("modelsList = %#v", m.modelsList)
	}
}

func TestChatModelProviderOverlayDeleteRefreshesModelCache(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	if err := auth.Save(&auth.Tokens{OpenAIAPIKey: "sk-test"}); err != nil {
		t.Fatalf("auth.Save: %v", err)
	}
	m := NewChatModel(ChatLiveConfig{
		Model:           "test",
		WorkDir:         "/tmp",
		AvailableModels: []string{"openai/gpt-5", "openai/o3"},
		Providers: []ProviderOption{
			{ID: "openai", Label: "OpenAI", Status: "ready", DefaultModel: "openai/gpt-5"},
		},
		RefreshProviders: func() []ProviderOption {
			return []ProviderOption{{ID: "openai", Label: "OpenAI", Status: "configure API key", DefaultModel: "openai/gpt-5"}}
		},
		RefreshModels: func() []string {
			return []string{"anthropic/claude-sonnet-4-6"}
		},
	})
	m.width = 100
	m.height = 24
	m.openProviderPicker()

	updated, _ := m.handleProvidersKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = updated.(ChatModel)

	if len(m.modelsList) != 1 || m.modelsList[0] != "anthropic/claude-sonnet-4-6" {
		t.Fatalf("modelsList = %#v", m.modelsList)
	}
}

func TestChatModelProviderOverlaySavesAPIKey(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	switched := ""
	m := NewChatModel(ChatLiveConfig{
		Model:   "test",
		WorkDir: "/tmp",
		Providers: []ProviderOption{
			{ID: "openai", Label: "OpenAI", Status: "configure API key", DefaultModel: "openai/gpt-5"},
		},
		RefreshProviders: func() []ProviderOption {
			return []ProviderOption{{ID: "openai", Label: "OpenAI", Status: "ready", DefaultModel: "openai/gpt-5"}}
		},
		SwitchModel: func(name string) (string, error) {
			switched = name
			return name, nil
		},
	})
	m.width = 100
	m.height = 24
	m.openProviderPicker()

	updated, _ := m.handleProvidersKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(ChatModel)
	if !m.providerPromptingKey {
		t.Fatal("expected API key prompt")
	}
	m.providerKeyInput = "sk-test"
	m.providerKeyPos = len(m.providerKeyInput)
	updated, _ = m.handleProvidersKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(ChatModel)

	tokens, err := auth.Load()
	if err != nil {
		t.Fatalf("auth.Load: %v", err)
	}
	if tokens.OpenAIAPIKey != "sk-test" {
		t.Fatalf("OpenAI key = %q", tokens.OpenAIAPIKey)
	}
	if switched != "openai/gpt-5" {
		t.Fatalf("switched = %q", switched)
	}
}

func TestChatModelProviderOverlaySavesAPIKeyPreservesSelectedProviderAcrossRefresh(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	t.Setenv("HOME", configDir)
	switched := ""
	m := NewChatModel(ChatLiveConfig{
		Model:   "test",
		WorkDir: "/tmp",
		Providers: []ProviderOption{
			{ID: "openrouter", Label: "OpenRouter", Status: "configure API key", DefaultModel: "openrouter/openai/gpt-5"},
			{ID: "openai", Label: "OpenAI", Status: "ready", DefaultModel: "openai/gpt-5"},
		},
		RefreshProviders: func() []ProviderOption {
			return []ProviderOption{
				{ID: "openai", Label: "OpenAI", Status: "ready", DefaultModel: "openai/gpt-5"},
				{ID: "openrouter", Label: "OpenRouter", Status: "ready", DefaultModel: "openrouter/openai/gpt-5"},
			}
		},
		SwitchModel: func(name string) (string, error) {
			switched = name
			return name, nil
		},
	})
	m.width = 100
	m.height = 24
	m.openProviderPicker()
	m.providersCursor = 1

	updated, _ := m.handleProvidersKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(ChatModel)
	if !m.providerPromptingKey {
		t.Fatal("expected API key prompt")
	}
	m.providerKeyInput = "sk-openrouter"
	m.providerKeyPos = len(m.providerKeyInput)
	updated, _ = m.handleProvidersKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(ChatModel)

	tokens, err := auth.Load()
	if err != nil {
		t.Fatalf("auth.Load: %v", err)
	}
	if tokens.OpenRouterAPIKey != "sk-openrouter" {
		t.Fatalf("OpenRouter key = %q", tokens.OpenRouterAPIKey)
	}
	if switched != "openrouter/openai/gpt-5" {
		t.Fatalf("switched = %q", switched)
	}
	if m.providersCursor != 1 {
		t.Fatalf("providersCursor = %d, want 1", m.providersCursor)
	}
}

func TestChatModelProviderOverlayDeletesAPIKey(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	if err := auth.Save(&auth.Tokens{OpenAIAPIKey: "sk-test"}); err != nil {
		t.Fatalf("auth.Save: %v", err)
	}
	m := NewChatModel(ChatLiveConfig{
		Model:   "test",
		WorkDir: "/tmp",
		Providers: []ProviderOption{
			{ID: "openai", Label: "OpenAI", Status: "ready", DefaultModel: "openai/gpt-5"},
		},
	})
	m.width = 100
	m.height = 24
	m.openProviderPicker()

	updated, _ := m.handleProvidersKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = updated.(ChatModel)

	tokens, err := auth.Load()
	if err != nil {
		t.Fatalf("auth.Load: %v", err)
	}
	if tokens.OpenAIAPIKey != "" {
		t.Fatalf("expected OpenAI key deleted, got %q", tokens.OpenAIAPIKey)
	}
}

func TestChatModelCustomProviderSavesAPIKey(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)

	switched := ""
	m := NewChatModel(ChatLiveConfig{
		Model:   "test",
		WorkDir: "/tmp",
		Providers: []ProviderOption{
			{ID: "oca", Label: "My New Provider", Status: "configure API key", DefaultModel: "oca/gpt-5.4"},
		},
		RefreshProviders: func() []ProviderOption {
			return []ProviderOption{{ID: "oca", Label: "My New Provider", Status: "ready", DefaultModel: "oca/gpt-5.4"}}
		},
		RefreshModels: func() []string {
			return []string{"oca/gpt-5.4", "oca/gpt-5.4-mini"}
		},
		SwitchModel: func(name string) (string, error) {
			switched = name
			return name, nil
		},
	})
	m.width = 100
	m.height = 24
	m.openProviderPicker()

	updated, _ := m.handleProvidersKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(ChatModel)
	if !m.providerPromptingKey {
		t.Fatal("expected API key prompt for custom provider")
	}

	m.providerKeyInput = "sk-oca"
	m.providerKeyPos = len(m.providerKeyInput)
	updated, _ = m.handleProvidersKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(ChatModel)

	tokens, err := auth.Load()
	if err != nil {
		t.Fatalf("auth.Load: %v", err)
	}
	if got := tokens.CustomProviderKey("oca"); got != "sk-oca" {
		t.Fatalf("custom provider key = %q, want %q", got, "sk-oca")
	}
	if switched != "oca/gpt-5.4" {
		t.Fatalf("switched = %q, want %q", switched, "oca/gpt-5.4")
	}
	if len(m.providersList) != 1 || m.providersList[0].Status != "ready" {
		t.Fatalf("providersList = %#v", m.providersList)
	}
	if len(m.modelsList) != 2 || m.modelsList[0] != "oca/gpt-5.4" {
		t.Fatalf("modelsList = %#v", m.modelsList)
	}
}

func TestChatModelCustomProviderDeletesAPIKey(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	if err := auth.Save(&auth.Tokens{
		ProviderAPIKeys: map[string]string{"oca": "sk-oca"},
	}); err != nil {
		t.Fatalf("auth.Save: %v", err)
	}

	m := NewChatModel(ChatLiveConfig{
		Model:           "oca/gpt-5.4",
		WorkDir:         "/tmp",
		AvailableModels: []string{"oca/gpt-5.4"},
		Providers: []ProviderOption{
			{ID: "oca", Label: "My New Provider", Status: "ready", DefaultModel: "oca/gpt-5.4"},
		},
		RefreshProviders: func() []ProviderOption {
			return []ProviderOption{{ID: "oca", Label: "My New Provider", Status: "configure API key", DefaultModel: "oca/gpt-5.4"}}
		},
		RefreshModels: func() []string {
			return []string{"openai/gpt-5"}
		},
	})
	m.width = 100
	m.height = 24
	m.openProviderPicker()

	updated, _ := m.handleProvidersKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = updated.(ChatModel)

	tokens, err := auth.Load()
	if err != nil {
		t.Fatalf("auth.Load: %v", err)
	}
	if got := tokens.CustomProviderKey("oca"); got != "" {
		t.Fatalf("custom provider key = %q, want empty", got)
	}
	if len(m.providersList) != 1 || m.providersList[0].Status != "configure API key" {
		t.Fatalf("providersList = %#v", m.providersList)
	}
	if len(m.modelsList) != 1 || m.modelsList[0] != "openai/gpt-5" {
		t.Fatalf("modelsList = %#v", m.modelsList)
	}
}

func TestChatModelProviderOverlayChatGPTLoginFlow(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	prevStart := startChatGPTDeviceAuth
	prevWait := waitChatGPTDeviceAuth
	startChatGPTDeviceAuth = func(ctx context.Context) (*chatgptauth.DeviceFlow, error) {
		return &chatgptauth.DeviceFlow{}, nil
	}
	waitChatGPTDeviceAuth = func(ctx context.Context, flow *chatgptauth.DeviceFlow) (chatgptauth.Session, error) {
		return chatgptauth.Session{
			AccessToken:  "access-token",
			RefreshToken: "refresh-token",
			AccountID:    "acct",
			ExpiresAt:    time.Now().Add(time.Hour),
		}, nil
	}
	t.Cleanup(func() {
		startChatGPTDeviceAuth = prevStart
		waitChatGPTDeviceAuth = prevWait
	})

	authenticated := false
	switched := ""
	m := NewChatModel(ChatLiveConfig{
		Model:   "test",
		WorkDir: "/tmp",
		Providers: []ProviderOption{
			{ID: "chatgpt", Label: "ChatGPT subscription", Status: "sign in", DefaultModel: "chatgpt/gpt-5.4"},
		},
		RefreshProviders: func() []ProviderOption {
			status := "sign in"
			if authenticated {
				status = "ready"
			}
			return []ProviderOption{{ID: "chatgpt", Label: "ChatGPT subscription", Status: status, DefaultModel: "chatgpt/gpt-5.4"}}
		},
		RefreshModels: func() []string {
			return []string{"chatgpt/gpt-5.4"}
		},
		SwitchModel: func(name string) (string, error) {
			switched = name
			return name, nil
		},
	})
	m.width = 100
	m.height = 24
	m.openProviderPicker()

	updated, cmd := m.handleProvidersKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(ChatModel)
	if cmd == nil {
		t.Fatal("expected login start command")
	}
	startMsg := cmd().(providerAuthStartedMsg)
	startMsg.verifyURL = "https://auth.openai.com/codex/device"
	startMsg.userCode = "ABCD-1234"
	updated, cmd = m.Update(startMsg)
	m = updated.(ChatModel)
	if !m.providerAuthWaiting {
		t.Fatal("expected provider auth waiting state")
	}
	if got := m.View(); !strings.Contains(got, "ABCD-1234") {
		t.Fatalf("view missing device code: %s", got)
	}
	authenticated = true
	successMsg := cmd().(providerAuthSucceededMsg)
	updated, _ = m.Update(successMsg)
	m = updated.(ChatModel)

	tokens, err := auth.Load()
	if err != nil {
		t.Fatalf("auth.Load: %v", err)
	}
	if tokens.ChatGPTAccessToken != "access-token" || tokens.ChatGPTRefreshToken != "refresh-token" {
		t.Fatal("expected ChatGPT tokens to be saved")
	}
	if switched != "chatgpt/gpt-5.4" {
		t.Fatalf("switched = %q", switched)
	}
}

func TestChatModelProviderOverlayCopilotLoginFlow(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	prevStart := startCopilotDeviceAuth
	prevWait := waitCopilotDeviceAuth
	startCopilotDeviceAuth = func(ctx context.Context, clientID string) (*copilot.DeviceCode, error) {
		return &copilot.DeviceCode{VerificationURI: "https://github.com/login/device", UserCode: "GH-1234"}, nil
	}
	waitCopilotDeviceAuth = func(ctx context.Context, clientID string, dc *copilot.DeviceCode) (string, error) {
		return "copilot-token", nil
	}
	t.Cleanup(func() {
		startCopilotDeviceAuth = prevStart
		waitCopilotDeviceAuth = prevWait
	})

	authenticated := false
	switched := ""
	m := NewChatModel(ChatLiveConfig{
		Model:           "test",
		WorkDir:         "/tmp",
		CopilotClientID: "client-id",
		Providers: []ProviderOption{
			{ID: "copilot", Label: "GitHub Copilot", Status: "sign in", DefaultModel: "copilot/gpt-5"},
		},
		RefreshProviders: func() []ProviderOption {
			status := "sign in"
			if authenticated {
				status = "ready"
			}
			return []ProviderOption{{ID: "copilot", Label: "GitHub Copilot", Status: status, DefaultModel: "copilot/gpt-5"}}
		},
		RefreshModels: func() []string {
			return []string{"copilot/gpt-5"}
		},
		SwitchModel: func(name string) (string, error) {
			switched = name
			return name, nil
		},
	})
	m.width = 100
	m.height = 24
	m.openProviderPicker()

	updated, cmd := m.handleProvidersKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(ChatModel)
	startMsg := cmd().(providerAuthStartedMsg)
	updated, cmd = m.Update(startMsg)
	m = updated.(ChatModel)
	authenticated = true
	successMsg := cmd().(providerAuthSucceededMsg)
	updated, _ = m.Update(successMsg)
	m = updated.(ChatModel)

	tokens, err := auth.Load()
	if err != nil {
		t.Fatalf("auth.Load: %v", err)
	}
	if tokens.CopilotToken != "copilot-token" {
		t.Fatalf("expected copilot token saved, got %q", tokens.CopilotToken)
	}
	if switched != "copilot/gpt-5" {
		t.Fatalf("switched = %q", switched)
	}
}

func TestChatModelProviderOverlayClaudeLoginFlow(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	prevStart := startClaudeAuth
	prevExchange := exchangeClaudeAuth
	prevOpen := openProviderAuthURL
	startClaudeAuth = func() (*claudeauth.Flow, error) {
		return &claudeauth.Flow{
			AuthorizationURL: "https://claude.ai/oauth/authorize?code=true",
			Verifier:         "verifier",
			State:            "state",
		}, nil
	}
	exchangeClaudeAuth = func(ctx context.Context, flow *claudeauth.Flow, pasted string) (claudeauth.Session, error) {
		if pasted != "https://console.anthropic.com/oauth/code/callback?code=abc&state=xyz" {
			t.Fatalf("unexpected pasted callback: %q", pasted)
		}
		return claudeauth.Session{
			AccessToken:  "claude-access",
			RefreshToken: "claude-refresh",
			ExpiresAt:    time.Now().Add(time.Hour),
		}, nil
	}
	t.Cleanup(func() {
		startClaudeAuth = prevStart
		exchangeClaudeAuth = prevExchange
		openProviderAuthURL = prevOpen
	})

	authenticated := false
	switched := ""
	m := NewChatModel(ChatLiveConfig{
		Model:   "test",
		WorkDir: "/tmp",
		Providers: []ProviderOption{
			{ID: "claude", Label: "Claude.ai subscription", Status: "sign in", DefaultModel: "claude/claude-sonnet-4-6"},
		},
		RefreshProviders: func() []ProviderOption {
			status := "sign in"
			if authenticated {
				status = "ready"
			}
			return []ProviderOption{{ID: "claude", Label: "Claude.ai subscription", Status: status, DefaultModel: "claude/claude-sonnet-4-6"}}
		},
		RefreshModels: func() []string {
			return []string{"claude/claude-sonnet-4-6"}
		},
		SwitchModel: func(name string) (string, error) {
			switched = name
			return name, nil
		},
	})
	m.width = 100
	m.height = 24
	m.openProviderPicker()

	updated, cmd := m.handleProvidersKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(ChatModel)
	if cmd == nil {
		t.Fatal("expected Claude login start command")
	}
	startMsg := cmd().(providerAuthStartedMsg)
	updated, _ = m.Update(startMsg)
	m = updated.(ChatModel)
	if !m.providerPromptingKey {
		t.Fatal("expected callback paste prompt")
	}
	if got := m.View(); !strings.Contains(got, "Open URL:") || !strings.Contains(got, "Paste callback/code:") {
		t.Fatalf("view missing Claude auth labels: %s", got)
	} else if !strings.Contains(got, "Open Claude sign-in page") {
		t.Fatalf("view missing Claude auth link label: %s", got)
	} else if !strings.Contains(got, "\x1b]8;;https://claude.ai/oauth/authorize?code=true") {
		t.Fatalf("view missing Claude auth hyperlink: %q", got)
	}

	m.providerKeyInput = "https://console.anthropic.com/oauth/code/callback?code=abc&state=xyz"
	m.providerKeyPos = len([]rune(m.providerKeyInput))
	updated, cmd = m.handleProvidersKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(ChatModel)
	if cmd == nil {
		t.Fatal("expected Claude exchange command")
	}
	authenticated = true
	successMsg := cmd().(providerAuthSucceededMsg)
	updated, _ = m.Update(successMsg)
	m = updated.(ChatModel)

	tokens, err := auth.Load()
	if err != nil {
		t.Fatalf("auth.Load: %v", err)
	}
	if tokens.ClaudeAccessToken != "claude-access" || tokens.ClaudeRefreshToken != "claude-refresh" {
		t.Fatal("expected Claude tokens to be saved")
	}
	if switched != "claude/claude-sonnet-4-6" {
		t.Fatalf("switched = %q", switched)
	}
}

func TestChatModelProviderOverlayClaudeCtrlOOpensURL(t *testing.T) {
	prevOpen := openProviderAuthURL
	openProviderAuthURL = func(target string) tea.Cmd {
		return func() tea.Msg { return providerAuthURLOpenedMsg{target: target} }
	}
	t.Cleanup(func() {
		openProviderAuthURL = prevOpen
	})

	m := NewChatModel(ChatLiveConfig{
		Model:   "test",
		WorkDir: "/tmp",
	})
	m.providersVisible = true
	m.providerPromptingKey = true
	m.providerAuthProvider = "claude"
	m.providerAuthURL = "https://claude.ai/oauth/authorize?code=true"

	updated, cmd := m.handleProvidersKey(tea.KeyMsg{Type: tea.KeyCtrlO})
	m = updated.(ChatModel)
	if cmd == nil {
		t.Fatal("expected open URL command")
	}
	updated, _ = m.Update(cmd())
	m = updated.(ChatModel)
	if m.providerStatus != "opened browser" {
		t.Fatalf("providerStatus = %q", m.providerStatus)
	}
}

func TestWrapProviderAuthValueBreaksLongURLs(t *testing.T) {
	got := wrapProviderAuthValue("https://claude.ai/oauth/authorize?code=true&state=abcdefghijklmnopqrstuvwxyz", 20)
	if !strings.Contains(got, "\n") {
		t.Fatalf("expected wrapped output, got %q", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if lipgloss.Width(line) > 20 {
			t.Fatalf("wrapped line width = %d, want <= 20 for %q", lipgloss.Width(line), line)
		}
	}
}

func TestProviderAuthHyperlink(t *testing.T) {
	got := providerAuthHyperlink("Open Claude sign-in page", "https://claude.ai/oauth/authorize?code=true")
	if !strings.Contains(got, "\x1b]8;;https://claude.ai/oauth/authorize?code=true") {
		t.Fatalf("missing hyperlink target: %q", got)
	}
	if !strings.Contains(got, "Open Claude sign-in page") {
		t.Fatalf("missing hyperlink label: %q", got)
	}
}

func TestChatModelProviderOverlaySaveDoesNotFetchCodexUsageAutomatically(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	codexCalls := 0
	m := NewChatModel(ChatLiveConfig{
		Model:   "test",
		WorkDir: "/tmp",
		Providers: []ProviderOption{
			{ID: "openai", Label: "OpenAI", Status: "configure API key", DefaultModel: "openai/gpt-5"},
		},
		RefreshProviders: func() []ProviderOption {
			return []ProviderOption{{ID: "openai", Label: "OpenAI", Status: "ready", DefaultModel: "openai/gpt-5"}}
		},
		SwitchModel: func(name string) (string, error) {
			return name, nil
		},
		FetchCodexUsage: func(ctx context.Context) (*codexusage.Snapshot, error) {
			codexCalls++
			return &codexusage.Snapshot{Plan: "pro"}, nil
		},
	})
	m.width = 100
	m.height = 24
	m.openProviderPicker()

	updated, _ := m.handleProvidersKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(ChatModel)
	m.providerKeyInput = "sk-test"
	m.providerKeyPos = len(m.providerKeyInput)
	updated, cmd := m.handleProvidersKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(ChatModel)

	if cmd != nil {
		if msg := cmd(); msg != nil {
			updated, _ = m.Update(msg)
			m = updated.(ChatModel)
		}
	}
	if codexCalls != 0 {
		t.Fatalf("codexCalls = %d, want 0 after provider save", codexCalls)
	}
}

func TestChatModelProviderOverlayCopilotAuthDoesNotFetchQuotaAutomatically(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	prevStart := startCopilotDeviceAuth
	prevWait := waitCopilotDeviceAuth
	startCopilotDeviceAuth = func(ctx context.Context, clientID string) (*copilot.DeviceCode, error) {
		return &copilot.DeviceCode{VerificationURI: "https://github.com/login/device", UserCode: "GH-1234"}, nil
	}
	waitCopilotDeviceAuth = func(ctx context.Context, clientID string, dc *copilot.DeviceCode) (string, error) {
		return "copilot-token", nil
	}
	t.Cleanup(func() {
		startCopilotDeviceAuth = prevStart
		waitCopilotDeviceAuth = prevWait
	})

	copilotCalls := 0
	authenticated := false
	m := NewChatModel(ChatLiveConfig{
		Model:           "test",
		WorkDir:         "/tmp",
		CopilotClientID: "client-id",
		Providers: []ProviderOption{
			{ID: "copilot", Label: "GitHub Copilot", Status: "sign in", DefaultModel: "copilot/gpt-5"},
		},
		RefreshProviders: func() []ProviderOption {
			status := "sign in"
			if authenticated {
				status = "ready"
			}
			return []ProviderOption{{ID: "copilot", Label: "GitHub Copilot", Status: status, DefaultModel: "copilot/gpt-5"}}
		},
		RefreshModels: func() []string {
			return []string{"copilot/gpt-5"}
		},
		SwitchModel: func(name string) (string, error) {
			return name, nil
		},
		FetchLiveCopilotQuota: func(ctx context.Context) (*copilot.UserQuota, error) {
			copilotCalls++
			return &copilot.UserQuota{}, nil
		},
	})
	m.width = 100
	m.height = 24
	m.openProviderPicker()

	updated, cmd := m.handleProvidersKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(ChatModel)
	startMsg := cmd().(providerAuthStartedMsg)
	updated, cmd = m.Update(startMsg)
	m = updated.(ChatModel)
	authenticated = true
	successMsg := cmd().(providerAuthSucceededMsg)
	updated, cmd = m.Update(successMsg)
	m = updated.(ChatModel)

	if cmd != nil {
		if msg := cmd(); msg != nil {
			updated, _ = m.Update(msg)
			m = updated.(ChatModel)
		}
	}
	if copilotCalls != 0 {
		t.Fatalf("copilotCalls = %d, want 0 after provider auth", copilotCalls)
	}
}

func TestChatModelProviderOverlayCopilotLoginPreservesSelectedProviderAcrossRefresh(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	t.Setenv("HOME", configDir)
	prevStart := startCopilotDeviceAuth
	prevWait := waitCopilotDeviceAuth
	startCopilotDeviceAuth = func(ctx context.Context, clientID string) (*copilot.DeviceCode, error) {
		return &copilot.DeviceCode{VerificationURI: "https://github.com/login/device", UserCode: "GH-1234"}, nil
	}
	waitCopilotDeviceAuth = func(ctx context.Context, clientID string, dc *copilot.DeviceCode) (string, error) {
		return "copilot-token", nil
	}
	t.Cleanup(func() {
		startCopilotDeviceAuth = prevStart
		waitCopilotDeviceAuth = prevWait
	})

	authenticated := false
	switched := ""
	m := NewChatModel(ChatLiveConfig{
		Model:           "test",
		WorkDir:         "/tmp",
		CopilotClientID: "client-id",
		Providers: []ProviderOption{
			{ID: "copilot", Label: "GitHub Copilot", Status: "sign in", DefaultModel: "copilot/gpt-5"},
			{ID: "openai", Label: "OpenAI", Status: "ready", DefaultModel: "openai/gpt-5"},
		},
		RefreshProviders: func() []ProviderOption {
			status := "sign in"
			if authenticated {
				status = "ready"
			}
			return []ProviderOption{
				{ID: "openai", Label: "OpenAI", Status: "ready", DefaultModel: "openai/gpt-5"},
				{ID: "copilot", Label: "GitHub Copilot", Status: status, DefaultModel: "copilot/gpt-5"},
			}
		},
		RefreshModels: func() []string {
			return []string{"copilot/gpt-5", "openai/gpt-5"}
		},
		SwitchModel: func(name string) (string, error) {
			switched = name
			return name, nil
		},
	})
	m.width = 100
	m.height = 24
	m.openProviderPicker()
	m.providersCursor = 1

	updated, cmd := m.handleProvidersKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(ChatModel)
	startMsg := cmd().(providerAuthStartedMsg)
	updated, cmd = m.Update(startMsg)
	m = updated.(ChatModel)
	authenticated = true
	successMsg := cmd().(providerAuthSucceededMsg)
	updated, _ = m.Update(successMsg)
	m = updated.(ChatModel)

	tokens, err := auth.Load()
	if err != nil {
		t.Fatalf("auth.Load: %v", err)
	}
	if tokens.CopilotToken != "copilot-token" {
		t.Fatalf("expected copilot token saved, got %q", tokens.CopilotToken)
	}
	if switched != "copilot/gpt-5" {
		t.Fatalf("switched = %q", switched)
	}
	if m.providersCursor != 1 {
		t.Fatalf("providersCursor = %d, want 1", m.providersCursor)
	}
}

func TestChatModelProviderOverlayDeletesCopilotCredential(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	if err := auth.Save(&auth.Tokens{CopilotToken: "copilot-token"}); err != nil {
		t.Fatalf("auth.Save: %v", err)
	}
	m := NewChatModel(ChatLiveConfig{
		Model:   "test",
		WorkDir: "/tmp",
		Providers: []ProviderOption{
			{ID: "copilot", Label: "GitHub Copilot", Status: "ready", DefaultModel: "copilot/gpt-5"},
		},
	})
	m.width = 100
	m.height = 24
	m.openProviderPicker()

	updated, _ := m.handleProvidersKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = updated.(ChatModel)

	tokens, err := auth.Load()
	if err != nil {
		t.Fatalf("auth.Load: %v", err)
	}
	if tokens.CopilotToken != "" {
		t.Fatalf("expected Copilot token deleted, got %q", tokens.CopilotToken)
	}
}

func TestChatModelSlashModelSwitchesModel(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{
		Model:   "openai/gpt-5",
		WorkDir: "/tmp",
		AvailableModels: []string{
			"openai/gpt-5",
			"anthropic/claude-sonnet-4-6",
			"chatgpt/gpt-5.1-codex-mini",
		},
		SwitchModel: func(name string) (string, error) {
			if name == "bad" {
				return "", errors.New("boom")
			}
			return name, nil
		},
	})
	m.width = 100
	m.height = 24

	m.inputBuf = "/model anthropic/claude-sonnet-4-6"
	m.inputPos = len(m.inputBuf)
	updated, _ := m.submitInput()
	m = updated.(ChatModel)
	if m.model != "anthropic/claude-sonnet-4-6" {
		t.Fatalf("model = %q", m.model)
	}
	if m.flash != "switched to anthropic/claude-sonnet-4-6" {
		t.Fatalf("flash = %q", m.flash)
	}
}

func TestChatModelSlashModelResolvesPartialName(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{
		Model:   "openai/gpt-5",
		WorkDir: "/tmp",
		AvailableModels: []string{
			"openai/gpt-5",
			"anthropic/claude-sonnet-4-6",
			"chatgpt/gpt-5.1-codex-mini",
		},
		SwitchModel: func(name string) (string, error) {
			return name, nil
		},
	})
	m.width = 100
	m.height = 24

	m.inputBuf = "/model codex-mini"
	m.inputPos = len(m.inputBuf)
	updated, _ := m.submitInput()
	m = updated.(ChatModel)
	if m.model != "chatgpt/gpt-5.1-codex-mini" {
		t.Fatalf("model = %q", m.model)
	}
}

func TestChatModelSlashModelResolvesNumericIndex(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{
		Model:   "openai/gpt-5",
		WorkDir: "/tmp",
		AvailableModels: []string{
			"openai/gpt-5",
			"anthropic/claude-sonnet-4-6",
			"chatgpt/gpt-5.1-codex-mini",
		},
		SwitchModel: func(name string) (string, error) {
			return name, nil
		},
	})
	m.width = 100
	m.height = 24

	m.inputBuf = "/model 2"
	m.inputPos = len(m.inputBuf)
	updated, _ := m.submitInput()
	m = updated.(ChatModel)
	if m.model != "anthropic/claude-sonnet-4-6" {
		t.Fatalf("model = %q", m.model)
	}
}

func TestChatModelSlashModelRejectsUnknownName(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{
		Model:           "openai/gpt-5",
		WorkDir:         "/tmp",
		AvailableModels: []string{"openai/gpt-5"},
		SwitchModel: func(name string) (string, error) {
			t.Fatalf("SwitchModel should not be called for unknown model %q", name)
			return name, nil
		},
	})
	m.width = 100
	m.height = 24

	m.inputBuf = "/model does-not-exist"
	m.inputPos = len(m.inputBuf)
	updated, _ := m.submitInput()
	m = updated.(ChatModel)
	if got := m.flash; got != `unknown model "does-not-exist" — try /models` {
		t.Fatalf("flash = %q", got)
	}
}

func TestChatModelSlashSkillsShowsVisibleOutput(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{
		Model:   "test",
		WorkDir: "/tmp",
		Skills: []skills.Skill{
			{Name: "tdd", Description: "Test driven development"},
		},
	})
	m.width = 100
	m.height = 24

	m.inputBuf = "/skills"
	m.inputPos = len("/skills")
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	got := m.View()
	if !strings.Contains(got, "/tdd") || !strings.Contains(got, "Test driven development") {
		t.Fatalf("view missing skills output: %s", got)
	}
}

func TestChatModelAutoSkillsCommands(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24

	m.inputBuf = "/auto-skills"
	m.inputPos = len(m.inputBuf)
	updated, _ := m.submitInput()
	m = updated.(ChatModel)
	if got := m.flash; got != "auto-skills: suggest" {
		t.Fatalf("flash = %q", got)
	}

	m.inputBuf = "/auto-skills auto"
	m.inputPos = len(m.inputBuf)
	updated, _ = m.submitInput()
	m = updated.(ChatModel)
	if got := m.flash; got != "auto-skills: auto" {
		t.Fatalf("flash = %q", got)
	}
}

func TestChatModelAutoSkillAlreadyActiveDoesNotSwallowInput(t *testing.T) {
	inputCh := make(chan string, 1)
	state := chatstate.New()
	state.ActivateSkill("brainstorming")
	m := NewChatModel(ChatLiveConfig{
		Model:          "test",
		WorkDir:        "/tmp",
		Skills:         []skills.Skill{{Name: "brainstorming", Description: "Plan first", Body: "Brainstorm before coding."}},
		AutoSkillsMode: skills.AutoSkillsAuto,
		State:          state,
	})
	m.inputCh = inputCh
	m.width = 100
	m.height = 24
	m.inputBuf = "hello, i am asking what your plan is to remedy all the stuff you said is missing"
	m.inputPos = len(m.inputBuf)

	updated, cmd := m.submitInput()
	m = updated.(ChatModel)

	if cmd == nil {
		t.Fatal("expected active auto-skill input to submit normally")
	}
	if len(m.messages) != 1 || m.messages[0].Kind != MsgUser || !strings.Contains(m.messages[0].Content, "what your plan is") {
		t.Fatalf("messages = %#v", m.messages)
	}
	cmd()
	select {
	case raw := <-inputCh:
		var ui chatstate.ChatUserInput
		if err := json.Unmarshal([]byte(raw), &ui); err != nil {
			t.Fatal(err)
		}
		if !ui.IsInput || !strings.Contains(ui.Text, "remedy all the stuff") {
			t.Fatalf("queued input = %#v", ui)
		}
	default:
		t.Fatal("expected queued chat input")
	}
}

func TestChatModelAutoSkillsDoesNotBrainstormExistingPlanAudit(t *testing.T) {
	inputCh := make(chan string, 1)
	m := NewChatModel(ChatLiveConfig{
		Model:   "test",
		WorkDir: "/tmp",
		Skills: []skills.Skill{
			{Name: "brainstorming", Description: "Plan first", Body: "Brainstorm before coding."},
			{Name: "requesting-code-review", Description: "Review work", Body: "Findings first."},
		},
		AutoSkillsMode: skills.AutoSkillsAuto,
		State:          chatstate.New(),
	})
	m.inputCh = inputCh
	m.width = 100
	m.height = 24
	m.inputBuf = "forge has had many changes, did they all follow the plan, are there any gaps, whats next, figure this out and write me a nice doc"
	m.inputPos = len(m.inputBuf)

	updated, cmd := m.submitInput()
	m = updated.(ChatModel)

	if cmd == nil {
		t.Fatal("expected audit input to submit")
	}
	cmd()
	select {
	case raw := <-inputCh:
		if strings.Contains(raw, "[Skill: brainstorming]") {
			t.Fatalf("audit input should not activate brainstorming: %q", raw)
		}
		if !strings.Contains(raw, "[Skill: requesting-code-review]") {
			t.Fatalf("audit input should use review-oriented skill: %q", raw)
		}
	default:
		t.Fatal("expected queued chat input")
	}
}

func TestChatModelSlashClearVariants(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24
	m.AddMessage(ChatMessage{Kind: MsgUser, Header: "You", Content: "hello"})
	setToolsContent(&m, "tool output")

	m.inputBuf = "/clear tools"
	m.inputPos = len(m.inputBuf)
	updated, _ := m.submitInput()
	m = updated.(ChatModel)
	if m.renderedToolsBuf() != "" {
		t.Fatalf("tools = %q, want empty", m.renderedToolsBuf())
	}
	if len(m.messages) == 0 {
		t.Fatal("conversation should remain after /clear tools")
	}

	m.inputBuf = "/clear agent"
	m.inputPos = len(m.inputBuf)
	updated, _ = m.submitInput()
	m = updated.(ChatModel)
	if len(m.messages) != 0 {
		t.Fatalf("messages = %#v, want empty after /clear agent", m.messages)
	}
}

func TestChatModelSlashFindOpensOverlayAndTracksMatches(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24
	m.AddMessage(ChatMessage{Kind: MsgAgent, Header: "Agent", Content: "hello world"})
	m.AddMessage(ChatMessage{Kind: MsgAgent, Header: "Agent", Content: "another world"})

	m.inputBuf = "/find world"
	m.inputPos = len(m.inputBuf)
	updated, _ := m.submitInput()
	m = updated.(ChatModel)
	if !m.searchVisible {
		t.Fatal("expected search overlay")
	}
	if m.searchQuery != "world" {
		t.Fatalf("searchQuery = %q", m.searchQuery)
	}
	if len(m.searchMatches) == 0 {
		t.Fatal("expected search matches")
	}
	if got := m.View(); !strings.Contains(got, "Search") || !strings.Contains(got, "Query: world") {
		t.Fatalf("view missing search overlay: %s", got)
	}
}

func TestChatModelAtOpensFilePickerAndInsertsSelection(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: workDir})
	m.width = 100
	m.height = 24

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("@")})
	m = updated.(ChatModel)
	if !m.filesVisible {
		t.Fatal("expected file picker to open after typing @")
	}
	if got := m.View(); !strings.Contains(got, "Add context file (@...)") {
		t.Fatalf("view missing file picker overlay: %s", got)
	}

	updated, _ = m.handleFilePickerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("main")})
	m = updated.(ChatModel)
	updated, _ = m.handleFilePickerKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(ChatModel)

	if m.filesVisible {
		t.Fatal("expected file picker to close after selection")
	}
	if m.inputBuf != "@main.go " {
		t.Fatalf("inputBuf = %q", m.inputBuf)
	}
	if len(m.contextFiles) != 1 || m.contextFiles[0] != "main.go" {
		t.Fatalf("contextFiles = %#v", m.contextFiles)
	}
}

func TestChatModelFilePickerAcceptsExplicitRelativePath(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "pkg.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: workDir})
	m.width = 100
	m.height = 24
	m.inputBuf = "@pkg.go"
	m.inputPos = len([]rune(m.inputBuf))
	m.openFilePicker("pkg.go")

	updated, _ := m.handleFilePickerKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(ChatModel)
	if m.inputBuf != "@pkg.go " {
		t.Fatalf("inputBuf = %q", m.inputBuf)
	}
}

func TestChatModelFilesCommandBrowsesAndScrollsFilePreview(t *testing.T) {
	workDir := t.TempDir()
	lines := make([]string, 0, 40)
	for i := 1; i <= 40; i++ {
		lines = append(lines, fmt.Sprintf("line %02d", i))
	}
	if err := os.WriteFile(filepath.Join(workDir, "pkg.go"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: workDir})
	m.width = 100
	m.height = 18
	m.inputBuf = "/files pkg"
	m.inputPos = len(m.inputBuf)

	updated, _ := m.submitInput()
	m = updated.(ChatModel)
	if !m.filesVisible || !m.filesBrowser || m.filesViewing {
		t.Fatalf("expected files browser list, visible=%v browser=%v viewing=%v", m.filesVisible, m.filesBrowser, m.filesViewing)
	}
	if len(m.filesFiltered) != 1 || m.filesFiltered[0] != "pkg.go" {
		t.Fatalf("filesFiltered = %#v", m.filesFiltered)
	}
	if got := m.View(); !strings.Contains(got, "Browse workspace files") || !strings.Contains(got, "pkg.go") {
		t.Fatalf("view missing files browser overlay: %s", got)
	}

	updated, _ = m.handleFilePickerKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(ChatModel)
	if !m.filesViewing || m.filesViewPath != "pkg.go" {
		t.Fatalf("expected file preview for pkg.go, viewing=%v path=%q", m.filesViewing, m.filesViewPath)
	}
	if got := m.View(); !strings.Contains(got, "File preview") || !strings.Contains(got, "line 01") {
		t.Fatalf("view missing file preview: %s", got)
	}

	updated, _ = m.handleFilePickerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = updated.(ChatModel)
	if m.filesViewScroll != 1 {
		t.Fatalf("filesViewScroll = %d", m.filesViewScroll)
	}
}

func TestChatModelFilesCommandAddsPreviewedFileAsContext(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "pkg.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: workDir})
	m.width = 100
	m.height = 18
	m.inputBuf = "/files"
	m.inputPos = len(m.inputBuf)

	updated, _ := m.submitInput()
	m = updated.(ChatModel)
	updated, _ = m.handleFilePickerKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(ChatModel)
	updated, _ = m.handleFilePickerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("@")})
	m = updated.(ChatModel)

	if len(m.contextFiles) != 1 || m.contextFiles[0] != "pkg.go" {
		t.Fatalf("contextFiles = %#v", m.contextFiles)
	}
	if m.flash != "added context pkg.go" {
		t.Fatalf("flash = %q", m.flash)
	}
}

func TestChatModelSlashExpandIsUnknownCommand(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24
	m.AddMessage(ChatMessage{Kind: MsgAgent, Header: "Scout • 10:00:00", Content: "Source: /repo/main.go:12"})
	before := len(m.messages)

	m.inputBuf = "/expand"
	m.inputPos = len(m.inputBuf)
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	if len(m.messages) != before {
		t.Fatalf("messages changed after unknown /expand command: %#v", m.messages)
	}
	if m.flash != "unknown command: /expand" {
		t.Fatalf("flash = %q", m.flash)
	}
}

func TestChatModelToolResultsDoNotAdvertiseExpand(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24
	m.pendingSubAgentSummary = &subAgentSummary{role: "scout", turns: 1, tools: 1}

	longJSON := `{"source_file":"/repo/first.go","source_line":1,"detail":"` + strings.Repeat("x", 260) + `"}`
	updated, _ := m.Update(llm.Event{Kind: llm.EventToolResult, Agent: "delegate", Text: "Source: /repo/first.go:1", Content: longJSON})
	m = updated.(ChatModel)

	if got := m.messages[len(m.messages)-1].Content; strings.Contains(got, "/expand") {
		t.Fatalf("unexpected /expand hint in transcript message: %q", got)
	}
	if got := m.renderedToolsBuf(); strings.Contains(got, "/expand") {
		t.Fatalf("unexpected /expand hint in tools buffer: %q", got)
	}
	if got := m.lastToolResult; got != longJSON {
		t.Fatalf("lastToolResult = %q", got)
	}
}

func TestChatModelCopyCommands(t *testing.T) {
	var copied []string
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24
	m.copyFn = func(s string) error {
		copied = append(copied, s)
		return nil
	}
	m.AddMessage(ChatMessage{Kind: MsgAgent, Header: "Agent", Content: "hello"})
	m.AppendToLastAgent("\n```go\nfmt.Println(\"hi\")\n```\n")
	setToolsContent(&m, "tool output")
	m.lastToolResult = "result output"

	for _, input := range []string{"/copy agent", "/copy tools", "/copy code", "/copy result"} {
		m.inputBuf = input
		m.inputPos = len(input)
		updated, _ := m.submitInput()
		m = updated.(ChatModel)
	}

	if len(copied) != 4 {
		t.Fatalf("copied = %#v", copied)
	}
	if !strings.Contains(copied[0], "hello") {
		t.Fatalf("agent copy = %q", copied[0])
	}
	if copied[1] != "tool output" {
		t.Fatalf("tools copy = %q", copied[1])
	}
	if copied[2] != `fmt.Println("hi")` {
		t.Fatalf("code copy = %q", copied[2])
	}
	if copied[3] != "result output" {
		t.Fatalf("result copy = %q", copied[3])
	}
}

func TestChatModelCopyCommandsStripANSIEscapes(t *testing.T) {
	var copied []string
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24
	m.copyFn = func(s string) error {
		copied = append(copied, s)
		return nil
	}

	m.chatContent = "agent \x1b[31mwarning\x1b[0m and \x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\"
	setToolsContent(&m, "tools \x1b[32mok\x1b[0m")
	m.lastCodeBlock = "echo \x1b[36mhello\x1b[0m"
	m.lastToolResult = "result \x1b[33mwarn\x1b[0m"

	for _, input := range []string{"/copy agent", "/copy tools", "/copy code", "/copy result"} {
		m.inputBuf = input
		m.inputPos = len(input)
		updated, _ := m.submitInput()
		m = updated.(ChatModel)
	}

	if len(copied) != 4 {
		t.Fatalf("copied = %#v", copied)
	}
	for idx, item := range copied {
		if strings.Contains(item, "\x1b[") || strings.Contains(item, "\x1b]") {
			t.Fatalf("copy %d still contains ANSI escape sequences: %q", idx, item)
		}
	}
	if !strings.Contains(copied[0], "warning") || !strings.Contains(copied[0], "link") {
		t.Fatalf("agent copy missing expected plain text: %q", copied[0])
	}
}

func TestChatModelTabCompletesSlashCommand(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24
	m.inputBuf = "/pro"
	m.inputPos = len("/pro")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(ChatModel)

	if m.inputBuf != "/provider" {
		t.Fatalf("inputBuf = %q, want /provider", m.inputBuf)
	}
	if m.toolsVisible {
		t.Fatal("tools pane should remain hidden while slash-completing")
	}
}

func TestChatModelTabCyclesSlashCommandMatches(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24
	m.inputBuf = "/th"
	m.inputPos = len("/th")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(ChatModel)
	if m.inputBuf != "/theme" {
		t.Fatalf("first tab inputBuf = %q", m.inputBuf)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(ChatModel)
	if m.inputBuf != "/theme low" {
		t.Fatalf("second tab inputBuf = %q", m.inputBuf)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(ChatModel)
	if m.inputBuf != "/theme default" {
		t.Fatalf("third tab inputBuf = %q", m.inputBuf)
	}
}

func TestChatModelTabDoesNothingWhenNotCompletingSlashCommand(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(ChatModel)

	if m.toolsVisible {
		t.Fatal("tools pane should stay hidden when tab is not completing a slash command")
	}
}

func TestChatModelTabDoesNotExpandHiddenToolsSections(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 120
	m.height = 24
	m.paneFocus = focusTools
	m.toolsSections = []toolsSection{
		{buf: "main tool output\n"},
		{role: "scout", buf: "full scout output\n", summary: "scout summary\n", collapsed: true},
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(ChatModel)

	if !m.toolsSections[1].collapsed {
		t.Fatal("tab should not reopen hidden tools sections")
	}
}

func TestChatModelViewShowsChatScrollbarWhenOverflowing(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 16
	m.chatViewport.Width = m.chatPaneWidth()
	m.chatViewport.Height = 8

	for i := 0; i < 20; i++ {
		m.AddMessage(ChatMessage{Kind: MsgUser, Header: "You", Content: strings.Repeat("line ", 8)})
	}
	v := m.View()
	if !strings.Contains(v, "█") {
		t.Fatal("expected visible scrollbar thumb in chat pane")
	}
}

func TestChatModelSubmitKeepsTranscriptVisibleAndClearsComposer(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = updated.(ChatModel)

	m.AddMessage(ChatMessage{Kind: MsgAgent, Header: "Agent • 12:00:00", Content: "previous answer remains visible"})

	m.inputBuf = "question should stay visible"
	m.inputPos = len([]rune(m.inputBuf))
	updated, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(ChatModel)

	got := strippedLine(m.View())
	if !strings.Contains(got, "previous answer remains visible") {
		t.Fatalf("expected previous transcript to stay visible, got:\n%s", got)
	}
	if !strings.Contains(got, "question should stay visible") {
		t.Fatalf("expected same-cycle user echo in transcript, got:\n%s", got)
	}
	tail := strings.Join(strings.Split(got, "\n")[max(0, len(strings.Split(got, "\n"))-8):], "\n")
	if !strings.Contains(tail, "Ask Forge anything") {
		t.Fatalf("expected empty composer placeholder after submit, got:\n%s", got)
	}
	if strings.Contains(tail, "> question should stay visible") {
		t.Fatalf("expected submitted prompt to clear from composer after Enter, got:\n%s", got)
	}
}

func TestPromptEchoSubmitClearsComposerAndKeepsUserTurnVisible(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = updated.(ChatModel)
	m.AddMessage(ChatMessage{Kind: MsgAgent, Header: "Forge • 12:00:00", Content: "previous answer remains visible"})

	m.inputBuf = "question should stay visible"
	m.inputPos = len([]rune(m.inputBuf))
	updated, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(ChatModel)

	view := strippedLine(m.View())
	if !strings.Contains(view, "previous answer remains visible") {
		t.Fatalf("expected previous transcript to stay visible, got:\n%s", view)
	}
	if !strings.Contains(view, "question should stay visible") {
		t.Fatalf("expected submitted prompt echoed into transcript, got:\n%s", view)
	}
	tailLines := strings.Split(view, "\n")
	tail := strings.Join(tailLines[max(0, len(tailLines)-8):], "\n")
	if !strings.Contains(tail, "Ask Forge anything") {
		t.Fatalf("expected empty composer placeholder after submit, got:\n%s", view)
	}
	if strings.Contains(tail, "> question should stay visible") {
		t.Fatalf("expected submitted prompt to clear from composer after Enter, got:\n%s", view)
	}
}

func TestChatModelEnterWhileBusyQueuesSteering(t *testing.T) {
	inputCh := make(chan string, 1)
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.inputCh = inputCh
	m.width = 80
	m.height = 20
	m.busy = true
	m.inputBuf = "draft while running"
	m.inputPos = len([]rune(m.inputBuf))
	m.messages = []ChatMessage{
		{Kind: MsgUser, Header: "You • 12:00:00", Content: "first prompt"},
		{Kind: MsgAgent, Header: "Forge • 12:00:01", Content: "working on it"},
	}

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(ChatModel)

	if cmd == nil {
		t.Fatal("expected queued steering command while busy")
	}
	if got := len(m.messages); got != 2 {
		t.Fatalf("messages = %#v", m.messages)
	}
	if got := m.inputBuf; got != "" {
		t.Fatalf("inputBuf = %q", got)
	}
	msg := cmd()
	if msg != nil {
		t.Fatalf("expected nil tea.Msg from queue command, got %#v", msg)
	}
	select {
	case prompt := <-inputCh:
		var ui chatstate.ChatUserInput
		if err := json.Unmarshal([]byte(prompt), &ui); err != nil || ui.Text != "draft while running" {
			t.Fatalf("queued prompt = %q", prompt)
		}
	default:
		t.Fatal("expected queued prompt")
	}
	if !strings.Contains(m.flash, "queued steering") {
		t.Fatalf("flash = %q", m.flash)
	}
	if got := strippedLine(m.View()); !strings.Contains(got, "Queued input") || !strings.Contains(got, "draft while running") {
		t.Fatalf("expected queued input preview, got:\n%s", got)
	}
}

func TestChatModelPasteImagePathAttachesImage(t *testing.T) {
	// Create a real test PNG so DetectImageReferences and ValidateImageAttachment succeed.
	tmpDir := t.TempDir()
	pngPath := filepath.Join(tmpDir, "screenshot.png")
	img := image.NewRGBA(image.Rect(0, 0, 10, 20))
	f, err := os.Create(pngPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	_ = f.Close()

	m := NewChatModel(ChatLiveConfig{Model: "chatgpt/gpt-5.5", WorkDir: tmpDir})
	m.width = 80
	m.height = 20

	// Simulate drag-and-drop of an image path (with escaped spaces, as Terminal.app pastes)
	escaped := strings.ReplaceAll(pngPath, " ", "\\ ")
	updated, _ := m.handleKey(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune(escaped),
		Paste: true,
	})
	m = updated.(ChatModel)

	if len(m.attachments) != 1 {
		t.Fatalf("expected 1 attachment after paste, got %d", len(m.attachments))
	}
	if m.attachments[0].Name != "screenshot.png" {
		t.Errorf("attachment name = %q, want screenshot.png", m.attachments[0].Name)
	}
	if m.inputBuf != "" {
		t.Errorf("inputBuf should be empty after stripping image path, got %q", m.inputBuf)
	}
}

func TestChatModelPasteImagePathWithoutPasteFlag(t *testing.T) {
	// Create a real test PNG.
	tmpDir := t.TempDir()
	pngPath := filepath.Join(tmpDir, "photo.png")
	img := image.NewRGBA(image.Rect(0, 0, 10, 20))
	f, err := os.Create(pngPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	_ = f.Close()

	m := NewChatModel(ChatLiveConfig{Model: "chatgpt/gpt-5.5", WorkDir: tmpDir})
	m.width = 80
	m.height = 20

	// Simulate drag-and-drop without bracketed paste (some terminals)
	updated, _ := m.handleKey(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune(pngPath),
	})
	m = updated.(ChatModel)

	if len(m.attachments) != 1 {
		t.Fatalf("expected 1 attachment without paste flag, got %d", len(m.attachments))
	}
}

func TestChatModelViewUsesAiryComposer(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	m = updated.(ChatModel)

	rawLines := strings.Split(m.View(), "\n")
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		lines = append(lines, strippedLine(line))
	}
	tail := strings.Join(lines[max(0, len(lines)-8):], "\n")
	if strings.ContainsAny(tail, "╭╮╰╯│") {
		t.Fatalf("expected borderless composer, got:\n%s", tail)
	}
	if !strings.ContainsAny(tail, "─") {
		t.Fatalf("expected subtle top divider, got:\n%s", tail)
	}
	if !strings.Contains(tail, "Ask Forge anything...") {
		t.Fatalf("expected placeholder, got:\n%s", tail)
	}
}

func TestChatModelViewAddsSpacerAfterHeaderBeforeFirstMessage(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	m = updated.(ChatModel)
	m.AddMessage(ChatMessage{Kind: MsgUser, Header: "You • 12:00:00", Content: "first message"})

	lines := strings.Split(m.View(), "\n")
	messageLine := -1
	for i, line := range lines {
		if strings.Contains(strippedLine(line), "You • 12:00:00") {
			messageLine = i
			break
		}
	}
	if messageLine <= 0 {
		t.Fatalf("expected first message in view:\n%s", strippedLine(m.View()))
	}
	if strings.TrimSpace(strippedLine(lines[messageLine-1])) != "" {
		t.Fatalf("expected blank spacer above first message, got %q in:\n%s", strippedLine(lines[messageLine-1]), strippedLine(m.View()))
	}
}

func TestChatModelViewAddsSpacerBeforeComposer(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	m = updated.(ChatModel)
	m.AddMessage(ChatMessage{Kind: MsgAgent, Header: "Forge • 12:00:00", Content: "latest transcript line"})

	view := m.View()
	lines := strings.Split(view, "\n")
	promptLine := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(strippedLine(lines[i]), "Ask Forge anything...") {
			promptLine = i
			break
		}
	}
	if promptLine <= 0 {
		t.Fatalf("expected composer prompt line, got:\n%s", view)
	}
	if strings.TrimSpace(strippedLine(lines[promptLine-2])) != "" {
		t.Fatalf("expected blank spacer before composer top divider, got line %q in view:\n%s", strippedLine(lines[promptLine-2]), view)
	}
}

func TestChatModelViewKeepsBlankSpacerBeforeComposer(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	m = updated.(ChatModel)
	m.AddMessage(ChatMessage{Kind: MsgAgent, Header: "Forge • 12:00:00", Content: "latest transcript line"})

	view := m.View()
	lines := strings.Split(view, "\n")
	promptLine := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(strippedLine(lines[i]), "Ask Forge anything...") {
			promptLine = i
			break
		}
	}
	if promptLine <= 0 {
		t.Fatalf("expected composer prompt line, got:\n%s", strippedLine(view))
	}
	if strings.TrimSpace(strippedLine(lines[promptLine-2])) != "" {
		t.Fatalf("expected blank spacer before composer top divider, got %q", strippedLine(lines[promptLine-2]))
	}
}

func TestChatModelViewRendersTranscriptLinesWithoutForcedBackground(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	m = updated.(ChatModel)
	m.AddMessage(ChatMessage{Kind: MsgAgent, Header: "Forge • 12:00:00", Content: "latest transcript line"})

	view := m.View()
	lines := strings.Split(view, "\n")
	var sawTranscript bool
	for _, line := range lines {
		if strings.Contains(line, "latest transcript line") {
			sawTranscript = true
			if strings.Contains(line, ansiBackgroundFragment(m.theme().AppBG)) {
				t.Fatalf("transcript line should not force app background fill: %q", line)
			}
		}
	}
	if !sawTranscript {
		t.Fatalf("expected transcript line in view: %q", strippedLine(view))
	}
}

func TestChatModelViewHasNoPlainTrailingSpacesOnSurfaceRows(t *testing.T) {
	withTrueColorProfile(t)

	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 90, Height: 18})
	m = updated.(ChatModel)
	m.AddMessage(ChatMessage{Kind: MsgUser, Header: "You • 12:00:00", Content: "check the repo and tell me how it looks"})

	view := m.View()
	for idx, line := range strings.Split(view, "\n") {
		if strings.HasSuffix(line, " ") {
			t.Fatalf("line %d ends with plain spaces: %q", idx+1, line)
		}
	}
}

func TestChatModelViewKeepsPlainMessageSeparators(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 90, Height: 20})
	m = updated.(ChatModel)
	m.AddMessage(ChatMessage{Kind: MsgUser, Header: "You • 12:00:00", Content: "first message"})
	m.AddMessage(ChatMessage{Kind: MsgAgent, Header: "Forge • 12:00:01", Content: "second message"})

	lines := strings.Split(m.chatVisible, "\n")
	foundSeparator := false
	for _, line := range lines {
		if strings.TrimSpace(strippedLine(line)) != "" {
			continue
		}
		foundSeparator = true
		if strings.Contains(line, ansiBackgroundFragment(m.theme().AppBG)) {
			t.Fatalf("separator line should not force app background fill: %q", line)
		}
	}
	if !foundSeparator {
		t.Fatalf("expected separator line between messages in chat content:\n%s", strippedLine(m.chatVisible))
	}
}

func TestChatModelKeepsAutoscrollWhenManualModeIsAlreadyAtBottom(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = updated.(ChatModel)

	for i := 0; i < 12; i++ {
		m.AddMessage(ChatMessage{
			Kind:    MsgAgent,
			Header:  fmt.Sprintf("Forge • 12:00:%02d", i),
			Content: fmt.Sprintf("line %02d", i),
		})
	}

	totalBefore := strings.Count(m.chatVisible, "\n") + 1
	maxBefore := max(0, totalBefore-max(1, m.chatViewport.Height))
	m.followMode = followManual
	m.chatViewport.YOffset = maxBefore

	m.AddMessage(ChatMessage{
		Kind:    MsgStatus,
		Content: "Error: context canceled",
	})

	totalAfter := strings.Count(m.chatVisible, "\n") + 1
	maxAfter := max(0, totalAfter-max(1, m.chatViewport.Height))
	if m.chatViewport.YOffset != maxAfter {
		t.Fatalf("viewport yOffset = %d, want %d", m.chatViewport.YOffset, maxAfter)
	}
}

func TestProgressSlotRendersAboveComposerWhileTranscriptShowsDetails(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	m = updated.(ChatModel)
	m.AddMessage(ChatMessage{Kind: MsgAgent, Header: "Forge • 12:00:00", Content: "latest transcript line"})

	updated, _ = m.Update(llm.Event{Kind: llm.EventProgress, Agent: "scout", Text: "reading app.go"})
	m = updated.(ChatModel)

	view := m.View()
	if !strings.Contains(strippedLine(view), "latest transcript line") {
		t.Fatalf("expected transcript content to remain visible, got:\n%s", strippedLine(view))
	}
	if count := strings.Count(strippedLine(view), "reading app.go"); count != 1 {
		t.Fatalf("expected active progress to appear only once (live slot), found %d in:\n%s", count, strippedLine(view))
	}
	if !strings.Contains(strippedLine(view), "reading app.go") {
		t.Fatalf("expected live progress slot to show actual message, got:\n%s", strippedLine(view))
	}

	lines := strings.Split(view, "\n")
	promptLine := -1
	progressLine := -1
	for i, line := range lines {
		stripped := strippedLine(line)
		if strings.Contains(stripped, "Ask Forge anything") {
			promptLine = i
		}
		if strings.Contains(stripped, "reading app.go") {
			progressLine = i
		}
	}
	if promptLine <= 0 || progressLine < 0 {
		t.Fatalf("expected progress slot and composer, got:\n%s", strippedLine(view))
	}
	if progressLine != promptLine-2 {
		t.Fatalf("expected live progress directly above composer, got progress line %d prompt line %d in:\n%s", progressLine, promptLine, strippedLine(view))
	}
}

func TestProgressSlotTruncatesLongMessageToSingleLine(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 36, Height: 16})
	m = updated.(ChatModel)
	m.AddMessage(ChatMessage{Kind: MsgAgent, Header: "Forge • 12:00:00", Content: "latest transcript line"})

	longProgress := "reading this path and that path and every other path until the slot would otherwise wrap into the composer"
	updated, _ = m.Update(llm.Event{Kind: llm.EventProgress, Agent: "scout", Text: longProgress})
	m = updated.(ChatModel)

	view := m.View()
	lines := strings.Split(view, "\n")
	promptLine := -1
	for i, line := range lines {
		stripped := strippedLine(line)
		if strings.Contains(stripped, "Ask Forge anything") {
			promptLine = i
		}
	}
	if promptLine <= 0 {
		t.Fatalf("expected composer in view, got:\n%s", strippedLine(view))
	}
	progressLine := strings.TrimSpace(strippedLine(lines[promptLine-2]))
	if progressLine == "" || !strings.Contains(progressLine, "reading this path") {
		t.Fatalf("expected truncated progress directly above composer, got line %q in:\n%s", progressLine, strippedLine(view))
	}
}

func TestProgressSlotShowsSpinnerWhileBusy(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	m = updated.(ChatModel)
	m.busy = true
	m.spinnerFrame = 2
	updated, _ = m.Update(llm.Event{Kind: llm.EventProgress, Agent: "scout", Text: "reading app.go"})
	m = updated.(ChatModel)

	view := strippedLine(m.View())
	if !strings.Contains(view, "| reading app.go") {
		t.Fatalf("expected spinner-prefixed progress while busy, got:\n%s", view)
	}
}

func TestProgressSlotFallsBackToRunningStatusWhileBusy(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	m = updated.(ChatModel)
	m.busy = true
	m.status = "running"
	m.spinnerFrame = 1

	view := strippedLine(m.View())
	if !strings.Contains(view, "\\ running") {
		t.Fatalf("expected busy indicator without explicit progress event, got:\n%s", view)
	}
}

func TestDebugChatViewShowsTraceOverlayAndDebugContent(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "openai/gpt-5", WorkDir: "/tmp", DebugEnabled: true})
	m.width = 100
	m.height = 24
	m.toolsSections = []toolsSection{{buf: "tool_call read_file\nobserve complete\n"}}
	m.traceVisible = true

	view := m.View()
	if !strings.Contains(view, "Debug trace") || !strings.Contains(view, "tool_call read_file") {
		t.Fatalf("debug trace overlay missing content: %s", view)
	}
}

func TestChatModelViewKeepsHiddenToolBufferOutOfSingleColumnShell(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 120
	m.height = 16
	m.toolsVisible = true
	setToolsContent(&m, strings.Repeat("tool output line\n", 40))
	v := m.View()
	if strings.Contains(v, "tool output line") {
		t.Fatalf("expected hidden tools buffer to stay out of the main shell, got: %s", v)
	}
}

func TestChatModelAgentPaneStillRendersWhenToolsVisible(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 120
	m.height = 24
	m.toolsVisible = true
	setToolsContent(&m, "tool output")
	m.AddMessage(ChatMessage{Kind: MsgAgent, Header: "Forge", Content: "agent text should remain visible"})

	v := m.View()
	if !strings.Contains(v, "agent text should remain visible") {
		t.Fatalf("expected agent pane content to render with tools visible, got: %s", v)
	}
}

func TestChatModelToolsToggleDoesNotRevealHiddenBuffer(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = updated.(ChatModel)
	setToolsContent(&m, "tool output")
	m.AddMessage(ChatMessage{Kind: MsgAgent, Header: "Forge", Content: "agent text should remain visible after toggle"})

	m.inputBuf = "/tools"
	m.inputPos = len(m.inputBuf)
	updated, _ = m.submitInput()
	m = updated.(ChatModel)
	updated, _ = m.Update(chatTickMsg(time.Now()))
	m = updated.(ChatModel)

	v := m.View()
	if strings.Contains(v, "tool output") {
		t.Fatalf("hidden tools buffer should stay hidden after /tools, got: %s", v)
	}
}

func TestChatModelSlashSessionsOpensOverlay(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24
	if err := m.saveSession("example-session"); err != nil {
		t.Fatalf("saveSession: %v", err)
	}

	m.inputBuf = "/sessions"
	m.inputPos = len("/sessions")
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	if !m.sessionsVisible {
		t.Fatal("expected sessions overlay to be visible")
	}
	if len(m.sessionsList) < 2 || m.sessionsList[0].name != "last-session" || m.sessionsList[1].name != "example-session" {
		t.Fatalf("expected sessions picker to show last-session first and saved session next, got %#v", m.sessionsList)
	}
}

func TestChatModelSessionsOverlayRenamesSession(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24
	if err := m.saveSession("rename-me"); err != nil {
		t.Fatalf("saveSession: %v", err)
	}
	if !m.refreshSessionsPicker(true) {
		t.Fatal("expected sessions picker to load")
	}
	for i, entry := range m.sessionsList {
		if entry.name == "rename-me" {
			m.sessionsCursor = i
			break
		}
	}

	updated, _ := m.handleSessionsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = updated.(ChatModel)
	if !m.sessionRenaming {
		t.Fatal("expected rename mode")
	}
	m.sessionRenameBuf = "renamed-session"
	m.sessionRenamePos = len(m.sessionRenameBuf)
	updated, _ = m.handleSessionsKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(ChatModel)

	path, err := chatSessionFile("renamed-session")
	if err != nil {
		t.Fatalf("chatSessionFile: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected renamed session file: %v", err)
	}
}

func TestChatModelSessionsOverlayDeletesSession(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24
	if err := m.saveSession("delete-me"); err != nil {
		t.Fatalf("saveSession: %v", err)
	}
	if !m.refreshSessionsPicker(true) {
		t.Fatal("expected sessions picker to load")
	}
	for i, entry := range m.sessionsList {
		if entry.name == "delete-me" {
			m.sessionsCursor = i
			break
		}
	}

	updated, _ := m.handleSessionsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = updated.(ChatModel)

	path, err := chatSessionFile("delete-me")
	if err != nil {
		t.Fatalf("chatSessionFile: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected session to be deleted, stat err=%v", err)
	}
}

func TestChatModelSessionsOverlayMouseRestoresSession(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24
	m.inputBuf = "draft input"
	if err := m.saveSession("restore-me"); err != nil {
		t.Fatalf("saveSession: %v", err)
	}
	m.inputBuf = ""
	if !m.refreshSessionsPicker(true) {
		t.Fatal("expected sessions picker to load")
	}
	for i, entry := range m.sessionsList {
		if entry.name == "restore-me" {
			m.sessionsCursor = i
			break
		}
	}
	x0, _, _, _, listY, _, start := m.sessionsOverlayLayout()
	y := listY + (m.sessionsCursor - start)

	updated, _ := m.Update(tea.MouseMsg{X: x0 + 2, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = updated.(ChatModel)

	if m.sessionsVisible {
		t.Fatal("expected sessions overlay to close after restore")
	}
	if m.inputBuf != "draft input" {
		t.Fatalf("inputBuf = %q", m.inputBuf)
	}
}

func TestChatModelMouseClickDoesNotFocusHiddenToolsPane(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = updated.(ChatModel)
	m.toolsVisible = true
	setToolsContent(&m, strings.Repeat("tool output line\n", 20))

	x := m.chatPaneWidth() + 1
	y := 2
	updated, _ = m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = updated.(ChatModel)

	if m.paneFocus != focusChat {
		t.Fatal("expected hidden tools pane to stay unfocusable")
	}
}

func TestChatModelMouseWheelDoesNotScrollHiddenToolsPane(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 16})
	m = updated.(ChatModel)
	m.toolsVisible = true
	setToolsContent(&m, strings.Repeat("tool output line\n", 80))

	x := m.chatPaneWidth() + 1
	y := 2
	updated, _ = m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = updated.(ChatModel)
	updated, _ = m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	m = updated.(ChatModel)

	if m.toolsScroll != 0 {
		t.Fatal("expected hidden tools pane to ignore wheel scrolling")
	}
}

func TestChatModelMouseClickScrollbarDoesNotScrollHiddenToolsPane(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 16})
	m = updated.(ChatModel)
	m.toolsVisible = true
	setToolsContent(&m, strings.Repeat("tool output line\n", 80))
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 16})
	m = updated.(ChatModel)

	toolsX := m.chatPaneWidth()
	toolsW := m.width - m.chatPaneWidth()
	scrollbarX := toolsX + max(1, toolsW-2)
	clickY := m.chatViewport.Height
	updated, _ = m.Update(tea.MouseMsg{X: scrollbarX, Y: clickY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = updated.(ChatModel)

	if m.toolsScroll != 0 {
		t.Fatal("expected hidden tools pane scrollbar clicks to do nothing")
	}
}

func TestChatModelSaveAndRestoreSessionCommands(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24
	setToolsContent(&m, "tool output")
	m.AddMessage(ChatMessage{Kind: MsgUser, Header: "You", Content: "hello"})

	m.inputBuf = "/save named-session"
	m.inputPos = len(m.inputBuf)
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	path, err := chatSessionFile("named-session")
	if err != nil {
		t.Fatalf("chatSessionFile: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected saved session file at %s: %v", path, err)
	}

	m.messages = nil
	m.chatContent = ""
	m.chatViewport.SetContent("")
	setToolsContent(&m, "")
	m.inputBuf = "/restore named-session"
	m.inputPos = len(m.inputBuf)
	updated, _ = m.submitInput()
	m = updated.(ChatModel)

	if !strings.Contains(m.chatContent, "hello") {
		t.Fatal("expected restored chat content to include saved conversation")
	}
	if m.renderedToolsBuf() != "tool output" {
		t.Fatalf("expected restored tools, got %q", m.renderedToolsBuf())
	}
}

func TestChatModelRestoreSessionRebuildsTranscriptState(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24
	m.AddMessage(ChatMessage{Kind: MsgUser, Header: "You", Content: "saved hello"})

	m.inputBuf = "/save named-session"
	m.inputPos = len(m.inputBuf)
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	m.AddMessage(ChatMessage{Kind: MsgStatus, Content: "Error: stale"})
	updated, _ = m.Update(llm.Event{Kind: llm.EventProgress, Agent: "scout", Text: "stale progress"})
	m = updated.(ChatModel)
	if m.liveProgress.IsZero() {
		t.Fatal("expected stale progress before restore")
	}

	m.inputBuf = "/restore named-session"
	m.inputPos = len(m.inputBuf)
	updated, _ = m.submitInput()
	m = updated.(ChatModel)

	if !m.liveProgress.IsZero() {
		t.Fatalf("expected restore to clear live progress, got %#v", m.liveProgress)
	}
	for _, msg := range m.messages {
		if msg.Kind == MsgWorking {
			t.Fatalf("expected restore to remove working rows, got %#v", m.messages)
		}
	}
	if len(m.records) != 1 {
		t.Fatalf("expected restore to rebuild transcript records, got %#v", m.records)
	}
	if m.records[0].Kind != RecordSystem {
		t.Fatalf("expected restored record kind to match restored message, got %#v", m.records[0])
	}
	if len(m.records[0].Segments) != 1 || !strings.Contains(m.records[0].Segments[0].Text, "saved hello") || strings.Contains(m.records[0].Segments[0].Text, "stale") {
		t.Fatalf("unexpected restored record segments: %#v", m.records[0].Segments)
	}
	if m.nextRecordSeq != len(m.records) {
		t.Fatalf("expected record sequence to match rebuilt transcript, seq=%d records=%d", m.nextRecordSeq, len(m.records))
	}

	m.AddMessage(ChatMessage{Kind: MsgUser, Header: "You", Content: "after restore"})
	if got := m.records[len(m.records)-1].ID; got != "record-2" {
		t.Fatalf("expected next record id after restore to continue rebuilt transcript, got %q", got)
	}
}

func TestNormalizeRuntimeProgressMessageUsesNaturalPhrasing(t *testing.T) {
	got, ok := normalizeRuntimeProgressMessage("react runtime: executing turn 3")
	if !ok {
		t.Fatal("expected react runtime progress message to normalize")
	}
	if got != "Starting analysis pass 3" {
		t.Fatalf("normalized progress = %q", got)
	}
}

func TestToolResultProgressLineSuppressesNonErrors(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	got := m.toolResultProgressLine(llm.Event{Kind: llm.EventToolResult, Agent: "forge"})
	if got != "" {
		t.Fatalf("progress line = %q", got)
	}
}

func TestToolResultProgressLineKeepsErrors(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	got := m.toolResultProgressLine(llm.Event{Kind: llm.EventToolResult, Agent: "forge", IsError: true, Text: "permission denied"})
	if got == "" || !strings.Contains(got, "hit an issue") {
		t.Fatalf("progress line = %q", got)
	}
}

func TestToolCallProgressLineDescribesExecSessionStart(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	got := m.toolCallProgressLine(llm.Event{Kind: llm.EventToolCall, Agent: "exec_session_start", Text: "npm run dev"})
	if got != "Starting terminal session: npm run dev" {
		t.Fatalf("progress line = %q", got)
	}
}
func TestChatModelToolCallCheckpointIncludesSemanticToolName(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24

	updated, _ := m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "lsp_definition", Text: "main.go:12:4"})
	m = updated.(ChatModel)

	for _, msg := range m.messages {
		if msg.Kind == MsgStatus && strings.Contains(msg.Content, "• lsp_definition") && strings.Contains(msg.Content, "main.go:12:4") {
			return
		}
	}
	t.Fatalf("expected semantic tool checkpoint in transcript, got %#v", m.messages)
}

func TestChatModelShowsTaskContextOnTaskContextEvent(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 90
	m.height = 28

	updated, _ := m.Update(llm.Event{
		Kind:  llm.EventToolResult,
		Agent: "__task_context",
		Text:  "Objective: merge the feature branch\nVerify: branch main contains HEAD",
	})
	m = updated.(ChatModel)

	var found bool
	for _, msg := range m.messages {
		if msg.Kind == MsgForge && strings.TrimSpace(msg.Header) == "Task" &&
			strings.Contains(msg.Content, "Objective:") && strings.Contains(msg.Content, "Verify:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected Task message in main pane, got %#v", m.messages)
	}
	if got := strippedLine(m.View()); !strings.Contains(got, "Objective:") {
		t.Fatalf("expected task context visible in main pane, got:\n%s", got)
	}
}

func TestChatModelUpsertsTaskContextOnRepeatEvent(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 90
	m.height = 28

	updated, _ := m.Update(llm.Event{Kind: llm.EventToolResult, Agent: "__task_context", Text: "Objective: first"})
	m = updated.(ChatModel)
	updated, _ = m.Update(llm.Event{Kind: llm.EventToolResult, Agent: "__task_context", Text: "Objective: updated"})
	m = updated.(ChatModel)

	taskCount := 0
	for _, msg := range m.messages {
		if msg.Kind == MsgForge && strings.TrimSpace(msg.Header) == "Task" {
			taskCount++
		}
	}
	if taskCount != 1 {
		t.Fatalf("expected exactly one Task message (upserted), got %d, messages=%#v", taskCount, m.messages)
	}
}

func TestChatModelShowsValidationStatusOnValidationEvent(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 90
	m.height = 28

	updated, _ := m.Update(llm.Event{
		Kind:    llm.EventToolResult,
		Agent:   "__validation",
		Text:    "validation passed: go test ./...",
		IsError: false,
	})
	m = updated.(ChatModel)

	var found bool
	for _, msg := range m.messages {
		if msg.Kind == MsgStatus && strings.Contains(msg.Content, "validation passed") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected validation status message, got %#v", m.messages)
	}
}

func TestChatModelValidationEventDoesNotUpdateLastToolResult(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 90
	m.height = 28

	updated, _ := m.Update(llm.Event{Kind: llm.EventToolResult, Agent: "run_command", Text: "real output", Content: "real output"})
	m = updated.(ChatModel)
	updated, _ = m.Update(llm.Event{Kind: llm.EventToolResult, Agent: "__validation", Text: "validation passed: go test ./..."})
	m = updated.(ChatModel)

	if m.lastToolResult != "real output" {
		t.Fatalf("lastToolResult should not be overwritten by __validation event, got %q", m.lastToolResult)
	}
}
