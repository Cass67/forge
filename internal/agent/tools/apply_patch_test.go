package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyPatchAppliesUnifiedDiff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(path, []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewApplyPatch(dir, func(a Action) (bool, error) { return true, nil })
	result, err := tool.Execute(context.Background(), map[string]any{
		"patch": `diff --git a/hello.txt b/hello.txt
--- a/hello.txt
+++ b/hello.txt
@@ -1,2 +1,2 @@
 hello
-world
+forge`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "applied patch") {
		t.Fatalf("result = %q", result)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "hello\nforge\n" {
		t.Fatalf("file content = %q", got)
	}
}

func TestApplyPatchUsesActiveWorkspaceProviderForExternalNewRepo(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "arkanoid")
	tool := NewApplyPatchWithWorkDirProvider(base, func() string { return workspace }, func(a Action) (bool, error) { return true, nil })

	result, err := tool.Execute(context.Background(), map[string]any{
		"patch": `diff --git a/index.html b/index.html
new file mode 100644
--- /dev/null
+++ b/index.html
@@ -0,0 +1 @@
+game`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "applied patch") {
		t.Fatalf("result = %q", result)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "game\n" {
		t.Fatalf("file content = %q", string(data))
	}
}

func TestApplyPatchBlocksSecretByDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	approved := false
	tool := NewApplyPatch(dir, func(a Action) (bool, error) {
		approved = true
		return true, nil
	})

	result, err := tool.Execute(context.Background(), map[string]any{
		"patch": `diff --git a/hello.txt b/hello.txt
--- a/hello.txt
+++ b/hello.txt
@@ -1 +1,2 @@
 hello
+token=` + dummySecret(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "blocked") || strings.Contains(result, dummySecret()) {
		t.Fatalf("expected redacted block result, got: %s", result)
	}
	if approved {
		t.Fatal("patch containing secret should not request normal approval")
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), dummySecret()) {
		t.Fatal("secret patch should not be applied")
	}
}

func TestApplyPatchAcceptsV4AEnvelope(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(path, []byte("hello\nworld\ntail\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewApplyPatch(dir, func(a Action) (bool, error) { return true, nil })
	result, err := tool.Execute(context.Background(), map[string]any{
		"patch": `*** Begin Patch
*** Update File: hello.txt
@@
 hello
-world
+forge
*** Add File: new/created.txt
+fresh
*** End Patch`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "applied patch") {
		t.Fatalf("result = %q", result)
	}
	if data, _ := os.ReadFile(path); string(data) != "hello\nforge\ntail\n" {
		t.Fatalf("file content = %q", string(data))
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "new", "created.txt")); string(data) != "fresh\n" {
		t.Fatalf("added file = %q", string(data))
	}
	if diff := tool.LastDiff(); !strings.Contains(diff, "+forge") {
		t.Fatalf("last diff = %q", diff)
	}
}

func TestApplyPatchV4ADeleteAndMove(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gone.txt"), []byte("bye\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "old.txt"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewApplyPatch(dir, func(a Action) (bool, error) { return true, nil })
	result, err := tool.Execute(context.Background(), map[string]any{
		"patch": `*** Begin Patch
*** Delete File: gone.txt
*** Update File: old.txt
*** Move to: moved.txt
@@
-a
+A
 b
*** End Patch`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "applied patch") {
		t.Fatalf("result = %q", result)
	}
	if _, err := os.Stat(filepath.Join(dir, "gone.txt")); !os.IsNotExist(err) {
		t.Fatal("gone.txt should be deleted")
	}
	if _, err := os.Stat(filepath.Join(dir, "old.txt")); !os.IsNotExist(err) {
		t.Fatal("old.txt should be moved away")
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "moved.txt")); string(data) != "A\nb\n" {
		t.Fatalf("moved content = %q", string(data))
	}
}

func TestApplyPatchV4AReportsMissingContext(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewApplyPatch(dir, func(a Action) (bool, error) { return true, nil })
	result, err := tool.Execute(context.Background(), map[string]any{
		"patch": `*** Begin Patch
*** Update File: f.txt
@@
-nope
+yes
*** End Patch`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "context not found") {
		t.Fatalf("result = %q", result)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "f.txt")); string(data) != "a\n" {
		t.Fatalf("file should be untouched, got %q", string(data))
	}
}
