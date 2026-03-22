package drivers

import "testing"

func TestModelRequiresResponses(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{model: "gpt-5.4", want: true},
		{model: "gpt-5-mini", want: true},
		{model: "gpt5.1", want: true},
		{model: "gpt5-mini", want: true},
		{model: "o1", want: true},
		{model: "o1-mini", want: true},
		{model: "o3-mini", want: true},
		{model: "o4-mini", want: true},
		{model: "gpt-4o", want: false},
		{model: "openai/gpt-5.4", want: false},
	}

	for _, tt := range tests {
		if got := modelRequiresResponses(tt.model); got != tt.want {
			t.Fatalf("modelRequiresResponses(%q) = %v, want %v", tt.model, got, tt.want)
		}
	}
}

func TestUseResponsesAPI(t *testing.T) {
	tests := []struct {
		name              string
		driver            *OpenAIDriver
		wantUsesResponses bool
	}{
		{
			name:              "openai gpt-5 uses responses",
			driver:            NewOpenAI("sk-test", "gpt-5.4"),
			wantUsesResponses: true,
		},
		{
			name:              "openai gpt-4o stays on chat completions",
			driver:            NewOpenAI("sk-test", "gpt-4o"),
			wantUsesResponses: false,
		},
		{
			name:              "openai o3 uses responses",
			driver:            NewOpenAI("sk-test", "o3-mini"),
			wantUsesResponses: true,
		},
		{
			name:              "copilot gpt-5 uses responses",
			driver:            NewCopilot("gho-test", "copilot/gpt-5.4", "gpt-5.4"),
			wantUsesResponses: true,
		},
		{
			name:              "compat providers stay on chat completions",
			driver:            NewOpenAICompatible("sk-test", "https://example.com/v1", "gpt-5.4"),
			wantUsesResponses: false,
		},
	}

	for _, tt := range tests {
		if got := tt.driver.useResponsesAPI(); got != tt.wantUsesResponses {
			t.Fatalf("%s: useResponsesAPI() = %v, want %v", tt.name, got, tt.wantUsesResponses)
		}
	}
}

func TestModelSupportsTemperature(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{model: "gpt-4o", want: true},
		{model: "gpt-4o-mini", want: true},
		{model: "gpt-5.4", want: false},
		{model: "o1", want: false},
		{model: "o3-mini", want: false},
		{model: "o4-mini", want: false},
	}

	for _, tt := range tests {
		if got := modelSupportsTemperature(tt.model); got != tt.want {
			t.Fatalf("modelSupportsTemperature(%q) = %v, want %v", tt.model, got, tt.want)
		}
	}
}
