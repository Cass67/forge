package agent

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"forge/internal/agent/tools"
	"forge/internal/llm"
	"forge/internal/skills"
)

// mockDriver returns predefined responses in sequence.
type mockDriver struct {
	responses []string
	callIdx   int
}

func (d *mockDriver) Name() string { return "mock" }

func (d *mockDriver) Stream(ctx context.Context, messages []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	if d.callIdx >= len(d.responses) {
		out <- llm.Token{Text: "done"}
		return nil
	}
	resp := d.responses[d.callIdx]
	d.callIdx++
	out <- llm.Token{Text: resp}
	return nil
}

func TestAgentRunNoTools(t *testing.T) {
	driver := &mockDriver{responses: []string{"Hello! I can help with that."}}
	reg := tools.NewRegistry()
	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)

	agent := NewAgent(driver, reg, YoloApproval(), "/tmp", 10, renderer, nil, nil)
	err := agent.Run(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Hello") {
		t.Errorf("output = %q", output.String())
	}
}

func TestAgentRunWithToolCall(t *testing.T) {
	dir := t.TempDir()
	driver := &mockDriver{responses: []string{
		"Let me read the file.\n\n<tool_call>\n{\"name\": \"list_dir\", \"args\": {}}\n</tool_call>",
		"I see the directory listing. All done.",
	}}

	reg := tools.NewRegistry()
	reg.Register(tools.NewListDir(dir, nil))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)

	agent := NewAgent(driver, reg, YoloApproval(), dir, 10, renderer, nil, nil)
	err := agent.Run(context.Background(), "list files")
	if err != nil {
		t.Fatal(err)
	}
}

func TestAgentMaxTurns(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"list_dir\", \"args\": {}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"list_dir\", \"args\": {}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"list_dir\", \"args\": {}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"list_dir\", \"args\": {}}\n</tool_call>",
	}}
	reg := tools.NewRegistry()
	reg.Register(tools.NewListDir(t.TempDir(), nil))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)

	agent := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 3, renderer, nil, nil)
	err := agent.Run(context.Background(), "loop forever")
	if err != nil && !strings.Contains(err.Error(), "max turns") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCompressHistory(t *testing.T) {
	a := &Agent{maxTurns: 50}

	for i := 0; i < 20; i++ {
		a.history = append(a.history, llm.Message{
			Role:    llm.RoleAssistant,
			Content: fmt.Sprintf("<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"file%d.go\"}}\n</tool_call>", i),
		})
		a.history = append(a.history, llm.Message{
			Role:    llm.RoleUser,
			Content: "Tool results:\n\n[read_file] " + strings.Repeat("x", 5000),
		})
	}

	a.compressHistory(50000)

	totalLen := 0
	for _, m := range a.history {
		totalLen += len(m.Content)
	}
	if totalLen > 55000 {
		t.Errorf("compressed history too large: %d chars", totalLen)
	}
}

func TestEstimateTokens(t *testing.T) {
	if got := estimateTokens("hello world"); got < 2 || got > 4 {
		t.Errorf("estimateTokens('hello world') = %d", got)
	}
}

func TestAgentRunRequiresSkillActivation(t *testing.T) {
	driver := &mockDriver{responses: []string{"ok"}}
	reg := tools.NewRegistry()
	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	loaded := []skills.Skill{{Name: "brainstorming", Description: "Planning", Body: "Plan first."}}

	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, loaded, nil)
	err := a.Run(context.Background(), "please plan the architecture first")
	if err == nil {
		t.Fatal("expected missing skill activation to fail")
	}
	if !strings.Contains(err.Error(), "/brainstorming") {
		t.Fatalf("err = %v, want activation guidance", err)
	}
}

func TestAgentRunAllowsActivatedSkill(t *testing.T) {
	driver := &mockDriver{responses: []string{"planned"}}
	reg := tools.NewRegistry()
	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	s := skills.Skill{Name: "brainstorming", Description: "Planning", Body: "Plan first."}

	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, []skills.Skill{s}, nil)
	a.InjectSkill(s)
	err := a.Run(context.Background(), "please plan the architecture first")
	if err != nil {
		t.Fatal(err)
	}
}

func TestAgentClearHistoryResetsActivatedSkills(t *testing.T) {
	driver := &mockDriver{responses: []string{"planned"}}
	reg := tools.NewRegistry()
	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	s := skills.Skill{Name: "brainstorming", Description: "Planning", Body: "Plan first."}

	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, []skills.Skill{s}, nil)
	a.InjectSkill(s)
	a.ClearHistory()

	err := a.Run(context.Background(), "please plan the architecture first")
	if err == nil {
		t.Fatal("expected missing skill activation after clear history")
	}
	if !strings.Contains(err.Error(), "/brainstorming") {
		t.Fatalf("err = %v, want activation guidance", err)
	}
}
