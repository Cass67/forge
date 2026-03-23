package history

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestListSortsNewestSessionFirstByStartedAt(t *testing.T) {
	base := t.TempDir()

	writeSession := func(dirName, sessionID string, startedAt time.Time) {
		dir := filepath.Join(base, dirName)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		data := []byte(`{
			"id": "` + sessionID + `",
			"prompt": "prompt",
			"writer": "writer",
			"auditor": "auditor",
			"status": "complete",
			"started_at": "` + startedAt.Format(time.RFC3339) + `"
		}`)
		if err := os.WriteFile(filepath.Join(dir, "session.json"), data, 0o644); err != nil {
			t.Fatalf("write session.json: %v", err)
		}
	}

	older := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	newer := older.Add(2 * time.Hour)

	writeSession("2025-01-01T10-00-00", "zzz-older", older)
	writeSession("2025-01-01T12-00-00", "aaa-newer", newer)

	sessions, err := List(base)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	if sessions[0].ID != "aaa-newer" {
		t.Fatalf("expected newest session first, got %#v", sessions)
	}
}

func TestListFallsBackToDirectoryTimestampWhenStartedAtMissing(t *testing.T) {
	base := t.TempDir()

	writeSession := func(dirName, sessionID string) {
		dir := filepath.Join(base, dirName)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		data := []byte(`{
			"id": "` + sessionID + `",
			"prompt": "prompt",
			"writer": "writer",
			"auditor": "auditor",
			"status": "complete"
		}`)
		if err := os.WriteFile(filepath.Join(dir, "session.json"), data, 0o644); err != nil {
			t.Fatalf("write session.json: %v", err)
		}
	}

	writeSession("2025-01-01T10-00-00", "older")
	writeSession("2025-01-01T12-00-00", "newer")

	sessions, err := List(base)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	if sessions[0].ID != "newer" {
		t.Fatalf("expected directory timestamp fallback to sort newest first, got %#v", sessions)
	}
}
