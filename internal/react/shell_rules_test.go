package react

import "testing"

func TestShellRuleExactMatch(t *testing.T) {
	rule, err := parseShellRule("git status --short")
	if err != nil {
		t.Fatal(err)
	}
	if !rule.matches("git status --short") {
		t.Fatal("expected exact command match")
	}
	if rule.matches("git status --short --porcelain") {
		t.Fatal("exact command should not match trailing extra args")
	}
}

func TestShellRuleTokenPrefixMatch(t *testing.T) {
	rule, err := parseShellRulePrefix([]string{"Git", "STATUS"})
	if err != nil {
		t.Fatal(err)
	}
	if !rule.matches("git status --short") {
		t.Fatal("expected token-prefix match to ignore case")
	}
}

func TestShellRuleWildcardMatch(t *testing.T) {
	rule, err := parseShellRule("git * status")
	if err != nil {
		t.Fatal(err)
	}
	if !rule.matches("git commit status") {
		t.Fatal("expected wildcard match")
	}
}

func TestShellRuleWildcardMatchesSingleTokenOnly(t *testing.T) {
	rule, err := parseShellRule("git * status")
	if err != nil {
		t.Fatal(err)
	}
	if !rule.matches("git commit status") {
		t.Fatal("expected single-token wildcard match")
	}
	if rule.matches("git commit --amend status") {
		t.Fatal("wildcard should not span multiple tokens")
	}
}

func TestShellRuleEscapedWildcardLiteral(t *testing.T) {
	rule, err := parseShellRule(`git \* status`)
	if err != nil {
		t.Fatal(err)
	}
	if !rule.matches("git * status") {
		t.Fatal("expected escaped wildcard to match literal *")
	}
	if rule.matches("git commit status") {
		t.Fatal("escaped wildcard should not act as a glob")
	}
}

func TestShellRuleMismatch(t *testing.T) {
	rule, err := parseShellRule("git push")
	if err != nil {
		t.Fatal(err)
	}
	if rule.matches("git pull") {
		t.Fatal("expected mismatch")
	}
}
