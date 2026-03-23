package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"forge/internal/agent/tools"
)

func BuildSystemPrompt(workDir string, registry *tools.Registry, skillsDesc string) string {
	var sb strings.Builder
	sb.WriteString("You are forge, a coding agent. You work in the user's project directory.\n\n")
	sb.WriteString(fmt.Sprintf("Working directory: %s\n", workDir))

	info := detectProject(workDir)
	if info != "" {
		sb.WriteString(info + "\n")
	}

	sb.WriteString("\n")
	sb.WriteString(registry.Describe())
	sb.WriteString("\nGuidelines:\n")
	sb.WriteString("- Read files before editing them. Understand what you're changing.\n")
	sb.WriteString("- Use edit_file for surgical changes to existing files. Use write_file only for new files or complete rewrites.\n")
	sb.WriteString("- After making changes, run relevant tests or build commands to verify.\n")
	sb.WriteString("- Explain what you're doing and why before making changes.\n")
	sb.WriteString("- Continue working after progress updates; do not pause waiting for confirmation unless you need missing information, explicit approval for a consequential action, or the task is complete.\n")
	sb.WriteString("- If something fails, read the error, diagnose, and fix. Don't repeat the same failing approach.\n")
	sb.WriteString("- Ask the user for clarification if the request is ambiguous.\n")

	if skillsDesc != "" {
		sb.WriteString("\n")
		sb.WriteString(skillsDesc)
	}

	return sb.String()
}

func detectProject(workDir string) string {
	indicators := map[string]string{
		"go.mod":           "Go",
		"package.json":     "JavaScript/TypeScript",
		"Cargo.toml":       "Rust",
		"pyproject.toml":   "Python",
		"requirements.txt": "Python",
		"Makefile":         "Make",
		"CMakeLists.txt":   "C/C++",
	}

	var detected []string
	for file, lang := range indicators {
		if _, err := os.Stat(filepath.Join(workDir, file)); err == nil {
			detected = append(detected, lang)
		}
	}

	fileCount := 0
	filepath.WalkDir(workDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() && (name == ".git" || name == "node_modules" || name == "vendor" || name == "__pycache__") {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			fileCount++
		}
		if fileCount > 1000 {
			return filepath.SkipAll
		}
		return nil
	})

	parts := []string{fmt.Sprintf("Files: ~%d", fileCount)}
	if len(detected) > 0 {
		parts = append(parts, fmt.Sprintf("Languages: %s", strings.Join(detected, ", ")))
	}
	return strings.Join(parts, "  ")
}
