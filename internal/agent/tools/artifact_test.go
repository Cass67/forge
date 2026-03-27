package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func approveAll(Action) (bool, error) {
	return true, nil
}

func parseToolJSONResult(t *testing.T, raw string) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal result: %v\nraw=%s", err, raw)
	}
	return got
}

func TestArtifactWriteReturnsTrackedHandleAndMetadata(t *testing.T) {
	dir := t.TempDir()
	runtime := NewPreviewRuntime(dir, approveAll)
	t.Cleanup(func() { _ = runtime.Close() })

	raw, err := NewArtifactWrite(runtime).Execute(context.Background(), map[string]any{
		"path":    "mockups/themes_preview.html",
		"content": "<html><body>preview</body></html>",
	})
	if err != nil {
		t.Fatal(err)
	}

	got := parseToolJSONResult(t, raw)
	handle, _ := got["handle"].(string)
	if handle == "" {
		t.Fatalf("expected artifact handle, got %#v", got)
	}
	if got["path"] != "mockups/themes_preview.html" {
		t.Fatalf("unexpected path: %#v", got)
	}
	if got["bytes"] != float64(len("<html><body>preview</body></html>")) {
		t.Fatalf("unexpected byte count: %#v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "mockups/themes_preview.html")); err != nil {
		t.Fatalf("artifact file missing: %v", err)
	}
}
