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

func fill(w, h int, c color.Color) image.Image {
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			m.Set(x, y, c)
		}
	}
	return m
}

func TestDescribeImageDetectsBlackRender(t *testing.T) {
	got := strings.Join(describeImage(fill(64, 64, color.RGBA{0, 0, 0, 255})), "\n")
	if !strings.Contains(got, "uniformly black") {
		t.Fatalf("black frame not reported as black:\n%s", got)
	}
}

func TestDescribeImageDetectsContent(t *testing.T) {
	m := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			if x < 32 {
				m.Set(x, y, color.RGBA{200, 30, 30, 255})
			} else {
				m.Set(x, y, color.RGBA{20, 20, 220, 255})
			}
		}
	}
	got := strings.Join(describeImage(m), "\n")
	if !strings.Contains(got, "content: varied") {
		t.Fatalf("two-tone image not reported as varied:\n%s", got)
	}
	// left half red, right half blue must be visible in the grid
	if !strings.Contains(got, "c81e1e") || !strings.Contains(got, "1414dc") {
		t.Fatalf("grid did not carry the two regions:\n%s", got)
	}
}

func TestViewImageReturnsImagePartOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shot.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, fill(8, 8, color.RGBA{10, 200, 10, 255})); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	tool := NewViewImage(dir)
	if tool.LastParts == nil {
		t.Fatal("view_image must expose LastParts or the loop cannot replay the image")
	}
	if got := tool.LastParts(); len(got) != 0 {
		t.Fatalf("no parts before execution, got %d", len(got))
	}
	out, err := tool.Execute(context.Background(), map[string]any{"path": "shot.png"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "content: ") {
		t.Fatalf("text description missing, got:\n%s", out)
	}
	parts := tool.LastParts()
	if len(parts) != 1 || parts[0].Type != "image" || parts[0].Image == nil {
		t.Fatalf("expected one image part, got %#v", parts)
	}
	if parts[0].Image.MIMEType != "image/png" {
		t.Errorf("mime = %q, want image/png", parts[0].Image.MIMEType)
	}
	// Draining must clear it, or the next unrelated tool call replays a stale image.
	if got := tool.LastParts(); len(got) != 0 {
		t.Fatalf("parts must be consumed once, got %d on second read", len(got))
	}
}
