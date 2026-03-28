package tui

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

type semanticKind int

const (
	semanticPlain semanticKind = iota
	semanticANSI
	semanticInlineCode
	semanticPath
	semanticCommand
	semanticEnv
	semanticNumber
	semanticStatusGood
	semanticStatusBad
	semanticStatusWarn
	semanticLabel
)

type semanticProfile int

const (
	profileProse semanticProfile = iota
	profileStatus
	profileTrace
)

type semanticSpan struct {
	Kind semanticKind
	Text string
}

func TokenizePlain(text string) []semanticSpan {
	opaque := splitOpaqueSemanticSpans(text)
	out := make([]semanticSpan, 0, len(opaque))
	for _, span := range opaque {
		if span.Kind != semanticPlain {
			out = appendSpan(out, span.Kind, span.Text)
			continue
		}
		out = append(out, tokenizePlainSegment(span.Text)...)
	}
	return out
}

func RenderSemantic(spans []semanticSpan, profile semanticProfile, theme chatTheme) string {
	if len(spans) == 0 {
		return ""
	}

	var out strings.Builder
	line := make([]semanticSpan, 0, 8)
	flush := func() {
		if len(line) == 0 {
			return
		}
		structured := lineHasStructuredLabel(line)
		for _, span := range line {
			out.WriteString(renderSemanticSpan(span, profile, theme, structured))
		}
		line = line[:0]
	}

	for _, span := range spans {
		if span.Kind == semanticPlain && strings.Contains(span.Text, "\n") {
			parts := strings.SplitAfter(span.Text, "\n")
			for _, part := range parts {
				if part == "" {
					continue
				}
				if strings.HasSuffix(part, "\n") {
					body := strings.TrimSuffix(part, "\n")
					if body != "" {
						line = append(line, semanticSpan{Kind: semanticPlain, Text: body})
					}
					flush()
					out.WriteString("\n")
					continue
				}
				line = append(line, semanticSpan{Kind: semanticPlain, Text: part})
			}
			continue
		}
		line = append(line, span)
	}
	flush()
	return out.String()
}

func RenderSemanticPlain(text string, profile semanticProfile, theme chatTheme) string {
	return RenderSemantic(TokenizePlain(text), profile, theme)
}

func splitOpaqueSemanticSpans(text string) []semanticSpan {
	out := make([]semanticSpan, 0, 8)
	var plain strings.Builder
	ansiActive := false
	flushPlain := func() {
		if plain.Len() == 0 {
			return
		}
		out = appendSpan(out, semanticPlain, plain.String())
		plain.Reset()
	}

	for i := 0; i < len(text); {
		switch text[i] {
		case '\x1b':
			if esc, n := consumeANSIEscape(text[i:]); n > 0 {
				flushPlain()
				out = appendSpan(out, semanticANSI, esc)
				ansiActive = esc != "\x1b[0m"
				i += n
				continue
			}
		case '`':
			if !ansiActive {
				if end := strings.IndexByte(text[i+1:], '`'); end >= 0 {
					flushPlain()
					inline := text[i : i+end+2]
					out = appendSpan(out, semanticInlineCode, inline)
					i += len(inline)
					continue
				}
			}
		}
		if ansiActive {
			flushPlain()
			start := i
			for i < len(text) && text[i] != '\x1b' {
				i++
			}
			out = appendSpan(out, semanticANSI, text[start:i])
			continue
		}
		switch text[i] {
		case '`':
			if end := strings.IndexByte(text[i+1:], '`'); end >= 0 {
				flushPlain()
				inline := text[i : i+end+2]
				out = appendSpan(out, semanticInlineCode, inline)
				i += len(inline)
				continue
			}
		}
		plain.WriteByte(text[i])
		i++
	}
	flushPlain()
	return out
}

func consumeANSIEscape(text string) (string, int) {
	if len(text) < 2 || text[0] != '\x1b' || text[1] != '[' {
		return "", 0
	}
	for i := 2; i < len(text); i++ {
		if text[i] >= '@' && text[i] <= '~' {
			return text[:i+1], i + 1
		}
	}
	return "", 0
}

func tokenizePlainSegment(text string) []semanticSpan {
	out := make([]semanticSpan, 0, 8)
	lines := strings.SplitAfter(text, "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		if strings.HasSuffix(line, "\n") {
			body := strings.TrimSuffix(line, "\n")
			out = append(out, tokenizePlainLine(body)...)
			out = appendSpan(out, semanticPlain, "\n")
			continue
		}
		out = append(out, tokenizePlainLine(line)...)
	}
	return out
}

