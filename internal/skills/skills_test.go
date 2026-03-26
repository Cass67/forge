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
