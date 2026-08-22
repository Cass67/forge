package drivers

import (
	"testing"

	"forge/internal/llm"
)

// A history rewritten only in ContentParts or ReasoningContent is not
// append-only: sending just the delta would replay it against a server-side
// conversation that no longer matches.
func TestAppendOnlyHistoryDetectsNonContentRewrites(t *testing.T) {
	base := []llm.Message{{
		Role:    llm.RoleUser,
		Content: "look",
		ContentParts: []llm.MessageContentPart{
			{Type: "image", Image: &llm.ImageContent{Path: "a.png", MIMEType: "image/png"}},
		},
	}}

	same := []llm.Message{base[0], {Role: llm.RoleAssistant, Content: "ok"}}
	if !isAppendOnlyMessageHistory(base, same) {
		t.Fatal("appending a turn must stay append-only")
	}

	swappedImage := []llm.Message{{
		Role:    llm.RoleUser,
		Content: "look",
		ContentParts: []llm.MessageContentPart{
			{Type: "image", Image: &llm.ImageContent{Path: "b.png", MIMEType: "image/png"}},
		},
	}}
	if isAppendOnlyMessageHistory(base, swappedImage) {
		t.Fatal("a swapped image must not count as append-only")
	}

	trimmedReasoning := []llm.Message{{Role: llm.RoleUser, Content: "look", ReasoningContent: "gone"}}
	if isAppendOnlyMessageHistory([]llm.Message{{Role: llm.RoleUser, Content: "look"}}, trimmedReasoning) {
		t.Fatal("changed reasoning must not count as append-only")
	}
}