func tokenizePlainLine(line string) []semanticSpan {
	if line == "" {
		return nil
	}

	ranges := findCommandRanges(line)
	out := make([]semanticSpan, 0, 8)
	prevLabel := false
	cursor := 0
	for _, r := range ranges {
		if cursor < r.start {
			out, prevLabel = appendTokenizedPlainText(out, line[cursor:r.start], prevLabel)
		}
		out = appendSpan(out, semanticCommand, line[r.start:r.end])
		prevLabel = false
		cursor = r.end
	}
	if cursor < len(line) {
		out, _ = appendTokenizedPlainText(out, line[cursor:], prevLabel)
	}
	return out
}

type byteRange struct {
	start int
	end   int
}

type lineToken struct {
	start int
	end   int
	text  string
}

func findCommandRanges(line string) []byteRange {
	tokens := scanLineTokens(line)
	ranges := make([]byteRange, 0, 2)
	for i := 0; i < len(tokens); i++ {
		firstCore, firstTrail := trimCommandToken(tokens[i].text)
		if firstCore == "" || firstTrail != "" || !isPotentialCommandStart(firstCore) || i+1 >= len(tokens) {
			continue
		}
		secondCore, _ := trimCommandToken(tokens[i+1].text)
		if !commandStartAllowed(firstCore, secondCore) {
			continue
		}

		j := i
		lastEnd := tokens[i].start + len(firstCore)
		hasSignal := true
		for ; j < len(tokens); j++ {
			if j > i && !onlyHorizontalWhitespace(line[lastEnd:tokens[j].start]) {
				break
			}
			core, trailing := trimCommandToken(tokens[j].text)
			if core == "" || !isCommandTokenCore(core, j == i) {
				break
			}
			if j == i+1 && !commandStartAllowed(firstCore, core) {
				break
			}
			if j > i+1 && !isTrailingCommandToken(firstCore, secondCore, core) {
				break
			}
			lastEnd = tokens[j].start + len(core)
			if trailing != "" {
				break
			}
		}
		if j-i >= 2 && hasSignal {
			ranges = append(ranges, byteRange{start: tokens[i].start, end: lastEnd})
			i = j - 1
		}
	}
	return ranges
}

func scanLineTokens(line string) []lineToken {
	tokens := make([]lineToken, 0, 8)
	for i := 0; i < len(line); {
		if unicode.IsSpace(rune(line[i])) {
			i++
			continue
		}
		start := i
		for i < len(line) && !unicode.IsSpace(rune(line[i])) {
			i++
		}
		tokens = append(tokens, lineToken{
			start: start,
			end:   i,
			text:  line[start:i],
		})
	}
	return tokens
}

func appendTokenizedPlainText(out []semanticSpan, text string, prevLabel bool) ([]semanticSpan, bool) {
	for i := 0; i < len(text); {
		if unicode.IsSpace(rune(text[i])) {
			start := i
			for i < len(text) && unicode.IsSpace(rune(text[i])) {
				i++
			}
			out = appendSpan(out, semanticPlain, text[start:i])
			continue
		}
		start := i
		for i < len(text) && !unicode.IsSpace(rune(text[i])) {
			i++
		}
		var next bool
		out, next = appendStandaloneToken(out, text[start:i], prevLabel)
		prevLabel = next
	}
	return out, prevLabel
}

func appendStandaloneToken(out []semanticSpan, token string, prevLabel bool) ([]semanticSpan, bool) {
	if token == "" {
		return out, prevLabel
	}
	if key, value, ok := splitEnvAssignment(token); ok {
		out = appendSpan(out, semanticEnv, key)
		out = appendSpan(out, semanticPlain, "=")
		if value != "" {
			var next bool
			out, next = appendStandaloneToken(out, value, false)
			return out, next
		}
		return out, false
	}
	if isLabelToken(token) {
		out = appendSpan(out, semanticLabel, token)
		return out, true
	}

	core, trailing := trimStandaloneToken(token)
	kind := classifyCoreToken(core, prevLabel)
	if kind == semanticPlain {
		out = appendSpan(out, semanticPlain, token)
		return out, false
	}
	out = appendSpan(out, kind, core)
	if trailing != "" {
		out = appendSpan(out, semanticPlain, trailing)
	}
	return out, false
}

