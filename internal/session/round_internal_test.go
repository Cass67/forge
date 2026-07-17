package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forge/internal/llm"
	"forge/internal/logger"
)

// stallDriver fails mid-stream with a transient error until failures is
// exhausted, then streams resp successfully.
type stallDriver struct {
	failures int
	calls    int
	resp     string
}

func (d *stallDriver) Name() string { return "stall" }
func (d *stallDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	d.calls++
	if d.calls <= d.failures {
		out <- llm.Token{Text: "partial "}
		close(out)
		return errors.New("stream idle timeout after 2m0s")
	}
	out <- llm.Token{Text: d.resp}
	out <- llm.Token{Done: true}
	close(out)
	return nil
}

func TestStreamAgentRetriesMidStreamStall(t *testing.T) {
	events := make(chan llm.Event, 64)
	driver := &stallDriver{failures: 2, resp: "full response"}
	r := &Round{writer: driver, events: events, log: logger.Nop()}

	got, err := r.streamAgent(context.Background(), "writer", "sys", "user", 1, 1)
	if err != nil {
		t.Fatalf("streamAgent: %v", err)
	}
	if got != "full response" {
		t.Fatalf("got %q, want %q", got, "full response")
	}
	if driver.calls != 3 {
		t.Fatalf("driver calls = %d, want 3", driver.calls)
	}

	close(events)
	warnings := 0
	for ev := range events {
		if ev.Kind == llm.EventWarning {
			warnings++
		}
	}
	if warnings != 2 {
		t.Fatalf("warning events = %d, want 2", warnings)
	}
}

func TestStreamAgentGivesUpAfterMaxAttempts(t *testing.T) {
	events := make(chan llm.Event, 64)
	driver := &stallDriver{failures: 99}
	r := &Round{writer: driver, events: events, log: logger.Nop()}

	_, err := r.streamAgent(context.Background(), "writer", "sys", "user", 1, 1)
	if err == nil {
		t.Fatal("expected error after exhausting attempts")
	}
	if !strings.Contains(err.Error(), "attempts failed") {
		t.Fatalf("error should mark attempts exhausted, got: %v", err)
	}
	if driver.calls != 3 {
		t.Fatalf("driver calls = %d, want 3", driver.calls)
	}
}

func TestStreamAgentDoesNotRetryNonRetryableError(t *testing.T) {
	events := make(chan llm.Event, 64)
	driver := &fatalDriver{}
	r := &Round{writer: driver, events: events, log: logger.Nop()}

	_, err := r.streamAgent(context.Background(), "writer", "sys", "user", 1, 1)
	if err == nil {
		t.Fatal("expected error")
	}
	if driver.calls != 1 {
		t.Fatalf("driver calls = %d, want 1 (no retry on non-retryable)", driver.calls)
	}
}

type fatalDriver struct{ calls int }

func (d *fatalDriver) Name() string { return "fatal" }
func (d *fatalDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	d.calls++
	close(out)
	return errors.New("401 unauthorized: invalid api key")
}

func TestBuildLanguageGuidance(t *testing.T) {
	tests := []struct {
		name         string
		hint         string
		inlinedCode  string
		wantContains string
	}{
		{
			name:         "auto without code chooses best language",
			hint:         "auto",
			wantContains: "Choose the best language",
		},
		{
			name:         "auto with code matches existing language",
			hint:         "auto",
			inlinedCode:  "```py:main.py\nprint('hi')\n```",
			wantContains: "Match the language, framework, and project conventions",
		},
		{
			name:         "explicit hint is preserved",
			hint:         "python",
			wantContains: "Use python unless the existing codebase shown below makes that unsafe or incompatible.",
		},
	}

	for _, tt := range tests {
		got := buildLanguageGuidance(tt.hint, tt.inlinedCode)
		if !strings.Contains(got, tt.wantContains) {
			t.Fatalf("%s: got %q, want substring %q", tt.name, got, tt.wantContains)
		}
	}
}

func TestBuildUserContentIncludesLanguageGuidance(t *testing.T) {
	got := buildUserContent("build a cli", "auto", "", "", "", "")
	if !strings.Contains(got, "LANGUAGE GUIDANCE:\nChoose the best language") {
		t.Fatalf("expected language guidance in user content, got %q", got)
	}
}

func TestMirrorCodeFiles(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "index.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "assets", "app.js"), []byte("js"), 0o644); err != nil {
		t.Fatal(err)
	}

	copied, err := mirrorCodeFiles(src, dst)
	if err != nil {
		t.Fatalf("mirrorCodeFiles: %v", err)
	}
	if len(copied) != 2 {
		t.Fatalf("copied = %v, want 2 files", copied)
	}
	for _, rel := range []string{"index.html", "assets/app.js"} {
		if _, err := os.Stat(filepath.Join(dst, rel)); err != nil {
			t.Fatalf("missing mirrored file %s: %v", rel, err)
		}
	}

	if copied, err := mirrorCodeFiles(src, ""); err != nil || copied != nil {
		t.Fatalf("empty dst should be a no-op, got %v, %v", copied, err)
	}
}
