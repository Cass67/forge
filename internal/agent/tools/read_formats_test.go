package tools

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadNotebook(t *testing.T) {
	nb := `{"cells":[
	 {"cell_type":"markdown","source":["# Title\n","text\n"]},
	 {"cell_type":"code","source":"print(1)\n","outputs":[
	   {"output_type":"stream","name":"stdout","text":["1\n"]},
	   {"output_type":"error","ename":"ValueError","evalue":"bad","traceback":["line one"]},
	   {"output_type":"display_data","data":{"image/png":"...."}}]}]}`
	dir := t.TempDir()
	path := filepath.Join(dir, "n.ipynb")
	if err := os.WriteFile(path, []byte(nb), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := readFile(t, dir, map[string]any{"path": "n.ipynb"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"cell 1 (markdown)", "# Title", "cell 2 (code)", "print(1)", "ValueError: bad", "line one", "image/png, not shown"} {
		if !strings.Contains(out, want) {
			t.Errorf("notebook render missing %q:\n%s", want, out)
		}
	}
}

func TestReadZipListingAndMember(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("src/main.go")
	if _, err := w.Write([]byte("package main\n")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	out, err := readFile(t, dir, map[string]any{"path": "a.zip"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "src/main.go") || !strings.Contains(out, "1 entries") {
		t.Fatalf("listing wrong:\n%s", out)
	}

	out, err = readFile(t, dir, map[string]any{"path": "a.zip", "member": "src/main.go"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "package main\n" {
		t.Fatalf("member content wrong: %q", out)
	}

	out, err = readFile(t, dir, map[string]any{"path": "a.zip", "member": "nope"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "is not in") {
		t.Fatalf("missing member should explain itself: %q", out)
	}
}

func TestReadTarGz(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.tar.gz")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte("hello tar\n")
	if err := tw.WriteHeader(&tar.Header{Name: "doc/readme.txt", Size: int64(len(body)), Mode: 0o644}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := readFile(t, dir, map[string]any{"path": "a.tar.gz"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "doc/readme.txt") {
		t.Fatalf("listing wrong:\n%s", out)
	}
	out, err = readFile(t, dir, map[string]any{"path": "a.tar.gz", "member": "doc/readme.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello tar\n" {
		t.Fatalf("member content wrong: %q", out)
	}
}

func TestPlainGzipIsNotTreatedAsTarball(t *testing.T) {
	if _, handled, _ := readFormat(context.Background(), "/tmp/x.gz", ""); handled {
		t.Error("a plain .gz should fall through to the text path")
	}
	if _, handled, _ := readFormat(context.Background(), "/tmp/x.tar.gz", ""); !handled {
		t.Error(".tar.gz should be handled as an archive")
	}
}

func TestSQLiteRejectsNonIdentifierTable(t *testing.T) {
	if findBinary("sqlite3") == "" {
		t.Skip("sqlite3 not installed")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "x.db")
	if err := os.WriteFile(path, []byte("not really a db"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := readFormat(context.Background(), path, "users; DROP TABLE users")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "is not a table name") {
		t.Errorf("injection attempt should be refused, got %q", out)
	}
}

func TestReadSQLiteSchemaAndRows(t *testing.T) {
	if findBinary("sqlite3") == "" {
		t.Skip("sqlite3 not installed")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "x.db")
	seed := "CREATE TABLE users (id INTEGER, name TEXT); INSERT INTO users VALUES (1, 'ada');"
	cmd := exec.Command(findBinary("sqlite3"), path, seed)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seeding failed: %v %s", err, out)
	}

	out, err := readFile(t, dir, map[string]any{"path": "x.db"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "CREATE TABLE users") {
		t.Fatalf("schema missing:\n%s", out)
	}
	out, err = readFile(t, dir, map[string]any{"path": "x.db", "member": "users"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "id,name") || !strings.Contains(out, "1,ada") {
		t.Fatalf("rows missing:\n%s", out)
	}
}

func TestReadPDFWithoutPoppler(t *testing.T) {
	if findBinary("pdftotext") != "" {
		t.Skip("pdftotext is installed; the missing-binary path cannot be exercised")
	}
	out, handled, err := readFormat(context.Background(), "/tmp/x.pdf", "")
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if !strings.Contains(out, "pdftotext") {
		t.Errorf("should name the missing binary, got %q", out)
	}
}

func readFile(t *testing.T, dir string, args map[string]any) (string, error) {
	t.Helper()
	return NewReadFile(dir).Execute(context.Background(), args)
}
