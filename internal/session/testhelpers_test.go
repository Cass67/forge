package session_test

import (
    "context"
    "forge/internal/llm"
)

type mockDriver struct{ resp string }

func (m *mockDriver) Name() string { return "mock" }
func (m *mockDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
    out <- llm.Token{Text: m.resp}
    out <- llm.Token{Done: true}
    close(out)
    return nil
}
