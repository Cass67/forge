package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestTokenizePlainClassifiesRepresentativeExamples(t *testing.T) {
	spans := TokenizePlain("Run go test ./... from ./internal/tui\nstatus: approved in 1.2s\ntool_call: forge -d\nSet $FORGE_THEME before restarting\nReview main.go and docs/spec.md")

	assertSpanKind(t, spans, "go test ./...", semanticCommand)
	assertSpanKind(t, spans, "./internal/tui", semanticPath)
	assertSpanKind(t, spans, "status:", semanticLabel)
	assertSpanKind(t, spans, "approved", semanticStatusGood)
	assertSpanKind(t, spans, "1.2s", semanticNumber)
	assertSpanKind(t, spans, "tool_call:", semanticLabel)
	assertSpanKind(t, spans, "forge -d", semanticCommand)
	assertSpanKind(t, spans, "$FORGE_THEME", semanticEnv)
	assertSpanKind(t, spans, "main.go", semanticPath)
	assertSpanKind(t, spans, "docs/spec.md", semanticPath)
}

func TestTokenizePlainRecognizesPathsAndEnvForms(t *testing.T) {
	spans := TokenizePlain("Paths: C:\\work\\forge ~/src/forge ../internal/tui\nTheme ${FORGE_THEME}\nconfig: FORGE_THEME=low")

	assertSpanKind(t, spans, "C:\\work\\forge", semanticPath)
	assertSpanKind(t, spans, "~/src/forge", semanticPath)
	assertSpanKind(t, spans, "../internal/tui", semanticPath)
	assertSpanKind(t, spans, "${FORGE_THEME}", semanticEnv)
	assertSpanKind(t, spans, "FORGE_THEME", semanticEnv)
}

func TestTokenizePlainKeepsPunctuationOutsideSemanticToken(t *testing.T) {
	spans := TokenizePlain("See ./internal/tui, then run go test ./....")

	assertSpanKind(t, spans, "./internal/tui", semanticPath)
	assertSpanKind(t, spans, ", ", semanticPlain)
	assertSpanKind(t, spans, "go test ./...", semanticCommand)
}

func TestTokenizePlainLeavesAmbiguousProsePlain(t *testing.T) {
	spans := TokenizePlain("Please review this carefully and let me know what you think.")

	assertNoSemanticKinds(t, spans)
}

func TestTokenizePlainTreatsInlineCodeURLsAndANSIAsOpaque(t *testing.T) {
	spans := TokenizePlain("Use `go test ./...` if you want the exact command.\nhttps://platform.openai.com should remain plain here.\n\x1b[31merror\x1b[0m")

	assertSpanKind(t, spans, "`go test ./...`", semanticInlineCode)
	assertSpanKind(t, spans, "https://platform.openai.com", semanticPlain)
	assertSpanKind(t, spans, "\x1b[31m", semanticANSI)
	assertSpanKind(t, spans, "\x1b[0m", semanticANSI)
}

func TestRenderSemanticProseStylesStandaloneStatusWord(t *testing.T) {
	withTrueColorProfile(t)

	theme := lookupThemeForTest(t, "default")
	rendered := RenderSemanticPlain("approved", profileProse, theme)

	assertStyledSubstring(t, rendered, "approved", theme.Success)
}

func TestRenderSemanticProseStylesStructuredStatusAndNumbers(t *testing.T) {
	withTrueColorProfile(t)

	theme := lookupThemeForTest(t, "default")
	rendered := RenderSemanticPlain("status: approved in 1.2s", profileProse, theme)

	assertStyledSubstring(t, rendered, "status:", theme.TextDim)
	assertStyledSubstring(t, rendered, "approved", theme.Success)
	assertStyledSubstring(t, rendered, "1.2s", theme.Success)
}

func TestRenderSemanticProseStylesInlineCodeContents(t *testing.T) {
	withTrueColorProfile(t)

	theme := lookupThemeForTest(t, "default")
	rendered := RenderSemanticPlain("Use `go test ./...` from `./internal/tui` and inspect `verify_cpe_transfer_and_ack.sh`.", profileProse, theme)

	assertStyledSubstring(t, rendered, "go test ./...", theme.AccentSecondary)
	assertStyledSubstring(t, rendered, "./internal/tui", theme.AccentPrimary)
	assertStyledSubstring(t, rendered, "verify_cpe_transfer_and_ack.sh", theme.AccentPrimary)
}

