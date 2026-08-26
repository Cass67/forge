package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSkillFromFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := "---\nname: tdd\ndescription: Write tests first\n---\n\nAlways write a failing test before implementation."
	if err := os.WriteFile(filepath.Join(dir, "tdd.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	skill, err := LoadFile(filepath.Join(dir, "tdd.md"))
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	if skill.Name != "tdd" {
		t.Fatalf("Name = %q, want %q", skill.Name, "tdd")
	}
	if skill.Description != "Write tests first" {
		t.Fatalf("Description = %q, want %q", skill.Description, "Write tests first")
	}
	if want := "Always write a failing test before implementation."; skill.Body != want {
		t.Fatalf("Body = %q, want %q", skill.Body, want)
	}
}

func TestLoadFileMissingFrontmatter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.md"), []byte("no frontmatter here"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadFile(filepath.Join(dir, "bad.md"))
	if err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
}

func TestLoadFileMissingName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "noname.md"), []byte("---\ndescription: no name\n---\nbody"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadFile(filepath.Join(dir, "noname.md"))
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestLoadDirSkipsInvalidFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "good.md"), []byte("---\nname: good\ndescription: works\n---\nbody"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.md"), []byte("no frontmatter"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notmd.txt"), []byte("---\nname: x\ndescription: y\n---\nz"), 0644); err != nil {
		t.Fatal(err)
	}

	skills, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir() error = %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("got %d skills, want 1", len(skills))
	}
	if skills[0].Name != "good" {
		t.Fatalf("Name = %q, want %q", skills[0].Name, "good")
	}
}

func TestGitCloneURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want bool
	}{
		{"https://github.com/JetBrains/go-modern-guidelines", true},
		{"https://github.com/anthropics/claude-code", true},
		{"git@github.com:acme/repo.git", true},
		{"git@github.com:acme/repo", true},
		{"https://gitlab.com/group/proj", true},
		{"https://github.com/owner/repo.git", true},
		{"https://github.com/owner/repo/blob/main/myskill.md", false},
		{"https://raw.githubusercontent.com/owner/repo/main/skill.md", false},
		{"https://example.com/plain.md", false},
		{"./skills", false},
		{"/abs/path/skills", false},
		{"https://github.com/blah", false}, // need owner/repo
		{"", false},
	}
	for _, c := range cases {
		gotURL, ok := gitCloneURL(c.in)
		if ok != c.want {
			t.Errorf("gitCloneURL(%q) ok=%v, want %v", c.in, ok, c.want)
			continue
		}
		if ok && gotURL != c.in {
			t.Errorf("gitCloneURL(%q) = %q, want input", c.in, gotURL)
		}
	}
}

