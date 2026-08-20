package runtime

import "testing"

// A model that advertises effort levels but is sent none never reasons, so its
// thinking cannot be displayed however the renderer is configured.
func TestLowestReasoningEffort(t *testing.T) {
	cases := []struct {
		name    string
		efforts []string
		want    string
	}{
		{"conventional ladder", []string{"low", "medium", "high"}, "low"},
		{"unordered", []string{"high", "medium", "low"}, "low"},
		{"minimal wins", []string{"minimal", "low", "high"}, "minimal"},
		{"explicit none opts out", []string{"none", "low", "high"}, ""},
		{"unknown vocabulary falls back to first", []string{"fast", "slow"}, "fast"},
		{"model without reasoning", nil, ""},
	}
	for _, tc := range cases {
		if got := lowestReasoningEffort(tc.efforts); got != tc.want {
			t.Errorf("%s: lowestReasoningEffort(%v) = %q, want %q", tc.name, tc.efforts, got, tc.want)
		}
	}
}

func TestEffectiveReasoningEffortRespectsChoice(t *testing.T) {
	// An explicit level is sent as chosen.
	s := &ChatSetup{ReasoningEffort: "high", reasoningEffortChosen: true}
	if got := s.effectiveReasoningEffort(); got != "high" {
		t.Fatalf("chosen effort = %q, want high", got)
	}
	// "none" is how a user opts out entirely.
	s = &ChatSetup{ReasoningEffort: "none", reasoningEffortChosen: true}
	if got := s.effectiveReasoningEffort(); got != "" {
		t.Fatalf("none = %q, want empty", got)
	}
	// An unknown model advertises nothing, so nothing is sent.
	s = &ChatSetup{ChatModel: "nosuchprovider/nosuchmodel"}
	if got := s.effectiveReasoningEffort(); got != "" {
		t.Fatalf("unknown model = %q, want empty", got)
	}
}
