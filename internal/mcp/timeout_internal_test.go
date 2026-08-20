package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"forge/internal/config"
)

func TestTimeoutForConfigUsesPhaseFallback(t *testing.T) {
	if got := timeoutForConfig(config.MCPServerConfig{}, defaultCallTimeout); got != defaultCallTimeout {
		t.Fatalf("fallback = %s, want %s", got, defaultCallTimeout)
	}
	if got := timeoutForConfig(config.MCPServerConfig{TimeoutMS: 250}, defaultCallTimeout); got != 250*time.Millisecond {
		t.Fatalf("override = %s, want 250ms", got)
	}
	if defaultCallTimeout <= defaultListTimeout || defaultConnectTimeout <= defaultListTimeout {
		t.Fatal("tool calls and cold starts need more budget than list calls")
	}
}

func TestDescribeTimeoutExplainsDeadline(t *testing.T) {
	err := describeTimeout(context.DeadlineExceeded, "docs/search", 3*time.Second)
	if !strings.Contains(err.Error(), "docs/search timed out after 3s") || !strings.Contains(err.Error(), "timeout_ms") {
		t.Fatalf("message = %q", err.Error())
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("wrapped error lost its cause")
	}
	other := errors.New("boom")
	if got := describeTimeout(other, "docs/search", time.Second); got != other {
		t.Fatalf("non-timeout error rewritten: %v", got)
	}
	if describeTimeout(nil, "x", time.Second) != nil {
		t.Fatal("nil error should stay nil")
	}
}
