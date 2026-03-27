package harness

import (
	"path/filepath"
	"regexp"
	"sort"
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
	planningTokens = tokenSet(
		"plan", "plans", "roadmap", "roadmaps", "priority", "priorities",
		"prioritize", "prioritise", "phase", "phases", "sequence", "sequencing",
	)
	implementTokens = tokenSet(
		"implement", "implementation", "build", "create", "add", "change", "update",
		"clean",
		"modify", "remove", "delete", "refactor", "wire", "replace", "fix", "patch",
	)
	implementArtifactTokens = tokenSet(
		"script", "scripts", "test", "tests", "tool", "tools", "helper", "helpers",
		"function", "functions", "command", "commands", "file", "files", "module", "modules",
		"handler", "handlers", "workflow", "workflows", "migration", "migrations", "patch", "patches",
	)
	inspectScopeArtifactTokens = tokenSet(
		"file", "files", "module", "modules", "code",
	)
	debugTokens = tokenSet(
		"debug", "bug", "bugs", "broken", "failing", "failure", "failures", "error",
		"errors", "regression", "regressions", "root", "cause", "diagnose", "diagnosis",
	)
	transformTokens = tokenSet(
		"rewrite", "rephrase", "translate", "convert", "format", "transform",
	)
	ideationTokens = tokenSet(
		"idea", "ideas", "brainstorm", "brainstorming", "concept", "concepts",
		"direction", "directions", "option", "options", "theme", "themes",
		"mock", "mockup", "mockups", "prototype", "prototypes",
	)
	decisionSupportTokens = tokenSet(
		"decide", "decision", "choose", "choice", "choices", "pick",
		"compare", "comparison", "comparisons",
	)
	previewTokens = tokenSet(
		"server", "preview", "browser", "mockup", "mockups", "localhost", "port", "ports",
	)
	previewActionTokens = tokenSet(
		"start", "launch", "open", "serve", "show", "restart", "see", "view",
	)
	followUpPronouns = tokenSet(
		"this", "that", "it", "they", "those", "these",
	)
	continuationHeadTokens = tokenSet(
		"and", "also", "but", "or", "so", "then", "yet",
	)
	continuationConfirmTokens = tokenSet(
		"yes", "yeah", "yep", "sure", "ok", "okay", "alright", "fine",
		"go", "do", "please", "proceed", "continue", "carry", "on", "then",
	)
	continuationNegativeTokens = tokenSet(
		"no", "nah", "nope", "stop", "cancel", "dont", "don't", "not", "later", "thanks", "thank", "cheers",
	)
	continuationReferentialTokens = tokenSet(
		"see", "above", "earlier", "previous", "prior", "same",
	)
	promptBoundaryTokens = tokenSet(
		"prompt", "prompts", "instruction", "instructions",
	)
	promptBoundaryReferenceTokens = tokenSet(
		"you", "your", "yours", "forge", "harness", "system", "developer", "hidden", "internal",
	)
	promptDisclosureTokens = tokenSet(
		"tell", "show", "give", "share", "copy", "paste", "send", "provide", "reveal", "quote", "repeat", "paraphrase",
	)
	promptQualifierTokens = tokenSet(
		"exact", "real", "actual", "accurate", "verbatim", "full",
	)
	selfReferenceTokens = tokenSet(
		"you", "your", "yourself",
	)
	processPromptTokens = tokenSet(
		"prompt", "prompts", "prompted", "prompting",
	)
	processFollowUpTokens = tokenSet(
		"skill", "skills", "use", "using", "used", "copy", "yours", "mine",
	)
)

var (
	classifierLexiconWords = buildClassifierLexiconWords()
	classifierLexiconSet   = tokenSet(classifierLexiconWords...)
)

