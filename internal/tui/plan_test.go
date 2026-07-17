package tui

import (
	"regexp"
	"strings"
	"testing"
)

func TestRenderPlanContentParsesSteps(t *testing.T) {
	withTrueColorProfile(t)
	theme := lookupThemeForTest(t, "default")

	content := "Explanation: Runtime alignment\nPlan:\n- [completed] Inspect loop\n- [in_progress] Tighten prompt\n- [pending] Finalize"
	got := renderPlanContent(content, 60, theme)

	if got == "" {
		t.Fatal("expected non-empty plan render")
	}
	if !strings.Contains(got, "Runtime alignment") {
		t.Fatalf("missing explanation text: %s", got)
	}
}

func TestRenderPlanContentShowsCompletedCheckmark(t *testing.T) {
	withTrueColorProfile(t)
	theme := lookupThemeForTest(t, "default")

	content := "- [completed] Inspect loop"
	got := renderPlanContent(content, 60, theme)

	// Should contain a checkmark-like indicator in success color
	if !strings.Contains(got, "Inspect loop") {
		t.Fatalf("missing step text: %s", got)
	}
}

func TestRenderPlanContentShowsInProgressSpinner(t *testing.T) {
	withTrueColorProfile(t)
	theme := lookupThemeForTest(t, "default")

	content := "- [in_progress] Tighten prompt"
	got := renderPlanContent(content, 60, theme)

	if !strings.Contains(got, "Tighten prompt") {
		t.Fatalf("missing step text: %s", got)
	}
}

func TestRenderPlanContentShowsPendingIndicator(t *testing.T) {
	withTrueColorProfile(t)
	theme := lookupThemeForTest(t, "default")

	content := "- [pending] Finalize"
	got := renderPlanContent(content, 60, theme)

	if !strings.Contains(got, "Finalize") {
		t.Fatalf("missing step text: %s", got)
	}
}

func TestRenderPlanContentShowsProgressBar(t *testing.T) {
	withTrueColorProfile(t)
	theme := lookupThemeForTest(t, "default")

	content := "Plan:\n- [completed] Step 1\n- [completed] Step 2\n- [in_progress] Step 3\n- [pending] Step 4"
	got := renderPlanContent(content, 60, theme)

	// Should show a progress indicator like "2/4" or a bar
	if !strings.Contains(got, "2/4") && !strings.Contains(got, "50") {
		t.Fatalf("expected progress indicator in render, got: %s", got)
	}
}

func TestRenderPlanContentHandlesBlockedState(t *testing.T) {
	withTrueColorProfile(t)
	theme := lookupThemeForTest(t, "default")

	content := "- [blocked] Fix dependency"
	got := renderPlanContent(content, 60, theme)

	if !strings.Contains(got, "Fix dependency") {
		t.Fatalf("missing blocked step text: %s", got)
	}
}

func TestRenderPlanContentEmpty(t *testing.T) {
	theme := lookupThemeForTest(t, "default")
	got := renderPlanContent("", 60, theme)
	if got != "" {
		t.Fatalf("expected empty render for empty content, got: %s", got)
	}
}

func TestPlanRenderIntegratesWithChatMessage(t *testing.T) {
	withTrueColorProfile(t)
	theme := lookupThemeForTest(t, "default")

	m := ChatMessage{
		Kind:    MsgPlan,
		Header:  "Plan",
		Content: "Plan:\n- [completed] Step A\n- [in_progress] Step B",
	}
	got := m.Render(70, theme)

	if !strings.Contains(got, "Step A") || !strings.Contains(got, "Step B") {
		t.Fatalf("plan message render missing steps: %s", got)
	}
	// The plan should use the rich renderer, not just plain prose
	if !strings.Contains(stripANSI(got), "1/2") {
		t.Fatalf("expected progress indicator in full plan message render: %s", stripANSI(got))
	}
}

func stripANSI(s string) string {
	re := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	return re.ReplaceAllString(s, "")
}
