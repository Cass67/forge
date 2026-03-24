package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestStartsWithFlag(t *testing.T) {
	if !startsWithFlag("--model") {
		t.Fatal("expected leading flag to be treated as default chat args")
	}
	if startsWithFlag("make") {
		t.Fatal("expected subcommand name not to be treated as a flag")
	}
}

func TestRunMakeWithoutArgsUsesInteractiveMode(t *testing.T) {
	called := false
	prev := runMakeInteractiveFn
	runMakeInteractiveFn = func() {
		called = true
	}
	defer func() { runMakeInteractiveFn = prev }()

	runMake(nil)

	if !called {
		t.Fatal("expected forge make with no args to launch the legacy interactive pipeline")
	}
}

func TestRunMakeWithArgsUsesBatchMode(t *testing.T) {
	var gotCommand string
	var gotArgs []string
	prev := runImproveArgsFn
	runImproveArgsFn = func(commandName string, args []string) {
		gotCommand = commandName
		gotArgs = append([]string(nil), args...)
	}
	defer func() { runImproveArgsFn = prev }()

	runMake([]string{"./repo", "--prompt", "build app"})

	if gotCommand != "make" {
		t.Fatalf("runMake() command = %q, want make", gotCommand)
	}
	if len(gotArgs) != 3 || gotArgs[0] != "./repo" || gotArgs[1] != "--prompt" || gotArgs[2] != "build app" {
		t.Fatalf("runMake() args = %#v", gotArgs)
	}
}

func TestPrintHelpPromotesMakeAndKeepsImproveAlias(t *testing.T) {
	output := captureStdout(t, printHelp)
	if !strings.Contains(output, "forge                           Start interactive chat session") {
		t.Fatalf("expected bare forge help entry, got:\n%s", output)
	}
	if !strings.Contains(output, "forge make                      Launch the legacy writer/auditor pipeline UI") {
		t.Fatalf("expected forge make help entry, got:\n%s", output)
	}
	if !strings.Contains(output, "forge improve <path> [flags]    Compatibility alias for forge make <path> [flags]") {
		t.Fatalf("expected forge improve alias help entry, got:\n%s", output)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	prev := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = prev }()

	fn()

	_ = w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("io.Copy: %v", err)
	}
	_ = r.Close()
	return buf.String()
}
