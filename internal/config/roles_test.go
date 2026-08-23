package config

import "testing"

func TestRoleModel(t *testing.T) {
	c := &Config{
		Chat:   ChatConfig{Model: "anthropic/claude-opus-5"},
		Models: map[string]string{"smol": "groq/llama-3.3-70b-versatile"},
	}
	if got := c.RoleModel("smol"); got != "groq/llama-3.3-70b-versatile" {
		t.Errorf("smol = %q", got)
	}
	if got := c.RoleModel("default"); got != "anthropic/claude-opus-5" {
		t.Errorf("default should fall back to the chat model, got %q", got)
	}
	if got := c.RoleModel("commit"); got != "" {
		t.Errorf("unconfigured role should be empty so the caller keeps its default, got %q", got)
	}
}

func TestValidateRejectsUnknownRole(t *testing.T) {
	c := &Config{Models: map[string]string{"smal": "x"}}
	found := false
	for _, issue := range c.Validate() {
		if issue.Field == "models.smal" {
			found = true
		}
	}
	if !found {
		t.Error("typo'd role name was not reported")
	}
}
