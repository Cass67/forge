package tools

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestPreviewServerStatusReportsStoppedWithoutActiveServer(t *testing.T) {
	runtime := NewPreviewRuntime(t.TempDir(), approveAll)
	t.Cleanup(func() { _ = runtime.Close() })

	raw, err := NewPreviewServerStatus(runtime).Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	got := parseToolJSONResult(t, raw)
	if got["status"] != "stopped" {
		t.Fatalf("unexpected status: %#v", got)
	}
}

func TestPreviewServerEnsureServesArtifactHandle(t *testing.T) {
	dir := t.TempDir()
	runtime := NewPreviewRuntime(dir, approveAll)
	t.Cleanup(func() { _ = runtime.Close() })

	artifactRaw, err := NewArtifactWrite(runtime).Execute(context.Background(), map[string]any{
		"path":    "themes_preview.html",
		"content": "<html><body>dark mockups</body></html>",
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact := parseToolJSONResult(t, artifactRaw)
	handle, _ := artifact["handle"].(string)
	if handle == "" {
		t.Fatalf("missing handle: %#v", artifact)
	}

	raw, err := NewPreviewServerEnsure(runtime).Execute(context.Background(), map[string]any{
		"handle": handle,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := parseToolJSONResult(t, raw)
	url, _ := got["url"].(string)
	if url == "" {
		t.Fatalf("missing preview url: %#v", got)
	}

	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if string(body) != "<html><body>dark mockups</body></html>" {
		t.Fatalf("unexpected body: %q", string(body))
	}
}

func TestPreviewServerEnsureReusesServerForSameRoot(t *testing.T) {
	dir := t.TempDir()
	runtime := NewPreviewRuntime(dir, approveAll)
	t.Cleanup(func() { _ = runtime.Close() })

	write := NewArtifactWrite(runtime)
	firstRaw, err := write.Execute(context.Background(), map[string]any{
		"path":    "mockups/one.html",
		"content": "<html>one</html>",
	})
	if err != nil {
		t.Fatal(err)
	}
	secondRaw, err := write.Execute(context.Background(), map[string]any{
		"path":    "mockups/two.html",
		"content": "<html>two</html>",
	})
	if err != nil {
		t.Fatal(err)
	}

	firstArtifact := parseToolJSONResult(t, firstRaw)
	secondArtifact := parseToolJSONResult(t, secondRaw)
	ensure := NewPreviewServerEnsure(runtime)

	firstPreviewRaw, err := ensure.Execute(context.Background(), map[string]any{
		"handle": firstArtifact["handle"],
	})
	if err != nil {
		t.Fatal(err)
	}
	secondPreviewRaw, err := ensure.Execute(context.Background(), map[string]any{
		"handle": secondArtifact["handle"],
	})
	if err != nil {
		t.Fatal(err)
	}

	firstPreview := parseToolJSONResult(t, firstPreviewRaw)
	secondPreview := parseToolJSONResult(t, secondPreviewRaw)
	if firstPreview["port"] != secondPreview["port"] {
		t.Fatalf("expected preview server reuse, got %#v then %#v", firstPreview, secondPreview)
	}
	if secondPreview["reused"] != true {
		t.Fatalf("expected reused server, got %#v", secondPreview)
	}
}

func TestPreviewServerEnsureUsesRequestedPortFromJSONNumber(t *testing.T) {
	dir := t.TempDir()
	runtime := NewPreviewRuntime(dir, approveAll)
	t.Cleanup(func() { _ = runtime.Close() })

	if err := os.WriteFile(filepath.Join(dir, "themes_preview.html"), []byte("<html>preview</html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := NewPreviewServerEnsure(runtime).Execute(context.Background(), map[string]any{
		"path": "themes_preview.html",
		"port": float64(port),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := parseToolJSONResult(t, raw)
	if got["port"] != float64(port) {
		t.Fatalf("port = %#v, want %d", got["port"], port)
	}
}
