package skills

import "testing"

func TestRequiredForInput(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "planning", input: "please plan the architecture first", want: "brainstorming"},
		{name: "existing plan audit", input: "did they all follow the plan and what gaps remain", want: ""},
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
