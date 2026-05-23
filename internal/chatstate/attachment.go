package chatstate

import (
	"encoding/base64"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// MaxImageBytes is the maximum size of a single attached image (20 MB).
const MaxImageBytes = 20 * 1024 * 1024

// MaxAttachments is the maximum number of images per message.
const MaxAttachments = 10

// ChatAttachment represents an image attached to a chat message.
type ChatAttachment struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Name     string `json:"name"`
	MIMEType string `json:"mime_type"`
	Size     int64  `json:"size"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

// ChatUserInput represents a structured user input with optional attachments.
type ChatUserInput struct {
	IsInput     bool             `json:"is_input"`
	Text        string           `json:"text,omitempty"`
	SkillName   string           `json:"skill_name,omitempty"`
	SkillBody   string           `json:"skill_body,omitempty"`
	Attachments []ChatAttachment `json:"attachments,omitempty"`
}

// supportedImageMIMEs maps MIME types to their Go image decoder functions.
var supportedImageMIMEs = map[string]func(io.Reader) (image.Config, error){
	"image/png":  func(r io.Reader) (image.Config, error) { return png.DecodeConfig(r) },
	"image/jpeg": func(r io.Reader) (image.Config, error) { return jpeg.DecodeConfig(r) },
	"image/gif":  func(r io.Reader) (image.Config, error) { _, err := gif.DecodeConfig(r); return image.Config{}, err },
}

// DetectImageReferences scans pasted text for image file paths or file:// URIs.
// Returns a slice of normalized absolute paths that look like image references.
func DetectImageReferences(text string, workDir string) []string {
	var refs []string
	seen := map[string]bool{}
	addRef := func(ref string) {
		ref = filepath.Clean(ref)
		if !seen[ref] {
			seen[ref] = true
			refs = append(refs, ref)
		}
	}

	// Try 1: the full text as a single path (handles paths with spaces that were
	// pasted as "/path/to/my\ image.png" — the \! space is the terminal escape).
	fullText := strings.Trim(text, "'\" \n\t")
	fullText = strings.ReplaceAll(fullText, "\\ ", " ")
	ext := strings.ToLower(filepath.Ext(fullText))
	if isImageExtension(ext) {
		abs, err := normalizeImagePath(fullText, workDir)
		if err == nil {
			addRef(abs)
			return refs
		}
	}

	// Try 2: file:// URI
	if strings.HasPrefix(fullText, "file://") {
		if parsed, err := url.Parse(fullText); err == nil {
			abs, err := normalizeImagePath(parsed.Path, workDir)
			if err == nil {
				addRef(abs)
				return refs
			}
		}
	}

	// Try 3: split by whitespace and check each token
	normalized := strings.ReplaceAll(text, "\\ ", " ")
	tokens := strings.FieldsFunc(normalized, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t'
	})

	for _, token := range tokens {
		token = strings.Trim(token, "'\"")
		if token == "" {
			continue
		}

		if strings.HasPrefix(token, "file://") {
			if parsed, err := url.Parse(token); err == nil {
				abs, err := normalizeImagePath(parsed.Path, workDir)
				if err == nil {
					addRef(abs)
				}
			}
			continue
		}

		ext := strings.ToLower(filepath.Ext(token))
		if isImageExtension(ext) {
			abs, err := normalizeImagePath(token, workDir)
			if err == nil {
				addRef(abs)
			}
		}
	}

	return refs
}

func normalizeImagePath(path string, workDir string) (string, error) {
	// Expand ~
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot resolve home directory: %w", err)
		}
		path = filepath.Join(home, path[1:])
	}

	// Make absolute
	if !filepath.IsAbs(path) {
		path = filepath.Join(workDir, path)
	}

	path = filepath.Clean(path)
	if path == "" || path == "." || path == string(filepath.Separator) {
		return "", fmt.Errorf("invalid path: %s", path)
	}

	return path, nil
}

func isImageExtension(ext string) bool {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif":
		return true
	}
	return false
}

// ValidateImageAttachment validates an image file and returns attachment metadata.
func ValidateImageAttachment(path string) (*ChatAttachment, error) {
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("file not found: %s", path)
		}
		if os.IsPermission(err) {
			return nil, fmt.Errorf("permission denied: %s", path)
		}
		return nil, fmt.Errorf("cannot access file: %w", err)
	}

	if fi.IsDir() {
		return nil, fmt.Errorf("not a file: %s", path)
	}

	if fi.Size() > MaxImageBytes {
		sizeMB := float64(fi.Size()) / (1024 * 1024)
		return nil, fmt.Errorf("file too large: %.1f MB (limit: %d MB)", sizeMB, MaxImageBytes/(1024*1024))
	}

	if fi.Size() == 0 {
		return nil, fmt.Errorf("empty file: %s", path)
	}

	// Detect MIME type by sniffing, not extension
	mime, cfg, err := detectImageFormat(path)
	if err != nil {
		return nil, fmt.Errorf("unsupported image format: %w", err)
	}

	id := base64.RawURLEncoding.EncodeToString([]byte(path))[:12]

	return &ChatAttachment{
		ID:       id,
		Path:     path,
		Name:     filepath.Base(path),
		MIMEType: mime,
		Size:     fi.Size(),
		Width:    cfg.Width,
		Height:   cfg.Height,
	}, nil
}

func detectImageFormat(path string) (mime string, cfg image.Config, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", image.Config{}, fmt.Errorf("cannot open: %w", err)
	}
	defer func() { _ = f.Close() }()

	// Read first bytes for MIME sniffing
	header := make([]byte, 512)
	n, err := f.Read(header)
	if err != nil && err != io.EOF {
		return "", image.Config{}, fmt.Errorf("cannot read: %w", err)
	}

	// Detect MIME type from magic bytes
	mime = detectMIME(header[:n])
	if mime == "" {
		return "", image.Config{}, fmt.Errorf("unknown image format")
	}

	decoder, ok := supportedImageMIMEs[mime]
	if !ok {
		return "", image.Config{}, fmt.Errorf("unsupported image type: %s", mime)
	}

	// Seek back to decode config
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", image.Config{}, fmt.Errorf("cannot seek: %w", err)
	}

	cfg, err = decoder(f)
	if err != nil {
		return "", image.Config{}, fmt.Errorf("cannot decode image: %w", err)
	}

	return mime, cfg, nil
}

func detectMIME(header []byte) string {
	switch {
	case len(header) >= 4 && header[0] == 0x89 && header[1] == 0x50 && header[2] == 0x4E && header[3] == 0x47:
		return "image/png"
	case len(header) >= 2 && header[0] == 0xFF && header[1] == 0xD8:
		return "image/jpeg"
	case len(header) >= 6 && header[0] == 'G' && header[1] == 'I' && header[2] == 'F':
		return "image/gif"
	}
	return ""
}

// FormatSize formats a byte count as a human-readable string.
func FormatSize(size int64) string {
	if size < 1024 {
		return strconv.FormatInt(size, 10) + " B"
	}
	if size < 1024*1024 {
		return fmt.Sprintf("%.0f KB", float64(size)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
}
