# Skills System Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a skills system that loads markdown files with frontmatter and injects them into the system prompt on demand via slash commands.

**Architecture:** Skills are markdown files in `~/.config/forge/skills/` (user-global) or `.forge/skills/` (project-local). A new `internal/skills/` package handles loading and parsing. The system prompt in `agent/system.go` lists available skills. Users activate skills via `/skillname` slash commands in both TUI and console modes.

**Tech Stack:** Go stdlib, `gopkg.in/yaml.v3` for frontmatter parsing (already available as transitive dep — if not, use manual parsing to avoid new deps).

---

## File Structure

| Action | File | Responsibility |
|--------|------|----------------|
| Create | `internal/skills/skills.go` | Skill struct, Load/List functions, frontmatter parsing |
| Create | `internal/skills/skills_test.go` | Tests for loading, merging, edge cases |
| Modify | `internal/agent/system.go` | Append available skills section to system prompt |
| Modify | `internal/agent/agent.go:24-33` | Accept skills in constructor, store on Agent, add InjectSkill method |
| Modify | `internal/runtime/chat.go:85-104` | Load skills, pass to Agent, wire slash commands |
| Modify | `internal/tui/chatlive_commands.go:12-218` | Add `/skill` slash command handling in TUI |
| Modify | `internal/tui/chatlive.go` | Add skills field to chatLiveModel, pass through ChatLiveConfig |

---

## Chunk 1: Skills Package

### Task 1: Skills Loader

**Files:**
- Create: `internal/skills/skills.go`
- Create: `internal/skills/skills_test.go`

- [ ] **Step 1: Write failing test for loading a single skill file**

Create `internal/skills/skills_test.go`:

```go
package skills

import (
	"os"
	"path/filepath"
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/skills/ -run TestLoadSkillFromFile -v`
Expected: FAIL — package does not exist

- [ ] **Step 3: Write minimal implementation**

Create `internal/skills/skills.go`:

```go
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Skill represents a loaded skill file.
type Skill struct {
	Name        string
	Description string
	Body        string
	Source      string // file path it was loaded from
}

// LoadFile parses a single skill markdown file with YAML-style frontmatter.
func LoadFile(path string) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}
	return parse(string(data), path)
}

func parse(content, source string) (Skill, error) {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return Skill{}, fmt.Errorf("skill %s: missing frontmatter", source)
	}
	rest := content[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return Skill{}, fmt.Errorf("skill %s: unterminated frontmatter", source)
	}
	frontmatter := rest[:end]
	body := strings.TrimSpace(rest[end+4:])

	s := Skill{Source: source, Body: body}
	for _, line := range strings.Split(frontmatter, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch key {
		case "name":
			s.Name = val
		case "description":
			s.Description = val
		}
	}
	if s.Name == "" {
		return Skill{}, fmt.Errorf("skill %s: missing name in frontmatter", source)
	}
	return s, nil
}

// LoadDir loads all .md files from a directory. Non-skill files are silently skipped.
func LoadDir(dir string) ([]Skill, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Skill
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		s, err := LoadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue // skip invalid files
		}
		out = append(out, s)
	}
	return out, nil
}

// Load discovers skills from project-local and user-global directories.
// Project-local skills take precedence over user-global on name conflict.
func Load(workDir string) []Skill {
	projectDir := filepath.Join(workDir, ".forge", "skills")
	homeDir := ""
	if h, err := os.UserHomeDir(); err == nil {
		homeDir = filepath.Join(h, ".config", "forge", "skills")
	}

	byName := make(map[string]Skill)

	// Load user-global first (lower priority)
	if homeDir != "" {
		if global, err := LoadDir(homeDir); err == nil {
			for _, s := range global {
				byName[s.Name] = s
			}
		}
	}

	// Load project-local second (higher priority, overwrites)
	if local, err := LoadDir(projectDir); err == nil {
		for _, s := range local {
			byName[s.Name] = s
		}
	}

	out := make([]Skill, 0, len(byName))
	for _, s := range byName {
		out = append(out, s)
	}
	return out
}

// Describe returns a formatted string listing available skills for the system prompt.
func Describe(skills []Skill) string {
	if len(skills) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("Available skills (activate with /skillname):\n")
	for _, s := range skills {
		sb.WriteString(fmt.Sprintf("  - /%s: %s\n", s.Name, s.Description))
	}
	return sb.String()
}

// Get finds a skill by name. Returns the skill and true if found.
func Get(skills []Skill, name string) (Skill, bool) {
	for _, s := range skills {
		if s.Name == name {
			return s, true
		}
	}
	return Skill{}, false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/skills/ -run TestLoadSkillFromFile -v`
