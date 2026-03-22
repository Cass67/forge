package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePath(t *testing.T) {
	workDir := t.TempDir()

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"relative file", "main.go", false},
		{"nested relative", "pkg/util/helper.go", false},
		{"dot-slash prefix", "./main.go", false},
		{"parent escape", "../etc/passwd", true},
		{"absolute path outside", "/etc/passwd", true},
		{"absolute inside workdir", filepath.Join(workDir, "foo.go"), false},
		{"sneaky dotdot", "pkg/../../etc/passwd", true},
		{"empty path", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ResolvePath(workDir, tt.path)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error for path %q, got resolved: %q", tt.path, result)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error for path %q: %v", tt.path, err)
				return
			}
			if !filepath.IsAbs(result) {
				t.Errorf("expected absolute path, got %q", result)
			}
		})
	}
}

func TestResolvePathSymlinkEscape(t *testing.T) {
	workDir := t.TempDir()
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644)
	os.Symlink(outside, filepath.Join(workDir, "escape"))

	_, err := ResolvePath(workDir, "escape/secret.txt")
	if err == nil {
		t.Error("expected error for symlink escape")
	}
}

func TestIsBinary(t *testing.T) {
	if IsBinary([]byte("hello world\nfoo bar\n")) {
		t.Error("text detected as binary")
	}
	if !IsBinary([]byte{0x00, 0x01, 0x02, 0xff, 0xfe}) {
		t.Error("binary not detected")
	}
}
