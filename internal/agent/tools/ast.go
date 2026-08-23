package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// ast-grep drives the structural search and rewrite tools. It is an external
// binary rather than a linked library: the Go tree-sitter bindings need cgo
// and a grammar per language, which would cost more than the feature is worth.
// Both tools stay unregistered when the binary is missing, so the model never
// sees a tool it cannot run.
const astGrepBinary = "ast-grep"

// astMatch is the subset of ast-grep's --json=stream records we use.
type astMatch struct {
	Text        string `json:"text"`
	File        string `json:"file"`
	Lines       string `json:"lines"`
	Replacement string `json:"replacement"`
	Range       struct {
		Start struct {
			Line   int `json:"line"`
			Column int `json:"column"`
		} `json:"start"`
	} `json:"range"`
}

// line is 1-indexed; ast-grep reports 0-indexed rows.
func (m astMatch) line() int { return m.Range.Start.Line + 1 }

// AstGrepAvailable reports whether the ast-grep binary can be found.
func AstGrepAvailable() bool { return findBinary(astGrepBinary) != "" }

func NewAstGrep(workDir string, policies ...SecretPolicy) Tool {
	secretPolicy := secretPolicyFromOptions(policies)
	return Tool{
		Name:        "ast_grep",
		Description: "Search code structurally with ast-grep patterns. The pattern is source code with $META variables standing in for subtrees — 'if ($C) { $$$BODY }' matches any if statement. Use this instead of regex when the shape of the code matters.",
		Parameters: []ParameterDef{
			{Name: "pattern", Type: "string", Description: "ast-grep pattern, e.g. 'foo($A, $B)' or 'func $NAME($$$) error { $$$ }'", Required: true},
			{Name: "path", Type: "string", Description: "file or directory to search (default .)", Required: false},
			{Name: "lang", Type: "string", Description: "language override, e.g. go, ts, tsx, python, rust; inferred from file extensions when omitted", Required: false},
			{Name: "selector", Type: "string", Description: "node kind to extract from the pattern, e.g. call_expression. Needed when the pattern only parses inside surrounding code — see the empty-result hint", Required: false},
			{Name: "max_results", Type: "int", Description: "cap on reported matches (default 40)", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			pattern := strings.TrimSpace(stringArg(args, "pattern"))
			if pattern == "" {
				return "ast_grep failed: pattern is required", nil
			}
			target, err := astTargetPath(workDir, stringArg(args, "path"), false)
			if err != nil {
				return "", err
			}
			limit := 40
			if v, ok := args["max_results"].(float64); ok && v > 0 {
				limit = int(v)
			}

			matches, err := runAstGrep(ctx, workDir, astQuery{
				pattern:  pattern,
				lang:     stringArg(args, "lang"),
				selector: stringArg(args, "selector"),
			}, target, false)
			if err != nil {
				return fmt.Sprintf("ast_grep failed: %v", err), nil
			}
			if len(matches) == 0 {
				return fmt.Sprintf("no structural matches for %q%s", pattern, astContextHint(pattern, stringArg(args, "selector"))), nil
			}

			var sb strings.Builder
			for i, m := range matches {
				if i == limit {
					fmt.Fprintf(&sb, "... %d more matches not shown; narrow the pattern or raise max_results\n", len(matches)-limit)
					break
				}
				fmt.Fprintf(&sb, "%s:%d\n%s\n\n", astRelPath(workDir, m.File), m.line(), strings.TrimRight(m.Text, "\n"))
			}
			out, blocked := secretPolicy.ApplyRead(sb.String())
			if blocked {
				return out, nil
			}
			return fmt.Sprintf("%d match(es)\n\n%s", len(matches), out), nil
		},
	}
}

func NewAstEdit(workDir string, approve ApprovalFunc, policies ...SecretPolicy) Tool {
	var lastDiff string
	secretPolicy := secretPolicyFromOptions(policies)
	return Tool{
		Name:        "ast_edit",
		Description: "Rewrite every structural match of an ast-grep pattern. Both pattern and rewrite are source code with $META variables; captures bound by the pattern are substituted into the rewrite. Changes are previewed for approval before anything is written.",
		Parameters: []ParameterDef{
			{Name: "pattern", Type: "string", Description: "ast-grep pattern to match, e.g. 'errors.New($MSG)'", Required: true},
			{Name: "rewrite", Type: "string", Description: "replacement source, e.g. 'fmt.Errorf($MSG)'", Required: true},
			{Name: "path", Type: "string", Description: "file or directory to rewrite (default .)", Required: false},
			{Name: "lang", Type: "string", Description: "language override; inferred from file extensions when omitted", Required: false},
			{Name: "selector", Type: "string", Description: "node kind to extract from the pattern, e.g. call_expression", Required: false},
		},
		AutoApprove:      false,
		Concurrency:      ToolConcurrencySerial,
		MutatesWorkspace: true,
		LastDiff: func() string {
			d := lastDiff
			lastDiff = ""
			return d
		},
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			lastDiff = ""
			pattern := strings.TrimSpace(stringArg(args, "pattern"))
			rewrite := stringArg(args, "rewrite")
			if pattern == "" || strings.TrimSpace(rewrite) == "" {
				return "ast_edit failed: pattern and rewrite are both required", nil
			}
			if checked, blocked := secretPolicy.ApplyWrite(rewrite); blocked {
				return checked, nil
			}
			// The rewrite lands in files, so unlike ast_grep it may not
			// escape the working directory.
			target, err := astTargetPath(workDir, stringArg(args, "path"), true)
			if err != nil {
				return "", err
			}
			query := astQuery{
				pattern:  pattern,
				rewrite:  rewrite,
				lang:     stringArg(args, "lang"),
				selector: stringArg(args, "selector"),
			}

			matches, err := runAstGrep(ctx, workDir, query, target, false)
			if err != nil {
				return fmt.Sprintf("ast_edit failed: %v", err), nil
			}
			if len(matches) == 0 {
				return fmt.Sprintf("ast_edit made no changes: nothing matches %q%s", pattern, astContextHint(pattern, query.selector)), nil
			}

			// ast-grep reports paths relative to its working directory.
			files := astFilesTouched(workDir, matches)
			before := make(map[string]string, len(files))
			for _, file := range files {
				data, err := os.ReadFile(file)
				if err != nil {
					return fmt.Sprintf("ast_edit failed: %v", err), nil
				}
				before[file] = string(data)
			}

			preview := astPreview(workDir, matches)
			approved, err := approve(Action{
				Context: ctx,
				Tool:    "ast_edit",
				Summary: fmt.Sprintf("rewrite %d match(es) across %d file(s)", len(matches), len(files)),
				Detail:  preview,
				Path:    astRelPath(workDir, target),
			})
			if err != nil {
				return "", err
			}
			if !approved {
				return "ast_edit denied by user", nil
			}

			if _, err := runAstGrep(ctx, workDir, query, target, true); err != nil {
				return fmt.Sprintf("ast_edit failed: %v", err), nil
			}

			var diffs []string
			for _, file := range files {
				data, err := os.ReadFile(file)
				if err != nil {
					continue
				}
				if diff := simpleDiff(before[file], string(data), astRelPath(workDir, file)); strings.TrimSpace(diff) != "" {
					diffs = append(diffs, diff)
				}
			}
			lastDiff = strings.Join(diffs, "\n")
			return fmt.Sprintf("ast_edit rewrote %d match(es) in %d file(s): %s", len(matches), len(files), strings.Join(astRelPaths(workDir, files), ", ")), nil
		},
	}
}

