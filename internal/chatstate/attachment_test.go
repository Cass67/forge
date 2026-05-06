package chatstate

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestFormatSize(t *testing.T) {
	tests := []struct {
		size int64
		want string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1024, "1 KB"},
		{2048, "2 KB"},
		{1048576, "1.0 MB"},
		{20971520, "20.0 MB"},
	}
	for _, tt := range tests {
		got := FormatSize(tt.size)
		if got != tt.want {
			t.Errorf("FormatSize(%d) = %q, want %q", tt.size, got, tt.want)
		}
	}
}

func TestDetectImageReferences(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a dummy PNG file
	pngPath := filepath.Join(tmpDir, "screenshot.png")
	createTestPNG(t, pngPath)

	// Create a dummy JPEG file
	jpgPath := filepath.Join(tmpDir, "photo.jpg")
	createTestJPEG(t, jpgPath)

	// Create a PNG file with spaces in the name
	spacePNG := filepath.Join(tmpDir, "my screenshot.png")
	createTestPNG(t, spacePNG)

	tests := []struct {
		name    string
		text    string
		workDir string
		wantLen int
	}{
		{"absolute PNG path", pngPath, tmpDir, 1},
		{"absolute JPEG path", jpgPath, tmpDir, 1},
		{"file:// URI", "file://" + pngPath, tmpDir, 1},
		{"quoted path", "'" + jpgPath + "'", tmpDir, 1},
		{"plain text", "hello world", tmpDir, 0},
		{"unsupported extension", filepath.Join(tmpDir, "doc.pdf"), tmpDir, 0},
		{"mixed text and image", "hello " + pngPath + " world", tmpDir, 1},
		{"escaped-space paste", "/Users/test/my\\ screenshot.png", "/Users/test", 1},
		{"raw-space path", spacePNG, tmpDir, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			refs := DetectImageReferences(tt.text, tt.workDir)
			if len(refs) != tt.wantLen {
				t.Errorf("DetectImageReferences(%q) = %d refs, want %d: %v", tt.text, len(refs), tt.wantLen, refs)
			}
		})
	}
}

func TestValidateImageAttachment(t *testing.T) {
	tmpDir := t.TempDir()

	// Valid PNG
	pngPath := filepath.Join(tmpDir, "valid.png")
	createTestPNG(t, pngPath)

	att, err := ValidateImageAttachment(pngPath)
	if err != nil {
		t.Fatalf("ValidateImageAttachment(%q): %v", pngPath, err)
	}
	if att.MIMEType != "image/png" {
		t.Errorf("MIMEType = %q, want image/png", att.MIMEType)
	}
	if att.Name != "valid.png" {
		t.Errorf("Name = %q, want valid.png", att.Name)
	}
	if att.Width <= 0 || att.Height <= 0 {
		t.Errorf("dimensions = %dx%d, want positive", att.Width, att.Height)
	}
	if att.ID == "" {
		t.Error("ID should not be empty")
	}

	// Non-existent file
	_, err = ValidateImageAttachment(filepath.Join(tmpDir, "nonexistent.png"))
	if err == nil {
		t.Error("expected error for non-existent file")
	}

	// Directory
	_, err = ValidateImageAttachment(tmpDir)
	if err == nil {
		t.Error("expected error for directory")
	}
}

func TestValidateImageAttachmentRejectsNonImage(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file that is not an image
	txtPath := filepath.Join(tmpDir, "readme.txt")
	if err := os.WriteFile(txtPath, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := ValidateImageAttachment(txtPath)
	if err == nil {
		t.Error("expected error for non-image file")
	}
}

func TestNormalizeImagePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot resolve home directory")
	}

	tests := []struct {
		name    string
		path    string
		workDir string
		want    string
	}{
		{"absolute path", "/tmp/test.png", "/foo", "/tmp/test.png"},
		{"home tilde", "~/test.png", "/foo", filepath.Join(home, "test.png")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeImagePath(tt.path, tt.workDir)
			if err != nil {
				t.Fatalf("normalizeImagePath(%q): %v", tt.path, err)
			}
			if got != tt.want {
				t.Errorf("normalizeImagePath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestMaxImageBytes(t *testing.T) {
	if MaxImageBytes != 20*1024*1024 {
		t.Errorf("MaxImageBytes = %d, want 20MB", MaxImageBytes)
	}
}

func TestMaxAttachments(t *testing.T) {
	if MaxAttachments != 10 {
		t.Errorf("MaxAttachments = %d, want 10", MaxAttachments)
	}
}

func createTestPNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 10, 20))
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func createTestJPEG(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	// Minimal JPEG: SOI marker + JFIF header
	jfif := []byte{
		0xFF, 0xD8, // SOI
		0xFF, 0xE0, // APP0
		0x00, 0x10, // length
		0x4A, 0x46, 0x49, 0x46, 0x00, // JFIF\0
		0x01, 0x01, // version
		0x00,       // density
		0x00, 0x01, // x density
		0x00, 0x01, // y density
		0x00, 0x00, // thumbnail
		0xFF, 0xD9, // EOI
	}
	if _, err := f.Write(jfif); err != nil {
		t.Fatal(err)
	}
}
