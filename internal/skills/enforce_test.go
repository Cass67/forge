package skills

import "testing"

func TestRequiredForInput(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "planning", input: "please plan the architecture first", want: "brainstorming"},
		{name: "debugging", input: "debug this failing regression", want: "systematic-debugging"},
		{name: "implementation", input: "implement this feature with tests", want: "test-driven-development"},
		{name: "generic", input: "hello there", want: ""},
	}
	for _, tt := range tests {
		if got := RequiredForInput(tt.input); got != tt.want {
			t.Fatalf("%s: RequiredForInput(%q) = %q, want %q", tt.name, tt.input, got, tt.want)
		}
	}
}

func TestRequiredForInputMatchesRuntimeResolver(t *testing.T) {
	t.Parallel()
	rt := NewRuntime([]Skill{{
		Name:        "brainstorming",
		Description: "plan first",
		Body:        "Do not implement yet.",
	}})

	skill, ok := rt.ResolveRequired("please design the chat ui")
	if !ok {
		t.Fatal("expected ResolveRequired to return brainstorming")
	}
	if got := RequiredForInput("please design the chat ui"); got != skill.Name {
		t.Fatalf("RequiredForInput() = %q, want %q", got, skill.Name)
	}
}