Expected: PASS

- [ ] **Step 5: Write tests for LoadDir and Load (project-local overrides global)**

Add to `internal/skills/skills_test.go`:

```go
func TestLoadDirSkipsInvalidFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "good.md"), []byte("---\nname: good\ndescription: works\n---\nbody"), 0644)
	os.WriteFile(filepath.Join(dir, "bad.md"), []byte("no frontmatter"), 0644)
	os.WriteFile(filepath.Join(dir, "notmd.txt"), []byte("---\nname: x\ndescription: y\n---\nz"), 0644)

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
	// Set up a fake workdir with .forge/skills/ containing an override
	workDir := t.TempDir()
	globalDir := t.TempDir()
	projectSkillsDir := filepath.Join(workDir, ".forge", "skills")
	os.MkdirAll(projectSkillsDir, 0755)

	// Global skill
	os.WriteFile(filepath.Join(globalDir, "review.md"),
		[]byte("---\nname: review\ndescription: global review\n---\nglobal body"), 0644)
	// Project-local skill with same name
	os.WriteFile(filepath.Join(projectSkillsDir, "review.md"),
		[]byte("---\nname: review\ndescription: project review\n---\nproject body"), 0644)

	// Test LoadDir individually since Load uses hardcoded paths
	global, _ := LoadDir(globalDir)
	local, _ := LoadDir(projectSkillsDir)

	byName := make(map[string]Skill)
	for _, s := range global {
		byName[s.Name] = s
	}
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

func TestGetSkill(t *testing.T) {
	t.Parallel()
	skills := []Skill{{Name: "tdd", Description: "d", Body: "b"}}
	if _, ok := Get(skills, "tdd"); !ok {
		t.Fatal("Get(tdd) not found")
	}
	if _, ok := Get(skills, "nope"); ok {
		t.Fatal("Get(nope) should not be found")
	}
}
```

- [ ] **Step 6: Run all skills tests**

Run: `go test ./internal/skills/ -v`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add internal/skills/skills.go internal/skills/skills_test.go
git commit -m "feat: add skills package with loader, parser, and discovery"
```

---

## Chunk 2: System Prompt + Agent Integration

### Task 2: Wire Skills into System Prompt

**Files:**
- Modify: `internal/agent/system.go:12-33`
- Modify: `internal/agent/agent.go:13-33`

- [ ] **Step 1: Update BuildSystemPrompt to accept skills**

In `internal/agent/system.go`, change the signature and append skills section:

```go
// Change: func BuildSystemPrompt(workDir string, registry *tools.Registry) string
// To:     func BuildSystemPrompt(workDir string, registry *tools.Registry, skillsDesc string) string

// Before the return, add:
if skillsDesc != "" {
    sb.WriteString("\n")
    sb.WriteString(skillsDesc)
    sb.WriteString("\n")
}
```

- [ ] **Step 2: Update Agent to store skills and accept them in constructor**

In `internal/agent/agent.go`, add a `skills` field and an `InjectSkill` method:

```go
// Add to Agent struct:
//   skills []skills.Skill

// Update NewAgent to accept skills:
// func NewAgent(driver llm.Driver, toolReg *tools.Registry, approve tools.ApprovalFunc,
//     workDir string, maxTurns int, renderer RenderTarget, loadedSkills []skills.Skill) *Agent

// In the constructor body, change BuildSystemPrompt call:
//   system: BuildSystemPrompt(workDir, toolReg, skills.Describe(loadedSkills)),

// Add method:
// func (a *Agent) InjectSkill(s skills.Skill) {
//     a.history = append(a.history, llm.Message{
//         Role:    llm.RoleUser,
//         Content: fmt.Sprintf("[Skill: %s]\n\n%s", s.Name, s.Body),
//     })
// }
```

- [ ] **Step 3: Update all NewAgent call sites in runtime/chat.go**

In `internal/runtime/chat.go`, update both `RunChatLive` (line 120) and `RunChatConsole` (line 209) to pass skills:

```go
// Before creating the agent, load skills:
loadedSkills := skills.Load(setup.WorkDir)

