package harness

import (
	"path/filepath"
	"strings"
)

type requestScopeKind string

const (
	scopeUnknown      requestScopeKind = ""
	scopeDirectory    requestScopeKind = "directory"
	scopeRepository   requestScopeKind = "repository"
	scopeFocusedFiles requestScopeKind = "focused-files"
)

type requestScope struct {
	Kind       requestScopeKind
	TopicKey   string
	TargetLang string
	TargetGlob string
}

func (s requestScope) Inspectable() bool {
	return s.Kind != scopeUnknown
}

type languageScopeHint struct {
	Language string
	Aliases  []string
	Glob     string
}

var languageScopeHints = []languageScopeHint{
	{Language: "python", Aliases: []string{"python", "py"}, Glob: "**/*.py"},
	{Language: "go", Aliases: []string{"golang", "go"}, Glob: "**/*.go"},
	{Language: "javascript", Aliases: []string{"javascript", "js"}, Glob: "**/*.js"},
	{Language: "typescript", Aliases: []string{"typescript", "ts"}, Glob: "**/*.ts"},
	{Language: "tsx", Aliases: []string{"tsx"}, Glob: "**/*.tsx"},
	{Language: "shell", Aliases: []string{"shell", "bash", "zsh", "sh"}, Glob: "**/*.sh"},
	{Language: "ruby", Aliases: []string{"ruby", "rb"}, Glob: "**/*.rb"},
	{Language: "rust", Aliases: []string{"rust", "rs"}, Glob: "**/*.rs"},
	{Language: "java", Aliases: []string{"java"}, Glob: "**/*.java"},
	{Language: "kotlin", Aliases: []string{"kotlin", "kt", "kts"}, Glob: "**/*.kt"},
	{Language: "csharp", Aliases: []string{"c#", "csharp", "c-sharp", "cs"}, Glob: "**/*.cs"},
	{Language: "cpp", Aliases: []string{"c++", "cpp", "cxx", "cc"}, Glob: "**/*.cpp"},
	{Language: "c", Aliases: []string{"c"}, Glob: "**/*.c"},
	{Language: "php", Aliases: []string{"php"}, Glob: "**/*.php"},
	{Language: "swift", Aliases: []string{"swift"}, Glob: "**/*.swift"},
	{Language: "scala", Aliases: []string{"scala"}, Glob: "**/*.scala"},
	{Language: "lua", Aliases: []string{"lua"}, Glob: "**/*.lua"},
	{Language: "elixir", Aliases: []string{"elixir", "ex", "exs"}, Glob: "**/*.ex"},
	{Language: "sql", Aliases: []string{"sql"}, Glob: "**/*.sql"},
	{Language: "yaml", Aliases: []string{"yaml", "yml"}, Glob: "**/*.yaml"},
	{Language: "json", Aliases: []string{"json"}, Glob: "**/*.json"},
	{Language: "toml", Aliases: []string{"toml"}, Glob: "**/*.toml"},
	{Language: "hcl", Aliases: []string{"hcl", "terraform", "tf"}, Glob: "**/*.tf"},
}

func inferRequestScope(lower string, tokens map[string]struct{}) requestScope {
	if scope := inferFocusedFilesScope(lower); scope.Inspectable() {
		return scope
	}

	switch {
	case hasToken(tokens, "directory"), hasToken(tokens, "folder"), hasToken(tokens, "tree"), hasToken(tokens, "dir"):
		return requestScope{Kind: scopeDirectory, TopicKey: "workspace:directory"}
	case hasToken(tokens, "repo"), hasToken(tokens, "repository"), hasToken(tokens, "codebase"), hasToken(tokens, "project"):
		return requestScope{Kind: scopeRepository, TopicKey: "workspace:repository"}
	default:
		return requestScope{}
	}
}

func inferFocusedFilesScope(task string) requestScope {
	glob := inferTargetGlobFromText(task)
	lang := languageFromTargetGlob(glob)
	if lang == "" {
		if hint, ok := inferLanguageScopeHint(task); ok {
			lang = hint.Language
			if glob == "" {
				glob = hint.Glob
			}
		}
	}
	if glob == "" && lang == "" {
		return requestScope{}
	}

	return requestScope{
		Kind:       scopeFocusedFiles,
		TopicKey:   inferFocusedFilesTopic(glob, lang),
		TargetLang: lang,
		TargetGlob: glob,
	}
}

func inferFocusedFilesTopic(glob, lang string) string {
	if strings.TrimSpace(lang) != "" {
		return "files:" + strings.TrimSpace(lang)
	}
	if lang = languageFromTargetGlob(glob); strings.TrimSpace(lang) != "" {
		return "files:" + strings.TrimSpace(lang)
	}
	return ""
}

