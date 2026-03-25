package harness

import (
	"path/filepath"
	"regexp"
	"strings"
)

var pathLikePattern = regexp.MustCompile(`(?i)(?:^|[\s"'` + "`" + `])((?:\.{0,2}/|/)[^"'\s]+|[\w.-]+\.[\w.-]+(?:/[^"'\s]+)*)`)

var (
	inspectVerbs = tokenSet(
		"describe", "explain", "overview", "summarize", "summary", "show",
		"walk", "through", "understand", "tour", "inspect", "review",
	)
	workspaceNouns = tokenSet(
		"directory", "folder", "repo", "repository", "codebase", "project", "tree",
	)
	evaluationTokens = tokenSet(
		"issue", "issues", "problem", "problems", "risk", "risks", "concern", "concerns",
		"recommend", "recommendation", "recommendations", "improve", "improvements",
		"should", "opinion", "opinions", "audit", "smell", "smells", "cleanup",
	)
	interpretationTokens = tokenSet(
		"think", "thoughts", "mean", "means", "imply", "implies", "impression",
		"standout", "stands", "suggests", "suggest", "takeaway", "takeaways",
	)
	implementTokens = tokenSet(
		"implement", "implementation", "build", "create", "add", "change", "update",
		"modify", "remove", "delete", "refactor", "wire", "replace", "fix",
	)
	debugTokens = tokenSet(
		"debug", "bug", "bugs", "broken", "failing", "failure", "failures", "error",
		"errors", "regression", "regressions", "root", "cause", "diagnose", "diagnosis",
	)
	verifyTokens = tokenSet(
		"verify", "validation", "validate", "confirm", "check", "prove", "test",
	)
	transformTokens = tokenSet(
		"rewrite", "rephrase", "translate", "convert", "format", "transform",
	)
	followUpPronouns = tokenSet(
		"this", "that", "it", "they", "those", "these",
	)
)

func Classify(turn UserTurn, session SessionState) Classification {
	text := strings.TrimSpace(turn.Text)
	lower := strings.ToLower(text)
	tokens := tokenize(lower)

	class := Classification{
		Family:       FamilyAnswer,
		CanStayLocal: true,
		TopicKey:     resolveTopicKey(text, tokens),
	}

	switch {
	case containsAny(tokens, implementTokens):
		class.Family = FamilyImplement
		class.WantsAction = true
		class.Reason = "implementation language"
	case containsAny(tokens, debugTokens):
		class.Family = FamilyDebug
		class.WantsAction = true
		class.Reason = "debugging language"
	case wantsVerification(tokens, lower):
		class.Family = FamilyVerify
		class.WantsAction = true
		class.Reason = "verification language"
	case wantsResearch(tokens, lower):
		class.Family = FamilyResearch
		class.NeedsExternalSources = true
		class.CanStayLocal = false
		class.Reason = "research language"
	case containsAny(tokens, transformTokens):
		class.Family = FamilyTransform
		class.Reason = "transform language"
	case wantsInspection(tokens, lower):
		class.Family = FamilyInspect
		class.Reason = "inspection language"
	default:
		class.Reason = "default answer path"
	}

	class.WantsEvaluation = wantsEvaluation(tokens, lower)
	if wantsInterpretation(tokens, lower) && session.HasRecentEvidence() && !class.WantsAction {
		class.WantsInterpretation = true
		class.IsFollowUp = true
		if class.Family == FamilyAnswer {
			class.Family = FamilyInspect
		}
		if strings.TrimSpace(class.TopicKey) == "" {
			class.TopicKey = session.LastEvidence.TopicKey
		}
		class.Reason = "interpretive follow-up"
	}

	if !class.WantsInterpretation && session.HasRecentEvidence() && looksLikeReferentialFollowUp(tokens, lower) {
		class.IsFollowUp = true
		if strings.TrimSpace(class.TopicKey) == "" {
			class.TopicKey = session.LastEvidence.TopicKey
		}
	}
	if class.Family == FamilyResearch && strings.TrimSpace(class.TopicKey) == "" {
		class.TopicKey = session.LastEvidence.TopicKey
	}

	return class
}