// Update NewAgent calls to pass loadedSkills
```

- [ ] **Step 4: Run tests to verify nothing is broken**

Run: `go test ./internal/agent/... ./internal/runtime/... -v`
Expected: All PASS (may need to update test call sites for NewAgent if any exist)

- [ ] **Step 5: Commit**

```bash
git add internal/agent/system.go internal/agent/agent.go internal/runtime/chat.go
git commit -m "feat: wire skills into system prompt and agent constructor"
```

---

### Task 3: Slash Command Integration

**Files:**
- Modify: `internal/tui/chatlive_commands.go:12-218`
- Modify: `internal/tui/chatlive.go` (add skills field to model)
- Modify: `internal/runtime/chat.go` (pass skills through ChatLiveConfig, handle in console mode)

- [ ] **Step 1: Add skills to ChatLiveConfig and chatLiveModel**

In `internal/tui/chatlive.go`, add a `Skills` field to `ChatLiveConfig` and store it on the model. Look at the existing `ChatLiveConfig` struct and add:

```go
Skills []skills.Skill
```

Store it on `chatLiveModel` during initialization.

- [ ] **Step 2: Add /skills list command to TUI**

In `internal/tui/chatlive_commands.go`, add a case before the `default:` branch:

```go
case input == "/skills":
    if len(m.skills) == 0 {
        m.display.flash = "no skills loaded"
        return
    }
    var sb strings.Builder
    sb.WriteString("Available skills:\n")
    for _, s := range m.skills {
        sb.WriteString(fmt.Sprintf("  /%s — %s\n", s.Name, s.Description))
    }
    m.panes.tools.buf += sb.String()
    m.invalidatePaneCache(&m.panes.tools)
    m.display.flash = fmt.Sprintf("%d skills available", len(m.skills))
```

- [ ] **Step 3: Handle /skillname activation in TUI**

Change the `default:` case in `handleSlashCommand` to check for skill matches before showing "unknown command":

```go
default:
    // Check if it's a skill name
    cmd := strings.TrimPrefix(input, "/")
    if s, ok := skills.Get(m.skills, cmd); ok {
        // Prepend skill body to next message as context
        m.display.flash = fmt.Sprintf("skill activated: %s", s.Name)
        // Send skill content + any trailing text as a message
        msg := fmt.Sprintf("[Skill: %s]\n\n%s", s.Name, s.Body)
        m.inputCh <- msg
        return
    }
    m.display.flash = fmt.Sprintf("unknown command: %s (try /help)", input)
```

- [ ] **Step 4: Handle /skillname in console mode**

In `internal/runtime/chat.go` `handleChatSlashCommand`, add skill lookup before the `default:` case. The function will need access to loaded skills (pass as parameter or store on setup).

- [ ] **Step 5: Pass skills through from runtime to TUI**

In `internal/runtime/chat.go`, set `liveCfg.Skills = loadedSkills` before calling `tui.RunChatLive`.

- [ ] **Step 6: Run all tests**

Run: `go test ./... -count=1`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add internal/tui/chatlive_commands.go internal/tui/chatlive.go internal/runtime/chat.go
git commit -m "feat: add /skills list and /skillname activation commands"
```

---

## Chunk 3: Help Text + Docs

### Task 4: Update Help and Documentation

**Files:**
- Modify: `internal/runtime/chat.go` (PrintChatHelp)
- Modify: `internal/tui/chatlive.go` (help overlay if it lists commands)
- Modify: `docs/skills-system.md` (mark as implemented)

- [ ] **Step 1: Add /skills to console help**

In `internal/runtime/chat.go` `PrintChatHelp()`, add:

```go
fmt.Println("    /skills         list available skills")
fmt.Println("    /<skill>        activate a skill")
```

- [ ] **Step 2: Add /skills to TUI help overlay if applicable**

Check if the TUI help overlay lists commands and add skills there too.

- [ ] **Step 3: Verify end-to-end manually**

Create a test skill file:

```bash
mkdir -p ~/.config/forge/skills
cat > ~/.config/forge/skills/tdd.md << 'EOF'
---
name: tdd
description: Test-driven development workflow
---

When implementing a feature:
1. Write a failing test first
2. Run it to confirm it fails
3. Write the minimal code to make it pass
4. Run tests to confirm they pass
5. Refactor if needed
EOF
```

Then run forge and verify `/skills` lists it and `/tdd` activates it.

- [ ] **Step 4: Run full test suite**

Run: `go test ./... -count=1`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/runtime/chat.go internal/tui/chatlive.go docs/skills-system.md
git commit -m "feat: add skills to help text and documentation"
```
