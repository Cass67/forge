package permissions

import (
	"strings"

	"forge/internal/config"
)

func LoadConfigRules(cfg config.PermissionsConfig) []Rule {
	var out []Rule
	appendScope := func(scope Scope, scoped config.PermissionScopeConfig) {
		for _, rule := range scoped.Rules {
			loaded := Rule{
				Scope:    scope,
				Behavior: parseBehavior(rule.Behavior),
				Tool:     strings.TrimSpace(rule.Tool),
				Pattern:  strings.TrimSpace(rule.Pattern),
				Source:   string(scope),
			}
			if ValidateRule(loaded) == nil {
				out = append(out, loaded)
			}
		}
	}
	appendScope(ScopeManaged, cfg.Managed)
	appendScope(ScopeUser, cfg.User)
	appendScope(ScopeProject, cfg.Project)
	appendScope(ScopeLocal, cfg.Local)
	appendScope(ScopeSession, cfg.Session)
	appendScope(ScopeCLI, cfg.CLI)
	return out
}

func parseBehavior(value string) Behavior {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(BehaviorAllow):
		return BehaviorAllow
	case string(BehaviorAsk):
		return BehaviorAsk
	case string(BehaviorDeny):
		return BehaviorDeny
	default:
		return ""
	}
}
