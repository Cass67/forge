package react

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

type shellRuleKind int

const (
	shellRuleExact shellRuleKind = iota
	shellRulePrefix
	shellRuleWildcard
)

type shellRule struct {
	raw    string
	tokens []string
	kind   shellRuleKind
	regex  *regexp.Regexp
}

func parseShellRule(pattern string) (shellRule, error) {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if pattern == "" {
		return shellRule{}, fmt.Errorf("shell rule is empty")
	}

	tokens, err := splitShellWords(pattern)
	if err != nil {
		return shellRule{}, err
	}

	rule := shellRule{
		raw:    pattern,
		tokens: tokens,
		kind:   shellRuleExact,
	}
	if hasWildcard, regex, err := compileShellPattern(pattern); err != nil {
		return shellRule{}, err
	} else if hasWildcard {
		rule.kind = shellRuleWildcard
		rule.regex = regex
	}
	return rule, nil
}

func parseShellRulePrefix(tokens []string) (shellRule, error) {
	cleaned := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if trimmed := strings.TrimSpace(token); trimmed != "" {
			cleaned = append(cleaned, strings.ToLower(trimmed))
		}
	}
	if len(cleaned) == 0 {
		return shellRule{}, fmt.Errorf("shell rule is empty")
	}
	return shellRule{
		raw:    strings.Join(cleaned, " "),
		tokens: cleaned,
		kind:   shellRulePrefix,
	}, nil
}

func matchesAnyShellRulePrefix(summary string, patterns []string) bool {
	for _, pattern := range patterns {
		tokens, err := splitShellWords(pattern)
		if err != nil {
			continue
		}
		rule, err := parseShellRulePrefix(tokens)
		if err != nil {
			continue
		}
		if rule.matches(summary) {
			return true
		}
	}
	return false
}

func (r shellRule) matches(summary string) bool {
	summary = strings.ToLower(strings.TrimSpace(summary))
	if summary == "" {
		return false
	}
	if r.kind == shellRuleWildcard && r.regex != nil {
		return r.regex.MatchString(summary)
	}

	summaryTokens, err := splitShellWords(summary)
	if err != nil {
		return false
	}
	if len(summaryTokens) < len(r.tokens) {
		return false
	}
	for i, token := range r.tokens {
		if summaryTokens[i] != token {
			return false
		}
	}
	return r.kind == shellRulePrefix || len(summaryTokens) == len(r.tokens)
}

func splitShellWords(input string) ([]string, error) {
	input = strings.TrimSpace(strings.ToLower(input))
	if input == "" {
		return nil, fmt.Errorf("shell rule is empty")
	}

	words := make([]string, 0, 4)
	var current strings.Builder
	escaped := false
	for _, r := range input {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case unicode.IsSpace(r):
			if current.Len() > 0 {
				words = append(words, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if escaped {
		return nil, fmt.Errorf("shell rule ends with escape")
	}
	if current.Len() > 0 {
		words = append(words, current.String())
	}
	if len(words) == 0 {
		return nil, fmt.Errorf("shell rule is empty")
	}
	return words, nil
}

func compileShellPattern(pattern string) (bool, *regexp.Regexp, error) {
	var out strings.Builder
	out.WriteString("^")

	escaped := false
	lastWasSpace := false
	hasWildcard := false
	for _, r := range pattern {
		switch {
		case escaped:
			out.WriteString(regexp.QuoteMeta(strings.ToLower(string(r))))
			escaped = false
			lastWasSpace = false
		case r == '\\':
			escaped = true
		case unicode.IsSpace(r):
			if !lastWasSpace {
				out.WriteString(`\s+`)
				lastWasSpace = true
			}
		case r == '*':
			hasWildcard = true
			out.WriteString(`[^\s]+`)
			lastWasSpace = false
		default:
			out.WriteString(regexp.QuoteMeta(strings.ToLower(string(r))))
			lastWasSpace = false
		}
	}
	if escaped {
		return false, nil, fmt.Errorf("shell rule ends with escape")
	}

	out.WriteString("$")
	if !hasWildcard {
		return false, nil, nil
	}
	re, err := regexp.Compile(out.String())
	if err != nil {
		return false, nil, err
	}
	return true, re, nil
}