func TestInstallFromDirRecursive(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	// Fork the marketplace-style layout: plugin/skills/<name>/SKILL.md
	sub := filepath.Join(src, "plugin", "skills", "use-modern-go")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: use-modern-go\ndescription: modern go style\n---\nUse modern Go."
	if err := os.WriteFile(filepath.Join(sub, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	// A sibling doc next to SKILL.md must not be double-installed.
	if err := os.WriteFile(filepath.Join(sub, "INDEX.md"), []byte("---\nname: index\ndescription: ignored\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Non-frontmatter README at the tree root is skipped.
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("plain readme"), 0o644); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	skills, err := installFromDir(src, dest)
	if err != nil {
		t.Fatalf("installFromDir() error = %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("installed %d skills, want 1 (%v)", len(skills), skills)
	}
	if skills[0].Name != "use-modern-go" {
		t.Fatalf("Name = %q, want %q", skills[0].Name, "use-modern-go")
	}
	// SKILL.md is a bundled skill: it installs as dest/<name>/, not a flat file.
	if skills[0].Dir == "" {
		t.Fatal("SKILL.md skill should install as a bundle dir")
	}
	if _, err := os.Stat(filepath.Join(skills[0].Dir, "SKILL.md")); err != nil {
		t.Fatalf("installed SKILL.md missing: %v", err)
	}
}

func TestLoadDirNotExist(t *testing.T) {
	t.Parallel()
	skills, err := LoadDir("/nonexistent/path")
	if err != nil {
		t.Fatalf("LoadDir() error = %v", err)
	}
	if len(skills) != 0 {
		t.Fatalf("got %d skills, want 0", len(skills))
	}
}

func TestResolveBodyDir(t *testing.T) {
	t.Parallel()
	flat := Skill{Name: "flat", Body: "run <skill-dir>/x.sh"}
	if got := flat.ResolveBody(); got != "run <skill-dir>/x.sh" {
		t.Fatalf("flat ResolveBody = %q, want untouched", got)
	}
	bundled := Skill{Name: "b", Body: "sh \"<skill-dir>/scripts/run-tool.sh\" list", Dir: "/tmp/skills/b"}
	if got := bundled.ResolveBody(); got != "sh \"/tmp/skills/b/scripts/run-tool.sh\" list" {
		t.Fatalf("bundled ResolveBody = %q", got)
	}
}

func TestInstallBundledSkillPreservesAssets(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	bundleDir := filepath.Join(src, "plugin", "skills", "mytool")
	if err := os.MkdirAll(filepath.Join(bundleDir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "SKILL.md"),
		[]byte("---\nname: mytool\ndescription: bundled tool\n---\nsh \"<skill-dir>/scripts/run-tool.sh\""), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho tool"
	if err := os.WriteFile(filepath.Join(bundleDir, "scripts", "run-tool.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	workDir := t.TempDir()
	dest := filepath.Join(workDir, ".forge", "skills")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	skills, err := installFromDir(src, dest)
	if err != nil {
		t.Fatalf("installFromDir() error = %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("installed %d skills, want 1", len(skills))
	}
	s := skills[0]
	if s.Dir == "" {
		t.Fatal("bundled skill Dir should be set")
	}
	// The scripts/ asset must have survived, not been flattened away.
	if _, err := os.Stat(filepath.Join(s.Dir, "scripts", "run-tool.sh")); err != nil {
		t.Fatalf("bundled asset missing: %v", err)
	}
	// LoadDir must reconstruct the same skill with its Dir recorded.
	loaded, err := LoadDir(dest)
	if err != nil {
		t.Fatalf("LoadDir() error = %v", err)
	}
	var ls *Skill
	for i := range loaded {
		if loaded[i].Name == s.Name {
			ls = &loaded[i]
		}
	}
	if ls == nil {
		t.Fatalf("bundled skill not loaded: %v", loaded)
	}
	if ls.Dir != s.Dir {
		t.Fatalf("reloaded Dir = %q, want %q", ls.Dir, s.Dir)
	}
	if got := ls.ResolveBody(); got != "sh \"<skill-dir>/scripts/run-tool.sh\"" && !strings.Contains(got, s.Dir) {
		t.Fatalf("reloaded ResolveBody does not reference installed dir: %q", got)
	}
	// Removing by name removes the whole bundle directory.
	removed, err := RemoveByName(workDir, s.Name)
	if err != nil {
		t.Fatalf("RemoveByName() error = %v", err)
	}
	if removed != s.Dir {
		t.Fatalf("removed = %q, want %q", removed, s.Dir)
	}
	if _, err := os.Stat(s.Dir); !os.IsNotExist(err) {
		t.Fatalf("bundle dir still exists after remove: %v", err)
	}
}

func TestProjectLocalOverridesGlobal(t *testing.T) {
	t.Parallel()
	globalDir := t.TempDir()
	localDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(globalDir, "review.md"),
		[]byte("---\nname: review\ndescription: global review\n---\nglobal body"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "review.md"),
		[]byte("---\nname: review\ndescription: project review\n---\nproject body"), 0644); err != nil {
		t.Fatal(err)
	}

	byName := make(map[string]Skill)
	global, _ := LoadDir(globalDir)
	for _, s := range global {
		byName[s.Name] = s
	}
	local, _ := LoadDir(localDir)
	for _, s := range local {
		byName[s.Name] = s
	}

	got := byName["review"]
	if got.Description != "project review" {
		t.Fatalf("Description = %q, want %q (project-local should win)", got.Description, "project review")
	}
}

func TestDescribe(t *testing.T) {
	t.Parallel()
	skills := []Skill{
		{Name: "tdd", Description: "Write tests first"},
		{Name: "review", Description: "Code review checklist"},
	}
	got := Describe(skills)
	if !strings.Contains(got, "/tdd") || !strings.Contains(got, "/review") {
		t.Fatalf("Describe() = %q, want to contain /tdd and /review", got)
	}
}

func TestDescribeEmpty(t *testing.T) {
	t.Parallel()
	if got := Describe(nil); got != "" {
		t.Fatalf("Describe(nil) = %q, want empty", got)
	}
}

func TestDescriptors(t *testing.T) {
	t.Parallel()
	got := Descriptors([]Skill{
		{Name: "systematic-debugging", Description: "debug carefully", Source: "/tmp/debug/SKILL.md"},
		{Name: "brainstorming", Description: "plan first", Source: "/tmp/brain/SKILL.md"},
	})

	if len(got) != 2 {
		t.Fatalf("len(Descriptors()) = %d, want 2", len(got))
	}
	if got[0] != (Descriptor{Name: "brainstorming", Description: "plan first", Source: "/tmp/brain/SKILL.md"}) {
		t.Fatalf("first descriptor = %#v", got[0])
	}
	if got[1] != (Descriptor{Name: "systematic-debugging", Description: "debug carefully", Source: "/tmp/debug/SKILL.md"}) {
		t.Fatalf("second descriptor = %#v", got[1])
	}
}

func TestGetSkill(t *testing.T) {
	t.Parallel()
	skills := []Skill{
		{Name: "test-driven-development", Description: "TDD", Body: "b"},
		{Name: "systematic-debugging", Description: "Debug", Body: "b"},
		{Name: "brainstorming", Description: "Plan", Body: "b"},
	}
	// Exact match.
	if s, ok := Get(skills, "test-driven-development"); !ok || s.Name != "test-driven-development" {
		t.Fatal("exact match failed")
	}
	// Initials match.
	if s, ok := Get(skills, "tdd"); !ok || s.Name != "test-driven-development" {
		t.Fatal("initials match for tdd failed")
	}
	if s, ok := Get(skills, "sd"); !ok || s.Name != "systematic-debugging" {
		t.Fatal("initials match for sd failed")
	}
	// Prefix match.
	if s, ok := Get(skills, "brain"); !ok || s.Name != "brainstorming" {
		t.Fatal("prefix match for brain failed")
	}
	// No match.
	if _, ok := Get(skills, "nope"); ok {
		t.Fatal("Get(nope) should not be found")
	}
	// Ambiguous prefix should not match (both "systematic-debugging" and "brainstorming"
	// would be returned if prefix checked against substrings, but only one starts with "b").
	ambiguous := []Skill{
		{Name: "test-alpha", Description: "a", Body: "b"},
		{Name: "test-beta", Description: "b", Body: "b"},
	}
	if _, ok := Get(ambiguous, "test"); ok {
		t.Fatal("ambiguous prefix 'test' should not match")
	}
}