func inferTargetGlobFromText(task string) string {
	words := strings.Fields(task)
	for i, raw := range words {
		token := trimScopeToken(raw)
		switch {
		case strings.Contains(token, "*."):
			if dot := strings.LastIndex(token, "."); dot >= 0 {
				if ext := normalizeScopeExtension(token[dot:]); ext != "" {
					return "**/*" + ext
				}
			}
		case strings.HasPrefix(token, ".") && i+1 < len(words):
			next := trimScopeToken(words[i+1])
			if strings.HasPrefix(next, "file") || strings.HasPrefix(next, "script") || strings.HasPrefix(next, "source") || strings.HasPrefix(next, "module") {
				if ext := normalizeScopeExtension(token); ext != "" {
					return "**/*" + ext
				}
			}
		}
	}
	return ""
}

func inferLanguageScopeHint(task string) (languageScopeHint, bool) {
	ordered := tokenList(task)
	for _, hint := range languageScopeHints {
		for _, alias := range hint.Aliases {
			if hasScopedAliasPhrase(task, alias) || hasLooseScopedAliasTokens(ordered, alias) {
				return hint, true
			}
		}
	}
	return languageScopeHint{}, false
}

func hasLooseScopedAliasTokens(ordered []string, alias string) bool {
	if len(ordered) == 0 {
		return false
	}
	for idx, token := range ordered {
		if token != alias {
			continue
		}
		start := idx - 2
		if start < 0 {
			start = 0
		}
		end := idx + 2
		if end >= len(ordered) {
			end = len(ordered) - 1
		}
		for i := start; i <= end; i++ {
			if hasScopedContextToken(ordered[i]) {
				return true
			}
		}
	}
	return false
}

func hasScopedContextToken(token string) bool {
	switch token {
	case "file", "files", "script", "scripts", "source", "sources", "module", "modules", "code":
		return true
	default:
		return withinEditDistanceOne(token, "extension") || withinEditDistanceOne(token, "extensions")
	}
}

func hasScopedAliasPhrase(task, alias string) bool {
	phrases := []string{
		alias + " file",
		alias + " files",
		alias + " script",
		alias + " scripts",
		alias + " source",
		alias + " sources",
		alias + " code",
		alias + " module",
		alias + " modules",
	}
	return strings.Contains(task, alias+" file") ||
		strings.Contains(task, alias+" files") ||
		strings.Contains(task, alias+" script") ||
		strings.Contains(task, alias+" scripts") ||
		strings.Contains(task, alias+" source") ||
		strings.Contains(task, alias+" sources") ||
		strings.Contains(task, alias+" code") ||
		strings.Contains(task, alias+" module") ||
		strings.Contains(task, alias+" modules") ||
		containsPhrase(task, phrases)
}

func containsPhrase(task string, phrases []string) bool {
	for _, phrase := range phrases {
		if strings.Contains(task, phrase) {
			return true
		}
	}
	return false
}

func trimScopeToken(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, "\"'`()[]{}.,;:!?")
	return strings.TrimPrefix(raw, "@")
}

func normalizeScopeExtension(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if !strings.HasPrefix(raw, ".") {
		return ""
	}
	ext := strings.TrimPrefix(raw, ".")
	if ext == "" || len(ext) > 10 {
		return ""
	}
	for _, r := range ext {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return ""
		}
	}
	return "." + ext
}

func languageFromTargetGlob(glob string) string {
	base := filepath.Base(strings.TrimSpace(glob))
	if dot := strings.LastIndex(base, "."); dot >= 0 {
		return languageFromExtension(base[dot:])
	}
	return ""
}

func languageFromExtension(ext string) string {
	switch ext {
	case ".py":
		return "python"
	case ".go":
		return "go"
	case ".js":
		return "javascript"
	case ".ts":
		return "typescript"
	case ".tsx":
		return "tsx"
	case ".sh":
		return "shell"
	case ".rb":
		return "ruby"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".kt", ".kts":
		return "kotlin"
	case ".cs":
		return "csharp"
	case ".cpp", ".cc", ".cxx":
		return "cpp"
	case ".c":
		return "c"
	case ".php":
		return "php"
	case ".swift":
		return "swift"
	case ".scala":
		return "scala"
	case ".lua":
		return "lua"
	case ".ex", ".exs":
		return "elixir"
	case ".sql":
		return "sql"
	case ".yaml", ".yml":
		return "yaml"
	case ".json":
		return "json"
	case ".toml":
		return "toml"
	case ".tf", ".hcl":
		return "hcl"
	default:
		return strings.TrimPrefix(ext, ".")
	}
}
