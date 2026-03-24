package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// VerifyGate is a command that must pass after a builder delegation.
type VerifyGate struct {
	Command string
	Dir     string
}

// ProjectGates returns verification commands based on detected project type.
func ProjectGates(workDir string) []VerifyGate {
	var gates []VerifyGate

	if fileExists(workDir, "go.mod") {
		gates = append(gates, VerifyGate{Command: "go build ./...", Dir: workDir})
		if hasTestFiles(workDir) {
			gates = append(gates, VerifyGate{Command: "go test ./...", Dir: workDir})
		}
	}
	if fileExists(workDir, "Cargo.toml") {
		gates = append(gates, VerifyGate{Command: "cargo build", Dir: workDir})
		gates = append(gates, VerifyGate{Command: "cargo test", Dir: workDir})
	}
	if fileExists(workDir, "package.json") {
		if fileExists(workDir, "bun.lockb") {
			gates = append(gates, VerifyGate{Command: "bun run build", Dir: workDir})
		} else {
			gates = append(gates, VerifyGate{Command: "npm run build", Dir: workDir})
		}
	}
	if fileExists(workDir, "pyproject.toml") {
		gates = append(gates, VerifyGate{Command: "ruff check .", Dir: workDir})
	}

	return gates
}

// RunGates executes verification commands and returns a list of failures.
// Each failure string contains the command and its output.
func RunGates(ctx context.Context, gates []VerifyGate) []string {
	var failures []string
	for _, g := range gates {
		cmd := exec.CommandContext(ctx, "sh", "-c", g.Command)
		cmd.Dir = g.Dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			output := strings.TrimSpace(string(out))
			if len(output) > 2000 {
				output = output[:2000] + "\n... (truncated)"
			}
			failures = append(failures, fmt.Sprintf("FAILED: %s\n%s", g.Command, output))
		}
	}
	return failures
}

func fileExists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func hasTestFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), "_test.go") {
			return true
		}
	}
	// Check one level of subdirectories.
	for _, e := range entries {
		if e.IsDir() && e.Name() != ".git" && e.Name() != "vendor" {
			sub, err := os.ReadDir(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			for _, se := range sub {
				if !se.IsDir() && strings.HasSuffix(se.Name(), "_test.go") {
					return true
				}
			}
		}
	}
	return false
}
