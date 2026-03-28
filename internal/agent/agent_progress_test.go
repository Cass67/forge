package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"forge/internal/agent/tools"
	"forge/internal/llm"
)

type delayedProgressDriver struct {
	delay    time.Duration
	response string
}

func (d *delayedProgressDriver) Name() string { return "delayed-progress" }

func (d *delayedProgressDriver) Stream(ctx context.Context, _ []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	timer := time.NewTimer(d.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	out <- llm.Token{Text: d.response}
	return nil
}

func TestStrictActionEmitsUnpromptedProgressWhileWaitingForModel(t *testing.T) {
	previousInterval := strictActionProgressHeartbeatInterval
	strictActionProgressHeartbeatInterval = 10 * time.Millisecond
	t.Cleanup(func() {
		strictActionProgressHeartbeatInterval = previousInterval
	})

	driver := &delayedProgressDriver{
		delay:    40 * time.Millisecond,
		response: "Done.",
	}
	reg := tools.NewRegistry()
	events := make(chan llm.Event, 256)
	renderer := NewEventRenderer(events)

	agent := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 6, renderer, nil, nil)
	agent.SetRole("strictlocal")

	if err := agent.Run(context.Background(), "summarize the request and continue"); err != nil {
		t.Fatal(err)
	}

	var progressLines []string
	for len(events) > 0 {
		ev := <-events
		if ev.Kind == llm.EventProgress {
			progressLines = append(progressLines, strings.ToLower(strings.TrimSpace(ev.Text)))
		}
	}

	if len(progressLines) == 0 {
		t.Fatalf("expected strict action progress updates, got none")
	}
	var sawWaitingHeartbeat bool
	for _, line := range progressLines {
		if strings.Contains(line, "still waiting") {
			sawWaitingHeartbeat = true
			break
		}
	}
	if !sawWaitingHeartbeat {
		t.Fatalf("expected unprompted waiting heartbeat progress update, got %#v", progressLines)
	}
}

func TestGeneralTurnEmitsUnpromptedProgressWhileWaitingForModel(t *testing.T) {
	previousInterval := generalProgressHeartbeatInterval
	generalProgressHeartbeatInterval = 10 * time.Millisecond
	t.Cleanup(func() {
		generalProgressHeartbeatInterval = previousInterval
	})

	driver := &delayedProgressDriver{
		delay:    40 * time.Millisecond,
		response: "Done.",
	}
	reg := tools.NewRegistry()
	events := make(chan llm.Event, 256)
	renderer := NewEventRenderer(events)

	agent := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 6, renderer, nil, nil)

	if err := agent.Run(context.Background(), "summarize the request and continue"); err != nil {
		t.Fatal(err)
	}

	var progressLines []string
	for len(events) > 0 {
		ev := <-events
		if ev.Kind == llm.EventProgress {
			progressLines = append(progressLines, strings.ToLower(strings.TrimSpace(ev.Text)))
		}
	}

	if len(progressLines) == 0 {
		t.Fatalf("expected general turn progress updates, got none")
	}
	var sawNaturalHeartbeat bool
	for _, line := range progressLines {
		if strings.Contains(line, "i am ") {
			t.Fatalf("expected non-robotic phrasing, got %#v", progressLines)
		}
		if strings.Contains(line, "review") || strings.Contains(line, "synthes") || strings.Contains(line, "analy") {
			sawNaturalHeartbeat = true
		}
	}
	if !sawNaturalHeartbeat {
		t.Fatalf("expected non-strict waiting heartbeat progress update, got %#v", progressLines)
	}
}

func TestGeneralTurnContextHintListDirDotFallsBack(t *testing.T) {
	hint := generalTurnContextHint([]ToolCall{{
		Name: "list_dir",
		Args: map[string]any{"path": "."},
	}})
	if hint != "the repository scan results" {
		t.Fatalf("hint = %q", hint)
	}
}