func tokenize(input string) map[string]struct{} {
	fields := strings.FieldsFunc(input, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	out := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if field == "" {
			continue
		}
		out[field] = struct{}{}
	}
	return out
}

func tokenSet(values ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func containsAny(tokens, candidates map[string]struct{}) bool {
	for token := range candidates {
		if _, ok := tokens[token]; ok {
			return true
		}
	}
	return false
}

func wantsInspection(tokens map[string]struct{}, lower string) bool {
	if !containsAny(tokens, workspaceNouns) {
		return false
	}
	if containsAny(tokens, implementTokens) || containsAny(tokens, debugTokens) || containsAny(tokens, transformTokens) {
		return false
	}
	if containsAny(tokens, inspectVerbs) {
		return true
	}
	if hasToken(tokens, "search") {
		return true
	}
	if hasToken(tokens, "about") {
		return true
	}
	return strings.Contains(lower, "take me through") || strings.Contains(lower, "go over")
}

func wantsEvaluation(tokens map[string]struct{}, lower string) bool {
	if containsAny(tokens, evaluationTokens) {
		return true
	}
	return strings.Contains(lower, "what do you think") ||
		strings.Contains(lower, "how good") ||
		strings.Contains(lower, "what stands out")
}

func wantsInterpretation(tokens map[string]struct{}, lower string) bool {
	if containsAny(tokens, interpretationTokens) {
		return true
	}
	return strings.Contains(lower, "what do you think") ||
		strings.Contains(lower, "what do you make of") ||
		strings.Contains(lower, "what does that mean") ||
		strings.Contains(lower, "does that mean") ||
		strings.Contains(lower, "so what")
}

func wantsVerification(tokens map[string]struct{}, lower string) bool {
	if containsAny(tokens, verifyTokens) {
		return true
	}
	return strings.Contains(lower, "make sure")
}

func wantsResearch(tokens map[string]struct{}, lower string) bool {
	if hasToken(tokens, "internet") || hasToken(tokens, "online") {
		return true
	}
	if strings.Contains(lower, "look up") || strings.Contains(lower, "search the web") || strings.Contains(lower, "web search") || strings.Contains(lower, "search online") {
		return true
	}
	if strings.Contains(lower, "latest docs") || strings.Contains(lower, "latest documentation") || strings.Contains(lower, "official docs") || strings.Contains(lower, "official documentation") {
		return true
	}
	if (hasToken(tokens, "latest") || hasToken(tokens, "recent")) && (hasToken(tokens, "docs") || hasToken(tokens, "documentation") || hasToken(tokens, "news")) {
		return true
	}
	if hasToken(tokens, "web") && (hasToken(tokens, "docs") || hasToken(tokens, "documentation") || hasToken(tokens, "site")) {
		return true
	}
	return false
}

func looksLikeReferentialFollowUp(tokens map[string]struct{}, lower string) bool {
	if len(tokens) == 0 {
		return false
	}
	if strings.Contains(lower, "what do you think") || strings.Contains(lower, "what does that mean") {
		return true
	}
	for pronoun := range followUpPronouns {
		if _, ok := tokens[pronoun]; ok {
			return true
		}
	}
	return false
}

func resolveTopicKey(text string, tokens map[string]struct{}) string {
	matches := pathLikePattern.FindStringSubmatch(text)
	if len(matches) > 1 {
		return "path:" + filepath.Clean(strings.Trim(matches[1], `"'`))
	}
	switch {
	case hasToken(tokens, "directory"), hasToken(tokens, "folder"), hasToken(tokens, "tree"):
		return "workspace:directory"
	case hasToken(tokens, "repo"), hasToken(tokens, "repository"), hasToken(tokens, "codebase"), hasToken(tokens, "project"):
		return "workspace:repository"
	default:
		return ""
	}
}

func hasToken(tokens map[string]struct{}, token string) bool {
	_, ok := tokens[token]
	return ok
}
