package gui

import (
	"fmt"
	"strings"

	"forge/internal/skills"
)

// SkillInstallScope controls where an installed skill lands: the user-global
// skills directory (available in every workspace) or this workspace's project
// directory.
type SkillInstallScope string

const (
	SkillScopeGlobal  SkillInstallScope = "global"
	SkillScopeProject SkillInstallScope = "project"
)

// InstallSkill fetches a skill from a raw markdown URL, a github owner/repo
// reference, or a local file/dir and installs it, returning the freshly loaded
// skill list so the frontend can refresh its settings pane and palette.
func (s *Service) InstallSkill(source string, scope SkillInstallScope) ([]SkillPayload, error) {
	dest, err := skillDest(scope, s.currentDir())
	if err != nil {
		return nil, err
	}
	installed, err := installSkillSource(source, dest)
	if err != nil {
		return nil, err
	}
	if len(installed) == 0 {
		return nil, fmt.Errorf("installed no skills from %q", source)
	}
	return skillPayloads(skills.Load(s.currentDir())), nil
}

// RemoveSkill deletes the named skill wherever it lives and returns the fresh
// skill list. Removing a tracked skill also drops its lockfile record.
func (s *Service) RemoveSkill(name string) ([]SkillPayload, error) {
	if _, err := skills.RemoveByName(s.currentDir(), name); err != nil {
		return nil, err
	}
	return skillPayloads(skills.Load(s.currentDir())), nil
}

// Remember pins a user-supplied fact into the active session's memory, the GUI
// counterpart of the TUI's "/remember <text>".
func (s *Service) Remember(text string) string {
	cfg, _, ready := s.snapshot()
	text = strings.TrimSpace(text)
	if text == "" {
		return "usage: /remember <text>"
	}
	if !ready || cfg.Remember == nil {
		return "nothing to remember (runtime not ready)"
	}
	if cfg.Remember(text) {
		return "remembered (pinned)"
	}
	return "nothing to remember"
}

// skillDest resolves an install scope to a destination directory.
func skillDest(scope SkillInstallScope, workDir string) (string, error) {
	switch scope {
	case SkillScopeProject:
		return skills.ProjectDir(workDir), nil
	case SkillScopeGlobal:
		return skills.UserDir()
	default:
		return "", fmt.Errorf("unknown skill scope %q", scope)
	}
}

// installSkillSource pigeonholes a source into the matching installer:
// raw http(s) markdown or local file/dir via InstallFromSource, github
// owner/repo / bare repo refs via InstallFromGitRepo.
func installSkillSource(source, dest string) ([]skills.Skill, error) {
	trimmed := strings.TrimSpace(source)
	if trimmed == "" {
		return nil, fmt.Errorf("empty skill source")
	}
	if isGitReference(trimmed) {
		repo, subdir := splitGitReference(trimmed)
		return skills.InstallFromGitRepo(repo, subdir, dest)
	}
	return skills.InstallFromSource(trimmed, dest)
}

// isGitReference reports whether source names a git repository to clone rather
// than a raw markdown file/dir or local path to copy. http(s) URLs are handled
// by InstallFromSource (which fetches raw content), so they are excluded here.
func isGitReference(source string) bool {
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		return false
	}
	return strings.HasPrefix(source, "git@") ||
		strings.HasSuffix(source, ".git") ||
		strings.HasPrefix(source, "github:") ||
		// github "owner/repo[:subdir]" shorthand — owner has no dot, so a local
		// path like ./skills or /abs/path never matches.
		(strings.Contains(source, "/") &&
			!strings.Contains(strings.Split(source, "/")[0], ".") &&
			!strings.HasPrefix(source, "/") &&
			!strings.HasPrefix(source, "."))
}

// splitGitReference turns "owner/repo[:subdir]" into the clone URL and subdir.
func splitGitReference(source string) (repo, subdir string) {
	source = strings.TrimPrefix(source, "github:")
	if parts := strings.SplitN(source, ":", 2); len(parts) == 2 && !strings.HasPrefix(source, "http") {
		source, subdir = parts[0], parts[1]
	}
	if !strings.HasPrefix(source, "http") && !strings.HasPrefix(source, "git@") {
		source = "https://github.com/" + strings.TrimPrefix(source, "/")
	}
	return source, subdir
}
