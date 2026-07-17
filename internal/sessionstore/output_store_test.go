package sessionstore

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileOutputStorePutAndReadRange(t *testing.T) {
	store := NewFileOutputStore(t.TempDir())

	handle, err := store.Put(context.Background(), "thread-1", []byte("0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	if handle.ID == "" {
		t.Fatalf("handle ID is empty: %#v", handle)
	}
	if handle.Bytes != 10 {
		t.Fatalf("handle bytes = %d, want 10", handle.Bytes)
	}
	if handle.SHA256 == "" {
		t.Fatalf("handle SHA256 is empty: %#v", handle)
	}

	got, err := store.Read(context.Background(), handle, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("2345")) {
		t.Fatalf("Read = %q, want %q", got, "2345")
	}
}

func TestFileOutputStoreReadBoundsAreSafe(t *testing.T) {
	store := NewFileOutputStore(t.TempDir())
	handle, err := store.Put(context.Background(), "thread-1", []byte("0123456789"))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		offset int64
		limit  int64
		want   string
	}{
		{name: "negative offset clamps to zero", offset: -2, limit: 4, want: "0123"},
		{name: "negative limit returns empty", offset: 2, limit: -1, want: ""},
		{name: "offset past end returns empty", offset: 20, limit: 4, want: ""},
		{name: "limit past end clamps", offset: 8, limit: 10, want: "89"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := store.Read(context.Background(), handle, tt.offset, tt.limit)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Fatalf("Read = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFileOutputStorePutEnforcesPrivateModes(t *testing.T) {
	root := t.TempDir()
	store := NewFileOutputStore(root)
	handle, err := store.Put(context.Background(), "thread-1", []byte("0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	thread, hash, ok := parseHandleID(handle.ID)
	if !ok {
		t.Fatalf("invalid handle ID: %q", handle.ID)
	}
	dir := filepath.Join(root, "outputs", thread)
	path := filepath.Join(dir, hash+".out")
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Put(context.Background(), "thread-1", []byte("0123456789")); err != nil {
		t.Fatal(err)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("dir mode = %o, want 700", got)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("file mode = %o, want 600", got)
	}
}

func TestFileOutputStorePutNormalizesExistingOutputRootMode(t *testing.T) {
	root := t.TempDir()
	outputs := filepath.Join(root, "outputs")
	if err := os.Mkdir(outputs, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := NewFileOutputStore(root).Put(context.Background(), "thread-1", []byte("0123456789")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(outputs)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("outputs mode = %o, want 700", got)
	}
}

func TestFileOutputStorePutRejectsSymlinkedOutputRoot(t *testing.T) {
	root := t.TempDir()
	escape := t.TempDir()
	if err := os.Symlink(escape, filepath.Join(root, "outputs")); err != nil {
		t.Fatal(err)
	}

	if _, err := NewFileOutputStore(root).Put(context.Background(), "thread-1", []byte("0123456789")); err == nil {
		t.Fatal("Put accepted symlinked output root")
	}
	if entries, err := os.ReadDir(escape); err != nil {
		t.Fatal(err)
	} else if len(entries) != 0 {
		t.Fatalf("Put wrote outside store root through outputs symlink: %#v", entries)
	}
}

func TestFileOutputStorePutRejectsSymlinkedThreadDir(t *testing.T) {
	root := t.TempDir()
	escape := t.TempDir()
	outputs := filepath.Join(root, "outputs")
	if err := os.Mkdir(outputs, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(escape, filepath.Join(outputs, "thread-1")); err != nil {
		t.Fatal(err)
	}

	if _, err := NewFileOutputStore(root).Put(context.Background(), "thread-1", []byte("0123456789")); err == nil {
		t.Fatal("Put accepted symlinked thread directory")
	}
	if entries, err := os.ReadDir(escape); err != nil {
		t.Fatal(err)
	} else if len(entries) != 0 {
		t.Fatalf("Put wrote outside store root through thread symlink: %#v", entries)
	}
}

func TestFileOutputStoreReadRejectsSymlinkedThreadDir(t *testing.T) {
	root := t.TempDir()
	store := NewFileOutputStore(root)
	handle, err := store.Put(context.Background(), "thread-1", []byte("0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	thread, hash, ok := parseHandleID(handle.ID)
	if !ok {
		t.Fatalf("invalid handle ID: %q", handle.ID)
	}
	threadDir := filepath.Join(root, "outputs", thread)
	if err := os.RemoveAll(threadDir); err != nil {
		t.Fatal(err)
	}
	escape := t.TempDir()
	if err := os.WriteFile(filepath.Join(escape, hash+".out"), []byte("escape-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(escape, threadDir); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Read(context.Background(), handle, 0, 10); err == nil {
		t.Fatal("Read accepted symlinked thread directory")
	}
}

func TestFileOutputStoreReadRejectsSymlinkedOutputFile(t *testing.T) {
	root := t.TempDir()
	store := NewFileOutputStore(root)
	handle, err := store.Put(context.Background(), "thread-1", []byte("0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	thread, hash, ok := parseHandleID(handle.ID)
	if !ok {
		t.Fatalf("invalid handle ID: %q", handle.ID)
	}
	path := filepath.Join(root, "outputs", thread, hash+".out")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "external.out")
	if err := os.WriteFile(external, []byte("escape-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, path); err != nil {
		t.Fatal(err)
	}

	got, err := store.Read(context.Background(), handle, 0, 10)
	if err == nil {
		t.Fatalf("Read accepted symlinked output file and returned %q", got)
	}
	if bytes.Equal(got, []byte("escape-data")) {
		t.Fatalf("Read returned external symlink content: %q", got)
	}
}

func TestFileOutputStoreDuplicatePutPreservesExistingFile(t *testing.T) {
	root := t.TempDir()
	store := NewFileOutputStore(root)
	data := []byte("0123456789")
	handle, err := store.Put(context.Background(), "thread-1", data)
	if err != nil {
		t.Fatal(err)
	}
	thread, hash, ok := parseHandleID(handle.ID)
	if !ok {
		t.Fatalf("invalid handle ID: %q", handle.ID)
	}
	path := filepath.Join(root, "outputs", thread, hash+".out")
	oldTime := time.Unix(100, 0)
	if err := os.Chtimes(path, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	again, err := store.Put(context.Background(), "thread-1", data)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != handle.ID || again.Bytes != handle.Bytes || again.SHA256 != handle.SHA256 {
		t.Fatalf("duplicate handle = %#v, want %#v", again, handle)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("duplicate Put changed existing file content to %q, want %q", got, data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(oldTime) {
		t.Fatalf("duplicate Put rewrote existing file; mtime = %s, want %s", info.ModTime(), oldTime)
	}
}

func TestFileOutputStorePutRejectsTraversalThreadID(t *testing.T) {
	root := t.TempDir()
	store := NewFileOutputStore(root)

	if _, err := store.Put(context.Background(), "..", []byte("0123456789")); err == nil {
		t.Fatal("Put accepted traversal thread ID")
	}
	if _, err := os.Stat(filepath.Join(root, "outputs")); !os.IsNotExist(err) {
		t.Fatalf("output root exists after rejected traversal thread ID or stat failed unexpectedly: %v", err)
	}
}

func TestFileOutputStoreReadRejectsTraversalHandle(t *testing.T) {
	store := NewFileOutputStore(t.TempDir())
	valid, err := store.Put(context.Background(), "thread-1", []byte("0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	_, hash, ok := parseHandleID(valid.ID)
	if !ok {
		t.Fatalf("invalid valid handle ID: %q", valid.ID)
	}

	if _, err := store.Read(context.Background(), OutputHandle{ID: "../" + hash, Bytes: valid.Bytes, SHA256: valid.SHA256}, 0, 10); err == nil {
		t.Fatal("Read accepted traversal handle")
	}
}

func TestFileOutputStoreRejectsInvalidHandleParts(t *testing.T) {
	hash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	invalid := []string{
		"./" + hash,
		"../" + hash,
		"thread/extra/" + hash,
		"thread\\extra/" + hash,
		"thread/" + hash + "/extra",
		"thread/" + hash + "\\extra",
	}

	for _, id := range invalid {
		t.Run(id, func(t *testing.T) {
			if thread, gotHash, ok := parseHandleID(id); ok {
				t.Fatalf("parseHandleID(%q) = (%q, %q, true), want rejected", id, thread, gotHash)
			}
		})
	}
}

func TestFileOutputStoreHandleRejectsMalformedIDWithGuidance(t *testing.T) {
	store := NewFileOutputStore(t.TempDir())
	_, err := store.Handle(context.Background(), "a4e83c0a-6b92-4c41-8940-e410238b6348")
	if err == nil || !strings.Contains(err.Error(), `expected "<thread>/<sha256-hex>"`) {
		t.Fatalf("want guidance in error, got %v", err)
	}
}
