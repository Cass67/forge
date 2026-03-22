package output_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"forge/internal/output"
)

func TestTakeSnapshot(t *testing.T) {
	base := t.TempDir()
	w, _ := output.NewWriter(base, time.Now())
	w.WriteCode(output.CodeBlock{Filename: "main.go", Content: "package main\n"})
	w.WriteCode(output.CodeBlock{Filename: "sub/util.go", Content: "package sub\n"})

	snap := w.TakeSnapshot()
	if len(snap.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(snap.Files))
	}
	if snap.Files["main.go"] != "package main\n" {
		t.Errorf("unexpected main.go content: %q", snap.Files["main.go"])
	}
	if snap.Files["sub/util.go"] != "package sub\n" {
		t.Errorf("unexpected sub/util.go content: %q", snap.Files["sub/util.go"])
	}
}

func TestTakeSnapshotEmpty(t *testing.T) {
	base := t.TempDir()
	w, _ := output.NewWriter(base, time.Now())

	snap := w.TakeSnapshot()
	if len(snap.Files) != 0 {
		t.Fatalf("expected 0 files, got %d", len(snap.Files))
	}
}

func TestDiffSnapshotsAdded(t *testing.T) {
	before := output.CodeSnapshot{Files: map[string]string{}}
	after := output.CodeSnapshot{Files: map[string]string{
		"new.go": "package new\n",
	}}

	diffs := output.DiffSnapshots(before, after)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff entry, got %d", len(diffs))
	}
	if diffs[0].Status != "added" {
		t.Errorf("expected status added, got %s", diffs[0].Status)
	}
	if diffs[0].Filename != "new.go" {
		t.Errorf("expected filename new.go, got %s", diffs[0].Filename)
	}
	if !strings.Contains(diffs[0].Diff, "+package new") {
		t.Errorf("expected diff to contain +package new, got:\n%s", diffs[0].Diff)
	}
}

func TestDiffSnapshotsDeleted(t *testing.T) {
	before := output.CodeSnapshot{Files: map[string]string{
		"old.go": "package old\n",
	}}
	after := output.CodeSnapshot{Files: map[string]string{}}

	diffs := output.DiffSnapshots(before, after)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff entry, got %d", len(diffs))
	}
	if diffs[0].Status != "deleted" {
		t.Errorf("expected status deleted, got %s", diffs[0].Status)
	}
	if !strings.Contains(diffs[0].Diff, "-package old") {
		t.Errorf("expected diff to contain -package old, got:\n%s", diffs[0].Diff)
	}
}

func TestDiffSnapshotsModified(t *testing.T) {
	before := output.CodeSnapshot{Files: map[string]string{
		"main.go": "package main\n\nfunc old() {}\n",
	}}
	after := output.CodeSnapshot{Files: map[string]string{
		"main.go": "package main\n\nfunc new_() {}\n",
	}}

	diffs := output.DiffSnapshots(before, after)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff entry, got %d", len(diffs))
	}
	if diffs[0].Status != "modified" {
		t.Errorf("expected status modified, got %s", diffs[0].Status)
	}
	if !strings.Contains(diffs[0].Diff, "-func old()") {
		t.Errorf("expected diff to contain removed line, got:\n%s", diffs[0].Diff)
	}
	if !strings.Contains(diffs[0].Diff, "+func new_()") {
		t.Errorf("expected diff to contain added line, got:\n%s", diffs[0].Diff)
	}
}

func TestDiffSnapshotsUnchanged(t *testing.T) {
	snap := output.CodeSnapshot{Files: map[string]string{
		"main.go": "package main\n",
	}}
	diffs := output.DiffSnapshots(snap, snap)
	if len(diffs) != 0 {
		t.Errorf("expected 0 diffs for identical snapshots, got %d", len(diffs))
	}
}

func TestDiffSnapshotsSorted(t *testing.T) {
	before := output.CodeSnapshot{Files: map[string]string{}}
	after := output.CodeSnapshot{Files: map[string]string{
		"z.go": "z",
		"a.go": "a",
		"m.go": "m",
	}}
	diffs := output.DiffSnapshots(before, after)
	if len(diffs) != 3 {
		t.Fatalf("expected 3 diffs, got %d", len(diffs))
	}
	if diffs[0].Filename != "a.go" || diffs[1].Filename != "m.go" || diffs[2].Filename != "z.go" {
		t.Errorf("expected sorted order a,m,z got %s,%s,%s",
			diffs[0].Filename, diffs[1].Filename, diffs[2].Filename)
	}
}

func TestAppendDiffLog(t *testing.T) {
	base := t.TempDir()
	w, _ := output.NewWriter(base, time.Now())

	diffs := []output.DiffEntry{
		{Filename: "main.go", Status: "added", Diff: "+package main\n"},
	}
	if err := w.AppendDiffLog(1, 1, diffs); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(w.Dir(), "diff-log.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "# Diff Log") {
		t.Error("missing title")
	}
	if !strings.Contains(text, "## Pass 1 Round 1") {
		t.Error("missing round heading")
	}
	if !strings.Contains(text, "### main.go (added)") {
		t.Error("missing file heading")
	}
	if !strings.Contains(text, "```diff") {
		t.Error("missing diff fence")
	}
}

func TestAppendDiffLogMultipleRounds(t *testing.T) {
	base := t.TempDir()
	w, _ := output.NewWriter(base, time.Now())

	diffs1 := []output.DiffEntry{
		{Filename: "main.go", Status: "added", Diff: "+package main\n"},
	}
	diffs2 := []output.DiffEntry{
		{Filename: "main.go", Status: "modified", Diff: "-old\n+new\n"},
	}
	w.AppendDiffLog(1, 1, diffs1)
	w.AppendDiffLog(1, 2, diffs2)

	data, _ := os.ReadFile(filepath.Join(w.Dir(), "diff-log.md"))
	text := string(data)
	if !strings.Contains(text, "## Pass 1 Round 1") {
		t.Error("missing round 1 heading")
	}
	if !strings.Contains(text, "## Pass 1 Round 2") {
		t.Error("missing round 2 heading")
	}
	// Title should appear only once
	if strings.Count(text, "# Diff Log") != 1 {
		t.Error("title should appear exactly once")
	}
}

func TestAppendDiffLogNoDiffs(t *testing.T) {
	base := t.TempDir()
	w, _ := output.NewWriter(base, time.Now())

	if err := w.AppendDiffLog(1, 1, nil); err != nil {
		t.Fatal(err)
	}
	// File should not exist since there were no diffs
	if _, err := os.Stat(filepath.Join(w.Dir(), "diff-log.md")); err == nil {
		t.Error("diff-log.md should not exist when there are no diffs")
	}
}
