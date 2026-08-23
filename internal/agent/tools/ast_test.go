package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const astStreamFixture = `{"text":"errors.New(\"boom\")","file":"a.go","lines":"\treturn errors.New(\"boom\")","replacement":"fmt.Errorf(\"boom\")","range":{"start":{"line":9,"column":8}}}
{"text":"errors.New(\"nope\")","file":"b.go","replacement":"fmt.Errorf(\"nope\")","range":{"start":{"line":0,"column":1}}}`

func TestDecodeAstMatches(t *testing.T) {
	matches, err := decodeAstMatches(strings.NewReader(astStreamFixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 {
		t.Fatalf("got %d matches, want 2", len(matches))
	}
	if matches[0].line() != 10 {
		t.Errorf("line should be 1-indexed, got %d", matches[0].line())
	}
	if matches[1].Replacement != "fmt.Errorf(\"nope\")" {
		t.Errorf("replacement lost: %q", matches[1].Replacement)
	}
	if files := astFilesTouched("/repo", matches); len(files) != 2 || files[0] != "/repo/a.go" {
		t.Errorf("files touched: %v", files)
	}
	if preview := astPreview("/repo", matches); !strings.Contains(preview, "a.go:10") {
		t.Errorf("preview missing location: %q", preview)
	}
}

func TestAstContextHint(t *testing.T) {
	hint := astContextHint("errors.New($M)", "")
	if !strings.Contains(hint, "call_expression") {
		t.Errorf("dotted call should get the context hint, got %q", hint)
	}
	if astContextHint("errors.New($M)", "call_expression") != "" {
		t.Error("no hint once a selector is supplied")
	}
	if astContextHint("lineAnchor($A)", "") != "" {
		t.Error("undotted patterns parse fine and need no hint")
	}
}

func TestAstRelPath(t *testing.T) {
	if got := astRelPath("/repo", "/repo/pkg/a.go"); got != filepath.Join("pkg", "a.go") {
		t.Errorf("got %q", got)
	}
	if got := astRelPath("/repo", "pkg/a.go"); got != "pkg/a.go" {
		t.Errorf("relative paths pass through, got %q", got)
	}
	if got := astRelPath("/repo", "/elsewhere/a.go"); got != "/elsewhere/a.go" {
		t.Errorf("outside paths stay absolute, got %q", got)
	}
}

func TestAstEditRewritesAndPreviews(t *testing.T) {
	if !AstGrepAvailable() {
		t.Skip("ast-grep not installed")
	}
	dir := t.TempDir()
	src := "package main\n\nimport \"errors\"\n\nfunc a() error { return errors.New(\"boom\") }\n"
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	grep := NewAstGrep(dir)
	out, err := grep.Execute(context.Background(), map[string]any{
		"pattern": "func _() { errors.New($MSG) }", "selector": "call_expression", "lang": "go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "a.go:5") {
		t.Fatalf("ast_grep missed the match: %s", out)
	}

	var detail string
	edit := NewAstEdit(dir, func(a Action) (bool, error) { detail = a.Detail; return true, nil })
	out, err = edit.Execute(context.Background(), map[string]any{
		"pattern": "func _() { errors.New($MSG) }", "selector": "call_expression",
		"rewrite": "fmt.Errorf($MSG)", "lang": "go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "failed") || strings.Contains(out, "no changes") {
		t.Fatalf("ast_edit did not rewrite: %s", out)
	}
	if !strings.Contains(detail, "fmt.Errorf") {
		t.Errorf("approval preview did not show the replacement: %q", detail)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "a.go"))
	if !strings.Contains(string(data), "fmt.Errorf(\"boom\")") {
		t.Fatalf("rewrite not applied: %s", data)
	}

	// A denied edit must leave the file alone.
	denied := NewAstEdit(dir, func(Action) (bool, error) { return false, nil })
	if _, err := denied.Execute(context.Background(), map[string]any{
		"pattern": "func _() { fmt.Errorf($MSG) }", "selector": "call_expression",
		"rewrite": "errors.New($MSG)", "lang": "go",
	}); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(dir, "a.go"))
	if !strings.Contains(string(data), "fmt.Errorf") {
		t.Error("denied ast_edit still wrote to the file")
	}
}
