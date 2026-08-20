package skills

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const lockFileName = ".forge-skill-lock.json"

type Lockfile struct {
	Version int                   `json:"version"`
	Skills  map[string]LockRecord `json:"skills"`
}

type LockRecord struct {
	Name     string `json:"name"`
	File     string `json:"file"`
	Provider string `json:"provider,omitempty"`
	Source   string `json:"source,omitempty"`
	Origin   string `json:"origin,omitempty"`
}

type StatusEntry struct {
	Scope       string
	Name        string
	Description string
	File        string
	Provider    string
	Source      string
	Tracked     bool
}

func UserDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "forge", "skills"), nil
}

func ProjectDir(workDir string) string {
	return filepath.Join(workDir, ".forge", "skills")
}

func InstallFromSource(source, destDir string) ([]Skill, error) {
	var installed []Skill
	var records []LockRecord
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		skills, err := installFromURL(source, destDir)
		if err != nil {
			return nil, err
		}
		installed = skills
		for _, s := range installed {
			records = append(records, LockRecord{Name: s.Name, File: s.Source, Provider: "url", Source: source, Origin: source})
		}
	} else {
		info, err := os.Stat(source)
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			skills, err := installFromDir(source, destDir)
			if err != nil {
				return nil, err
			}
			installed = skills
			for _, s := range installed {
				records = append(records, LockRecord{Name: s.Name, File: s.Source, Provider: "dir", Source: source})
			}
		} else {
			skills, err := installFromFile(source, destDir)
			if err != nil {
				return nil, err
			}
			installed = skills
			for _, s := range installed {
				records = append(records, LockRecord{Name: s.Name, File: s.Source, Provider: "file", Source: source, Origin: source})
			}
		}
	}
	if err := mergeLockRecords(destDir, records); err != nil {
		return nil, err
	}
	return installed, nil
}

func InstallFromGitRepo(repoURL, repoSubdir, destDir string) ([]Skill, error) {
	tmpDir, err := os.MkdirTemp("", "forge-skills-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	if err := run(tmpDir, "git", "clone", "--depth=1", repoURL, "repo"); err != nil {
		return nil, err
	}
	sourceDir := filepath.Join(tmpDir, "repo")
	if repoSubdir != "" {
		sourceDir = filepath.Join(sourceDir, filepath.FromSlash(repoSubdir))
	}
	installed, err := installFromDir(sourceDir, destDir)
	if err != nil {
		return nil, err
	}
	var records []LockRecord
	for _, s := range installed {
		records = append(records, LockRecord{
			Name:     s.Name,
			File:     s.Source,
			Provider: "git",
			Source:   repoURL,
			Origin:   joinOrigin(repoURL, repoSubdir, s.Name),
		})
	}
	if err := mergeLockRecords(destDir, records); err != nil {
		return nil, err
	}
	return installed, nil
}

