package harness

import (
	"path/filepath"
	"regexp"
	"strings"
)

var pathLikePattern = regexp.MustCompile(`(?i)(?:^|[\s"'` + "`" + `])((?:\.{0,2}/|/)[^"'\s]+|[\w.-]+\.[\w.-]+(?:/[^"'\s]+)*)`)
var slashCommandPattern = regexp.MustCompile(`(?:^|[\s"'([{])/[a-z][a-z0-9-]*\b`)

var (
	inspectVerbs = tokenSet(
		"describe", "explain", "overview", "summarize", "summary", "show",
		"walk", "through", "understand", "tour", "inspect", "review",
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
	implementArtifactTokens = tokenSet(
		"script", "scripts", "test", "tests", "tool", "tools", "helper", "helpers",
		"function", "functions", "command", "commands", "file", "files", "module", "modules",
		"handler", "handlers", "workflow", "workflows", "migration", "migrations", "patch", "patches",
	)
	debugTokens = tokenSet(
		"debug", "bug", "bugs", "broken", "failing", "failure", "failures", "error",
		"errors", "regression", "regressions", "root", "cause", "diagnose", "diagnosis",
	)
	transformTokens = tokenSet(
		"rewrite", "rephrase", "translate", "convert", "format", "transform",
	)
	followUpPronouns = tokenSet(
		"this", "that", "it", "they", "those", "these",
	)
	continuationHeadTokens = tokenSet(
		"and", "also", "but", "or", "so", "then", "yet",
	)
	promptBoundaryTokens = tokenSet(
		"prompt", "prompts", "instruction", "instructions",
	)
	selfReferenceTokens = tokenSet(
		"you", "your", "yourself",
	)
	processPromptTokens = tokenSet(
		"prompt", "prompts", "prompted", "prompting",
	)
)

func Classify(turn UserTurn, session SessionState) Classification {
	text := strings.TrimSpace(turn.Text)
	lower := strings.ToLower(text)
	ordered := tokenList(lower)
	tokens := tokenize(lower)
	scope := inferRequestScope(lower, tokens)

	class := Classification{
		Family:       FamilyAnswer,
		CanStayLocal: true,
		TopicKey:     resolveTopicKey(text, scope),
	}

	if isPromptBoundaryQuestion(text, lower, tokens) {
		class.TopicKey = ""
		class.NeedsPolicyGuard = true
		class.NeedsTerseAnswer = true
		class.Reason = "prompt boundary question"
		return class
	}
	if isProcessQuestion(text, lower, tokens, scope, class.TopicKey) {
		class.TopicKey = ""
		class.NeedsTerseAnswer = true
		class.Reason = "process question"
		return class
	}

	switch {
	case wantsImplementation(scope, ordered, tokens, lower, text):
		class.Family = FamilyImplement
		class.WantsAction = true
		class.Reason = "implementation language"
	case containsAny(tokens, debugTokens):
		class.Family = FamilyDebug
		class.WantsAction = true
		class.Reason = "debugging language"
	case wantsInspection(scope, tokens, lower):
		class.Family = FamilyInspect
		class.Reason = "inspection language"
	case wantsVerification(scope, tokens, lower):
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
	default:
		class.Reason = "default answer path"
	}

	class.WantsEvaluation = wantsEvaluation(tokens, lower)
	if class.Family == FamilyImplement && hasExplicitImplementationDeliverable(tokens) {
		class.WantsEvaluation = false
	}
	if wantsContextualEvaluationFollowUp(text, lower, ordered, tokens, class, session) {
		class.Family = FamilyInspect
		class.WantsEvaluation = true
		class.WantsAction = false
		class.IsFollowUp = true
		class.CanStayLocal = true
		if strings.TrimSpace(class.TopicKey) == "" {
			class.TopicKey = session.LastEvidence.TopicKey
		}
		class.Reason = "contextual follow-up"
	}
	if wantsContextualActionFollowUp(lower, ordered, tokens, class, session) {
		class.Family = FamilyImplement
		class.WantsAction = true
		class.IsFollowUp = true
		class.CanStayLocal = true
		if strings.TrimSpace(class.TopicKey) == "" {
			class.TopicKey = session.LastEvidence.TopicKey
		}
		class.Reason = "contextual action follow-up"
	}
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

	if !class.WantsInterpretation && session.HasRecentEvidence() && looksLikeReferentialFollowUp(tokens, lower, ordered) {
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
	fields := tokenList(input)
	out := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if field == "" {
			continue
		}
		out[field] = struct{}{}
	}
	return out
}

func tokenList(input string) []string {
	return strings.FieldsFunc(input, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
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

func wantsInspection(scope requestScope, tokens map[string]struct{}, lower string) bool {
	if !scope.Inspectable() {
		return false
	}
	if containsAny(tokens, debugTokens) || containsAny(tokens, transformTokens) {
		return false
	}
	if containsAny(tokens, implementTokens) && !wantsScopedEvaluation(scope, tokens, lower) {
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
	if hasToken(tokens, "check") {
		return true
	}
	if wantsScopedEvaluation(scope, tokens, lower) {
		return true
	}
	return strings.Contains(lower, "take me through") ||
		strings.Contains(lower, "go over") ||
		strings.Contains(lower, "tell me about") ||
		strings.Contains(lower, "look at") ||
		strings.Contains(lower, "look over") ||
		strings.Contains(lower, "how we looking") ||
		strings.Contains(lower, "how's this looking") ||
		strings.Contains(lower, "how is this looking") ||
		strings.Contains(lower, "what's in") ||
		strings.Contains(lower, "what is in")
}

func wantsImplementation(scope requestScope, ordered []string, tokens map[string]struct{}, lower, text string) bool {
	if !containsImplementationSignal(tokens) {
		return false
	}
	if scope.Inspectable() && wantsScopedEvaluation(scope, tokens, lower) && !hasExplicitImplementationDeliverable(tokens) {
		return false
	}
	if looksQuestionLike(text) && lacksConcreteActionTarget(ordered) {
		return false
	}
	return true
}

func containsImplementationSignal(tokens map[string]struct{}) bool {
	if containsAny(tokens, implementTokens) {
		return true
	}
	return hasToken(tokens, "write") && containsAny(tokens, implementArtifactTokens)
}

func hasExplicitImplementationDeliverable(tokens map[string]struct{}) bool {
	return containsAny(tokens, implementArtifactTokens)
}

func wantsEvaluation(tokens map[string]struct{}, lower string) bool {
	if containsAny(tokens, evaluationTokens) {
		return true
	}
	return strings.Contains(lower, "what do you think") ||
		strings.Contains(lower, "tell me what you think") ||
		strings.Contains(lower, "let me know what you think") ||
		strings.Contains(lower, "how we looking") ||
		strings.Contains(lower, "how's this looking") ||
		strings.Contains(lower, "how is this looking") ||
		hasQualityAssessmentPhrase(lower) ||
		strings.Contains(lower, "anything to change") ||
		strings.Contains(lower, "anything i need") ||
		strings.Contains(lower, "cleaned up or changed") ||
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

func wantsVerification(scope requestScope, tokens map[string]struct{}, lower string) bool {
	if hasToken(tokens, "verify") || hasToken(tokens, "validation") || hasToken(tokens, "validate") ||
		hasToken(tokens, "confirm") || hasToken(tokens, "prove") || hasToken(tokens, "test") {
		return true
	}
	if strings.Contains(lower, "make sure") {
		return true
	}
	if !hasToken(tokens, "check") {
		return false
	}
	if scope.Inspectable() && !hasVerificationTarget(tokens) {
		return false
	}
	return true
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

func looksLikeReferentialFollowUp(tokens map[string]struct{}, lower string, ordered []string) bool {
	if len(tokens) == 0 {
		return false
	}
	if strings.Contains(lower, "what do you think") || strings.Contains(lower, "what does that mean") || strings.Contains(lower, "so what") {
		return true
	}
	if len(ordered) > 15 && !startsWithContinuationHeadToken(ordered) {
		return false
	}
	for pronoun := range followUpPronouns {
		if _, ok := tokens[pronoun]; ok {
			return true
		}
	}
	return false
}

func wantsContextualEvaluationFollowUp(text, lower string, ordered []string, tokens map[string]struct{}, class Classification, session SessionState) bool {
	if !session.HasRecentEvidence() || strings.TrimSpace(session.LastEvidence.TopicKey) == "" {
		return false
	}
	if strings.TrimSpace(class.TopicKey) != "" {
		return false
	}
	if class.Family == FamilyDebug || class.Family == FamilyResearch || class.Family == FamilyTransform || class.WantsAction {
		return false
	}
	if len(ordered) == 0 || len(ordered) > 12 {
		return false
	}
	if class.WantsEvaluation {
		if looksLikeReferentialFollowUp(tokens, lower, ordered) || looksLikeContextualContinuation(lower) {
			return true
		}
		if looksQuestionLike(text) && containsImplementToken(ordered) {
			return lacksConcreteActionTarget(ordered)
		}
		return false
	}
	if !containsImplementToken(ordered) {
		return false
	}
	if !looksQuestionLike(text) && !looksLikeReferentialFollowUp(tokens, lower, ordered) && !looksLikeContextualContinuation(lower) {
		return false
	}
	return lacksConcreteActionTarget(ordered)
}

func wantsContextualActionFollowUp(lower string, ordered []string, tokens map[string]struct{}, class Classification, session SessionState) bool {
	if !session.HasRecentEvidence() || strings.TrimSpace(session.LastEvidence.TopicKey) == "" {
		return false
	}
	if strings.TrimSpace(class.TopicKey) != "" {
		return false
	}
	if class.Family != FamilyImplement && !class.WantsAction {
		return false
	}
	if class.Family == FamilyDebug || class.Family == FamilyResearch || class.Family == FamilyTransform {
		return false
	}
	return looksLikeReferentialFollowUp(tokens, lower, ordered) || looksLikeContextualContinuation(lower)
}

func wantsScopedEvaluation(scope requestScope, tokens map[string]struct{}, lower string) bool {
	if !scope.Inspectable() {
		return false
	}
	if wantsEvaluation(tokens, lower) {
		return true
	}
	return (strings.Contains(lower, "tell me if") || strings.Contains(lower, "let me know if")) &&
		hasQualityAssessmentPhrase(lower)
}

func looksQuestionLike(text string) bool {
	return strings.Contains(text, "?")
}

func looksLikeContextualContinuation(lower string) bool {
	trimmed := strings.TrimLeft(lower, `"'([{ `)
	ordered := tokenList(trimmed)
	if startsWithContinuationHeadToken(ordered) {
		return true
	}
	return strings.HasPrefix(trimmed, "what about ") ||
		strings.HasPrefix(trimmed, "how about ")
}

func startsWithContinuationHeadToken(ordered []string) bool {
	if len(ordered) == 0 {
		return false
	}
	_, ok := continuationHeadTokens[ordered[0]]
	return ok
}

func lacksConcreteActionTarget(ordered []string) bool {
	lastVerb := lastImplementTokenIndex(ordered)
	if lastVerb < 0 {
		return false
	}
	for _, token := range ordered[lastVerb+1:] {
		if isWeakFollowUpToken(token) {
			continue
		}
		return false
	}
	return true
}

func containsImplementToken(ordered []string) bool {
	return lastImplementTokenIndex(ordered) >= 0
}

func lastImplementTokenIndex(ordered []string) int {
	lastVerb := -1
	for i, token := range ordered {
		if _, ok := implementTokens[token]; ok {
			lastVerb = i
		}
	}
	return lastVerb
}

func isWeakFollowUpToken(token string) bool {
	switch token {
	case "", "a", "an", "the", "to", "i", "me", "my", "we", "us", "our", "you", "your",
		"need", "needs", "should", "can", "could", "would", "must", "please":
		return true
	default:
		_, ok := followUpPronouns[token]
		return ok
	}
}

func hasVerificationTarget(tokens map[string]struct{}) bool {
	return hasToken(tokens, "test") ||
		hasToken(tokens, "tests") ||
		hasToken(tokens, "build") ||
		hasToken(tokens, "compile") ||
		hasToken(tokens, "compiles") ||
		hasToken(tokens, "lint") ||
		hasToken(tokens, "syntax") ||
		hasToken(tokens, "pass") ||
		hasToken(tokens, "passes")
}

func hasQualityAssessmentPhrase(lower string) bool {
	return strings.Contains(lower, "look ok") ||
		strings.Contains(lower, "looks ok") ||
		strings.Contains(lower, "look okay") ||
		strings.Contains(lower, "looks okay") ||
		strings.Contains(lower, "look good") ||
		strings.Contains(lower, "looks good") ||
		strings.Contains(lower, "seem ok") ||
		strings.Contains(lower, "seems ok") ||
		strings.Contains(lower, "seem okay") ||
		strings.Contains(lower, "seems okay") ||
		strings.Contains(lower, "seem good") ||
		strings.Contains(lower, "seems good")
}

func resolveTopicKey(text string, scope requestScope) string {
	matches := pathLikePattern.FindStringSubmatch(text)
	if len(matches) > 1 {
		return "path:" + filepath.Clean(strings.Trim(matches[1], `"'`))
	}
	return strings.TrimSpace(scope.TopicKey)
}

func hasToken(tokens map[string]struct{}, token string) bool {
	_, ok := tokens[token]
	return ok
}

func isPromptBoundaryQuestion(text, lower string, tokens map[string]struct{}) bool {
	if !containsAny(tokens, promptBoundaryTokens) {
		return false
	}
	if strings.Contains(lower, "your prompt") ||
		strings.Contains(lower, "your system prompt") ||
		strings.Contains(lower, "your developer prompt") ||
		strings.Contains(lower, "your instructions") ||
		strings.Contains(lower, "tell me your prompt") ||
		strings.Contains(lower, "tell me your instructions") ||
		strings.Contains(lower, "allowed to tell me your prompt") ||
		strings.Contains(lower, "allowed to tell me your instructions") {
		return true
	}
	if strings.Contains(lower, "system prompt") ||
		strings.Contains(lower, "developer prompt") ||
		strings.Contains(lower, "hidden prompt") ||
		strings.Contains(lower, "internal instructions") ||
		strings.Contains(lower, "developer instructions") ||
		strings.Contains(lower, "system instructions") {
		return looksQuestionLike(text) || startsWithAny(lower,
			"what", "whats", "what's", "tell me", "show me", "if you were allowed",
		)
	}
	return false
}

func isProcessQuestion(text, lower string, tokens map[string]struct{}, scope requestScope, topicKey string) bool {
	if !containsAny(tokens, selfReferenceTokens) {
		return false
	}
	if !looksQuestionLike(text) && !startsWithAny(lower,
		"are you", "do you", "did you", "why do you", "why did you", "why didnt you", "why didn't you", "what do you", "what did you", "which skills", "what skills",
	) {
		return false
	}
	if containsAny(tokens, processPromptTokens) {
		return true
	}
	if hasToken(tokens, "skill") || hasToken(tokens, "skills") {
		return true
	}
	if hasToken(tokens, "use") || hasToken(tokens, "using") || hasToken(tokens, "used") {
		if slashCommandMentioned(lower) {
			return true
		}
		if scope.Inspectable() || strings.HasPrefix(topicKey, "path:") || strings.HasPrefix(topicKey, "files:") {
			return false
		}
		return true
	}
	return slashCommandMentioned(lower)
}

func slashCommandMentioned(lower string) bool {
	return slashCommandPattern.FindStringIndex(lower) != nil
}

func startsWithAny(input string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(input, prefix) {
			return true
		}
	}
	return false
}
