package summarizer_test

import (
    "context"
    "testing"
    "forge/internal/llm"
    "forge/internal/summarizer"
)

type mockDriver struct {
    response string
}

func (m *mockDriver) Name() string { return "mock" }
func (m *mockDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
    out <- llm.Token{Text: m.response}
    out <- llm.Token{Done: true}
    close(out)
    return nil
}

func TestSummarizeRound(t *testing.T) {
    d := &mockDriver{response: "**Writer:** built it\n**Auditor:** looks ok\n**Decisions:** used x\n**Outstanding:** none"}
    agent := summarizer.NewAgent(d, "system prompt")

    result, err := agent.SummarizeRound(context.Background(), "writer text", "auditor text")
    if err != nil {
        t.Fatal(err)
    }
    if result == "" {
        t.Error("expected non-empty result")
    }
}