func splitEnvAssignment(token string) (string, string, bool) {
	idx := strings.IndexByte(token, '=')
	if idx <= 0 || idx >= len(token)-1 {
		return "", "", false
	}
	key := token[:idx]
	if !isConfigIdentifier(key) {
		return "", "", false
	}
	return key, token[idx+1:], true
}

func trimStandaloneToken(token string) (string, string) {
	if token == "" {
		return "", ""
	}
	if isLabelToken(token) {
		return token, ""
	}
	core := token
	trailing := ""
	for core != "" {
		if isPathLike(core) || isEnvToken(core, false) || isNumberToken(core) || isConfigIdentifier(core) {
			return core, trailing
		}
		r, size := utf8.DecodeLastRuneInString(core)
		if !isTrailingPunctuation(r) {
			break
		}
		trailing = string(r) + trailing
		core = core[:len(core)-size]
	}
	if core == "" {
		return token, ""
	}
	return core, trailing
}

func trimCommandToken(token string) (string, string) {
	core, trailing := trimStandaloneToken(token)
	if strings.Contains(trailing, "|") || strings.Contains(trailing, "&") || strings.Contains(trailing, ";") {
		return core, trailing
	}
	return core, trailing
}

func classifyCoreToken(token string, prevLabel bool) semanticKind {
	switch {
	case token == "":
		return semanticPlain
	case isPathLike(token):
		return semanticPath
	case isEnvToken(token, prevLabel):
		return semanticEnv
	case isNumberToken(token):
		return semanticNumber
	}
	if kind, ok := statusKind(token); ok {
		return kind
	}
	return semanticPlain
}

func isPotentialCommandStart(token string) bool {
	if strings.Contains(token, "://") || isPathLike(token) || isEnvToken(token, false) || isLabelToken(token) {
		return false
	}
	return isCommandWord(token) && knownCommandStarter(token)
}

func commandStartAllowed(first, second string) bool {
	if first == "" || second == "" {
		return false
	}
	return knownCommandPair(first, second) || isFlagToken(second) || isPathLike(second) || isEnvToken(second, false)
}

func isTrailingCommandToken(first, second, token string) bool {
	switch {
	case isFlagToken(token), isPathLike(token), isEnvToken(token, false), isNumberToken(token):
		return true
	case expectsRunSubcommand(first, second) && isCommandWord(token):
		return true
	default:
		return false
	}
}

func isCommandTokenCore(token string, first bool) bool {
	if token == "" {
		return false
	}
	if strings.HasPrefix(token, "|") || strings.HasPrefix(token, "&&") || strings.HasPrefix(token, "||") || strings.HasPrefix(token, ";") {
		return false
	}
	if first {
		return isCommandWord(token)
	}
	return isCommandWord(token) || isFlagToken(token) || isPathLike(token) || isEnvToken(token, false) || isNumberToken(token)
}

func knownCommandPair(first, second string) bool {
	second = strings.ToLower(strings.TrimSpace(second))
	if isFlagToken(second) || isPathLike(second) {
		return true
	}
	pairs, ok := commonCommandPairs[strings.ToLower(strings.TrimSpace(first))]
	if !ok {
		return false
	}
	_, ok = pairs[second]
	return ok
}