type astQuery struct {
	pattern  string
	rewrite  string
	lang     string
	selector string
}

// runAstGrep runs one ast-grep invocation. With update set it applies the
// rewrite in place and returns no matches; otherwise it reports what would
// change.
func runAstGrep(ctx context.Context, workDir string, q astQuery, target string, update bool) ([]astMatch, error) {
	bin := findBinary(astGrepBinary)
	if bin == "" {
		return nil, fmt.Errorf("ast-grep is not installed — `brew install ast-grep` or `npm i -g @ast-grep/cli`")
	}
	argv := []string{"run", "--pattern", q.pattern}
	if q.rewrite != "" {
		argv = append(argv, "--rewrite", q.rewrite)
	}
	if lang := strings.TrimSpace(q.lang); lang != "" {
		argv = append(argv, "--lang", lang)
	}
	if selector := strings.TrimSpace(q.selector); selector != "" {
		argv = append(argv, "--selector", selector)
	}
	if update {
		argv = append(argv, "--update-all")
	} else {
		argv = append(argv, "--json=stream")
	}
	argv = append(argv, target)

	cmd := exec.CommandContext(ctx, bin, argv...)
	cmd.Dir = workDir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return nil, fmt.Errorf("%s", detail)
	}
	if update {
		return nil, nil
	}

	return decodeAstMatches(strings.NewReader(string(out)))
}

