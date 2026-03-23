package drivers_test

import (
	"testing"

	anthropic "github.com/anthropics/anthropic-sdk-go"

	"forge/internal/llm/drivers"
)

func TestClaudeDriverName(t *testing.T) {
	d := drivers.NewClaude("sk-test", "claude-sonnet-4-6")
	if d.Name() != "claude-sonnet-4-6" {
		t.Fatalf("unexpected name: %s", d.Name())
	}
}

func TestClaudeSDKSupportsEphemeralCacheTTLConstant(t *testing.T) {
	if anthropic.CacheControlEphemeralTTLTTL5m != "5m" {
		t.Fatalf("unexpected ttl constant: %q", anthropic.CacheControlEphemeralTTLTTL5m)
	}
}
