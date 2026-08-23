package tools

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Formats read_file understands beyond plain text. Each one either renders the
// whole file (a notebook, an archive listing, a database schema) or, given a
// member, the one part of it that was asked for.

const (
	formatMemberMaxBytes = 200 * 1024
	formatEntryLimit     = 500
	formatRowLimit       = 50
)

// readFormat renders a non-text file. handled is false for anything that
// should fall through to the plain-text path.
func readFormat(ctx context.Context, path, member string) (out string, handled bool, err error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ipynb":
		data, err := os.ReadFile(path)
		if err != nil {
			return "", true, err
		}
		return readNotebook(data)
	case ".zip", ".jar", ".whl", ".egg":
		return readZip(path, member)
	case ".tar", ".tgz", ".gz":
		if strings.HasSuffix(strings.ToLower(path), ".gz") && !strings.HasSuffix(strings.ToLower(path), ".tar.gz") && !strings.HasSuffix(strings.ToLower(path), ".tgz") {
			return "", false, nil // a plain gzipped file, not a tarball
		}
		return readTar(path, member)
	case ".db", ".sqlite", ".sqlite3":
		return readSQLite(ctx, path, member)
	case ".pdf":
		return readPDF(ctx, path)
	}
	return "", false, nil
}

// ---- notebooks ----------------------------------------------------------

type notebook struct {
	Cells []struct {
		CellType string          `json:"cell_type"`
		Source   json.RawMessage `json:"source"`
		Outputs  []struct {
			OutputType string          `json:"output_type"`
			Text       json.RawMessage `json:"text"`
			Name       string          `json:"name"`
			EName      string          `json:"ename"`
			EValue     string          `json:"evalue"`
			Traceback  []string        `json:"traceback"`
			Data       map[string]any  `json:"data"`
		} `json:"outputs"`
	} `json:"cells"`
}

func readNotebook(data []byte) (string, bool, error) {
	var nb notebook
	if err := json.Unmarshal(data, &nb); err != nil {
		return fmt.Sprintf("error: not a readable notebook: %v", err), true, nil
	}
	var sb strings.Builder
	for i, cell := range nb.Cells {
		kind := cell.CellType
		if kind == "" {
			kind = "unknown"
		}
		fmt.Fprintf(&sb, "# cell %d (%s)\n%s\n", i+1, kind, strings.TrimRight(jsonText(cell.Source), "\n"))
		for _, out := range cell.Outputs {
			switch out.OutputType {
			case "stream":
				fmt.Fprintf(&sb, "--- output (%s) ---\n%s\n", orDefault(out.Name, "stream"), clip(jsonText(out.Text), 2000))
			case "error":
				fmt.Fprintf(&sb, "--- error ---\n%s: %s\n%s\n", out.EName, out.EValue, clip(strings.Join(out.Traceback, "\n"), 2000))
			default:
				if text, ok := out.Data["text/plain"]; ok {
					raw, _ := json.Marshal(text)
					fmt.Fprintf(&sb, "--- output ---\n%s\n", clip(jsonText(raw), 2000))
				} else if len(out.Data) > 0 {
					fmt.Fprintf(&sb, "--- output (%s, not shown) ---\n", strings.Join(sortedKeys(out.Data), ", "))
				}
			}
		}
		sb.WriteString("\n")
	}
	if sb.Len() == 0 {
		return "the notebook has no cells", true, nil
	}
	return clip(sb.String(), formatMemberMaxBytes), true, nil
}

// jsonText renders a notebook field that is either a string or a list of
// strings, which is how the format stores multi-line text.
func jsonText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var lines []string
	if err := json.Unmarshal(raw, &lines); err == nil {
		return strings.Join(lines, "")
	}
	return string(raw)
}

// ---- archives -----------------------------------------------------------

func readZip(path, member string) (string, bool, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Sprintf("error: %v", err), true, nil
	}
	defer func() { _ = r.Close() }()

	if member == "" {
		entries := make([]string, 0, len(r.File))
		for _, f := range r.File {
			if f.FileInfo().IsDir() {
				continue
			}
			entries = append(entries, fmt.Sprintf("%10d  %s", f.UncompressedSize64, f.Name))
		}
		return archiveListing(path, entries), true, nil
	}

	for _, f := range r.File {
		if f.Name != member {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Sprintf("error: %v", err), true, nil
		}
		defer func() { _ = rc.Close() }()
		return archiveMember(member, rc), true, nil
	}
	return fmt.Sprintf("error: %q is not in %s — read the archive without a member to list it", member, filepath.Base(path)), true, nil
}

