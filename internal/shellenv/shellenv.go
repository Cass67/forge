// Package shellenv recovers the user's interactive shell environment.
//
// A forge started from Finder, the Dock, or an .app bundle inherits launchd's
// minimal environment: no PATH entries for homebrew/nvm/cargo, and none of the
// API keys or tokens exported by the user's shell profile. MCP stdio servers
// then fail to launch ("executable not found") or refuse to start because a
// {env:TOKEN} reference is unset. Hydrate imports the login shell's exported
// environment into this process so those launches behave like terminal ones.
package shellenv

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const probeTimeout = 5 * time.Second

// Hydrate loads the login shell environment and applies any variable that is
// not already set in this process. PATH is merged: the shell's entries come
// first, then anything already present that the shell did not list.
func Hydrate() {
	if os.Getenv("FORGE_SHELL_ENV") == "0" {
		return
	}
	env, err := loginShellEnv(context.Background())
	if err != nil || len(env) == 0 {
		return
	}
	apply(env)
}

func apply(env map[string]string) {
	for key, value := range env {
		switch key {
		case "PATH":
			_ = os.Setenv("PATH", mergePath(value, os.Getenv("PATH")))
		case "PWD", "OLDPWD", "SHLVL", "_":
			// Positional to whatever shell we spawned; never useful here.
		default:
			if _, ok := os.LookupEnv(key); !ok {
				_ = os.Setenv(key, value)
			}
		}
	}
}

func mergePath(shellPath, current string) string {
	seen := map[string]bool{}
	out := make([]string, 0, 16)
	for _, list := range []string{shellPath, current} {
		for _, dir := range filepath.SplitList(list) {
			if dir == "" || seen[dir] {
				continue
			}
			seen[dir] = true
			out = append(out, dir)
		}
	}
	return strings.Join(out, string(filepath.ListSeparator))
}

// loginShellEnv runs the user's shell as a login shell and reads back its
// exported environment, NUL-delimited so values containing newlines survive.
func loginShellEnv(ctx context.Context) (map[string]string, error) {
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		shell = "/bin/zsh"
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, shell, "-l", "-i", "-c", "command env -0")
	cmd.Stderr = nil
	// A profile that reads stdin would otherwise hang until the timeout.
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		// Some shells reject -i without a tty; retry as login only.
		cmd = exec.CommandContext(ctx, shell, "-l", "-c", "command env -0")
		cmd.Stdin = strings.NewReader("")
		out, err = cmd.Output()
		if err != nil && len(out) == 0 {
			return nil, err
		}
	}
	return parseNulEnv(out), nil
}

func parseNulEnv(data []byte) map[string]string {
	env := make(map[string]string)
	for _, entry := range strings.Split(string(data), "\x00") {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			continue
		}
		env[key] = value
	}
	return env
}
