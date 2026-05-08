package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"forge/internal/llm"
	"forge/internal/permissions"
)

type timeoutWithoutCloseDriver struct{}

func (timeoutWithoutCloseDriver) Name() string { return "timeout-without-close" }

func (timeoutWithoutCloseDriver) Stream(ctx context.Context, _ []llm.Message, _ chan<- llm.Token) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestLLMPermissionClassifierReturnsWhenTimedOutDriverDoesNotCloseTokens(t *testing.T) {
	classifier := newLLMPermissionClassifier(timeoutWithoutCloseDriver{}, 10*time.Millisecond)
	done := make(chan error, 1)

	go func() {
		_, err := classifier.Classify(context.Background(), permissions.ClassifierRequest{
			Action: permissions.Action{Tool: "run_command", Summary: "go test ./...", Detail: "go test ./..."},
		})
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("classifier error = %v, want deadline exceeded", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("classifier did not return after timeout")
	}
}

func TestLLMPermissionClassifierReturnsWhenCallerCancelsDriverDoesNotCloseTokens(t *testing.T) {
	classifier := newLLMPermissionClassifier(timeoutWithoutCloseDriver{}, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		_, err := classifier.Classify(ctx, permissions.ClassifierRequest{
			Action: permissions.Action{Tool: "run_command", Summary: "go test ./...", Detail: "go test ./..."},
		})
		done <- err
	}()
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("classifier error = %v, want context canceled", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("classifier did not return after caller cancellation")
	}
}
