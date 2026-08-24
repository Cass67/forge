package runtime

import (
	"strings"
	"testing"
)

// The GUI has no event log, so a dropped model used to leave it with an empty
// model pill and no explanation. The notice is built once and read by both
// surfaces.
func TestStartupNoticeNamesTheDroppedModel(t *testing.T) {
	if got := (&ChatSetup{}).startupNotice(); got != "" {
		t.Fatalf("no dropped model should mean no notice, got %q", got)
	}

	notice := (&ChatSetup{DroppedModel: "localllm/qwen"}).startupNotice()
	if !strings.Contains(notice, "localllm/qwen") {
		t.Fatalf("notice does not name the model: %q", notice)
	}
}
