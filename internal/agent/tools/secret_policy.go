package tools

import (
	"fmt"
	"strings"

	"forge/internal/secscan"
)

type SecretPolicyMode string

const (
	SecretPolicyAllow  SecretPolicyMode = "allow"
	SecretPolicyRedact SecretPolicyMode = "redact"
	SecretPolicyAsk    SecretPolicyMode = "ask"
	SecretPolicyBlock  SecretPolicyMode = "block"
)

type SecretPolicy struct {
	Read           SecretPolicyMode
	Write          SecretPolicyMode
	CommandOutput  SecretPolicyMode
	ApprovalDetail SecretPolicyMode
}

func DefaultSecretPolicy() SecretPolicy {
	return SecretPolicy{
		Read:           SecretPolicyRedact,
		Write:          SecretPolicyBlock,
		CommandOutput:  SecretPolicyRedact,
		ApprovalDetail: SecretPolicyRedact,
	}
}

func secretPolicyFromOptions(policies []SecretPolicy) SecretPolicy {
	policy := DefaultSecretPolicy()
	if len(policies) > 0 {
		policy = policies[0].WithDefaults()
	}
	return policy
}

func (p SecretPolicy) WithDefaults() SecretPolicy {
	defaults := DefaultSecretPolicy()
	if p.Read == "" {
		p.Read = defaults.Read
	}
	if p.Write == "" {
		p.Write = defaults.Write
	}
	if p.CommandOutput == "" {
		p.CommandOutput = defaults.CommandOutput
	}
	if p.ApprovalDetail == "" {
		p.ApprovalDetail = defaults.ApprovalDetail
	}
	return p
}

func (p SecretPolicy) ApplyRead(text string) (string, bool) {
	return applySecretMode(text, p.WithDefaults().Read)
}

func (p SecretPolicy) ApplyWrite(text string) (string, bool) {
	return applySecretMode(text, p.WithDefaults().Write)
}

func (p SecretPolicy) ApplyCommandOutput(text string) (string, bool) {
	return applySecretMode(text, p.WithDefaults().CommandOutput)
}

func (p SecretPolicy) RedactApprovalDetail(text string) string {
	policy := p.WithDefaults()
	if policy.ApprovalDetail == SecretPolicyAllow || text == "" {
		return text
	}
	matches := secscan.NewDefaultScanner().Scan(text)
	if len(matches) == 0 {
		return text
	}
	return secscan.Redact(text, matches)
}

func applySecretMode(text string, mode SecretPolicyMode) (string, bool) {
	if text == "" || mode == SecretPolicyAllow {
		return text, false
	}
	matches := secscan.NewDefaultScanner().Scan(text)
	if len(matches) == 0 {
		return text, false
	}
	redacted := secscan.Redact(text, matches)
	switch mode {
	case SecretPolicyRedact:
		return redacted, false
	case SecretPolicyAsk, SecretPolicyBlock:
		return fmt.Sprintf("blocked: content matched secret rule %s", secscan.Summary(matches)), true
	default:
		if strings.TrimSpace(string(mode)) == "" {
			return redacted, false
		}
		return fmt.Sprintf("blocked: content matched secret rule %s", secscan.Summary(matches)), true
	}
}
