package drivers

import (
	"context"
	"encoding/json"
	"testing"

	"forge/internal/llm"

	"github.com/openai/openai-go/responses"
)

// Providers send finished items twice — once on output_item.done and again in
// the response.completed output array. Emitting both ran every tool twice.
func TestResponsesFunctionCallsAreEmittedOnce(t *testing.T) {
	raw := `{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"a.go\"}","status":"completed"}`
	var item responses.ResponseOutputItemUnion
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		t.Fatal(err)
	}

	out := make(chan llm.Token, 8)
	if err := emitResponsesFunctionCalls(context.Background(), out, []responses.ResponseOutputItemUnion{item, item}); err != nil {
		t.Fatal(err)
	}
	close(out)

	var calls []llm.NativeToolCall
	for tok := range out {
		if tok.ToolCall != nil {
			calls = append(calls, *tok.ToolCall)
		}
	}
	if len(calls) != 1 {
		t.Fatalf("emitted %d tool calls, want 1: %+v", len(calls), calls)
	}
	if calls[0].Name != "read_file" || calls[0].ID != "call_1" {
		t.Fatalf("call = %+v, want read_file/call_1", calls[0])
	}
}
