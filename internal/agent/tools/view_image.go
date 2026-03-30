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
)

func NewViewImage(workDir string) Tool {
	return Tool{
		Name:        "view_image",
		Description: "Inspect a local image file and return dimensions, format, and a compact visual summary.",
		Parameters: []ParameterDef{
			{Name: "path", Type: "string", Description: "image path relative to working directory", Required: true},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			_ = ctx
			path, _ := args["path"].(string)
			resolved, err := ResolvePath(workDir, path)
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
			return strings.Join(summary, "\n"), nil
		},
	}
}
