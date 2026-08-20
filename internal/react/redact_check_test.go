package react

import (
	"strings"
	"testing"

	"forge/internal/llm"
)

func TestToolCallArgsStillRedactedAfterDerivation(t *testing.T) {
	s := NewSession()
	s.RecordInput("go")
	secret := "TOKEN=" + strings.Repeat("x", 24)
	if err := s.AppendAssistantToolTurn("", []llm.NativeToolCall{
		{ID: "c1", Name: "run_command", ArgsJSON: `{"command":"echo ` + secret + `"}`},
	}); err != nil {
		t.Fatal(err)
	}
	for _, msg := range s.Snapshot().History {
		for _, call := range msg.ToolCalls {
			if strings.Contains(call.ArgsJSON, secret) {
				t.Fatalf("secret survived in derived history: %s", call.ArgsJSON)
			}
			if !strings.Contains(call.ArgsJSON, "REDACTED") {
				t.Fatalf("args not redacted: %s", call.ArgsJSON)
			}
		}
	}
}
