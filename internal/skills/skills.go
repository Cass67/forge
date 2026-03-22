package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
			continue
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
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
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
		fmt.Fprintf(&sb, "  - /%s: %s\n", s.Name, s.Description)
	}
	return sb.String()
}

// Get finds a skill by exact name, or by unambiguous prefix/abbreviation.
// For example, "tdd" matches "test-driven-development" if it's the only skill
// whose initials or name starts with "tdd".
func Get(skills []Skill, name string) (Skill, bool) {
	// Exact match first.
	for _, s := range skills {
		if s.Name == name {
			return s, true
		}
	}
	lower := strings.ToLower(name)
	// Prefix match (e.g. "test" matches "test-driven-development").
	var prefixMatches []Skill
	for _, s := range skills {
		if strings.HasPrefix(strings.ToLower(s.Name), lower) {
			prefixMatches = append(prefixMatches, s)
		}
	}
	if len(prefixMatches) == 1 {
		return prefixMatches[0], true
	}
	// Initials match (e.g. "tdd" matches "test-driven-development").
	for _, s := range skills {
		if skillInitials(s.Name) == lower {
			return s, true
		}
	}
	return Skill{}, false
}

// skillInitials returns the lowercase initials of a hyphen-separated skill name.
// e.g. "test-driven-development" -> "tdd", "systematic-debugging" -> "sd".
func skillInitials(name string) string {
	parts := strings.Split(name, "-")
	var b strings.Builder
	for _, p := range parts {
		if len(p) > 0 {
			b.WriteByte(p[0])
		}
	}
	return strings.ToLower(b.String())
}