func TestRenderSemanticPreservesPrintableWidth(t *testing.T) {
	withTrueColorProfile(t)

	theme := lookupThemeForTest(t, "default")
	input := "tool_call: forge -d from ./internal/tui in 1.2s"
	rendered := RenderSemanticPlain(input, profileTrace, theme)

	if got, want := lipgloss.Width(rendered), lipgloss.Width(input); got != want {
		t.Fatalf("rendered width = %d, want %d for %q", got, want, rendered)
	}
}

func assertSpanKind(t *testing.T, spans []semanticSpan, text string, want semanticKind) {
	t.Helper()

	for _, span := range spans {
		if span.Text == text {
			if span.Kind != want {
				t.Fatalf("span %q kind = %v, want %v", text, span.Kind, want)
			}
			return
		}
	}
	for _, span := range spans {
		if strings.Contains(span.Text, text) {
			if span.Kind != want {
				t.Fatalf("span %q kind = %v, want %v", text, span.Kind, want)
			}
			return
		}
	}
	t.Fatalf("missing span %q in %#v", text, spans)
}

func assertNoSemanticKinds(t *testing.T, spans []semanticSpan) {
	t.Helper()

	for _, span := range spans {
		switch span.Kind {
		case semanticPlain:
		default:
			t.Fatalf("unexpected semantic kind %v for span %q", span.Kind, span.Text)
		}
	}
}

func assertStyledSubstring(t *testing.T, rendered, substring string, want lipgloss.Color) {
	t.Helper()

	wantHex := strings.ToLower(string(want))
	for _, segment := range styledSegments(rendered) {
		if strings.Contains(segment.Text, substring) && strings.EqualFold(segment.Foreground, wantHex) {
			return
		}
	}
	t.Fatalf("substring %q not styled with %s in %q", substring, wantHex, rendered)
}

func assertSubstringNotColor(t *testing.T, rendered, substring string, forbidden lipgloss.Color) {
	t.Helper()

	forbiddenHex := strings.ToLower(string(forbidden))
	for _, segment := range styledSegments(rendered) {
		if strings.Contains(segment.Text, substring) && strings.EqualFold(segment.Foreground, forbiddenHex) {
			t.Fatalf("substring %q unexpectedly styled with %s in %q", substring, forbiddenHex, rendered)
		}
	}
}

type styledSegment struct {
	Text       string
	Foreground string
}

func styledSegments(rendered string) []styledSegment {
	segments := make([]styledSegment, 0, 8)
	var current strings.Builder
	currentFG := ""
	flush := func() {
		if current.Len() == 0 {
			return
		}
		segments = append(segments, styledSegment{
			Text:       current.String(),
			Foreground: currentFG,
		})
		current.Reset()
	}

	for i := 0; i < len(rendered); {
		if rendered[i] == '\x1b' && i+1 < len(rendered) && rendered[i+1] == '[' {
			flush()
			end := i + 2
			for end < len(rendered) && (rendered[end] < '@' || rendered[end] > '~') {
				end++
			}
			if end >= len(rendered) {
				break
			}
			if rendered[end] == 'm' {
				currentFG = applySGRForeground(currentFG, rendered[i+2:end])
			}
			i = end + 1
			continue
		}
		current.WriteByte(rendered[i])
		i++
	}
	flush()
	return segments
}

func applySGRForeground(current, raw string) string {
	parts := strings.Split(raw, ";")
	for i := 0; i < len(parts); i++ {
		part := parts[i]
		switch part {
		case "0", "39":
			current = ""
		case "38":
			if i+4 < len(parts) && parts[i+1] == "2" {
				current = fmt.Sprintf("#%02x%02x%02x", atoi(parts[i+2]), atoi(parts[i+3]), atoi(parts[i+4]))
				i += 4
			}
		}
	}
	return current
}

func atoi(raw string) int {
	var n int
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
