package gui

import "testing"

func TestInstalledMacAppIgnoresOtherPlatforms(t *testing.T) {
	if got := installedMacApp("linux"); got != "" {
		t.Fatalf("installedMacApp(linux) = %q, want empty", got)
	}
}
