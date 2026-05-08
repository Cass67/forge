package permissions

import (
	"strings"
	"testing"
)

func TestBuildClassifierPromptRedactsSecrets(t *testing.T) {
	secret := "ghp_" + strings.Repeat("a", 36)
	secretPath := "config/" + secret
	req := ClassifierRequest{
		Action: Action{
			Tool:    "run_command",
			Summary: "use token " + secret,
			Detail:  "TOKEN=" + secret,
			Path:    secretPath,
		},
		Rules: []Rule{{
			Scope:    ScopeLocal,
			Behavior: BehaviorAsk,
			Tool:     "run_command",
			Pattern:  "token=" + secret,
		}},
		Transcript: "user pasted " + secret,
	}

	prompt := BuildClassifierPrompt(req)

	if strings.Contains(prompt, secret) {
		t.Fatal("classifier prompt leaked secret")
	}
	if got := strings.Count(prompt, "<REDACTED:github-pat>"); got != 5 {
		t.Fatalf("redaction count = %d, want 5", got)
	}
	if req.Action.Summary != "use token "+secret || req.Action.Detail != "TOKEN="+secret || req.Action.Path != secretPath || req.Transcript != "user pasted "+secret || req.Rules[0].Pattern != "token="+secret {
		t.Fatal("BuildClassifierPrompt mutated caller request")
	}
}

func TestBuildClassifierPromptRedactsExpandedSecretMatrix(t *testing.T) {
	anthropicKeyName := strings.Join([]string{"ANTHROPIC", "API", "KEY"}, "_")
	anthropicPrefix := strings.Join([]string{"sk", "ant", "api03"}, "-")
	cases := []struct {
		name   string
		secret string
	}{
		{name: "bearer", secret: "Authorization: Bearer " + strings.Repeat("b", 32)},
		{name: "openai", secret: "OPENAI_API_KEY=" + "sk-proj-" + strings.Repeat("c", 48)},
		{name: "anthropic", secret: anthropicKeyName + "=" + anthropicPrefix + "-" + strings.Repeat("d", 84)},
		{name: "aws", secret: "AWS_ACCESS_KEY_ID=" + "AKIA" + strings.Repeat("A", 16)},
		{name: "generic-token", secret: "TOKEN=" + strings.Repeat("e", 24)},
		{name: "private-key", secret: "-----BEGIN PRIVATE KEY-----\n" + strings.Repeat("f", 64) + "\n-----END PRIVATE KEY-----"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := ClassifierRequest{
				Action: Action{
					Tool:    "run_command",
					Summary: "use " + tc.secret,
					Detail:  tc.secret,
					Path:    "config/" + tc.secret,
				},
				Rules: []Rule{{
					Scope:    ScopeLocal,
					Behavior: BehaviorAsk,
					Tool:     "run_command",
					Pattern:  tc.secret,
				}},
				Transcript: "user pasted " + tc.secret,
			}

			prompt := BuildClassifierPrompt(req)
			if strings.Contains(prompt, tc.secret) {
				t.Fatalf("classifier prompt leaked secret shape")
			}
			if !strings.Contains(prompt, "<REDACTED:") {
				t.Fatalf("classifier prompt did not include redaction marker: %s", prompt)
			}
		})
	}
}
