package providerauth

import (
	"os"
	"path/filepath"
	"testing"

	"forge/internal/auth"
)

// guardRealAuthStore fails loudly if a test would touch the user's real
// credential file. auth paths resolve through XDG_CONFIG_HOME, and getting
// that wrong overwrites live API keys.
func guardRealAuthStore(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	home, _ := os.UserHomeDir()
	if got := filepath.Dir(auth.Path()); got == filepath.Join(home, ".config", "forge") {
		t.Fatalf("auth path did not redirect to the temp dir; refusing to run against %s", got)
	}
}

// signOut must not go through auth.Save, which merges non-empty fields onto
// what is already stored and would leave the credential in place.
func TestSignOutActuallyRemovesCredentials(t *testing.T) {
	guardRealAuthStore(t)

	if err := auth.Save(&auth.Tokens{
		ChatGPTAccessToken:  "access",
		ChatGPTRefreshToken: "refresh",
		ChatGPTAccountID:    "acct",
		OpenAIAPIKey:        "keep-me",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if !SignedIn("chatgpt") {
		t.Fatal("expected chatgpt to be signed in after seeding")
	}
	if err := SignOut("chatgpt"); err != nil {
		t.Fatalf("SignOut: %v", err)
	}
	if SignedIn("chatgpt") {
		t.Fatal("chatgpt still has stored credentials after SignOut")
	}

	// Unrelated providers must survive the write.
	tokens, err := auth.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if tokens.OpenAIAPIKey != "keep-me" {
		t.Fatalf("SignOut clobbered another provider: %+v", tokens)
	}
	if err := SignOut("chatgpt"); err != ErrNotSignedIn {
		t.Fatalf("second SignOut = %v, want ErrNotSignedIn", err)
	}
}

func TestAPIKeyRoundTrip(t *testing.T) {
	guardRealAuthStore(t)

	if err := SaveKey("openrouter", "sk-test"); err != nil {
		t.Fatalf("SaveKey: %v", err)
	}
	if !SignedIn("openrouter") {
		t.Fatal("openrouter should be signed in after SaveKey")
	}
	if err := SignOut("openrouter"); err != nil {
		t.Fatalf("SignOut: %v", err)
	}
	if SignedIn("openrouter") {
		t.Fatal("openrouter key survived SignOut")
	}
}

func TestInteractiveAndAPIKeySplit(t *testing.T) {
	for _, id := range []string{"chatgpt", "claude", "copilot"} {
		if !Interactive(id) || UsesAPIKey(id) {
			t.Errorf("%s should be interactive and not API-key based", id)
		}
	}
	for _, id := range []string{"openai", "openrouter", "groq"} {
		if Interactive(id) || !UsesAPIKey(id) {
			t.Errorf("%s should be API-key based", id)
		}
	}
}
