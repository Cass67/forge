package version

import (
	"strings"
	"testing"
)

func TestStringNamesTheCommitWhenStamped(t *testing.T) {
	original := Commit
	defer func() { Commit = original }()

	Commit = "abc1234"
	if got := String(); got != "forge v"+Version+" (abc1234)" {
		t.Fatalf("String() = %q, want the commit named", got)
	}

	// A plain `go build` leaves the placeholder, which must not be printed.
	for _, unstamped := range []string{"unknown", ""} {
		Commit = unstamped
		got := String()
		if strings.Contains(got, "(") {
			t.Errorf("String() with Commit=%q = %q, want no commit suffix", unstamped, got)
		}
		if got != "forge v"+Version {
			t.Errorf("String() with Commit=%q = %q", unstamped, got)
		}
	}
}