// decodeAstMatches reads ast-grep's --json=stream output: one JSON object per
// match, concatenated.
func decodeAstMatches(r io.Reader) ([]astMatch, error) {
	var matches []astMatch
	dec := json.NewDecoder(r)
	for {
		var m astMatch
		if err := dec.Decode(&m); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("could not read ast-grep output: %w", err)
		}
		matches = append(matches, m)
	}
	return matches, nil
}

// astContextHint explains the trap that eats most first attempts: several
// grammars — Go's among them — cannot parse a bare expression, so a pattern
// like "errors.New($M)" parses as an error node and silently matches nothing.
// Wrapping it in enough code to parse and naming the node kind fixes it.
func astContextHint(pattern, selector string) string {
	if strings.TrimSpace(selector) != "" {
		return ""
	}
	call := strings.Index(pattern, "(")
	if call <= 0 || !strings.Contains(pattern[:call], ".") || strings.Contains(pattern, "\n") {
		return ""
	}
	return fmt.Sprintf("\n\nHint: a bare dotted call does not parse as a pattern in every language. Retry with the pattern wrapped in surrounding code and the node kind named, e.g. pattern \"func _() { %s }\" with selector \"call_expression\".", pattern)
}

func astPreview(workDir string, matches []astMatch) string {
	var sb strings.Builder
	for i, m := range matches {
		if i == 20 {
			fmt.Fprintf(&sb, "... and %d more\n", len(matches)-20)
			break
		}
		fmt.Fprintf(&sb, "%s:%d\n- %s\n+ %s\n\n", astRelPath(workDir, m.File), m.line(),
			strings.TrimRight(m.Text, "\n"), strings.TrimRight(m.Replacement, "\n"))
	}
	return sb.String()
}

func astFilesTouched(workDir string, matches []astMatch) []string {
	seen := make(map[string]bool, len(matches))
	var files []string
	for _, m := range matches {
		if m.File == "" {
			continue
		}
		path := m.File
		if !filepath.IsAbs(path) {
			path = filepath.Join(workDir, path)
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		files = append(files, path)
	}
	sort.Strings(files)
	return files
}

// astTargetPath resolves the search root. ast-grep reports paths relative to
// its working directory, which is why the result is passed through unchanged
// for the relative case.
func astTargetPath(workDir, path string, mustStayInside bool) (string, error) {
	if strings.TrimSpace(path) == "" {
		return ".", nil
	}
	if mustStayInside {
		return ResolvePath(workDir, path)
	}
	return ResolvePathAllowEscape(workDir, path)
}

func astRelPath(workDir, path string) string {
	if !filepath.IsAbs(path) {
		return path
	}
	if rel, err := filepath.Rel(workDir, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

func astRelPaths(workDir string, paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, astRelPath(workDir, p))
	}
	return out
}

// findBinary locates an executable on PATH, falling back to the install
// directories package managers use but login shells often leave out of a GUI
// app's environment.
func findBinary(name string) string {
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	home, _ := os.UserHomeDir()
	for _, dir := range []string{"/opt/homebrew/bin", "/usr/local/bin", filepath.Join(home, ".local", "bin"), filepath.Join(home, ".cargo", "bin")} {
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate
		}
	}
	return ""
}
