package tools

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestViewImageReportsMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tiny.png")
	img := image.NewRGBA(image.Rect(0, 0, 2, 3))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	tool := NewViewImage(dir)
	result, err := tool.Execute(context.Background(), map[string]any{"path": "tiny.png"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"format: png", "dimensions: 2x3", "path: tiny.png"} {
		if !strings.Contains(result, want) {
			t.Fatalf("result missing %q: %s", want, result)
		}
	}
}
