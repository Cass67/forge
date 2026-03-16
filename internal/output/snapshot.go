package output

import (
	"io"
	"os"
	"path/filepath"
	"time"
)

// Snapshot copies code/ to snapshots/<timestamp>/ and returns the snapshot path.
func (w *Writer) Snapshot(ts time.Time) (string, error) {
	snapDir := filepath.Join(w.dir, "snapshots", ts.UTC().Format("2006-01-02T15-04-05"))
	src := filepath.Join(w.dir, "code")
	return snapDir, copyDir(src, snapDir)
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