func Classify(turn UserTurn, session SessionState) Classification {
	originalText := strings.TrimSpace(turn.Text)
	if pending, ok := classifyPendingActionContinuation(originalText, session); ok {
		return pending
	}

	text, detachedPolicyGuard := stripDetachedPromptBoundary(originalText)
	lower := strings.ToLower(text)
	ordered := tokenList(lower)
	tokens := tokenize(lower)
	scope := inferRequestScope(lower, tokens)

	class := Classification{
		Family:                  FamilyAnswer,
		CanStayLocal:            true,
		PrefersVisibleExecution: prefersVisibleExecution(lower, tokens),
		TopicKey:                resolveTopicKey(text, scope),
		TaskText:                text,
	}
	if detachedPolicyGuard {
		class.DetachedPolicyGuard = true
		class.ResponsePostlude = promptBoundaryRefusal
	}

	if followUp := isPromptBoundaryFollowUp(lower, tokens, scope, session); followUp || isPromptBoundaryQuestion(text, lower, tokens) {
		class.TopicKey = ""
		class.NeedsPolicyGuard = true
		class.NeedsTerseAnswer = true
		class.IsFollowUp = followUp
		class.Reason = "prompt boundary question"
		if followUp {
			class.Reason = "prompt boundary follow-up"
		}
		return class
	}
	if followUp := isProcessFollowUp(lower, tokens, scope, session); followUp || isProcessQuestion(text, lower, tokens, scope, class.TopicKey) {
		class.TopicKey = ""
		class.NeedsTerseAnswer = true
		class.IsFollowUp = followUp
		class.Reason = "process question"
		if followUp {
			class.Reason = "process follow-up"
		}
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
	case wantsInspection(scope, ordered, tokens, lower):
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
	if wantsContextualPlanningFollowUp(lower, ordered, tokens, class, session) {
		class.Family = FamilyAnswer
		class.WantsAction = false
		class.IsFollowUp = true
		class.CanStayLocal = true
		if strings.TrimSpace(class.TopicKey) == "" {
			class.TopicKey = session.LastEvidence.TopicKey
		}
		class.Reason = "planning follow-up"
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
	if strings.TrimSpace(class.TaskText) == "" {
		class.TaskText = text
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
	fields := strings.FieldsFunc(input, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if field == "" {
			continue
		}
		out = append(out, canonicalizeClassifierToken(field))
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

func buildClassifierLexiconWords() []string {
	set := make(map[string]struct{})
	addSet := func(values map[string]struct{}) {
		for value := range values {
			set[value] = struct{}{}
		}
	}
	addWords := func(values ...string) {
		for _, value := range values {
			value = strings.TrimSpace(strings.ToLower(value))
			if value != "" {
				set[value] = struct{}{}
			}
		}
	}

	addSet(inspectVerbs)
	addSet(evaluationTokens)
	addSet(interpretationTokens)
	addSet(planningTokens)
	addSet(implementTokens)
	addSet(implementArtifactTokens)
	addSet(debugTokens)
	addSet(transformTokens)
	addSet(ideationTokens)
	addSet(decisionSupportTokens)
	addSet(previewTokens)
	addSet(previewActionTokens)
	addSet(followUpPronouns)
	addSet(continuationHeadTokens)
	addSet(continuationConfirmTokens)
	addSet(continuationNegativeTokens)
	addSet(continuationReferentialTokens)
	addSet(promptBoundaryTokens)
	addSet(promptBoundaryReferenceTokens)
	addSet(promptDisclosureTokens)
	addSet(promptQualifierTokens)
	addSet(selfReferenceTokens)
	addSet(processPromptTokens)
	addSet(processFollowUpTokens)
	addWords(
		"directory", "folder", "tree", "dir", "repo", "repository", "codebase", "project",
		"file", "files", "script", "scripts", "source", "sources", "module", "modules", "code",
		"extension", "extensions", "search", "about", "check", "look",
		"verify", "validation", "validate", "confirm", "prove", "test", "tests", "build", "compile", "compiles", "lint", "syntax", "pass", "passes",
		"internet", "online", "look", "latest", "recent", "docs", "documentation", "news", "web", "site",
		"what", "whats", "tell", "know", "made", "make", "good", "okay",
	)
	for _, hint := range languageScopeHints {
		addWords(hint.Language)
		addWords(hint.Aliases...)
	}

	words := make([]string, 0, len(set))
	for word := range set {
		words = append(words, word)
	}
	sort.Strings(words)
	return words
}

func canonicalizeClassifierToken(token string) string {
	if token == "" {
		return ""
	}
	if _, ok := classifierLexiconSet[token]; ok {
		return token
	}
	if len(token) < 4 {
		return token
	}

	for _, candidate := range classifierLexiconWords {
		if len(candidate) < 4 {
			continue
		}
		if token[0] != candidate[0] {
			continue
		}
		if absInt(len(token)-len(candidate)) > 1 {
			continue
		}
		if withinClassifierTypoDistance(token, candidate) {
			return candidate
		}
	}
	return token
}

func withinClassifierTypoDistance(a, b string) bool {
	return withinEditDistanceOne(a, b) || withinAdjacentTransposition(a, b)
}

func withinAdjacentTransposition(a, b string) bool {
	if len(a) != len(b) || len(a) < 2 {
		return false
	}
	for i := 0; i < len(a)-1; i++ {
		if a[i] == b[i] {
			continue
		}
		if a[i] == b[i+1] && a[i+1] == b[i] && a[i+2:] == b[i+2:] {
			return a[:i] == b[:i]
		}
		return false
	}
	return false
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func containsAny(tokens, candidates map[string]struct{}) bool {
	for token := range candidates {
		if _, ok := tokens[token]; ok {
			return true
		}
	}
	return false
}

func wantsInspection(scope requestScope, ordered []string, tokens map[string]struct{}, lower string) bool {
	if !scope.Inspectable() {
		return false
	}
	if containsAny(tokens, debugTokens) || containsAny(tokens, transformTokens) {
		return false
	}
	if containsImplementationVerb(ordered) && !wantsScopedEvaluation(scope, tokens, lower) {
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
	if !containsImplementationSignal(ordered, tokens) {
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

func containsImplementationSignal(ordered []string, tokens map[string]struct{}) bool {
	if containsImplementationVerb(ordered) {
		return true
	}
	return hasToken(tokens, "write") && containsAny(tokens, implementArtifactTokens)
}

func containsImplementationVerb(ordered []string) bool {
	return lastImplementTokenIndex(ordered) >= 0
}

func hasExplicitImplementationDeliverable(tokens map[string]struct{}) bool {
	for token := range implementArtifactTokens {
		if _, ok := tokens[token]; !ok {
			continue
		}
		if _, generic := inspectScopeArtifactTokens[token]; generic {
			continue
		}
		return true
	}
	return false
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
		hasWouldChangeReviewPhrase(lower) ||
		strings.Contains(lower, "anything to change") ||
		strings.Contains(lower, "anything i need") ||
		strings.Contains(lower, "cleaned up or changed") ||
		strings.Contains(lower, "how good") ||
		strings.Contains(lower, "what stands out")
}

func hasWouldChangeReviewPhrase(lower string) bool {
	return strings.Contains(lower, "anything you would change") ||
		strings.Contains(lower, "anything you'd change") ||
		strings.Contains(lower, "what you would change") ||
		strings.Contains(lower, "what you'd change")
}

func wantsPlanning(tokens map[string]struct{}, lower string) bool {
	if containsAny(tokens, planningTokens) {
		return true
	}
	return strings.Contains(lower, "next steps") ||
		strings.Contains(lower, "what should we fix first") ||
		strings.Contains(lower, "what should i fix first") ||
		strings.Contains(lower, "what should change first")
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

func prefersVisibleExecution(lower string, tokens map[string]struct{}) bool {
	return asksForProgressUpdates(lower) || wantsCollaborativeIdeation(lower, tokens) || wantsVisiblePreviewExecution(lower, tokens)
}

func asksForProgressUpdates(lower string) bool {
	return strings.Contains(lower, "update me") ||
		strings.Contains(lower, "update us") ||
		strings.Contains(lower, "keep me updated") ||
		strings.Contains(lower, "keep us updated") ||
		strings.Contains(lower, "at every step") ||
		strings.Contains(lower, "each step") ||
		strings.Contains(lower, "as you go") ||
		strings.Contains(lower, "along the way")
}

func wantsCollaborativeIdeation(lower string, tokens map[string]struct{}) bool {
	wantsIdeas := containsAny(tokens, ideationTokens) ||
		strings.Contains(lower, "mock up") ||
		strings.Contains(lower, "mockup")
	wantsDecisionHelp := containsAny(tokens, decisionSupportTokens) ||
		strings.Contains(lower, "help me decide") ||
		strings.Contains(lower, "help me choose") ||
		strings.Contains(lower, "which one") ||
		strings.Contains(lower, "which should")
	wantsIdeaPresentation := strings.Contains(lower, "show me your ideas") ||
		strings.Contains(lower, "show me the ideas") ||
		strings.Contains(lower, "show me some ideas")
	return (wantsIdeas && wantsDecisionHelp) || (wantsIdeas && wantsIdeaPresentation)
}

func wantsVisiblePreviewExecution(lower string, tokens map[string]struct{}) bool {
	mentionsPreviewTarget := containsAny(tokens, previewTokens) ||
		strings.Contains(lower, "127.0.0.1") ||
		strings.Contains(lower, "http://") ||
		strings.Contains(lower, "https://")
	requestsPreviewAction := containsAny(tokens, previewActionTokens) ||
		strings.Contains(lower, "spin up") ||
		strings.Contains(lower, "showing me")
	return mentionsPreviewTarget && requestsPreviewAction
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

func wantsContextualPlanningFollowUp(lower string, ordered []string, tokens map[string]struct{}, class Classification, session SessionState) bool {
	if !session.HasRecentEvidence() || strings.TrimSpace(session.LastEvidence.TopicKey) == "" {
		return false
	}
	if class.NeedsPolicyGuard || class.NeedsTerseAnswer || class.WantsInterpretation {
		return false
	}
	if class.Family != FamilyAnswer && class.Family != FamilyInspect && class.Family != FamilyImplement {
		return false
	}
	if class.Family == FamilyImplement && hasExplicitImplementationDeliverable(tokens) {
		return false
	}
	if strings.TrimSpace(class.TopicKey) != "" && class.TopicKey != session.LastEvidence.TopicKey {
		return false
	}
	if len(ordered) == 0 || len(ordered) > 12 {
		return false
	}
	return wantsPlanning(tokens, lower)
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
	for i := range ordered {
		if isImplementationVerbToken(ordered, i) {
			lastVerb = i
		}
	}
	return lastVerb
}

func isImplementationVerbToken(ordered []string, idx int) bool {
	if idx < 0 || idx >= len(ordered) {
		return false
	}
	token := ordered[idx]
	if _, ok := implementTokens[token]; !ok {
		return false
	}
	if token == "update" && isProgressUpdateVerbContext(ordered, idx) {
		return false
	}
	return true
}

func isProgressUpdateVerbContext(ordered []string, idx int) bool {
	if idx+1 < len(ordered) {
		switch ordered[idx+1] {
		case "me", "us", "them", "him", "her":
			return true
		}
	}
	if idx-1 >= 0 {
		switch ordered[idx-1] {
		case "me", "us", "them", "him", "her":
			return true
		}
	}
	return false
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
	if candidate := firstConcretePathCandidate(text); candidate != "" {
		return "path:" + filepath.Clean(candidate)
	}
	return strings.TrimSpace(scope.TopicKey)
}

func firstConcretePathCandidate(text string) string {
	matches := pathLikePattern.FindAllStringSubmatch(text, -1)
	for _, match := range matches {
		if len(match) <= 1 {
			continue
		}
		candidate := strings.Trim(match[1], `"'`)
		if !looksLikeConcretePathCandidate(candidate) {
			continue
		}
		return candidate
	}
	return ""
}

func looksLikeConcretePathCandidate(candidate string) bool {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return false
	}
	if strings.HasPrefix(candidate, "/") || strings.HasPrefix(candidate, "./") || strings.HasPrefix(candidate, "../") {
		return true
	}
	if strings.Contains(candidate, "/") {
		return true
	}
	if strings.HasPrefix(candidate, ".") {
		return candidate != "." && candidate != ".."
	}
	ext := strings.TrimPrefix(filepath.Ext(candidate), ".")
	return containsASCIIAlpha(ext)
}

func containsASCIIAlpha(value string) bool {
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return true
		}
	}
	return false
}

func hasToken(tokens map[string]struct{}, token string) bool {
	_, ok := tokens[token]
	return ok
}

func isPromptBoundaryQuestion(text, lower string, tokens map[string]struct{}) bool {
	if !hasPromptBoundaryConcept(tokens) {
		return false
	}
	if len(tokenList(lower)) <= 3 && hasToken(tokens, "prompt") && (hasToken(tokens, "forge") || hasToken(tokens, "harness")) {
		return true
	}
	if containsAny(tokens, promptBoundaryReferenceTokens) &&
		(looksQuestionLike(text) ||
			startsWithAny(lower,
				"what", "whats", "what's", "tell me", "show me", "give me", "share", "copy", "paste", "reveal", "provide", "if you were allowed",
			) ||
			containsAny(tokens, promptDisclosureTokens) ||
			containsAny(tokens, promptQualifierTokens)) {
		return true
	}
	scope := inferRequestScope(lower, tokens)
	if !scope.Inspectable() &&
		len(tokenList(lower)) <= 6 &&
		(containsAny(tokens, promptDisclosureTokens) || containsAny(tokens, promptQualifierTokens)) {
		return true
	}
	return false
}

func isPromptBoundaryFollowUp(lower string, tokens map[string]struct{}, scope requestScope, session SessionState) bool {
	if !session.HasRecentMeta() || session.LastMeta != MetaPromptBoundary {
		return false
	}
	ordered := tokenList(lower)
	if scope.Inspectable() || containsAny(tokens, debugTokens) || containsImplementationSignal(ordered, tokens) || wantsResearch(tokens, lower) {
		return false
	}
	if len(ordered) == 0 || len(ordered) > 5 {
		return false
	}
	if hasPromptBoundaryConcept(tokens) ||
		containsAny(tokens, promptDisclosureTokens) ||
		containsAny(tokens, promptQualifierTokens) {
		return true
	}
	return looksLikeReferentialFollowUp(tokens, lower, ordered)
}

func isProcessQuestion(text, lower string, tokens map[string]struct{}, scope requestScope, topicKey string) bool {
	if !containsAny(tokens, selfReferenceTokens) {
		return false
	}
	if mentionsConcreteInspectScope(scope, tokens, lower) &&
		!containsAny(tokens, processPromptTokens) &&
		!hasToken(tokens, "skill") &&
		!hasToken(tokens, "skills") &&
		!slashCommandMentioned(lower) {
		return false
	}
	if !looksQuestionLike(text) && !startsWithAny(lower,
		"are you", "do you", "did you", "can you", "could you",
		"have you", "do you have",
		"why do you", "why did you", "why didnt you", "why didn't you", "why are you",
		"what", "what do you", "what did you", "which", "which skills", "what skills",
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

func isProcessFollowUp(lower string, tokens map[string]struct{}, scope requestScope, session SessionState) bool {
	if !session.HasRecentMeta() || session.LastMeta != MetaProcess {
		return false
	}
	ordered := tokenList(lower)
	if scope.Inspectable() || containsAny(tokens, debugTokens) || containsImplementationSignal(ordered, tokens) || wantsResearch(tokens, lower) {
		return false
	}
	if len(ordered) == 0 || len(ordered) > 10 {
		return false
	}
	if containsAny(tokens, processPromptTokens) || containsAny(tokens, processFollowUpTokens) {
		return true
	}
	return looksLikeReferentialFollowUp(tokens, lower, ordered)
}

func slashCommandMentioned(lower string) bool {
	return slashCommandPattern.FindStringIndex(lower) != nil
}

func classifyPendingActionContinuation(text string, session SessionState) (Classification, bool) {
	if !session.HasPendingAction() {
		return Classification{}, false
	}
	lower := strings.ToLower(strings.TrimSpace(text))
	tokens := tokenize(lower)
	if !looksLikePendingActionContinuation(text, lower, tokens) {
		return Classification{}, false
	}
	pending := session.PendingAction
	class := Classification{
		Family:               pending.Family,
		WantsEvaluation:      pending.WantsEvaluation,
		WantsAction:          pending.WantsAction,
		WantsInterpretation:  pending.WantsInterpretation,
		NeedsExternalSources: pending.NeedsExternalSources,
		CanStayLocal:         pending.CanStayLocal || pending.Family != FamilyResearch,
		IsFollowUp:           true,
		TopicKey:             pending.TopicKey,
		TaskText:             pending.TaskText,
		ResponsePostlude:     pending.ResponsePostlude,
		Reason:               "pending action continuation",
	}
	if strings.TrimSpace(class.TaskText) == "" {
		class.TaskText = strings.TrimSpace(text)
	}
	return class, true
}

func looksLikePendingActionContinuation(text, lower string, tokens map[string]struct{}) bool {
	if strings.Contains(text, "?") || firstConcretePathCandidate(text) != "" || slashCommandMentioned(lower) {
		return false
	}
	ordered := tokenList(lower)
	if len(ordered) == 0 || len(ordered) > 4 {
		return false
	}
	if containsAny(tokens, continuationNegativeTokens) {
		return false
	}
	scope := inferRequestScope(lower, tokens)
	if hasExplicitPendingContinuationOverride(text, lower, ordered, tokens, scope) {
		return false
	}
	if onlyContinuationScaffolding(ordered) {
		return true
	}
	return looksLikeOpaqueShortContinuation(ordered)
}

func hasExplicitPendingContinuationOverride(text, lower string, ordered []string, tokens map[string]struct{}, scope requestScope) bool {
	if mentionsConcreteInspectScope(scope, tokens, lower) ||
		containsAny(tokens, inspectVerbs) ||
		containsAny(tokens, debugTokens) ||
		containsAny(tokens, transformTokens) ||
		containsImplementationSignal(ordered, tokens) ||
		wantsVerification(scope, tokens, lower) ||
		wantsResearch(tokens, lower) ||
		isPromptBoundaryQuestion(text, lower, tokens) ||
		isProcessQuestion(text, lower, tokens, scope, "") {
		return true
	}
	if len(ordered) == 0 {
		return false
	}
	return startsWithAny(lower,
		"what", "why", "how", "which", "when", "where", "who",
		"can ", "could ", "should ", "would ", "will ",
	)
}

func isPendingReferentialToken(token string) bool {
	_, ok := continuationReferentialTokens[token]
	return ok
}

func onlyContinuationScaffolding(ordered []string) bool {
	if len(ordered) == 0 {
		return false
	}
	for _, token := range ordered {
		if _, ok := continuationConfirmTokens[token]; ok {
			continue
		}
		if isPendingReferentialToken(token) || isWeakFollowUpToken(token) {
			continue
		}
		return false
	}
	return true
}

func looksLikeOpaqueShortContinuation(ordered []string) bool {
	if len(ordered) == 0 || len(ordered) > 2 {
		return false
	}
	meaningful := 0
	for _, token := range ordered {
		if _, ok := continuationConfirmTokens[token]; ok {
			continue
		}
		if isPendingReferentialToken(token) || isWeakFollowUpToken(token) {
			continue
		}
		meaningful++
	}
	return meaningful > 0
}

func stripDetachedPromptBoundary(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}
	clauses := splitDetachedIntentClauses(text)
	if len(clauses) < 2 {
		return text, false
	}
	taskClauses := make([]string, 0, len(clauses))
	detached := false
	for _, clause := range clauses {
		clause = strings.TrimSpace(strings.Trim(clause, ",;"))
		if clause == "" {
			continue
		}
		lower := strings.ToLower(clause)
		tokens := tokenize(lower)
		if isDetachedPromptBoundaryClause(clause, lower, tokens) {
			detached = true
			continue
		}
		taskClauses = append(taskClauses, clause)
	}
	if !detached || len(taskClauses) == 0 {
		return text, false
	}
	return strings.TrimSpace(strings.Join(taskClauses, ", ")), true
}

func splitDetachedIntentClauses(text string) []string {
	replacer := strings.NewReplacer(
		"\n", ", ",
		";", ", ",
		" afterwards ", ", afterwards ",
		" afterward ", ", afterward ",
		" then ", ", then ",
		" also ", ", also ",
	)
	normalized := replacer.Replace(" " + text + " ")
	raw := strings.Split(normalized, ",")
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func isDetachedPromptBoundaryClause(text, lower string, tokens map[string]struct{}) bool {
	if !hasPromptBoundaryConcept(tokens) {
		return false
	}
	if containsAny(tokens, promptBoundaryReferenceTokens) &&
		(containsAny(tokens, promptDisclosureTokens) ||
			containsAny(tokens, promptQualifierTokens) ||
			strings.Contains(lower, "what your") ||
			strings.Contains(lower, "what you say") ||
			strings.Contains(lower, "what it says")) {
		return true
	}
	return isPromptBoundaryQuestion(text, lower, tokens)
}

func hasPromptBoundaryConcept(tokens map[string]struct{}) bool {
	if containsAny(tokens, promptBoundaryTokens) {
		return true
	}
	for token := range tokens {
		if withinEditDistanceOne(token, "prompt") ||
			withinEditDistanceOne(token, "prompts") ||
			withinEditDistanceOne(token, "instruction") ||
			withinEditDistanceOne(token, "instructions") {
			return true
		}
	}
	return false
}

func withinEditDistanceOne(a, b string) bool {
	if a == b {
		return true
	}
	la, lb := len(a), len(b)
	if la == 0 || lb == 0 {
		return false
	}
	if la-lb > 1 || lb-la > 1 {
		return false
	}
	if la == lb {
		diff := 0
		for i := 0; i < la; i++ {
			if a[i] != b[i] {
				diff++
				if diff > 1 {
					return false
				}
			}
		}
		return diff <= 1
	}
	if la > lb {
		a, b = b, a
		la, lb = lb, la
	}
	i, j, diff := 0, 0, 0
	for i < la && j < lb {
		if a[i] == b[j] {
			i++
			j++
			continue
		}
		diff++
		if diff > 1 {
			return false
		}
		j++
	}
	return true
}

func mentionsConcreteInspectScope(scope requestScope, tokens map[string]struct{}, lower string) bool {
	if scope.Inspectable() {
		return true
	}
	if hasToken(tokens, "file") || hasToken(tokens, "files") {
		if inferFocusedFilesScope(lower).Inspectable() {
			return true
		}
		for _, hint := range languageScopeHints {
			if hasToken(tokens, hint.Language) {
				return true
			}
			for _, alias := range hint.Aliases {
				if hasToken(tokens, alias) {
					return true
				}
			}
		}
	}
	return hasToken(tokens, "repo") ||
		hasToken(tokens, "repository") ||
		hasToken(tokens, "directory") ||
		hasToken(tokens, "dir") ||
		hasToken(tokens, "folder") ||
		hasToken(tokens, "project") ||
		hasToken(tokens, "codebase")
}

func startsWithAny(input string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(input, prefix) {
			return true
		}
	}
	return false
}
