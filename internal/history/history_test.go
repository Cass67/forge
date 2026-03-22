package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"forge/internal/output"
)

func writeSessionJSON(t *testing.T, dir string, meta output.SessionMeta) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListEmpty(t *testing.T) {
	dir := t.TempDir()
	sessions, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestListNonexistentDir(t *testing.T) {
	sessions, err := List("/tmp/forge-test-nonexistent-dir-xyz")
	if err != nil {
		t.Fatal(err)
	}
	if sessions != nil {
		t.Fatalf("expected nil, got %v", sessions)
	}
}

func TestListSessions(t *testing.T) {
	base := t.TempDir()

	writeSessionJSON(t, filepath.Join(base, "2025-01-01T10-00-00"), output.SessionMeta{
		ID:      "2025-01-01T10-00-00",
		Prompt:  "first session",
		Writer:  "claude-3-7-sonnet-latest",
		Auditor: "gpt-4o",
		Status:  "complete",
		StartedAt: time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC),
	})
	writeSessionJSON(t, filepath.Join(base, "2025-01-02T10-00-00"), output.SessionMeta{
		ID:      "2025-01-02T10-00-00",
		Prompt:  "second session",
		Writer:  "gpt-4o",
		Auditor: "gpt-4o",
		Status:  "aborted",
		StartedAt: time.Date(2025, 1, 2, 10, 0, 0, 0, time.UTC),
	})

	sessions, err := List(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	// Newest first
	if sessions[0].ID != "2025-01-02T10-00-00" {
		t.Errorf("expected newest first, got %s", sessions[0].ID)
	}
	if sessions[1].Status != "complete" {
		t.Errorf("expected complete, got %s", sessions[1].Status)
	}
}

func TestListDirWithoutSessionJSON(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "orphan-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	sessions, err := List(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].Status != "unknown" {
		t.Errorf("expected unknown status, got %s", sessions[0].Status)
	}
}

func TestListTruncatesLongPrompt(t *testing.T) {
	base := t.TempDir()
	long := strings.Repeat("x", 100)
	writeSessionJSON(t, filepath.Join(base, "2025-01-01T10-00-00"), output.SessionMeta{
		ID:     "2025-01-01T10-00-00",
		Prompt: long,
		Status: "complete",
	})
	sessions, err := List(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions[0].Prompt) > 80 {
		t.Errorf("prompt should be truncated to 80 chars, got %d", len(sessions[0].Prompt))
	}
	if !strings.HasSuffix(sessions[0].Prompt, "...") {
		t.Error("truncated prompt should end with ...")
	}
}

func TestShowExact(t *testing.T) {
	base := t.TempDir()
	completed := time.Date(2025, 1, 1, 11, 0, 0, 0, time.UTC)
	writeSessionJSON(t, filepath.Join(base, "2025-01-01T10-00-00"), output.SessionMeta{
		ID:            "2025-01-01T10-00-00",
		Prompt:        "build a widget",
		Writer:        "claude-3-7-sonnet-latest",
		Auditor:       "gpt-4o",
		RoundsPerPass: 5,
		Status:        "complete",
		StartedAt:     time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC),
		CompletedAt:   &completed,
		Passes: []output.PassRecord{
			{Name: "correctness", RoundsCompleted: 3, Status: "complete"},
		},
	})

	// Add a code file
	codeDir := filepath.Join(base, "2025-01-01T10-00-00", "code")
	os.MkdirAll(codeDir, 0o755)
	os.WriteFile(filepath.Join(codeDir, "main.go"), []byte("package main"), 0o644)

	// Add an artifact
	os.WriteFile(filepath.Join(base, "2025-01-01T10-00-00", "AI-1.md"), []byte("transcript"), 0o644)

	detail, err := Show(base, "2025-01-01T10-00-00")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Meta.ID != "2025-01-01T10-00-00" {
		t.Errorf("unexpected ID: %s", detail.Meta.ID)
	}
	if detail.Meta.Prompt != "build a widget" {
		t.Errorf("unexpected prompt: %s", detail.Meta.Prompt)
	}
	if len(detail.CodeFiles) != 1 {
		t.Fatalf("expected 1 code file, got %d", len(detail.CodeFiles))
	}
	if detail.CodeFiles[0].Name != "main.go" {
		t.Errorf("unexpected code file: %s", detail.CodeFiles[0].Name)
	}
	if len(detail.Artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(detail.Artifacts))
	}
	if detail.Artifacts[0].Name != "AI-1.md" {
		t.Errorf("unexpected artifact: %s", detail.Artifacts[0].Name)
	}
}

