package config

import (
	"fmt"
	"strings"
	"unicode"
)

type ValidationIssue struct {
	Field   string
	Message string
}

func (c *Config) Validate() []ValidationIssue {
	issues := make([]ValidationIssue, 0)
	add := func(field, message string) {
		issues = append(issues, ValidationIssue{Field: field, Message: message})
	}

	if c.Log.Level != "" {
		switch c.Log.Level {
		case "debug", "info", "warn", "error":
		default:
			add("log.level", fmt.Sprintf("must be one of debug, info, warn, error, got %q", c.Log.Level))
		}
	}
	if c.Retry.MaxAttempts < 1 {
		add("retry.max_attempts", "must be at least 1")
	}
	if c.Retry.InitialWait < 0 {
		add("retry.initial_wait_ms", "must be >= 0")
	}
	if c.Retry.MaxWait < c.Retry.InitialWait {
		add("retry.max_wait_ms", "must be >= retry.initial_wait_ms")
	}
	if c.Retry.Timeout < 1 {
		add("retry.timeout_seconds", "must be at least 1")
	}
	validateSecretPolicy := func(field, value string) {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "allow", "redact", "ask", "block":
		default:
			add(field, fmt.Sprintf("must be one of allow, redact, ask, block, got %q", value))
		}
	}
	validateSecretPolicy("security.secrets.read", c.Security.Secrets.Read)
	validateSecretPolicy("security.secrets.write", c.Security.Secrets.Write)
	validateSecretPolicy("security.secrets.command_output", c.Security.Secrets.CommandOutput)
	validateSecretPolicy("security.secrets.approval_detail", c.Security.Secrets.ApprovalDetail)
	if c.Chat.CommandTimeout < 0 {
		add("chat.command_timeout", "must be >= 0")
	}
	if c.Approval.DefaultPolicy != "" {
		switch strings.ToLower(strings.TrimSpace(c.Approval.DefaultPolicy)) {
		case "never", "on_failure", "on_request", "unless_trusted":
		default:
			add("approval.default_policy", fmt.Sprintf("must be one of never, on_failure, on_request, unless_trusted, got %q", c.Approval.DefaultPolicy))
		}
	}
	if c.Approval.SandboxPolicy != "" {
		switch strings.ToLower(strings.TrimSpace(c.Approval.SandboxPolicy)) {
		case "read_only", "workspace_write", "danger_full_access":
		default:
			add("approval.sandbox_policy", fmt.Sprintf("must be one of read_only, workspace_write, danger_full_access, got %q", c.Approval.SandboxPolicy))
		}
	}
	for i, rule := range c.Approval.Rules {
		ruleField := fmt.Sprintf("approval.rules[%d]", i)
		hasPrefix := len(rule.CommandPrefix) > 0
		hasCommand := strings.TrimSpace(rule.Command) != ""
		if hasPrefix && hasCommand {
			add(ruleField, "must set exactly one of command_prefix or command")
		}
		if hasPrefix {
			nonEmptyTokens := 0
			for _, token := range rule.CommandPrefix {
				if strings.TrimSpace(token) == "" {
					add(ruleField+".command_prefix", "must not contain empty tokens")
					continue
				}
				nonEmptyTokens++
			}
			if nonEmptyTokens == 0 {
				add(ruleField+".command_prefix", "must not be empty")
			}
		}
		if hasCommand {
			if err := validateShellCommandRule(rule.Command); err != nil {
				add(ruleField+".command", err.Error())
			}
		}
		if strings.TrimSpace(rule.Decision) == "" {
			add(fmt.Sprintf("approval.rules[%d].decision", i), "must not be empty")
		} else {
			switch strings.ToLower(strings.TrimSpace(rule.Decision)) {
			case "allow", "prompt", "forbidden":
			default:
				add(fmt.Sprintf("approval.rules[%d].decision", i), fmt.Sprintf("must be one of allow, prompt, forbidden, got %q", rule.Decision))
			}
		}
	}
	validatePermissionScope := func(scope string, cfg PermissionScopeConfig) {
		for i, rule := range cfg.Rules {
			ruleField := fmt.Sprintf("permissions.%s.rules[%d]", scope, i)
			if strings.TrimSpace(rule.Behavior) == "" {
				add(ruleField+".behavior", "must not be empty")
			} else {
				switch strings.ToLower(strings.TrimSpace(rule.Behavior)) {
				case "allow", "ask", "deny":
				default:
					add(ruleField+".behavior", fmt.Sprintf("must be one of allow, ask, deny, got %q", rule.Behavior))
				}
			}
			if strings.TrimSpace(rule.Tool) == "" {
				add(ruleField+".tool", "must not be empty")
			} else if !validPermissionTool(rule.Tool) {
				add(ruleField+".tool", fmt.Sprintf("unknown permission tool %q", rule.Tool))
			}
			if pattern := strings.TrimSpace(rule.Pattern); strings.Contains(pattern, "..") {
				add(ruleField+".pattern", "must not contain ..")
			}
		}
	}
	validatePermissionScope("managed", c.Permissions.Managed)
	validatePermissionScope("user", c.Permissions.User)
	validatePermissionScope("project", c.Permissions.Project)
	validatePermissionScope("local", c.Permissions.Local)
	validatePermissionScope("session", c.Permissions.Session)
	validatePermissionScope("cli", c.Permissions.CLI)
	switch strings.ToLower(strings.TrimSpace(c.Permissions.Auto.Posture)) {
	case "conservative", "balanced":
	default:
		add("permissions.auto.posture", fmt.Sprintf("must be one of conservative, balanced, got %q", c.Permissions.Auto.Posture))
	}
	switch strings.ToLower(strings.TrimSpace(c.Permissions.Auto.FailureBehavior)) {
	case "ask", "deny":
	default:
		add("permissions.auto.failure_behavior", fmt.Sprintf("must be one of ask, deny, got %q", c.Permissions.Auto.FailureBehavior))
	}
	if c.Permissions.Auto.MaxConsecutiveDenials < 1 {
		add("permissions.auto.max_consecutive_denials", "must be at least 1")
	}
	if c.Permissions.Auto.MaxTotalDenials < 1 {
		add("permissions.auto.max_total_denials", "must be at least 1")
	}
	seenPluginIDs := map[string]struct{}{}
	for i, plugin := range c.Plugins {
		pluginField := fmt.Sprintf("plugins[%d]", i)
		id := strings.TrimSpace(plugin.ID)
		if id == "" {
			add(pluginField+".id", "must not be empty")
		} else if !validPluginID(id) {
			add(pluginField+".id", "must contain only letters, digits, underscores, or hyphens")
		} else {
			key := strings.ToLower(id)
			if _, ok := seenPluginIDs[key]; ok {
				add(pluginField+".id", fmt.Sprintf("duplicate plugin id %q", id))
			} else {
				seenPluginIDs[key] = struct{}{}
			}
		}
		if strings.TrimSpace(plugin.Kind) != "" {
			switch strings.ToLower(strings.TrimSpace(plugin.Kind)) {
			case "forge-stdio", "native":
			default:
				add(pluginField+".kind", fmt.Sprintf("must be one of forge-stdio, native, got %q", plugin.Kind))
			}
		}
		kind := strings.ToLower(strings.TrimSpace(plugin.Kind))
		if kind != "native" && len(plugin.Command) == 0 {
			add(pluginField+".command", "must not be empty")
		}
		for j, token := range plugin.Command {
			if strings.TrimSpace(token) == "" {
				add(fmt.Sprintf("%s.command[%d]", pluginField, j), "must not be empty")
			}
		}
		for key := range plugin.Env {
			if !validEnvName(key) {
				add(pluginField+".env", fmt.Sprintf("invalid environment variable name %q", key))
			}
		}
		for j, key := range plugin.InheritEnv {
			if !validEnvName(key) {
				add(fmt.Sprintf("%s.inherit_env[%d]", pluginField, j), "must be a valid environment variable name")
			}
		}
		if plugin.StartupTimeoutMS < 0 {
			add(pluginField+".startup_timeout_ms", "must be >= 0")
		}
		if plugin.RequestTimeoutMS < 0 {
			add(pluginField+".request_timeout_ms", "must be >= 0")
		}
	}

	return issues
}

func validPermissionTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "apply_patch", "artifact_read", "artifact_write", "code_search", "edit_file", "exec_session_start",
		"glob", "lsp_definition", "lsp_document_symbols", "lsp_hover", "lsp_references", "read_file",
		"run_command", "search", "view_image", "web_fetch", "write_file":
		return true
	default:
		return strings.HasPrefix(tool, "mcp__") || strings.Contains(tool, "__")
	}
}

func validPluginID(id string) bool {
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

func validEnvName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for i, r := range name {
		if i == 0 {
			if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
				return false
			}
			continue
		}
		if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func validateShellCommandRule(pattern string) error {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return fmt.Errorf("must not be empty")
	}

	tokenCount := 0
	inToken := false
	escaped := false
	for _, r := range pattern {
		switch {
		case escaped:
			escaped = false
			if !inToken {
				tokenCount++
				inToken = true
			}
		case r == '\\':
			escaped = true
			if !inToken {
				tokenCount++
				inToken = true
			}
		case unicode.IsSpace(r):
			inToken = false
		default:
			if !inToken {
				tokenCount++
				inToken = true
			}
		}
	}
	if escaped {
		return fmt.Errorf("shell rule ends with escape")
	}
	if tokenCount == 0 {
		return fmt.Errorf("must not be empty")
	}
	return nil
}
