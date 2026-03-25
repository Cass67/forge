package agent

import (
	"path/filepath"
	"strconv"
	"strings"
)

type taskScope string

const (
	taskScopeUnknown      taskScope = ""
	taskScopeSingleFile   taskScope = "single-file"
	taskScopeFocusedFiles taskScope = "focused-files"
	taskScopeRepoReview   taskScope = "repo-review"
)

type taskProfile struct {
	Scope            taskScope
	Target           string
	TargetLang       string
	TargetGlob       string
	Topic            string
	EvidenceMinReads int
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

func classifyTaskProfile(task string) taskProfile {
	profile := mergeTaskProfiles(explicitTaskProfile(task), inferTaskProfile(task))
	return normalizeTaskProfile(profile)
}

func classifyDelegatedTaskProfile(userMessage, task string) taskProfile {
	profile := classifyTaskProfile(task)
	if strings.TrimSpace(userMessage) == "" {
		return profile
	}
	return normalizeTaskProfile(mergeTaskProfiles(profile, classifyTaskProfile(userMessage)))
}

func explicitTaskProfile(task string) taskProfile {
	return taskProfile{
		Scope:            normalizeTaskScope(taskSection(task, "SCOPE:")),
		Target:           normalizeFileReference(taskSection(task, "TARGET:")),
		TargetLang:       canonicalTaskLanguage(taskSection(task, "TARGET_LANG:")),
		TargetGlob:       normalizeTargetGlob(taskSection(task, "TARGET_GLOB:")),
		Topic:            normalizeTaskTopic(taskSection(task, "TOPIC:")),
		EvidenceMinReads: parsePositiveTaskInt(taskSection(task, "EVIDENCE_MIN_READS:")),
	}
}

func inferTaskProfile(task string) taskProfile {
	if target := extractSingleFileTaskTarget(task); target != "" {
		return taskProfile{
			Scope:  taskScopeSingleFile,
			Target: target,
			Topic:  inferTaskTopic(task),
		}
	}
	if profile := inferFocusedFilesTaskProfile(task); profile.Scope != taskScopeUnknown {
		return profile
	}
	if inferRepoReviewScope(task) {
		return taskProfile{
			Scope: taskScopeRepoReview,
			Topic: inferTaskTopic(task),
		}
	}
	return taskProfile{Topic: inferTaskTopic(task)}
}

func inferFocusedFilesTaskProfile(task string) taskProfile {
	lower := strings.ToLower(normalizePromptText(task))
	glob := inferTargetGlobFromText(lower)
	lang := languageFromTargetGlob(glob)
	if lang == "" {
		if hint, ok := inferLanguageScopeHint(lower); ok {
			lang = hint.Language
			if glob == "" {
				glob = hint.Glob
			}
		}
	}
	if glob == "" && lang == "" {
		return taskProfile{}
	}
	if !containsAny(lower, []string{
		" file", " files", " script", " scripts", " source", " sources",
		" repo", " repository", " codebase", " review", " inspect",
		" cleanup", " maintainability", " code smell", " code smells",
		" quality", " risky", " outdated",
	}) {
		return taskProfile{}
	}
	return taskProfile{
		Scope:            taskScopeFocusedFiles,
		TargetLang:       lang,
		TargetGlob:       glob,
		Topic:            inferTaskTopic(task),
		EvidenceMinReads: 3,
	}
}

func inferRepoReviewScope(task string) bool {
	lower := strings.ToLower(normalizePromptText(task))
	if containsAny(lower, []string{
		"repo review",
		"repository review",
		"inspect the repository",
		"inspect the go repository",
	}) {
		return true
	}
	if !containsAny(lower, []string{"repo", "repository", "codebase"}) {
		return false
	}
	if !containsAny(lower, []string{
		"review",
		"assess",
		"assessment",
		"recommend",
		"recommendation",
		"improve",
		"improvement",
		"cleanup",
		"maintenance",
	}) {
		return false
	}
	return containsAny(lower, []string{
		"purpose",
		"tech stack",
		"key modules",
		"main packages/binaries",
		"dependencies",
		"test/build health",
		"maintenance signals",
		"cleanup opportunities",
	})
}

func mergeTaskProfiles(preferred, fallback taskProfile) taskProfile {
	if taskScopeSpecificity(fallback.Scope) > taskScopeSpecificity(preferred.Scope) {
		preferred.Scope = fallback.Scope
	}
	if preferred.Target == "" {
		preferred.Target = fallback.Target
	}
	if preferred.TargetLang == "" {
		preferred.TargetLang = fallback.TargetLang
	}
	if preferred.TargetGlob == "" {
		preferred.TargetGlob = fallback.TargetGlob
	}
	if preferred.Topic == "" {
		preferred.Topic = fallback.Topic
	}
	if preferred.EvidenceMinReads == 0 {
		preferred.EvidenceMinReads = fallback.EvidenceMinReads
	}
	return preferred
}

func normalizeTaskProfile(profile taskProfile) taskProfile {
	profile.Scope = normalizeTaskScope(string(profile.Scope))
	profile.Target = normalizeFileReference(profile.Target)
	profile.TargetLang = canonicalTaskLanguage(profile.TargetLang)
	profile.TargetGlob = normalizeTargetGlob(profile.TargetGlob)
	profile.Topic = normalizeTaskTopic(profile.Topic)
	if profile.Scope == taskScopeUnknown {
		switch {
		case profile.Target != "":
			profile.Scope = taskScopeSingleFile
		case profile.TargetGlob != "" || profile.TargetLang != "":
			profile.Scope = taskScopeFocusedFiles
		}
	}
	switch profile.Scope {
	case taskScopeSingleFile:
		profile.TargetLang = ""
		profile.TargetGlob = ""
		profile.EvidenceMinReads = 0
	case taskScopeFocusedFiles:
		if profile.TargetGlob == "" && profile.TargetLang != "" {
			profile.TargetGlob = languageGlob(profile.TargetLang)
		}
		if profile.EvidenceMinReads <= 0 {
			profile.EvidenceMinReads = 3
		}
	case taskScopeRepoReview:
		profile.Target = ""
		profile.TargetLang = ""
		profile.TargetGlob = ""
		profile.EvidenceMinReads = 0
	}
	return profile
}

func normalizeTaskScope(raw string) taskScope {
	lower := strings.ToLower(normalizePromptText(strings.TrimSpace(raw)))
	switch {
	case lower == "":
		return taskScopeUnknown
	case containsAny(lower, []string{"single-file", "single file"}):
		return taskScopeSingleFile
	case containsAny(lower, []string{"focused-files", "focused files", "matching-files", "matching files", "language-scope", "language scope"}):
		return taskScopeFocusedFiles
	case containsAny(lower, []string{"repo-review", "repo review", "repository review", "repository-review", "codebase review", "codebase-review"}):
		return taskScopeRepoReview
	default:
		return taskScopeUnknown
	}
}

func taskScopeSpecificity(scope taskScope) int {
	switch scope {
	case taskScopeSingleFile:
		return 3
	case taskScopeFocusedFiles:
		return 2
	case taskScopeRepoReview:
		return 1
	default:
		return 0
	}
}

func normalizeTargetGlob(raw string) string {
	parts := splitTaskValueList(raw)
	if len(parts) == 0 {
		return ""
	}
	seen := make(map[string]struct{})
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(strings.Trim(part, "\"'`()[]{}"))
		part = filepath.ToSlash(part)
		part = strings.TrimPrefix(part, "./")
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		normalized = append(normalized, part)
	}
	return strings.Join(normalized, ", ")
}

