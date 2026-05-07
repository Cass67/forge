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
