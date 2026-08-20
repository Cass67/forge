package shellenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyFillsMissingAndMergesPath(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("FORGE_EXISTING", "keep")
	_ = os.Unsetenv("FORGE_FROM_SHELL")
	t.Cleanup(func() { _ = os.Unsetenv("FORGE_FROM_SHELL") })

	apply(map[string]string{
		"PATH":             "/opt/homebrew/bin" + string(filepath.ListSeparator) + "/usr/bin",
		"FORGE_EXISTING":   "shell",
		"FORGE_FROM_SHELL": "token",
		"PWD":              "/somewhere/else",
	})

	if got := os.Getenv("FORGE_FROM_SHELL"); got != "token" {
		t.Fatalf("missing var not imported: %q", got)
	}
	if got := os.Getenv("FORGE_EXISTING"); got != "keep" {
		t.Fatalf("existing var overwritten: %q", got)
	}
	if got := os.Getenv("PWD"); got == "/somewhere/else" {
		t.Fatal("PWD should not be imported")
	}
	path := filepath.SplitList(os.Getenv("PATH"))
	if len(path) != 2 || path[0] != "/opt/homebrew/bin" || path[1] != "/usr/bin" {
		t.Fatalf("PATH merge = %v", path)
	}
}

func TestParseNulEnvKeepsNewlines(t *testing.T) {
	env := parseNulEnv([]byte("A=1\nstill-a\x00B=2\x00"))
	if env["A"] != "1\nstill-a" || env["B"] != "2" {
		t.Fatalf("parsed = %#v", env)
	}
}

func TestLoginShellEnvReadsExports(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("FORGE_PROBE_MARKER", "present")
	env, err := loginShellEnv(t.Context())
	if err != nil {
		t.Fatalf("loginShellEnv: %v", err)
	}
	if env["FORGE_PROBE_MARKER"] != "present" {
		t.Fatalf("marker missing from %d vars", len(env))
	}
	if !strings.Contains(env["PATH"], "/") {
		t.Fatalf("no PATH in probe env")
	}
}
