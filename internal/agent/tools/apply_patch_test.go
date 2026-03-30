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
