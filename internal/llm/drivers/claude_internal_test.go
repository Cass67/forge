package drivers

import (
	"testing"

	"forge/internal/llm"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

func TestBuildClaudeBetaParamsUsesPromptCachingBeta(t *testing.T) {
	params := buildClaudeBetaParams("claude-sonnet-4-6", llm.Params{Temperature: 0.25}, []llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "hello"},
	}, 2048, claudeRequestOptions{promptCache: true})

	if params.Model != anthropic.Model("claude-sonnet-4-6") {
		t.Fatalf("Model = %q, want %q", params.Model, "claude-sonnet-4-6")
	}
	if params.CacheControl.TTL != anthropic.BetaCacheControlEphemeralTTLTTL5m {
		t.Fatalf("CacheControl.TTL = %q, want %q", params.CacheControl.TTL, anthropic.BetaCacheControlEphemeralTTLTTL5m)
	}
	if len(params.Betas) != 1 || params.Betas[0] != anthropic.AnthropicBetaPromptCaching2024_07_31 {
		t.Fatalf("Betas = %#v, want prompt caching beta", params.Betas)
	}
	if len(params.System) != 1 || params.System[0].Text != "system" {
		t.Fatalf("System = %#v", params.System)
	}
	if len(params.Messages) != 1 || params.Messages[0].Role != anthropic.BetaMessageParamRoleUser {
		t.Fatalf("Messages = %#v", params.Messages)
	}
	if !params.Temperature.Valid() || params.Temperature.Value != 0.25 {
		t.Fatalf("Temperature = %#v, want 0.25", params.Temperature)
	}
}

func TestBuildClaudeBetaParamsForOAuthSkipsPromptCachingAndPrependsPrefix(t *testing.T) {
	params := buildClaudeBetaParams("claude-sonnet-4-6", llm.Params{}, []llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "hello"},
	}, 2048, claudeRequestOptions{systemPrefix: claudeOAuthSystemPrefix})

	if len(params.Betas) != 0 {
		t.Fatalf("Betas = %#v, want none", params.Betas)
	}
	if params.CacheControl.TTL != "" {
		t.Fatalf("CacheControl.TTL = %q, want empty", params.CacheControl.TTL)
	}
	if len(params.System) != 2 {
		t.Fatalf("System = %#v", params.System)
	}
	if params.System[0].Text != claudeOAuthSystemPrefix {
		t.Fatalf("System[0] = %q, want %q", params.System[0].Text, claudeOAuthSystemPrefix)
	}
	if params.System[1].Text != "system" {
		t.Fatalf("System[1] = %q, want %q", params.System[1].Text, "system")
	}
}
