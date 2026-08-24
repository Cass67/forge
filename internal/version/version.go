package version

// Version is the release version.
const Version = "0.5.7"

// Commit is the short git SHA, injected at build time via
// -ldflags "-X forge/internal/version.Commit=...". It stays "unknown" for
// builds made with a plain `go build`.
var Commit = "unknown"

// String renders the version banner, naming the commit when one was stamped in.
func String() string {
	if Commit == "" || Commit == "unknown" {
		return "forge v" + Version
	}
	return "forge v" + Version + " (" + Commit + ")"
}
