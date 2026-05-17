package react

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

const (
	defaultPostEditValidationMaxOutputBytes = 64 * 1024
	postEditValidationWaitDelay             = 250 * time.Millisecond
)

type PostEditValidationRequest struct {
	ChangedFiles []string
}

type PostEditValidationResult struct {
	Output   string
	Err      error
	Duration time.Duration
}

type PostEditValidator struct {
	Command        []string
	Timeout        time.Duration
	MaxOutputBytes int
}

func (v PostEditValidator) Validate(ctx context.Context, req PostEditValidationRequest) PostEditValidationResult {
	_ = req
	start := time.Now()
	if len(v.Command) == 0 {
		return PostEditValidationResult{Err: fmt.Errorf("post-edit validation command is empty"), Duration: time.Since(start)}
	}

	runCtx := ctx
	cancel := func() {}
	if v.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, v.Timeout)
	}
	defer cancel()

	cmd := exec.CommandContext(runCtx, v.Command[0], v.Command[1:]...)
	cmd.WaitDelay = postEditValidationWaitDelay
	configurePostEditValidatorCommand(cmd)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Start()
	if err == nil {
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err = <-done:
		case <-runCtx.Done():
			killPostEditValidatorProcessGroup(cmd)
			<-done
			err = runCtx.Err()
		}
	}
	if runCtx.Err() != nil {
		err = runCtx.Err()
	}
	return PostEditValidationResult{
		Output:   truncatePostEditValidationOutput(out.Bytes(), v.MaxOutputBytes),
		Err:      err,
		Duration: time.Since(start),
	}
}

func truncatePostEditValidationOutput(out []byte, maxBytes int) string {
	if maxBytes <= 0 {
		maxBytes = defaultPostEditValidationMaxOutputBytes
	}
	if maxBytes > 0 && len(out) > maxBytes {
		out = out[:maxBytes]
	}
	return string(out)
}
