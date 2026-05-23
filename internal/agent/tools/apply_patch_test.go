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