func readTar(path, member string) (string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Sprintf("error: %v", err), true, nil
	}
	defer func() { _ = f.Close() }()

	var src io.Reader = f
	if lower := strings.ToLower(path); strings.HasSuffix(lower, ".gz") || strings.HasSuffix(lower, ".tgz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Sprintf("error: %v", err), true, nil
		}
		defer func() { _ = gz.Close() }()
		src = gz
	}

	tr := tar.NewReader(src)
	var entries []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Sprintf("error: %v", err), true, nil
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		if member == "" {
			entries = append(entries, fmt.Sprintf("%10d  %s", hdr.Size, hdr.Name))
			continue
		}
		if hdr.Name == member {
			return archiveMember(member, tr), true, nil
		}
	}
	if member != "" {
		return fmt.Sprintf("error: %q is not in %s — read the archive without a member to list it", member, filepath.Base(path)), true, nil
	}
	return archiveListing(path, entries), true, nil
}

func archiveListing(path string, entries []string) string {
	if len(entries) == 0 {
		return fmt.Sprintf("%s is empty", filepath.Base(path))
	}
	truncated := ""
	if len(entries) > formatEntryLimit {
		truncated = fmt.Sprintf("\n... %d more entries", len(entries)-formatEntryLimit)
		entries = entries[:formatEntryLimit]
	}
	return fmt.Sprintf("%s — %d entries. Pass member=<name> to read one.\n\n%s%s",
		filepath.Base(path), len(entries), strings.Join(entries, "\n"), truncated)
}

func archiveMember(member string, r io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(r, formatMemberMaxBytes+1))
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	if IsBinary(data) {
		return fmt.Sprintf("error: %s is binary, cannot display", member)
	}
	if len(data) > formatMemberMaxBytes {
		return string(data[:formatMemberMaxBytes]) + "\n... truncated"
	}
	return string(data)
}

// ---- sqlite -------------------------------------------------------------

var sqliteIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func readSQLite(ctx context.Context, path, member string) (string, bool, error) {
	bin := findBinary("sqlite3")
	if bin == "" {
		return "error: reading a database needs the sqlite3 CLI on PATH", true, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if member == "" {
		out, err := exec.CommandContext(ctx, bin, "-readonly", path, ".schema").CombinedOutput()
		if err != nil {
			return fmt.Sprintf("error: %s", strings.TrimSpace(string(out))), true, nil
		}
		schema := strings.TrimSpace(string(out))
		if schema == "" {
			return fmt.Sprintf("%s has no tables", filepath.Base(path)), true, nil
		}
		return fmt.Sprintf("%s schema. Pass member=<table> to read rows.\n\n%s", filepath.Base(path), clip(schema, formatMemberMaxBytes)), true, nil
	}

	// The table name goes into a query, so it has to be an identifier and
	// nothing else.
	if !sqliteIdentifier.MatchString(member) {
		return fmt.Sprintf("error: %q is not a table name", member), true, nil
	}
	query := fmt.Sprintf("SELECT * FROM %q LIMIT %d;", member, formatRowLimit)
	out, err := exec.CommandContext(ctx, bin, "-readonly", "-header", "-csv", path, query).CombinedOutput()
	if err != nil {
		return fmt.Sprintf("error: %s", strings.TrimSpace(string(out))), true, nil
	}
	return fmt.Sprintf("%s.%s, first %d rows:\n\n%s", filepath.Base(path), member, formatRowLimit, clip(string(out), formatMemberMaxBytes)), true, nil
}

// ---- pdf ----------------------------------------------------------------

func readPDF(ctx context.Context, path string) (string, bool, error) {
	bin := findBinary("pdftotext")
	if bin == "" {
		return "error: reading a PDF needs pdftotext on PATH (`brew install poppler`, `apt install poppler-utils`)", true, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-layout", path, "-").Output()
	if err != nil {
		return fmt.Sprintf("error: pdftotext failed: %v", err), true, nil
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return "the PDF has no extractable text — it is probably scanned images", true, nil
	}
	return clip(text, formatMemberMaxBytes), true, nil
}

// ---- shared -------------------------------------------------------------

func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n... truncated"
}

func orDefault(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
