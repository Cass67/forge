package drivers

import (
	"net/http"
	"strings"

	"forge/internal/claudeauth"
	"forge/internal/llm"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

var claudeOAuthBetaHeader = strings.Join([]string{
	"oauth-2025-04-20",
	"interleaved-thinking-" + "2025-05-14",
}, ", ")

const (
	claudeOAuthUserAgent    = "claude-cli/2.1.2 (external, cli)"
	claudeOAuthSystemPrefix = "You are Claude Code, Anthropic's official CLI for Claude."
)

type claudeAuthTransport struct {
	base http.RoundTripper
	mgr  *claudeauth.Manager
}

func NewClaudeOAuth(registryName, apiModel string) *ClaudeDriver {
	mgr, err := claudeauth.NewManager()
	if err != nil {
		return nil
	}
	return NewClaudeOAuthAlias(registryName, apiModel, mgr)
}

func NewClaudeOAuthAlias(registryName, apiModel string, mgr *claudeauth.Manager) *ClaudeDriver {
	if mgr == nil {
		return &ClaudeDriver{name: registryName, model: apiModel, params: llmDefaultParams()}
	}
	client := anthropic.NewClient(
		option.WithHTTPClient(&http.Client{
			Transport: &claudeAuthTransport{base: http.DefaultTransport, mgr: mgr},
		}),
		option.WithHeaderDel("X-Api-Key"),
		option.WithHeader("anthropic-beta", claudeOAuthBetaHeader),
		option.WithHeader("user-agent", claudeOAuthUserAgent),
	)
	return &ClaudeDriver{
		client:       &client,
		name:         registryName,
		model:        apiModel,
		promptCache:  false,
		systemPrefix: claudeOAuthSystemPrefix,
		params:       llmDefaultParams(),
	}
}

func (t *claudeAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := t.mgr.Authorization(req.Context())
	if err != nil {
		return nil, err
	}
	cloned := req.Clone(req.Context())
	cloned.Header = req.Header.Clone()
	cloned.Header.Set("Authorization", "Bearer "+token)
	cloned.Header.Del("X-Api-Key")
	return t.base.RoundTrip(cloned)
}

func llmDefaultParams() llm.Params {
	return llm.Params{Temperature: -1}
}