func Status(workDir string) ([]StatusEntry, error) {
	globalDir, err := UserDir()
	if err != nil {
		return nil, err
	}
	projectDir := ProjectDir(workDir)
	loaded := Load(workDir)
	projectLock, err := readLockfile(projectDir)
	if err != nil {
		return nil, err
	}
	globalLock, err := readLockfile(globalDir)
	if err != nil {
		return nil, err
	}
	var entries []StatusEntry
	for _, s := range loaded {
		entry := StatusEntry{
			Name:        s.Name,
			Description: s.Description,
			File:        s.Source,
		}
		if strings.HasPrefix(s.Source, projectDir) {
			entry.Scope = "project"
			if rec, ok := projectLock.Skills[s.Name]; ok {
				entry.Provider = rec.Provider
				entry.Source = rec.Source
				entry.Tracked = true
			}
		} else {
			entry.Scope = "global"
			if rec, ok := globalLock.Skills[s.Name]; ok {
				entry.Provider = rec.Provider
				entry.Source = rec.Source
				entry.Tracked = true
			}
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func Search(workDir, query string) []Skill {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil
	}
	var matches []Skill
	for _, s := range Load(workDir) {
		if strings.Contains(strings.ToLower(s.Name), query) || strings.Contains(strings.ToLower(s.Description), query) {
			matches = append(matches, s)
		}
	}
	return matches
}

func RemoveByName(workDir, name string) (string, error) {
	for _, s := range Load(workDir) {
		if s.Name != name {
			continue
		}
		if err := os.Remove(s.Source); err != nil {
			return "", err
		}
		_ = removeLockRecord(filepath.Dir(s.Source), name)
		return s.Source, nil
	}
	return "", fmt.Errorf("skill %q not found", name)
}

func installFromDir(sourceDir, destDir string) ([]Skill, error) {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return nil, err
	}
	var installed []Skill
	for _, e := range entries {
		path := filepath.Join(sourceDir, e.Name())
		if e.IsDir() {
			path = filepath.Join(path, "SKILL.md")
		} else if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		skill, err := copySkillFile(path, destDir)
		if err != nil {
			continue
		}
		installed = append(installed, skill)
	}
	if len(installed) == 0 {
		return nil, fmt.Errorf("no valid skill markdown files found in %s", sourceDir)
	}
	return installed, nil
}

func installFromFile(sourceFile, destDir string) ([]Skill, error) {
	skill, err := copySkillFile(sourceFile, destDir)
	if err != nil {
		return nil, err
	}
	return []Skill{skill}, nil
}

func installFromURL(sourceURL, destDir string) ([]Skill, error) {
	resp, err := http.Get(sourceURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download failed: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	skill, err := parse(string(body), sourceURL)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, err
	}
	dst := filepath.Join(destDir, skill.Name+".md")
	if err := os.WriteFile(dst, body, 0o644); err != nil {
		return nil, err
	}
	skill.Source = dst
	return []Skill{skill}, nil
}

func copySkillFile(src, destDir string) (Skill, error) {
	skill, err := LoadFile(src)
	if err != nil {
		return Skill{}, err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return Skill{}, err
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return Skill{}, err
	}
	dst := filepath.Join(destDir, skill.Name+".md")
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return Skill{}, err
	}
	skill.Source = dst
	return skill, nil
}

func readLockfile(dir string) (Lockfile, error) {
	path := filepath.Join(dir, lockFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Lockfile{Version: 1, Skills: map[string]LockRecord{}}, nil
		}
		return Lockfile{}, err
	}
	var lock Lockfile
	if err := json.Unmarshal(data, &lock); err != nil {
		return Lockfile{}, err
	}
	if lock.Version == 0 {
		lock.Version = 1
	}
	if lock.Skills == nil {
		lock.Skills = map[string]LockRecord{}
	}
	return lock, nil
}

func writeLockfile(dir string, lock Lockfile) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if lock.Version == 0 {
		lock.Version = 1
	}
	if lock.Skills == nil {
		lock.Skills = map[string]LockRecord{}
	}
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(dir, lockFileName), data, 0o644)
}

func mergeLockRecords(dir string, records []LockRecord) error {
	lock, err := readLockfile(dir)
	if err != nil {
		return err
	}
	for _, rec := range records {
		lock.Skills[rec.Name] = rec
	}
	return writeLockfile(dir, lock)
}

func removeLockRecord(dir, name string) error {
	lock, err := readLockfile(dir)
	if err != nil {
		return err
	}
	delete(lock.Skills, name)
	return writeLockfile(dir, lock)
}

func joinOrigin(repoURL, repoSubdir, name string) string {
	origin := repoURL
	if repoSubdir != "" {
		origin += "#" + filepath.ToSlash(repoSubdir)
	}
	if name != "" {
		origin += ":" + name
	}
	return origin
}

func run(dir string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(output.String())
		if msg != "" {
			return fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, msg)
		}
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}
