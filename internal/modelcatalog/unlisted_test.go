package modelcatalog

import "testing"

// An opencode-go model the catalog has never seen must still be usable: the
// catalog lags the provider, and refusing these reported a missing API key.
func TestUnlistedOpenCodeGoModelIsUsableOverChat(t *testing.T) {
	cap, ok := OpenCodeGoModelCapabilityFor("ox-alpha-free")
	if !ok {
		t.Fatal("unlisted opencode-go model should resolve a capability")
	}
	if !cap.SupportedByOpenAICompatibleChat {
		t.Fatal("unlisted opencode-go model should be reachable over openai-compatible chat")
	}
	if !OpenCodeGoModelSupportedByOpenAICompatibleChat("opencode-go/ox-alpha-free") {
		t.Fatal("qualified unlisted model should be supported too")
	}
	// Known reasoning models keep their explicit workarounds.
	if OpenCodeGoModelSupportedByOpenAICompatibleChat("minimax-m2.7") {
		t.Fatal("anthropic-wire model must stay excluded from chat")
	}
}
