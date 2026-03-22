package session

import (
	"strings"
	"testing"
)

func TestBuildLanguageGuidance(t *testing.T) {
	tests := []struct {
		name         string
		hint         string
		inlinedCode  string
		wantContains string
	}{
		{
			name:         "auto without code chooses best language",
			hint:         "auto",
			wantContains: "Choose the best language",
		},
		{
			name:         "auto with code matches existing language",
			hint:         "auto",
			inlinedCode:  "```py:main.py\nprint('hi')\n```",
			wantContains: "Match the language, framework, and project conventions",
		},
		{
			name:         "explicit hint is preserved",
			hint:         "python",
			wantContains: "Use python unless the existing codebase shown below makes that unsafe or incompatible.",
		},
	}

	for _, tt := range tests {
		got := buildLanguageGuidance(tt.hint, tt.inlinedCode)
		if !strings.Contains(got, tt.wantContains) {
			t.Fatalf("%s: got %q, want substring %q", tt.name, got, tt.wantContains)
		}
	}
}

func TestBuildUserContentIncludesLanguageGuidance(t *testing.T) {
	got := buildUserContent("build a cli", "auto", "", "", "", "")
	if !strings.Contains(got, "LANGUAGE GUIDANCE:\nChoose the best language") {
		t.Fatalf("expected language guidance in user content, got %q", got)
	}
}
