package permissions

import (
	"testing"

	"forge/internal/config"
)

func TestLoadConfigRulesPreservesScopes(t *testing.T) {
	cfg := config.PermissionsConfig{
		Project: config.PermissionScopeConfig{Rules: []config.PermissionRuleConfig{{Behavior: "deny", Tool: "run_command", Pattern: "rm:*"}}},
		User:    config.PermissionScopeConfig{Rules: []config.PermissionRuleConfig{{Behavior: "allow", Tool: "run_command", Pattern: "go test:*"}}},
	}

	rules := LoadConfigRules(cfg)
	if len(rules) != 2 {
		t.Fatalf("rules = %d, want 2", len(rules))
	}
	if rules[0].Scope != ScopeUser || rules[0].Behavior != BehaviorAllow {
		t.Fatalf("rules[0] = %#v", rules[0])
	}
	if rules[1].Scope != ScopeProject || rules[1].Behavior != BehaviorDeny {
		t.Fatalf("rules[1] = %#v", rules[1])
	}
}

func TestLoadConfigRulesSkipsInvalidRules(t *testing.T) {
	cfg := config.PermissionsConfig{
		User: config.PermissionScopeConfig{Rules: []config.PermissionRuleConfig{
			{Behavior: "maybe", Tool: "run_command", Pattern: "go test:*"},
			{Behavior: "allow", Tool: "run_command", Pattern: "go test:*"},
		}},
	}

	rules := LoadConfigRules(cfg)
	if len(rules) != 1 {
		t.Fatalf("rules = %d, want 1", len(rules))
	}
	if rules[0].Behavior != BehaviorAllow {
		t.Fatalf("rule = %#v", rules[0])
	}
}
