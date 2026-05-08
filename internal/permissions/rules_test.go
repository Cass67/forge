package permissions

import "testing"

func TestPermissionRuleScopePrecedence(t *testing.T) {
	rules := []Rule{
		{Scope: ScopeManaged, Behavior: BehaviorDeny, Tool: "run_command", Pattern: "go test:*", Source: "managed"},
		{Scope: ScopeUser, Behavior: BehaviorDeny, Tool: "run_command", Pattern: "go test:*", Source: "user"},
		{Scope: ScopeProject, Behavior: BehaviorDeny, Tool: "run_command", Pattern: "go test:*", Source: "project"},
		{Scope: ScopeLocal, Behavior: BehaviorDeny, Tool: "run_command", Pattern: "go test:*", Source: "local"},
		{Scope: ScopeSession, Behavior: BehaviorAsk, Tool: "run_command", Pattern: "go test:*", Source: "session"},
		{Scope: ScopeCLI, Behavior: BehaviorAllow, Tool: "run_command", Pattern: "go test:*", Source: "cli"},
	}

	decision := Evaluate(rules, Action{Tool: "run_command", Summary: "go test ./..."})
	if !decision.Matched {
		t.Fatal("expected matching rule")
	}
	if decision.Behavior != BehaviorAllow {
		t.Fatalf("Behavior = %q, want %q", decision.Behavior, BehaviorAllow)
	}
	if decision.Rule.Scope != ScopeCLI {
		t.Fatalf("Scope = %q, want %q", decision.Rule.Scope, ScopeCLI)
	}
}

func TestPermissionRuleCommandTrailingWildcardMatchesExactCommand(t *testing.T) {
	decision := Evaluate([]Rule{
		{Scope: ScopeUser, Behavior: BehaviorAllow, Tool: "run_command", Pattern: "git status:*"},
	}, Action{Tool: "run_command", Summary: "git status"})

	if decision.Behavior != BehaviorAllow {
		t.Fatalf("Behavior = %q, want %q", decision.Behavior, BehaviorAllow)
	}
}

func TestPermissionRuleSameScopeDenyBeatsAskBeatsAllow(t *testing.T) {
	rules := []Rule{
		{Scope: ScopeProject, Behavior: BehaviorAllow, Tool: "run_command", Pattern: "git:*"},
		{Scope: ScopeProject, Behavior: BehaviorAsk, Tool: "run_command", Pattern: "git push:*"},
		{Scope: ScopeProject, Behavior: BehaviorDeny, Tool: "run_command", Pattern: "git push --force:*"},
	}

	decision := Evaluate(rules, Action{Tool: "run_command", Summary: "git push --force origin main"})
	if decision.Behavior != BehaviorDeny {
		t.Fatalf("Behavior = %q, want %q", decision.Behavior, BehaviorDeny)
	}
}

func TestPermissionRuleMatchesToolWideRule(t *testing.T) {
	decision := Evaluate([]Rule{
		{Scope: ScopeUser, Behavior: BehaviorAsk, Tool: "write_file"},
	}, Action{Tool: "write_file", Summary: "write internal/app.go", Path: "internal/app.go"})

	if decision.Behavior != BehaviorAsk {
		t.Fatalf("Behavior = %q, want %q", decision.Behavior, BehaviorAsk)
	}
}

func TestPermissionRuleMatchesCommandRule(t *testing.T) {
	decision := Evaluate([]Rule{
		{Scope: ScopeUser, Behavior: BehaviorAllow, Tool: "run_command", Pattern: "git status:*"},
	}, Action{Tool: "run_command", Summary: "git status --short"})

	if decision.Behavior != BehaviorAllow {
		t.Fatalf("Behavior = %q, want %q", decision.Behavior, BehaviorAllow)
	}
}

func TestPermissionRuleMatchesPathRule(t *testing.T) {
	decision := Evaluate([]Rule{
		{Scope: ScopeProject, Behavior: BehaviorAsk, Tool: "write_file", Pattern: "docs/**/*.md"},
	}, Action{Tool: "write_file", Summary: "write docs/plans/demo.md", Path: "docs/plans/demo.md"})

	if decision.Behavior != BehaviorAsk {
		t.Fatalf("Behavior = %q, want %q", decision.Behavior, BehaviorAsk)
	}
}

func TestPermissionRuleDoubleStarPathRuleMatchesDirectChild(t *testing.T) {
	decision := Evaluate([]Rule{
		{Scope: ScopeProject, Behavior: BehaviorDeny, Tool: "write_file", Pattern: "docs/**/*.md"},
	}, Action{Tool: "write_file", Summary: "write docs/report.md", Path: "docs/report.md"})

	if decision.Behavior != BehaviorDeny {
		t.Fatalf("Behavior = %q, want %q", decision.Behavior, BehaviorDeny)
	}
}

func TestPermissionRuleRejectsUnknownTool(t *testing.T) {
	err := ValidateRule(Rule{Scope: ScopeUser, Behavior: BehaviorAllow, Tool: "unknown_tool"})
	if err == nil {
		t.Fatal("expected unknown tool validation error")
	}
}
