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

	if strings.TrimSpace(c.Models.Writer) == "" {
		add("models.writer", "writer model must not be empty")
	}
	if strings.TrimSpace(c.Models.Auditor) == "" {
		add("models.auditor", "auditor model must not be empty")
	}
	if strings.TrimSpace(c.Models.Summarizer) == "" {
		add("models.summarizer", "summarizer model must not be empty")
	}
	if !ValidRounds(c.Session.RoundsPerPass) {
		add("session.rounds_per_pass", fmt.Sprintf("must be between 1 and 10, got %d", c.Session.RoundsPerPass))
	}
	if strings.TrimSpace(c.Session.OutputDir) == "" {
		add("session.output_dir", "output dir must not be empty")
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
	if c.Chat.MaxTurns < 1 {
		add("chat.max_turns", "must be at least 1")
	}
	if c.Chat.CommandTimeout < 1 {
		add("chat.command_timeout", "must be at least 1")
	}
	if c.Chat.AutoSkills != "" {
		switch strings.ToLower(strings.TrimSpace(c.Chat.AutoSkills)) {
		case "off", "suggest", "auto":
		default:
			add("chat.auto_skills", fmt.Sprintf("must be one of off, suggest, auto, got %q", c.Chat.AutoSkills))
		}
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
	for i, pass := range c.Pipeline {
		if strings.TrimSpace(pass.Name) == "" {
			add(fmt.Sprintf("pipeline[%d].name", i), "must not be empty")
		}
		if pass.Rounds != 0 && !ValidRounds(pass.Rounds) {
			add(fmt.Sprintf("pipeline[%d].rounds", i), fmt.Sprintf("must be between 1 and 10 when set, got %d", pass.Rounds))
		}
	}

	return issues
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
