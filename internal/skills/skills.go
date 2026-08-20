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

// Descriptor is the stable skill catalog entry shared with the primary
// assistant and hidden workers.
type Descriptor struct {
	Name        string
	Description string
	Source      string
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

// LoadDir loads skills from a directory. It handles both flat .md files and
// subdirectories containing a SKILL.md file. Non-skill files are silently skipped.
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
		var path string
		if e.IsDir() {
			// Check for SKILL.md inside the subdirectory
			path = filepath.Join(dir, e.Name(), "SKILL.md")
			if _, err := os.Stat(path); err != nil {
				continue
			}
		} else if strings.HasSuffix(e.Name(), ".md") {
			path = filepath.Join(dir, e.Name())
		} else {
			continue
		}
		s, err := LoadFile(path)
		if err != nil {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

// Load discovers skills from user-global and project-local sources.
// Project-local skills take precedence on name conflict.
func Load(workDir string) []Skill {
	projectDir := filepath.Join(workDir, ".forge", "skills")
	byName := make(map[string]Skill)

	if h, err := os.UserHomeDir(); err == nil {
		// Load user-global skills (medium priority)
		globalDir := filepath.Join(h, ".config", "forge", "skills")
		if global, err := LoadDir(globalDir); err == nil {
			for _, s := range global {
				byName[s.Name] = s
			}
		}
	}

	// Load project-local (highest priority, overwrites)
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

// Descriptors returns a stable descriptor list for loaded skills.
func Descriptors(skills []Skill) []Descriptor {
	if len(skills) == 0 {
		return nil
	}
	descriptors := make([]Descriptor, 0, len(skills))
	for _, s := range skills {
		descriptors = append(descriptors, Descriptor{
			Name:        s.Name,
			Description: s.Description,
			Source:      s.Source,
		})
	}
	sort.Slice(descriptors, func(i, j int) bool { return descriptors[i].Name < descriptors[j].Name })
	return descriptors
}

// Describe returns a formatted string listing available skills for the system prompt.
func Describe(skills []Skill) string {
	if len(skills) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("Available skills (activate with /skillname):\n")
	for _, d := range Descriptors(skills) {
		fmt.Fprintf(&sb, "  - /%s: %s\n", d.Name, d.Description)
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
