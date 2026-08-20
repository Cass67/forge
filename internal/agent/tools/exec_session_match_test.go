package tools

import "testing"

// requiresExecSession diverts a command to an exec session. A false positive is
// worse than a false negative: the command never runs, the tool returns advice
// as a successful result, and a model that retries the same command loops.
func TestRequiresExecSessionOnlyMatchesInteractiveCommands(t *testing.T) {
	interactive := []string{
		"node",
		"node -i",
		"node --interactive",
		"cd /tmp && node",
		"/opt/homebrew/bin/node",
		"npm run dev",
		"pnpm dev",
		"tail -f log.txt",
		"vim main.go",
		"python3 -i",
	}
	for _, cmd := range interactive {
		if !requiresExecSession(cmd) {
			t.Errorf("requiresExecSession(%q) = false, want true", cmd)
		}
	}

	oneShot := []string{
		"npm exec -- node --version",
		"cd /tmp && /opt/homebrew/bin/npm exec -- node --version",
		"node --version",
		"node -v",
		"/opt/homebrew/bin/node -v",
		"node build.js",
		"node scripts/gen.js --out dist",
		"ls node_modules",
		"grep -rn node package.json",
		"which node",
		"go build ./...",
		// Interactive program names appearing as arguments or filenames.
		"cat vite.config.ts",
		"grep -rn vite package.json",
		"grep watch src/",
		"cat less.md",
		"ls internal/watch",
		"rm -f more.txt",
		"cat vim.md",
		"go test ./internal/top/...",
	}
	for _, cmd := range oneShot {
		if requiresExecSession(cmd) {
			t.Errorf("requiresExecSession(%q) = true, want false: it exits on its own", cmd)
		}
	}
}