func isCommandWord(token string) bool {
	if token == "" {
		return false
	}
	for i, r := range token {
		if i == 0 {
			if !unicode.IsLetter(r) {
				return false
			}
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func isFlagToken(token string) bool {
	return strings.HasPrefix(token, "-") && len(token) > 1
}

func isLabelToken(token string) bool {
	if !strings.HasSuffix(token, ":") || len(token) < 2 {
		return false
	}
	base := token[:len(token)-1]
	for i, r := range base {
		if i == 0 {
			if !unicode.IsLetter(r) && r != '_' {
				return false
			}
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func isPathLike(token string) bool {
	switch {
	case token == "":
		return false
	case strings.Contains(token, "://"):
		return false
	case strings.HasSuffix(token, "..."):
		return strings.HasPrefix(token, "./") || strings.HasPrefix(token, "../")
	case strings.HasPrefix(token, "/"), strings.HasPrefix(token, "./"), strings.HasPrefix(token, "../"), strings.HasPrefix(token, "~/"):
		return !hasTerminalPathPunctuation(token)
	case isWindowsPath(token):
		return true
	case strings.Contains(token, "/"), strings.Contains(token, `\`):
		return containsAlphaNum(token) && !hasTerminalPathPunctuation(token)
	case isBareFilename(token):
		return true
	default:
		return false
	}
}

func isWindowsPath(token string) bool {
	if len(token) < 3 || token[1] != ':' {
		return false
	}
	return (token[2] == '\\' || token[2] == '/') && unicode.IsLetter(rune(token[0]))
}

func isBareFilename(token string) bool {
	if hasTerminalPathPunctuation(token) {
		return false
	}
	dot := strings.LastIndexByte(token, '.')
	if dot <= 0 || dot == len(token)-1 {
		return false
	}
	ext := strings.ToLower(token[dot+1:])
	_, ok := knownFileExtensions[ext]
	return ok
}

func isEnvToken(token string, prevLabel bool) bool {
	switch {
	case strings.HasPrefix(token, "${") && strings.HasSuffix(token, "}") && len(token) > 3:
		return isConfigIdentifier(token[2 : len(token)-1])
	case strings.HasPrefix(token, "$") && len(token) > 1:
		return isConfigIdentifier(token[1:])
	case prevLabel:
		return isConfigIdentifier(token)
	default:
		return false
	}
}

func isConfigIdentifier(token string) bool {
	if token == "" {
		return false
	}
	for i, r := range token {
		if i == 0 {
			if !unicode.IsUpper(r) && r != '_' {
				return false
			}
			continue
		}
		if unicode.IsUpper(r) || unicode.IsDigit(r) || r == '_' {
			continue
		}
		return false
	}
	return true
}

func isNumberToken(token string) bool {
	switch {
	case token == "":
		return false
	case strings.HasSuffix(token, "%") && isDigits(token[:len(token)-1]):
		return true
	case strings.Contains(token, "/"):
		parts := strings.Split(token, "/")
		return len(parts) == 2 && isDigits(parts[0]) && isDigits(parts[1])
	case hasNumberUnitSuffix(token):
		core := token[:len(token)-1]
		if strings.HasSuffix(strings.ToLower(token), "ms") && len(token) > 2 {
			core = token[:len(token)-2]
		}
		return isNumericCore(core)
	default:
		return isDigits(token)
	}
}

func hasNumberUnitSuffix(token string) bool {
	lower := strings.ToLower(token)
	if strings.HasSuffix(lower, "ms") && len(token) > 2 {
		return true
	}
	if len(token) < 2 {
		return false
	}
	switch lower[len(lower)-1] {
	case 's', 'm', 'h', 'd', 'k':
		return true
	default:
		return false
	}
}

func isNumericCore(token string) bool {
	if token == "" {
		return false
	}
	if strings.Count(token, ".") > 1 {
		return false
	}
	for _, r := range token {
		if unicode.IsDigit(r) || r == '.' {
			continue
		}
		return false
	}
	return true
}

func isDigits(token string) bool {
	if token == "" {
		return false
	}
	for _, r := range token {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func statusKind(token string) (semanticKind, bool) {
	switch strings.ToLower(strings.TrimSpace(token)) {
	case "complete", "ready", "approved", "ok", "success":
		return semanticStatusGood, true
	case "error", "failed", "denied", "blocked":
		return semanticStatusBad, true
	case "warning", "retry", "pending":
		return semanticStatusWarn, true
	default:
		return semanticPlain, false
	}
}

func lineHasStructuredLabel(line []semanticSpan) bool {
	for _, span := range line {
		if span.Kind == semanticLabel {
			return true
		}
	}
	return false
}

func renderSemanticSpan(span semanticSpan, profile semanticProfile, theme chatTheme, structured bool) string {
	switch span.Kind {
	case semanticPlain:
		return lipgloss.NewStyle().
			Foreground(theme.Text).
			Background(theme.AppBG).
			Render(span.Text)
	case semanticANSI:
		return span.Text
	case semanticInlineCode:
		return renderInlineCodeSpan(span.Text, profile, theme)
	}

	style, ok := semanticStyle(span.Kind, profile, theme, structured, span.Text)
	if !ok {
		return span.Text
	}
	return style.Render(span.Text)
}

func renderInlineCodeSpan(text string, profile semanticProfile, theme chatTheme) string {
	if len(text) < 2 || !strings.HasPrefix(text, "`") || !strings.HasSuffix(text, "`") {
		return text
	}

	inner := text[1 : len(text)-1]
	delimiter := lipgloss.NewStyle().
		Foreground(theme.TextDim).
		Background(theme.AppBG).
		Render("`")
	if inner == "" {
		return delimiter + delimiter
	}
	innerRendered := lipgloss.NewStyle().
		Foreground(theme.Text).
		Background(theme.AppBG).
		Render(RenderSemantic(tokenizePlainSegment(inner), profile, theme))
	return delimiter + innerRendered + delimiter
}

func semanticStyle(kind semanticKind, profile semanticProfile, theme chatTheme, structured bool, text string) (lipgloss.Style, bool) {
	var fg lipgloss.Color
	bold := false

	switch kind {
	case semanticPath:
		fg = theme.AccentPrimary
	case semanticCommand:
		fg = theme.AccentSecondary
	case semanticEnv:
		fg = theme.Warning
	case semanticNumber:
		if profile == profileProse && !structured && !hasNumberUnitSuffix(text) && !strings.Contains(text, "/") && !strings.HasSuffix(text, "%") {
			return lipgloss.Style{}, false
		}
		fg = theme.Success
	case semanticStatusGood:
		fg = theme.Success
		bold = true
	case semanticStatusBad:
		fg = theme.Error
		bold = true
	case semanticStatusWarn:
		fg = theme.Warning
		bold = true
	case semanticLabel:
		fg = theme.TextDim
	default:
		return lipgloss.Style{}, false
	}

	if profile == profileTrace && kind != semanticLabel {
		bold = true
	}

	return lipgloss.NewStyle().
		Foreground(fg).
		Background(theme.AppBG).
		Bold(bold), true
}

func appendSpan(spans []semanticSpan, kind semanticKind, text string) []semanticSpan {
	if text == "" {
		return spans
	}
	if len(spans) > 0 && spans[len(spans)-1].Kind == kind {
		spans[len(spans)-1].Text += text
		return spans
	}
	return append(spans, semanticSpan{Kind: kind, Text: text})
}

func onlyHorizontalWhitespace(text string) bool {
	for _, r := range text {
		if r == '\n' || !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func containsAlphaNum(text string) bool {
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func defaultSemanticTheme() chatTheme {
	theme, ok := lookupChatTheme("default")
	if !ok {
		return chatTheme{}
	}
	return theme
}

func ansiPrintableWidth(text string) int {
	return lipgloss.Width(text)
}

func padStyledWidth(text string, width int) string {
	if width <= 0 {
		return ""
	}
	printable := ansiPrintableWidth(text)
	if printable >= width {
		return text
	}
	return text + strings.Repeat(" ", width-printable)
}

func hasTerminalPathPunctuation(token string) bool {
	if strings.HasSuffix(token, "...") {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(token)
	return isTrailingPunctuation(r)
}

func isTrailingPunctuation(r rune) bool {
	switch r {
	case '.', ',', ';', ')', ']', '!', '?':
		return true
	default:
		return false
	}
}

var commonCommandPairs = map[string]map[string]struct{}{
	"forge": {},
	"git": {
		"status": {}, "diff": {}, "show": {}, "log": {}, "add": {}, "commit": {}, "checkout": {}, "switch": {},
	},
	"go": {
		"test": {}, "build": {}, "run": {}, "fmt": {}, "vet": {}, "mod": {},
	},
	"npm":  {"run": {}, "test": {}, "install": {}, "ci": {}, "build": {}},
	"pnpm": {"run": {}, "test": {}, "install": {}, "build": {}},
	"yarn": {"test": {}, "install": {}, "build": {}},
	"uv":   {"run": {}, "pip": {}},
}

func knownCommandStarter(token string) bool {
	_, ok := commonCommandPairs[strings.ToLower(strings.TrimSpace(token))]
	return ok
}

func expectsRunSubcommand(first, second string) bool {
	first = strings.ToLower(strings.TrimSpace(first))
	second = strings.ToLower(strings.TrimSpace(second))
	return (first == "npm" || first == "pnpm" || first == "yarn") && second == "run"
}

var knownFileExtensions = map[string]struct{}{
	"go": {}, "md": {}, "json": {}, "jsonl": {}, "yaml": {}, "yml": {}, "toml": {}, "txt": {},
	"sh": {}, "bash": {}, "zsh": {}, "py": {}, "ts": {}, "tsx": {}, "js": {}, "jsx": {}, "css": {},
	"html": {}, "mod": {}, "sum": {}, "sql": {},
}
