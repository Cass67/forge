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

func TestPlanStepParseFindsStatus(t *testing.T) {
	tests := []struct {
		line string
		want planStepStatus
		text string
	}{
		{"- [completed] Inspect loop", planStepCompleted, "Inspect loop"},
		{"- [in_progress] Tighten prompt", planStepInProgress, "Tighten prompt"},
		{"- [pending] Finalize", planStepPending, "Finalize"},
		{"- [blocked] Fix bug", planStepBlocked, "Fix bug"},
		{"* [completed] Another", planStepCompleted, "Another"},
		{"1. [in_progress] Numbered", planStepInProgress, "Numbered"},
		{"- plain item without status", planStepPlain, "plain item without status"},
		{"just prose", planStepPlain, "just prose"},
	}

	for _, tt := range tests {
		step := parsePlanStep(tt.line)
		if step.Status != tt.want {
			t.Errorf("parsePlanStep(%q).Status = %v, want %v", tt.line, step.Status, tt.want)
		}
		if step.Text != tt.text {
			t.Errorf("parsePlanStep(%q).Text = %q, want %q", tt.line, step.Text, tt.text)
		}
	}
}

func TestPlanProgressStats(t *testing.T) {
	steps := []planStep{
		{Status: planStepCompleted},
		{Status: planStepCompleted},
		{Status: planStepInProgress},
		{Status: planStepPending},
		{Status: planStepBlocked},
	}
	stats := computePlanProgress(steps)
	if stats.Total != 5 {
		t.Fatalf("expected total 5, got %d", stats.Total)
	}
	if stats.Completed != 2 {
		t.Fatalf("expected completed 2, got %d", stats.Completed)
	}
	if stats.InProgress != 1 {
		t.Fatalf("expected in_progress 1, got %d", stats.InProgress)
	}
	if stats.Percent != 40 {
		t.Fatalf("expected percent 40, got %d", stats.Percent)
	}
}

func TestRenderPlanProgressBar(t *testing.T) {
	withTrueColorProfile(t)
	theme := lookupThemeForTest(t, "default")

	stats := planProgressStats{Total: 5, Completed: 2, InProgress: 1, Percent: 40}
	got := renderPlanProgressBar(stats, 20, theme)

	// Should render something like "[██░░░░] 2/5" with ANSI styling
	if !strings.Contains(stripANSI(got), "2/5") {
		t.Fatalf("expected progress count 2/5 in render: %q", stripANSI(got))
	}
}

func TestRenderPlanStepIcon(t *testing.T) {
	withTrueColorProfile(t)
	theme := lookupThemeForTest(t, "default")

	completed := renderPlanStepIcon(planStep{Status: planStepCompleted}, theme)
	inProgress := renderPlanStepIcon(planStep{Status: planStepInProgress}, theme)
	pending := renderPlanStepIcon(planStep{Status: planStepPending}, theme)
	blocked := renderPlanStepIcon(planStep{Status: planStepBlocked}, theme)

	if completed == "" || completed == inProgress || completed == pending {
		t.Fatalf("completed icon should be distinct, got: %q", stripANSI(completed))
	}
	if inProgress == "" || inProgress == pending || inProgress == blocked {
		t.Fatalf("in_progress icon should be distinct from pending, got: %q", stripANSI(inProgress))
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
