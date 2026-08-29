package tools

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"forge/internal/llm"
)

func NewViewImage(workDir string) Tool {
	// Guarded because parallel tool dispatch can run two view_image calls at once.
	var mu sync.Mutex
	var lastParts []llm.MessageContentPart
	t := Tool{
		Name:        "view_image",
		Description: "Inspect a local image file and return dimensions, format, and a compact visual summary.",
		Parameters: []ParameterDef{
			{Name: "path", Type: "string", Description: "image path relative to working directory", Required: true},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			_ = ctx
			path, _ := args["path"].(string)
			resolved, err := ResolvePathAllowEscape(workDir, path)
			if err != nil {
				return "", err
			}
			data, err := os.ReadFile(resolved)
			if err != nil {
				return fmt.Sprintf("error: %v", err), nil
			}
			cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
			if err != nil {
				return fmt.Sprintf("error: unsupported image: %v", err), nil
			}
			summary := []string{
				"path: " + filepath.Clean(path),
				"format: " + strings.ToLower(format),
				fmt.Sprintf("dimensions: %dx%d", cfg.Width, cfg.Height),
				fmt.Sprintf("file_size_bytes: %d", len(data)),
			}
			if img, _, err := image.Decode(bytes.NewReader(data)); err == nil {
				summary = append(summary, describeImage(img)...)
			}
			// Hand the real image to the loop, which replays it as a user message.
			// Vision models then see the picture; the text above is what everyone
			// else gets, and remains the fallback when a provider drops images.
			mu.Lock()
			lastParts = []llm.MessageContentPart{{
				Type: "image",
				Image: &llm.ImageContent{
					Path:     resolved,
					MIMEType: mimeForImageFormat(format),
					Width:    cfg.Width,
					Height:   cfg.Height,
				},
			}}
			mu.Unlock()
			return strings.Join(summary, "\n"), nil
		},
	}
	t.LastParts = func() []llm.MessageContentPart {
		mu.Lock()
		defer mu.Unlock()
		parts := lastParts
		lastParts = nil
		return parts
	}
	return t
}

func mimeForImageFormat(format string) string {
	switch strings.ToLower(format) {
	case "jpeg", "jpg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	default:
		return "image/png"
	}
}

// describeImage renders a coarse description of what the image actually shows.
// Dimensions and file size alone cannot answer the question this tool is usually
// asked -- "did it render, or is it black?" -- so callers were writing their own
// pixel readers. This keeps the tool result text, which is all the tool contract
// allows, while carrying enough signal to act on.
const describeGrid = 8

func describeImage(img image.Image) []string {
	b := img.Bounds()
	if b.Dx() < 1 || b.Dy() < 1 {
		return nil
	}
	type cell struct{ r, g, b, n int64 }
	cells := make([]cell, describeGrid*describeGrid)
	var sum, min, max int64
	min = 255
	for y := b.Min.Y; y < b.Max.Y; y++ {
		gy := (y - b.Min.Y) * describeGrid / b.Dy()
		for x := b.Min.X; x < b.Max.X; x++ {
			gx := (x - b.Min.X) * describeGrid / b.Dx()
			r16, g16, bl16, _ := img.At(x, y).RGBA()
			r, g, bl := int64(r16>>8), int64(g16>>8), int64(bl16>>8)
			c := &cells[gy*describeGrid+gx]
			c.r, c.g, c.b, c.n = c.r+r, c.g+g, c.b+bl, c.n+1
			lum := (r*299 + g*587 + bl*114) / 1000
			sum += lum
			if lum < min {
				min = lum
			}
			if lum > max {
				max = lum
			}
		}
	}
	total := int64(b.Dx()) * int64(b.Dy())
	mean := sum / total
	out := []string{
		fmt.Sprintf("brightness: mean %d, min %d, max %d (0-255)", mean, min, max),
	}
	switch {
	case max-min < 8 && mean < 16:
		out = append(out, "content: uniformly black — nothing rendered")
	case max-min < 8:
		out = append(out, "content: uniform flat colour — likely nothing rendered")
	default:
		out = append(out, "content: varied — something rendered")
	}
	out = append(out, fmt.Sprintf("%dx%d grid of average RGB (hex, row-major):", describeGrid, describeGrid))
	for gy := 0; gy < describeGrid; gy++ {
		row := make([]string, 0, describeGrid)
		for gx := 0; gx < describeGrid; gx++ {
			c := cells[gy*describeGrid+gx]
			if c.n == 0 {
				row = append(row, "------")
				continue
			}
			row = append(row, fmt.Sprintf("%02x%02x%02x", c.r/c.n, c.g/c.n, c.b/c.n))
		}
		out = append(out, "  "+strings.Join(row, " "))
	}
	return out
}
