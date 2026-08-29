package sessionstore

import (
	"strings"
	"testing"
)

// A bare integer is a background command session id, not an output handle.
// Explaining handle format leaves the caller to guess again; naming the right
// tool ends the search.
func TestInvalidHandleRoutesNumericIDToCommandStatus(t *testing.T) {
	got := errInvalidHandle("1").Error()
	if !strings.Contains(got, "command_status") {
		t.Errorf("numeric id should be routed to command_status: %s", got)
	}
	if !strings.Contains(got, "background command session") {
		t.Errorf("should say what the id actually is: %s", got)
	}
}

func TestInvalidHandleKeepsFormatHelpForRealHandles(t *testing.T) {
	got := errInvalidHandle("not-a-handle").Error()
	if !strings.Contains(got, "sha256-hex") {
		t.Errorf("non-numeric input should still get the format explanation: %s", got)
	}
	if strings.Contains(got, "command_status") {
		t.Errorf("must not misroute a malformed handle: %s", got)
	}
}
