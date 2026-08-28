package react

import (
	"strings"
	"testing"
)

// oldStreamRedactedPrefix is the pre-cursor implementation: it rescanned the
// whole accumulated buffer on every token. redactedStream must emit exactly
// the same bytes, so this stays as the reference.
func oldStreamRedactedPrefix(emit func(string), raw string, emitted int) int {
	matches := secretTriggerPattern.FindAllStringIndex(raw, -1)
	cut := len(raw)
	if len(matches) > 0 {
		cut = matches[len(matches)-1][0]
	}
	if cut <= 0 {
		return emitted
	}
	safe := safeRawMarkupStreamingPrefixLen(raw[:cut])
	redacted := redactRuntimeText(raw[:safe])
	if len(redacted) <= emitted {
		return emitted
	}
	emit(redacted[emitted:])
	return len(redacted)
}

func replayOld(full string, step int) string {
	var out strings.Builder
	emitted := 0
	for i := 0; i < len(full); i += step {
		end := min(i+step, len(full))
		emitted = oldStreamRedactedPrefix(func(s string) { out.WriteString(s) }, full[:end], emitted)
	}
	return out.String()
}

func replayNew(full string, step int) string {
	var out strings.Builder
	var s redactedStream
	for i := 0; i < len(full); i += step {
		end := min(i+step, len(full))
		s.next(func(t string) { out.WriteString(t) }, full[:end])
	}
	return out.String()
}

func TestRedactedStreamMatchesFullRescan(t *testing.T) {
	cases := map[string]string{
		"plain prose":       "The quick brown fox jumps over the lazy dog. It returns an error value.",
		"empty":             "",
		"markup":            "here is a value {\"a\": 1} and more text after it",
		"early markup":      "<tool_call>",
		"github pat":        "here you go ghp_" + strings.Repeat("A", 36) + " use it well",
		"aws key":           "creds AKIA" + strings.Repeat("B", 16) + " done",
		"openai key":        "key sk-" + strings.Repeat("c", 48) + " end",
		"bearer":            "send Authorization: bearer " + strings.Repeat("d", 40) + " ok",
		"generic token":     "export MY_SECRET=" + strings.Repeat("e", 24) + " after",
		"password assign":   "PASSWORD = \"hunter2hunter2hunter2\" trailing prose",
		"private key":       "-----BEGIN RSA PRIVATE KEY-----\n" + strings.Repeat("f", 64) + "\n-----END RSA PRIVATE KEY-----\n",
		"trigger word":      "the secret to good code is simplicity, no password needed",
		"two secrets":       "first ghp_" + strings.Repeat("A", 36) + " then AKIA" + strings.Repeat("B", 16) + " fin",
		"secret then prose": "SECRET=" + strings.Repeat("g", 20) + " and then a long tail of ordinary prose that keeps going for a while",
		"overlapping words": "SECRETOKEN and APIKEYPASSWORD tokens",
		"trailing trigger":  "all fine until the very end where we say token",
		"unicode":           "héllo wörld — em dash and ünïcödé, then ghp_" + strings.Repeat("A", 36),
	}
	for name, full := range cases {
		for _, step := range []int{1, 3, 4, 17, 512} {
			want := replayOld(full, step)
			got := replayNew(full, step)
			if got != want {
				t.Errorf("%s (step=%d):\n old=%q\n new=%q", name, step, want, got)
			}
		}
	}
}

// A secret must never reach the sink in the clear, whatever the token split.
func TestRedactedStreamNeverEmitsSecret(t *testing.T) {
	secrets := []string{
		"ghp_" + strings.Repeat("A", 36),
		"AKIA" + strings.Repeat("B", 16),
		"sk-" + strings.Repeat("c", 48),
	}
	for _, secret := range secrets {
		full := "prefix prose " + secret + " suffix prose that follows the secret"
		for _, step := range []int{1, 2, 5, 64} {
			if got := replayNew(full, step); strings.Contains(got, secret) {
				t.Errorf("step=%d leaked %q in %q", step, secret, got)
			}
		}
	}
}
