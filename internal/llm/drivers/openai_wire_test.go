package drivers

import "testing"

// The ChatGPT subscription serves gpt-5 and later over the responses endpoint
// only (use_responses_lite in /backend-api/codex/models). Routing one to
// chat/completions returns 404 Not Found, which is what gpt-6-astra did: the
// check was a literal "gpt-5" prefix, so a new family silently took the wrong
// wire.
func TestChatGPTFamilyRequiresResponsesWire(t *testing.T) {
	responses := []string{
		"gpt-5", "gpt-5.5", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna",
		"gpt-6-astra", "gpt-6-pro", "gpt-7", "gpt-10.2",
		"chatgpt/gpt-6-astra", // provider-qualified must still match
	}
	for _, m := range responses {
		if !providerRequiresStatelessResponses("chatgpt", m) {
			t.Errorf("%q must use the responses wire", m)
		}
	}

	chat := []string{"gpt-4o", "gpt-4-turbo", "o3", "o4-mini", "gpt-", ""}
	for _, m := range chat {
		if providerRequiresStatelessResponses("chatgpt", m) {
			t.Errorf("%q must not be forced onto the responses wire", m)
		}
	}

	// Only the chatgpt subscription backend has this constraint.
	if providerRequiresStatelessResponses("openai", "gpt-6-astra") {
		t.Error("the openai API provider must not be forced onto stateless responses")
	}
}
