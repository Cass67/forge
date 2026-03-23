package codexusage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseSnapshotRecognizesPrimaryAndSecondaryWindows(t *testing.T) {
	payload := map[string]any{
		"plan_type": "plus",
		"rate_limits": map[string]any{
			"primary_window": map[string]any{
				"used_percent":        39.0,
				"seconds_until_reset": 5160.0,
			},
			"secondary_window": map[string]any{
				"used_percent": 15.0,
				"reset_at":     "2026-03-30T12:00:00Z",
			},
		},
	}

	got := parseSnapshot(payload)
	if got == nil {
		t.Fatal("parseSnapshot returned nil")
	}
	if got.Plan != "plus" {
		t.Fatalf("plan = %q, want plus", got.Plan)
	}
	if got.Primary == nil || got.Primary.UsedPercent != 39 {
		t.Fatalf("primary = %#v", got.Primary)
	}
	if got.Primary.ResetIn != "1h26m" {
		t.Fatalf("primary reset in = %q, want 1h26m", got.Primary.ResetIn)
	}
	if got.Secondary == nil || got.Secondary.UsedPercent != 15 {
		t.Fatalf("secondary = %#v", got.Secondary)
	}
}

func TestAuthFilePathPrefersCodexHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)

	got, err := authFilePath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "auth.json")
	if got != want {
		t.Fatalf("authFilePath = %q, want %q", got, want)
	}
}

func TestLoadAuthFileParsesTokens(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, []byte(`{"tokens":{"access_token":"acc","refresh_token":"ref","account_id":"acct"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := loadAuthFile()
	if err != nil {
		t.Fatal(err)
	}
	if got.Tokens.AccessToken != "acc" {
		t.Fatalf("access token = %q", got.Tokens.AccessToken)
	}
	if got.Tokens.RefreshToken != "ref" {
		t.Fatalf("refresh token = %q", got.Tokens.RefreshToken)
	}
	if got.Tokens.AccountID != "acct" {
		t.Fatalf("account id = %q", got.Tokens.AccountID)
	}
}
