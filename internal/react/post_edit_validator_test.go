package react

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPostEditValidatorSuccess(t *testing.T) {
	validator := PostEditValidator{
		Command:        []string{"/bin/sh", "-c", "printf ok"},
		Timeout:        time.Second,
		MaxOutputBytes: 1024,
	}

	result := validator.Validate(context.Background(), PostEditValidationRequest{ChangedFiles: []string{"a.go"}})

	if result.Err != nil {
		t.Fatalf("Err = %v, want nil", result.Err)
	}
	if result.Output != "ok" {
		t.Fatalf("Output = %q, want ok", result.Output)
	}
	if result.Duration <= 0 {
		t.Fatalf("Duration = %v, want positive", result.Duration)
	}
}

func TestPostEditValidatorFailureIncludesCombinedOutput(t *testing.T) {
	validator := PostEditValidator{
		Command:        []string{"/bin/sh", "-c", "printf stdout; printf stderr >&2; exit 7"},
		Timeout:        time.Second,
		MaxOutputBytes: 1024,
	}

	result := validator.Validate(context.Background(), PostEditValidationRequest{})

	if result.Err == nil {
		t.Fatal("Err = nil, want command failure")
	}
	if !strings.Contains(result.Output, "stdout") || !strings.Contains(result.Output, "stderr") {
		t.Fatalf("Output = %q, want combined stdout/stderr", result.Output)
	}
}

func TestPostEditValidatorTimeoutCancelsCommand(t *testing.T) {
	validator := PostEditValidator{
		Command:        []string{"/bin/sh", "-c", "sleep 5"},
		Timeout:        10 * time.Millisecond,
		MaxOutputBytes: 1024,
	}

	result := validator.Validate(context.Background(), PostEditValidationRequest{})

	if !errors.Is(result.Err, context.DeadlineExceeded) {
		t.Fatalf("Err = %v, want context deadline exceeded", result.Err)
	}
}

func TestPostEditValidatorTimeoutBoundsSubprocessTreeWait(t *testing.T) {
	validator := PostEditValidator{
		Command:        []string{"/bin/sh", "-c", "sleep 5 & wait"},
		Timeout:        10 * time.Millisecond,
		MaxOutputBytes: 1024,
	}

	start := time.Now()
	result := validator.Validate(context.Background(), PostEditValidationRequest{})
	elapsed := time.Since(start)

	if !errors.Is(result.Err, context.DeadlineExceeded) {
		t.Fatalf("Err = %v, want context deadline exceeded", result.Err)
	}
	if elapsed > time.Second {
		t.Fatalf("elapsed = %v, want subprocess wait bounded below 1s", elapsed)
	}
}

func TestPostEditValidatorTimeoutKillsSubprocessTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group cleanup test is Unix-specific")
	}
	marker := filepath.Join(t.TempDir(), "child-survived")
	validator := PostEditValidator{
		Command:        []string{"/bin/sh", "-c", fmt.Sprintf("(trap '' HUP; sleep 2; printf survived > %q) & wait", marker)},
		Timeout:        10 * time.Millisecond,
		MaxOutputBytes: 1024,
	}

	result := validator.Validate(context.Background(), PostEditValidationRequest{})
	// The child writes its marker 2s in, so waiting past that proves the kill
	// happened rather than that we looked too early. The wide margin keeps a
	// loaded CI runner from failing a working process-group kill.
	time.Sleep(2500 * time.Millisecond)

	if !errors.Is(result.Err, context.DeadlineExceeded) {
		t.Fatalf("Err = %v, want context deadline exceeded", result.Err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker stat error = %v, want child process killed before writing marker", err)
	}
}

func TestPostEditValidatorDefaultOutputCap(t *testing.T) {
	validator := PostEditValidator{
		Command: []string{"/bin/sh", "-c", "i=0; while [ $i -lt 70000 ]; do printf x; i=$((i+1)); done"},
		Timeout: time.Second,
	}

	result := validator.Validate(context.Background(), PostEditValidationRequest{})

	if result.Err != nil {
		t.Fatalf("Err = %v, want nil", result.Err)
	}
	if len(result.Output) == 0 || len(result.Output) > 64*1024 {
		t.Fatalf("Output length = %d, want default cap at or below 64KiB", len(result.Output))
	}
}

func TestPostEditValidatorTruncatesOutput(t *testing.T) {
	validator := PostEditValidator{
		Command:        []string{"/bin/sh", "-c", "printf abcdef"},
		Timeout:        time.Second,
		MaxOutputBytes: 3,
	}

	result := validator.Validate(context.Background(), PostEditValidationRequest{})

	if result.Output != "abc" {
		t.Fatalf("Output = %q, want truncated output", result.Output)
	}
}