func TestShowPrefixMatch(t *testing.T) {
	base := t.TempDir()
	writeSessionJSON(t, filepath.Join(base, "2025-01-01T10-00-00"), output.SessionMeta{
		ID:     "2025-01-01T10-00-00",
		Prompt: "test",
		Status: "complete",
	})

	detail, err := Show(base, "2025-01-01")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Meta.ID != "2025-01-01T10-00-00" {
		t.Errorf("prefix match failed, got ID: %s", detail.Meta.ID)
	}
}

func TestShowNotFound(t *testing.T) {
	base := t.TempDir()
	_, err := Show(base, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing session")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestFormatListEmpty(t *testing.T) {
	out := FormatList(nil)
	if !strings.Contains(out, "No sessions found") {
		t.Errorf("expected 'No sessions found', got: %s", out)
	}
}

func TestFormatListOutput(t *testing.T) {
	sessions := []SessionSummary{
		{ID: "2025-01-02T10-00-00", Status: "complete", Writer: "claude", Auditor: "gpt-4o", Prompt: "do stuff"},
		{ID: "2025-01-01T10-00-00", Status: "aborted", Writer: "gpt-4o", Prompt: "other stuff"},
	}
	out := FormatList(sessions)
	if !strings.Contains(out, "2025-01-02T10-00-00") {
		t.Error("missing first session ID")
	}
	if !strings.Contains(out, "claude/gpt-4o") {
		t.Error("missing model pair")
	}
	if !strings.Contains(out, "do stuff") {
		t.Error("missing prompt")
	}
}

func TestFormatDetail(t *testing.T) {
	completed := time.Date(2025, 1, 1, 11, 0, 0, 0, time.UTC)
	d := &SessionDetail{
		Meta: output.SessionMeta{
			ID:            "2025-01-01T10-00-00",
			Prompt:        "build it",
			Writer:        "claude",
			Auditor:       "gpt-4o",
			RoundsPerPass: 5,
			Status:        "complete",
			AbortReason:   "",
			StartedAt:     time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC),
			CompletedAt:   &completed,
			Passes: []output.PassRecord{
				{Name: "correctness", RoundsCompleted: 3, Status: "complete"},
			},
		},
		Dir:       "/tmp/output/2025-01-01T10-00-00",
		CodeFiles: []FileInfo{{Name: "main.go", Size: 512}},
		Artifacts: []FileInfo{{Name: "AI-1.md", Size: 2048}},
	}
	out := FormatDetail(d)
	for _, want := range []string{
		"Session: 2025-01-01T10-00-00",
		"Status:  complete",
		"Writer:  claude",
		"Rounds:  5 per pass",
		"build it",
		"correctness",
		"main.go",
		"AI-1.md",
		"2.0 KB",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Reason:") {
		t.Error("should not show Reason when AbortReason is empty")
	}
}

func TestFormatDetailWithAbortReason(t *testing.T) {
	d := &SessionDetail{
		Meta: output.SessionMeta{
			ID:          "test",
			Status:      "aborted",
			AbortReason: "context canceled",
		},
	}
	out := FormatDetail(d)
	if !strings.Contains(out, "Reason:  context canceled") {
		t.Errorf("missing abort reason in output:\n%s", out)
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{2048, "2.0 KB"},
		{1536, "1.5 KB"},
	}
	for _, tt := range tests {
		got := formatSize(tt.bytes)
		if got != tt.want {
			t.Errorf("formatSize(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}
