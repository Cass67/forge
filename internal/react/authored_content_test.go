package react

import (
	"strings"
	"testing"

	"forge/internal/llm"
)

// A write large enough to be truncated became a file the model could not
// rewrite: its own past call came back with the body replaced by a marker,
// and validation rejects any new call carrying that marker.
func TestAuthoredContentSurvivesHistoryTruncation(t *testing.T) {
	body := strings.Repeat("line of source\n", 500) // ~7.5k, well past the hard limit
	for _, tool := range []string{"write_file", "edit_file", "apply_patch"} {
		msgs := []llm.Message{{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.NativeToolCall{{
				ID: "c1", Name: tool,
				ArgsJSON: `{"path":"main.go","content":` + quote(body) + `}`,
			}},
		}}
		got := truncateAssistantToolCalls(msgs)
		args := got[0].ToolCalls[0].ArgsJSON
		if strings.Contains(args, "omitted") {
			t.Errorf("%s content was truncated: %.80s", tool, args)
		}
		if !strings.Contains(args, "line of source") {
			t.Errorf("%s content lost", tool)
		}
	}
}

// Tools whose arguments are not authored content still get truncated, so the
// context saving is kept where it does not trap the model.
func TestNonAuthoredToolArgsStillTruncated(t *testing.T) {
	body := strings.Repeat("payload\n", 800)
	msgs := []llm.Message{{
		Role: llm.RoleAssistant,
		ToolCalls: []llm.NativeToolCall{{
			ID: "c1", Name: "web_fetch",
			ArgsJSON: `{"url":"https://example.com","body":` + quote(body) + `}`,
		}},
	}}
	got := truncateAssistantToolCalls(msgs)
	if !strings.Contains(got[0].ToolCalls[0].ArgsJSON, "omitted") {
		t.Fatal("non-authored bulky argument was not truncated")
	}
}

func quote(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `"`, `\"`), "\n", `\n`) + `"`
}
