package gui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"os"

	"forge/internal/chatstate"
)

// previewMaxEdge bounds a preview's longest side. Big enough to read a
// screenshot, small enough to sit in a transcript without bloating the DOM.
const previewMaxEdge = 640

// ImagePreview returns a downscaled data URL for an image on disk, so the
// window can show what was attached or what a tool pulled in. The path is
// validated as an image first, which also bounds the file size.
func (s *Service) ImagePreview(path string) (string, error) {
	if _, _, ready := s.snapshot(); !ready {
		return "", errNotReady
	}
	clean, err := resolveLocalPath(path)
	if err != nil {
		return "", err
	}
	if _, err := chatstate.ValidateImageAttachment(clean); err != nil {
		return "", err
	}
	f, err := os.Open(clean)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	src, _, err := image.Decode(f)
	if err != nil {
		return "", fmt.Errorf("cannot decode image: %w", err)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, downscale(src), &jpeg.Options{Quality: 78}); err != nil {
		return "", err
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// downscale shrinks an image to fit previewMaxEdge, sampling nearest-neighbour.
// A thumbnail does not warrant a resampling dependency.
func downscale(src image.Image) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= previewMaxEdge && h <= previewMaxEdge {
		return src
	}
	scale := float64(previewMaxEdge) / float64(max(w, h))
	dw, dh := max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	// Flatten onto white: JPEG has no alpha, and a transparent PNG would
	// otherwise come out with black where it should be clear.
	draw.Draw(dst, dst.Bounds(), image.NewUniform(image.White), image.Point{}, draw.Src)
	for y := range dh {
		sy := b.Min.Y + int(float64(y)/scale)
		for x := range dw {
			sx := b.Min.X + int(float64(x)/scale)
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}