func splitTaskValueList(raw string) []string {
	return strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n'
	})
}

func parsePositiveTaskInt(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0
	}
	return value
}

func inferTargetGlobFromText(task string) string {
	words := strings.Fields(task)
	for i, raw := range words {
		token := trimTaskToken(raw)
		switch {
		case strings.Contains(token, "*."):
			if dot := strings.LastIndex(token, "."); dot >= 0 {
				if ext := normalizeTaskExtension(token[dot:]); ext != "" {
					return "**/*" + ext
				}
			}
		case strings.HasPrefix(token, ".") && i+1 < len(words):
			next := trimTaskToken(words[i+1])
			if strings.HasPrefix(next, "file") || strings.HasPrefix(next, "script") || strings.HasPrefix(next, "source") || strings.HasPrefix(next, "module") {
				if ext := normalizeTaskExtension(token); ext != "" {
					return "**/*" + ext
				}
			}
		}
	}
	return ""
}

func trimTaskToken(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, "\"'`()[]{}.,;:!?")
	return strings.TrimPrefix(raw, "@")
}

func normalizeTaskExtension(raw string) string {
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

func inferLanguageScopeHint(task string) (languageScopeHint, bool) {
	for _, hint := range languageScopeHints {
		for _, alias := range hint.Aliases {
			if hasScopedAliasPhrase(task, alias) {
				return hint, true
			}
		}
	}
	return languageScopeHint{}, false
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
	return containsAny(task, phrases)
}

func canonicalTaskLanguage(raw string) string {
	lower := strings.ToLower(normalizePromptText(strings.TrimSpace(raw)))
	if lower == "" {
		return ""
	}
	for _, hint := range languageScopeHints {
		if hint.Language == lower {
			return hint.Language
		}
		for _, alias := range hint.Aliases {
			if alias == lower {
				return hint.Language
			}
		}
	}
	return lower
}

func languageGlob(language string) string {
	language = canonicalTaskLanguage(language)
	for _, hint := range languageScopeHints {
		if hint.Language == language {
			return hint.Glob
		}
	}
	return ""
}

func languageFromTargetGlob(glob string) string {
	for _, ext := range targetGlobExtensions(glob) {
		if lang := languageFromExtension(ext); lang != "" {
			return lang
		}
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

func targetGlobExtensions(glob string) []string {
	parts := splitTaskValueList(glob)
	seen := make(map[string]struct{})
	var exts []string
	for _, part := range parts {
		part = normalizeTargetGlob(part)
		if part == "" {
			continue
		}
		base := filepath.Base(part)
		if dot := strings.LastIndex(base, "."); dot >= 0 {
			if ext := normalizeTaskExtension(base[dot:]); ext != "" {
				if _, ok := seen[ext]; ok {
					continue
				}
				seen[ext] = struct{}{}
				exts = append(exts, ext)
			}
		}
	}
	return exts
}

func inferTaskTopic(task string) string {
	lower := strings.ToLower(normalizePromptText(task))
	switch {
	case containsAny(lower, []string{"security", "vulnerab", "permission", "auth"}):
		return "security"
	case containsAny(lower, []string{"performance", "latency", "slow", "throughput"}):
		return "performance"
	case containsAny(lower, []string{"test", "coverage"}):
		return "testing"
	case containsAny(lower, []string{"docs", "documentation"}):
		return "documentation"
	case containsAny(lower, []string{"cleanup", "clean up", "maintainability", "code smell", "code smells", "quality", "outdated", "risky"}):
		return "code-quality"
	default:
		return ""
	}
}

func normalizeTaskTopic(raw string) string {
	raw = strings.ToLower(normalizePromptText(strings.TrimSpace(raw)))
	if raw == "" {
		return ""
	}
	replacer := strings.NewReplacer(" ", "-", "_", "-", "/", "-", "\\", "-")
	raw = replacer.Replace(raw)
	raw = strings.Trim(raw, "-")
	return raw
}

func ensureTaskProfileSections(task string, profile taskProfile, includeEvidenceMin bool) string {
	task = appendTaskSectionIfMissing(task, "SCOPE:", string(profile.Scope))
	task = appendTaskSectionIfMissing(task, "TARGET:", profile.Target)
	task = appendTaskSectionIfMissing(task, "TARGET_LANG:", profile.TargetLang)
	task = appendTaskSectionIfMissing(task, "TARGET_GLOB:", profile.TargetGlob)
	task = appendTaskSectionIfMissing(task, "TOPIC:", profile.Topic)
	if includeEvidenceMin && profile.EvidenceMinReads > 0 {
		task = appendTaskSectionIfMissing(task, "EVIDENCE_MIN_READS:", strconv.Itoa(profile.EvidenceMinReads))
	}
	return task
}

func appendTaskSectionIfMissing(task, label, value string) string {
	if strings.TrimSpace(value) == "" {
		return task
	}
	if strings.TrimSpace(taskSection(task, label)) != "" {
		return task
	}
	if strings.TrimSpace(task) == "" {
		return label + " " + value
	}
	return strings.TrimRight(task, "\n") + "\n" + label + " " + value
}

func taskSectionStopLabels(except string) []string {
	stopLabels := make([]string, 0, len(taskSectionLabels)-1)
	for _, label := range taskSectionLabels {
		if label == except {
			continue
		}
		stopLabels = append(stopLabels, label)
	}
	return stopLabels
}

func ensureMustNotConstraints(task string, constraints ...string) string {
	missing := make([]string, 0, len(constraints))
	normalizedTask := strings.ToLower(normalizePromptText(task))
	for _, constraint := range constraints {
		if strings.TrimSpace(constraint) == "" {
			continue
		}
		normalizedConstraint := strings.ToLower(normalizePromptText(constraint))
		if strings.Contains(normalizedTask, normalizedConstraint) {
			continue
		}
		missing = append(missing, constraint)
	}
	if len(missing) == 0 {
		return task
	}
	current := taskSection(task, "MUST NOT:")
	if current == "" {
		return strings.TrimRight(task, "\n") + "\nMUST NOT: " + strings.Join(missing, " ")
	}
	replacement := "MUST NOT: " + strings.TrimSpace(current+" "+strings.Join(missing, " "))
	return replaceLabeledTaskSection(task, "MUST NOT:", replacement, taskSectionStopLabels("MUST NOT:")...)
}
