package gui

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forge/internal/chatstate"
	"forge/internal/tui"
)

func samplePNG(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(1, 1, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// Images are dropped onto the window as data URLs; the runtime loads
// attachments from disk, so the bytes have to be written out and validated.
func TestAttachImageAcceptsADroppedDataURL(t *testing.T) {
	s, c := New(func(string, any) {})
	c.Attach(tui.ChatLiveConfig{WorkDir: t.TempDir()}, make(chan string, 4))

	raw := samplePNG(t)
	for _, payload := range []string{raw, "data:image/png;base64," + raw} {
		att, err := s.AttachImage("Screenshot 2026-08-20 at 10.32.14.png", payload)
		if err != nil {
			t.Fatalf("AttachImage: %v", err)
		}
		if att.Path == "" || att.MIMEType != "image/png" || att.Width != 8 || att.Height != 8 {
			t.Fatalf("attachment = %+v", att)
		}
		if _, err := os.Stat(att.Path); err != nil {
			t.Fatalf("attachment file missing: %v", err)
		}
	}
}

func TestAttachImageRejectsRubbish(t *testing.T) {
	s, c := New(func(string, any) {})
	c.Attach(tui.ChatLiveConfig{WorkDir: t.TempDir()}, make(chan string, 1))
	if _, err := s.AttachImage("x.png", "not base64!!"); err == nil {
		t.Error("AttachImage accepted undecodable data")
	}
	if _, err := s.AttachImage("x.png", base64.StdEncoding.EncodeToString([]byte("not an image"))); err == nil {
		t.Error("AttachImage accepted a non-image")
	}
}

// A message carrying only an image, with no text, must still reach the runtime.
func TestSendWithImagesCarriesAnImageOnlyMessage(t *testing.T) {
	s, c := New(func(string, any) {})
	inputCh := make(chan string, 1)
	c.Attach(tui.ChatLiveConfig{WorkDir: t.TempDir()}, inputCh)

	att, err := s.AttachImage("shot.png", samplePNG(t))
	if err != nil {
		t.Fatalf("AttachImage: %v", err)
	}
	if err := s.SendWithImages("", []chatstate.ChatAttachment{att}); err != nil {
		t.Fatalf("SendWithImages: %v", err)
	}
	select {
	case got := <-inputCh:
		if !strings.Contains(got, att.Path) || !strings.Contains(got, `"is_input":true`) {
			t.Fatalf("runtime input did not carry the attachment: %s", got)
		}
	default:
		t.Fatal("nothing was sent to the runtime")
	}
}

// Finder drags arrive as file:// URIs, percent-encoded when the name has
// spaces. Those must attach without a byte round-trip.
func TestAttachPathAcceptsFileURIs(t *testing.T) {
	s, c := New(func(string, any) {})
	c.Attach(tui.ChatLiveConfig{WorkDir: t.TempDir()}, make(chan string, 1))

	raw, err := base64.StdEncoding.DecodeString(samplePNG(t))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "Screen Shot 2026.png")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	for _, form := range []string{path, "file://" + path, "file://" + strings.ReplaceAll(path, " ", "%20")} {
		att, err := s.AttachPath(form)
		if err != nil {
			t.Fatalf("AttachPath(%q): %v", form, err)
		}
		if att.Width != 8 || att.MIMEType != "image/png" {
			t.Fatalf("attachment = %+v", att)
		}
	}
	if _, err := s.AttachPath("file:///nope/missing.png"); err == nil {
		t.Error("AttachPath accepted a missing file")
	}
}
