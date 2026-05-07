package react

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"forge/internal/agent/tools"
	"forge/internal/gitutil"
	"forge/internal/permissions"
	"forge/internal/secscan"
)

type ApprovalPolicy string

const (
	ApprovalNever         ApprovalPolicy = "never"
	ApprovalOnFailure     ApprovalPolicy = "on_failure"
	ApprovalOnRequest     ApprovalPolicy = "on_request"
	ApprovalUnlessTrusted ApprovalPolicy = "unless_trusted"
)

type RuleDecision string

const (
	DecisionAllow     RuleDecision = "allow"
	DecisionPrompt    RuleDecision = "prompt"
	DecisionForbidden RuleDecision = "forbidden"
)

type ClassifierFailureBehavior string

const (
	ClassifierFailureAsk  ClassifierFailureBehavior = "ask"
	ClassifierFailureDeny ClassifierFailureBehavior = "deny"
)

type ApprovalRule struct {
	Tool          string
	CommandPrefix []string
	Command       string
	Decision      RuleDecision
	matcher       shellRule
	hasMatcher    bool
}

type ApprovalConfig struct {
	DefaultPolicy             ApprovalPolicy
	SandboxPolicy             SandboxPolicy
	Rules                     []ApprovalRule
	ScopedRules               []permissions.Rule
	KnownSafeCommand          []string
	SecretPolicy              tools.SecretPolicy
	Classifier                permissions.Classifier
	ClassifierObserver        func(ClassifierEvent)
	Denials                   *permissions.DenialTracker
	ClassifierFailureBehavior ClassifierFailureBehavior
}

type ClassifierEvent struct {
	Action   permissions.Action
	Risk     permissions.RiskFacts
	Decision permissions.ClassifierDecision
	Reason   string
	Fallback string
	Error    string
}

type GuardianEvent struct {
	Decision tools.GuardianDecision
	Reason   string
	Action   tools.Action
}

type ApprovalGate struct {
	workDir         string
	cfg             ApprovalConfig
	prompt          tools.ApprovalFunc
	guardian        func(string, tools.Action) tools.GuardianReview
	guardianContext func() string
	guardianObserve func(GuardianEvent)
	updates         []ApprovalUpdate
	progress        func(string)
	now             func() time.Time
	originalBranch  string
	didSwitchBranch bool
}

func NewApprovalGate(workDir string, cfg ApprovalConfig, prompt tools.ApprovalFunc, progress func(string)) *ApprovalGate {
	if prompt == nil {
		prompt = func(action tools.Action) (bool, error) {
			return true, nil
		}
	}
	return &ApprovalGate{
		workDir:  strings.TrimSpace(workDir),
		cfg:      normalizeApprovalConfig(cfg),
		prompt:   prompt,
		progress: progress,
		now:      time.Now,
	}
}

// SetPrompt replaces the approval prompt function. Useful when the prompt
// function depends on values not yet available at construction time.
func (g *ApprovalGate) SetPrompt(prompt tools.ApprovalFunc) {
	if prompt != nil {
		g.prompt = prompt
	}
}

func (g *ApprovalGate) SetGuardianReviewer(reviewer func(string, tools.Action) tools.GuardianReview) {
	if g != nil {
		g.guardian = reviewer
	}
}

func (g *ApprovalGate) SetGuardianContext(provider func() string) {
	if g != nil {
		g.guardianContext = provider
	}
}

func (g *ApprovalGate) SetGuardianObserver(observer func(GuardianEvent)) {
	if g != nil {
		g.guardianObserve = observer
	}
}

func (g *ApprovalGate) ApprovalUpdates() []ApprovalUpdate {
	if g == nil || len(g.updates) == 0 {
		return nil
	}
	return append([]ApprovalUpdate(nil), g.updates...)
}

func (g *ApprovalGate) recordApprovalUpdate(update ApprovalUpdate) {
	if g != nil {
		g.updates = append(g.updates, update)
	}
}

func CompactGuardianContext(snapshot SessionSnapshot) string {
	var parts []string
	if mode := strings.TrimSpace(string(snapshot.Mode)); mode != "" {
		parts = append(parts, "mode="+mode)
	}
	if snapshot.TaskState != nil {
		if op := strings.TrimSpace(snapshot.TaskState.Operation); op != "" {
			parts = append(parts, "operation="+op)
		}
		if obj := strings.TrimSpace(snapshot.TaskState.Objective); obj != "" {
			parts = append(parts, "objective="+clipApprovalText(obj, 160))
		}
	}
	if snapshot.PlanState != nil {
		if step, ok := snapshot.PlanState.ActiveStep(); ok {
			parts = append(parts, "active_step="+clipApprovalText(step.Step, 120))
			if blocker := strings.TrimSpace(step.Blocker); blocker != "" {
				parts = append(parts, "blocker="+clipApprovalText(blocker, 120))
			}
		}
	}
	if input := strings.TrimSpace(snapshot.LastInput); input != "" {
		parts = append(parts, "last_input="+clipApprovalText(input, 160))
	}
	return strings.Join(parts, "\n")
}

func clipApprovalText(text string, max int) string {
	text = strings.TrimSpace(text)
	if max < 1 || len(text) <= max {
		return text
	}
	return text[:max] + "..."
}

func (g *ApprovalGate) Approve(action tools.Action) (bool, error) {
	action.Tool = strings.TrimSpace(action.Tool)
	action.Summary = strings.TrimSpace(action.Summary)
	action.Detail = strings.TrimSpace(action.Detail)
	action.Detail = g.cfg.SecretPolicy.RedactApprovalDetail(action.Detail)
	if action.Tool == "" {
		return false, fmt.Errorf("approval action tool is required")
	}
	evaluationAction := action

	mutating := actionMutates(action)
	if mutating {
		if err := g.ensureSafeBranch(action); err != nil {
			return false, err
		}
	}

	sandboxAllowed := g.cfg.SandboxPolicy.Allows(action)
	if !sandboxAllowed {
		if g.cfg.DefaultPolicy != ApprovalOnFailure {
			g.recordApprovalUpdate(NewApprovalUpdate(ApprovalDecisionForbidden, ApprovalDecisionSourceSandbox, approvalUpdateDetail(action)))
			return false, nil
		}
		detail := approvalUpdateDetail(action)
		overrideAction := action
		overrideAction.Summary = firstNonEmpty(overrideAction.Summary, overrideAction.Tool)
		overrideAction.Summary = "sandbox denied: " + overrideAction.Summary
		return g.promptWithRecordedOutcome(ApprovalDecisionSourceSandbox, detail, overrideAction)
	}

	guardianWarn := false
	if g.guardian != nil {
		transcript := ""
		if g.guardianContext != nil {
			transcript = strings.TrimSpace(g.guardianContext())
		}
		review := g.guardian(transcript, action)
		g.emitGuardianEvent(GuardianEvent{
			Decision: review.Decision,
			Reason:   strings.TrimSpace(review.Reason),
			Action:   action,
		})
		switch review.Decision {
		case tools.GuardianBlock:
			g.recordApprovalUpdate(NewApprovalUpdate(ApprovalDecisionForbidden, ApprovalDecisionSourceGuardian, strings.TrimSpace(review.Reason)))
			if g.progress != nil && strings.TrimSpace(review.Reason) != "" {
				g.progress("guardian blocked approval: " + strings.TrimSpace(review.Reason))
			}
			return false, nil
		case tools.GuardianWarn:
			guardianWarn = true
			if reason := strings.TrimSpace(review.Reason); reason != "" {
				action.Summary = "[guardian] " + reason + " :: " + action.Summary
				if g.progress != nil {
					g.progress("guardian warned: " + reason)
				}
			}
		}
	}

	if decision, matched := g.ruleDecision(evaluationAction); matched {
		switch decision {
		case DecisionAllow:
			if guardianWarn {
				return g.promptWithRecordedOutcome(ApprovalDecisionSourceGuardian, approvalUpdateDetail(evaluationAction), action)
			}
			g.recordApprovalUpdate(NewApprovalUpdate(ApprovalDecisionAllow, ApprovalDecisionSourceRule, approvalUpdateDetail(evaluationAction)))
			return true, nil
		case DecisionForbidden:
			g.recordApprovalUpdate(NewApprovalUpdate(ApprovalDecisionForbidden, ApprovalDecisionSourceRule, approvalUpdateDetail(evaluationAction)))
			return false, nil
		case DecisionPrompt:
			return g.promptWithRecordedOutcome(ApprovalDecisionSourceRule, approvalUpdateDetail(evaluationAction), action)
		default:
			return false, fmt.Errorf("unknown approval rule decision %q", decision)
		}
	}

	if decision, matched := g.scopedRuleDecision(evaluationAction); matched {
		switch decision {
		case DecisionAllow:
			if guardianWarn {
				return g.promptWithRecordedOutcome(ApprovalDecisionSourceGuardian, approvalUpdateDetail(evaluationAction), action)
			}
			g.recordApprovalUpdate(NewApprovalUpdate(ApprovalDecisionAllow, ApprovalDecisionSourceRule, approvalUpdateDetail(evaluationAction)))
			return true, nil
		case DecisionForbidden:
			g.recordApprovalUpdate(NewApprovalUpdate(ApprovalDecisionForbidden, ApprovalDecisionSourceRule, approvalUpdateDetail(evaluationAction)))
			return false, nil
		case DecisionPrompt:
			return g.promptWithRecordedOutcome(ApprovalDecisionSourceRule, approvalUpdateDetail(evaluationAction), action)
		default:
			return false, fmt.Errorf("unknown scoped approval rule decision %q", decision)
		}
	}

	if g.cfg.DefaultPolicy == ApprovalUnlessTrusted && !guardianWarn && g.cfg.Classifier != nil {
		if g.cfg.Denials != nil && g.cfg.Denials.ShouldFallback() {
			return g.promptWithRecordedOutcome(ApprovalDecisionSourceClassifier, approvalUpdateDetail(evaluationAction), action)
		}
		if approved, handled, err := g.classifierDecision(evaluationAction, action); handled || err != nil {
			return approved, err
		}
	}

	switch g.cfg.DefaultPolicy {
	case ApprovalNever:
		if guardianWarn {
			return g.promptWithRecordedOutcome(ApprovalDecisionSourceGuardian, approvalUpdateDetail(evaluationAction), action)
		}
		g.recordApprovalUpdate(NewApprovalUpdate(ApprovalDecisionAllow, ApprovalDecisionSourcePolicy, approvalUpdateDetail(evaluationAction)))
		return true, nil
	case ApprovalOnFailure:
		if guardianWarn {
			return g.promptWithRecordedOutcome(ApprovalDecisionSourceGuardian, approvalUpdateDetail(evaluationAction), action)
		}
		g.recordApprovalUpdate(NewApprovalUpdate(ApprovalDecisionAllow, ApprovalDecisionSourcePolicy, approvalUpdateDetail(evaluationAction)))
		return true, nil
	case ApprovalOnRequest:
		if guardianWarn || actionNeedsPrompt(action) {
			source := ApprovalDecisionSourcePolicy
			if guardianWarn {
				source = ApprovalDecisionSourceGuardian
			}
			return g.promptWithRecordedOutcome(source, approvalUpdateDetail(evaluationAction), action)
		}
		g.recordApprovalUpdate(NewApprovalUpdate(ApprovalDecisionAllow, ApprovalDecisionSourcePolicy, approvalUpdateDetail(evaluationAction)))
		return true, nil
	case ApprovalUnlessTrusted:
		if !guardianWarn && actionTrusted(evaluationAction, g.cfg.KnownSafeCommand) {
			g.recordApprovalUpdate(NewApprovalUpdate(ApprovalDecisionAllow, ApprovalDecisionSourceTrusted, approvalUpdateDetail(evaluationAction)))
			return true, nil
		}
		source := ApprovalDecisionSourcePolicy
		if guardianWarn {
			source = ApprovalDecisionSourceGuardian
		}
		return g.promptWithRecordedOutcome(source, approvalUpdateDetail(evaluationAction), action)
	default:
		return false, fmt.Errorf("unknown approval policy %q", g.cfg.DefaultPolicy)
	}
}

func (g *ApprovalGate) classifierDecision(evaluationAction, promptAction tools.Action) (bool, bool, error) {
	riskAction := permissions.Action{Tool: evaluationAction.Tool, Summary: evaluationAction.Summary, Detail: evaluationAction.Detail}
	risk := permissions.AnalyzeAction(riskAction)
	if risk.ClassifierImmune {
		g.emitClassifierEvent(ClassifierEvent{Action: riskAction, Risk: risk, Decision: permissions.ClassifierAsk, Reason: "classifier-immune action", Fallback: string(ClassifierFailureAsk)})
		approved, err := g.promptWithRecordedOutcome(ApprovalDecisionSourcePolicy, approvalUpdateDetail(evaluationAction), promptAction)
		return approved, true, err
	}
	resp, err := g.cfg.Classifier.Classify(context.Background(), permissions.ClassifierRequest{
		Action: riskAction,
		Risk:   risk,
		Rules:  g.cfg.ScopedRules,
	})
	if err != nil {
		if g.cfg.ClassifierFailureBehavior == ClassifierFailureDeny {
			g.emitClassifierEvent(ClassifierEvent{Action: riskAction, Risk: risk, Decision: permissions.ClassifierDeny, Fallback: string(ClassifierFailureDeny), Error: err.Error()})
			g.recordApprovalUpdate(NewApprovalUpdate(ApprovalDecisionForbidden, ApprovalDecisionSourceClassifier, approvalUpdateDetail(evaluationAction)))
			return false, true, nil
		}
		g.emitClassifierEvent(ClassifierEvent{Action: riskAction, Risk: risk, Decision: permissions.ClassifierAsk, Fallback: string(ClassifierFailureAsk), Error: err.Error()})
		approved, promptErr := g.promptWithRecordedOutcome(ApprovalDecisionSourceClassifier, approvalUpdateDetail(evaluationAction), promptAction)
		return approved, true, promptErr
	}
	reason := strings.TrimSpace(resp.Reason)
	if reason == "" {
		reason = approvalUpdateDetail(evaluationAction)
	}
	switch resp.Decision {
	case permissions.ClassifierAllow:
		g.emitClassifierEvent(ClassifierEvent{Action: riskAction, Risk: risk, Decision: resp.Decision, Reason: reason})
		if g.cfg.Denials != nil {
			g.cfg.Denials.RecordAllowed()
		}
		g.recordApprovalUpdate(NewApprovalUpdate(ApprovalDecisionAllow, ApprovalDecisionSourceClassifier, reason))
		return true, true, nil
	case permissions.ClassifierDeny:
		g.emitClassifierEvent(ClassifierEvent{Action: riskAction, Risk: risk, Decision: resp.Decision, Reason: reason})
		if g.cfg.Denials != nil {
			g.cfg.Denials.RecordDenied()
		}
		g.recordApprovalUpdate(NewApprovalUpdate(ApprovalDecisionForbidden, ApprovalDecisionSourceClassifier, reason))
		return false, true, nil
	case permissions.ClassifierAsk, "":
		g.emitClassifierEvent(ClassifierEvent{Action: riskAction, Risk: risk, Decision: permissions.ClassifierAsk, Reason: reason, Fallback: string(ClassifierFailureAsk)})
		approved, err := g.promptWithRecordedOutcome(ApprovalDecisionSourceClassifier, reason, promptAction)
		return approved, true, err
	default:
		g.emitClassifierEvent(ClassifierEvent{Action: riskAction, Risk: risk, Decision: permissions.ClassifierAsk, Reason: reason, Fallback: string(ClassifierFailureAsk)})
		approved, err := g.promptWithRecordedOutcome(ApprovalDecisionSourceClassifier, approvalUpdateDetail(evaluationAction), promptAction)
		return approved, true, err
	}
}

func (g *ApprovalGate) scopedRuleDecision(action tools.Action) (RuleDecision, bool) {
	decision := permissions.Evaluate(g.cfg.ScopedRules, permissions.Action{
		Tool:    action.Tool,
		Summary: action.Summary,
		Detail:  action.Detail,
	})
	if !decision.Matched {
		return "", false
	}
	switch decision.Behavior {
	case permissions.BehaviorAllow:
		return DecisionAllow, true
	case permissions.BehaviorAsk:
		return DecisionPrompt, true
	case permissions.BehaviorDeny:
		return DecisionForbidden, true
	default:
		return "", false
	}
}

func (g *ApprovalGate) promptWithRecordedOutcome(source ApprovalDecisionSource, detail string, action tools.Action) (bool, error) {
	g.recordApprovalUpdate(NewApprovalUpdate(ApprovalDecisionPrompt, source, detail))
	approved, err := g.prompt(action)
	if err != nil {
		return false, err
	}
	finalDecision := ApprovalDecisionForbidden
	if approved {
		finalDecision = ApprovalDecisionAllow
	}
	g.recordApprovalUpdate(NewApprovalUpdate(finalDecision, ApprovalDecisionSourceUser, detail))
	return approved, nil
}

func (g *ApprovalGate) emitGuardianEvent(event GuardianEvent) {
	if g == nil || g.guardianObserve == nil {
		return
	}
	g.guardianObserve(event)
}

func (g *ApprovalGate) emitClassifierEvent(event ClassifierEvent) {
	if g == nil || g.cfg.ClassifierObserver == nil {
		return
	}
	event.Action.Summary = redactClassifierEventText(event.Action.Summary)
	event.Action.Detail = redactClassifierEventText(event.Action.Detail)
	g.cfg.ClassifierObserver(event)
}

func redactClassifierEventText(text string) string {
	scanner := secscan.NewDefaultScanner()
	return secscan.Redact(text, scanner.Scan(text))
}

func normalizeApprovalConfig(cfg ApprovalConfig) ApprovalConfig {
	if cfg.DefaultPolicy == "" {
		cfg.DefaultPolicy = ApprovalOnRequest
	}
	if cfg.SandboxPolicy == "" {
		cfg.SandboxPolicy = SandboxWorkspaceWrite
	}
	if len(cfg.KnownSafeCommand) == 0 {
		cfg.KnownSafeCommand = append([]string(nil), defaultKnownSafeCommandPrefixes...)
	}
	cfg.SecretPolicy = cfg.SecretPolicy.WithDefaults()
	if cfg.ClassifierFailureBehavior == "" {
		cfg.ClassifierFailureBehavior = ClassifierFailureAsk
	}
	return cfg
}

func (g *ApprovalGate) ruleDecision(action tools.Action) (RuleDecision, bool) {
	for _, rule := range g.cfg.Rules {
		if strings.TrimSpace(rule.Tool) != "" && !strings.EqualFold(strings.TrimSpace(rule.Tool), action.Tool) {
			continue
		}
		matcher, ok := rule.shellMatcher()
		if ok && !matcher.matches(action.Summary) {
			continue
		}
		if !ok && rule.hasExplicitMatcher() {
			continue
		}
		return rule.Decision, true
	}
	return "", false
}

func (r ApprovalRule) shellMatcher() (shellRule, bool) {
	if r.hasMatcher {
		return r.matcher, true
	}
	if strings.TrimSpace(r.Command) != "" {
		matcher, err := parseShellRule(r.Command)
		if err != nil {
			return shellRule{}, false
		}
		return matcher, true
	}
	if len(r.CommandPrefix) > 0 {
		matcher, err := parseShellRulePrefix(r.CommandPrefix)
		if err != nil {
			return shellRule{}, false
		}
		return matcher, true
	}
	return shellRule{}, false
}

func (r ApprovalRule) hasExplicitMatcher() bool {
	return r.hasMatcher || strings.TrimSpace(r.Command) != "" || len(r.CommandPrefix) > 0
}

func actionNeedsPrompt(action tools.Action) bool {
	switch action.Tool {
	case "write_file", "edit_file", "artifact_write", "git_commit":
		return true
	case "run_command":
		return true
	default:
		return false
	}
}

func actionTrusted(action tools.Action, knownSafe []string) bool {
	if action.Tool != "run_command" {
		return false
	}
	return matchesAnyShellRulePrefix(action.Summary, knownSafe)
}

func actionMutates(action tools.Action) bool {
	switch action.Tool {
	case "write_file", "edit_file", "artifact_write", "git_commit":
		return true
	case "run_command":
		return commandLikelyMutates(action.Summary)
	default:
		return false
	}
}

func commandLikelyMutates(command string) bool {
	lower := strings.ToLower(strings.TrimSpace(command))
	if lower == "" {
		return false
	}
	if gitBranchReadOnlyCommand(lower) {
		return false
	}
	markers := []string{
		"git add", "git commit", "git checkout", "git switch", "git branch", "git merge", "git rebase", "git push", "git pull",
		"rm ", "rm\t", "mv ", "mv\t", "cp ", "cp\t", "sed -i", "perl -i", "tee ", "cat >", ">>",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func gitBranchReadOnlyCommand(command string) bool {
	if !strings.HasPrefix(command, "git branch") {
		return false
	}
	readOnlyFlags := []string{" -a", " -r", " -vv", " --merged", " --no-merged", " --show-current", " --contains", " --sort"}
	for _, flag := range readOnlyFlags {
		if strings.Contains(command, flag) {
			return true
		}
	}
	return strings.TrimSpace(command) == "git branch"
}

func (g *ApprovalGate) ensureSafeBranch(action tools.Action) error {
	if g == nil || strings.TrimSpace(g.workDir) == "" {
		return nil
	}
	isRepo, err := gitutil.IsRepository(g.workDir)
	if err != nil || !isRepo {
		return err
	}
	current, err := gitutil.CurrentBranch(g.workDir)
	if err != nil {
		return err
	}
	if !isProtectedBranch(current) {
		return nil
	}
	if !g.didSwitchBranch {
		g.originalBranch = current
	}
	target := safeBranchName(action.Summary, g.now())
	exists, err := gitutil.BranchExists(g.workDir, target)
	if err != nil {
		return err
	}
	if exists {
		if err := checkoutBranch(g.workDir, target); err != nil {
			return err
		}
	} else if err := gitutil.CheckoutNewBranch(g.workDir, target); err != nil {
		return err
	}
	g.didSwitchBranch = true
	if g.progress != nil {
		g.progress("Switched to branch " + target)
	}
	return nil
}

// Restore switches back to the original branch if the gate created a
// safety branch during the session, and deletes the safety branch when
// its commits have been merged into the original branch. This should be
// called when the session ends.
func (g *ApprovalGate) Restore() {
	if g == nil || !g.didSwitchBranch || g.originalBranch == "" {
		return
	}
	// Record the safety branch name before switching away from it.
	safetyBranch, err := gitutil.CurrentBranch(g.workDir)
	if err != nil {
		safetyBranch = ""
	}
	if err := checkoutBranch(g.workDir, g.originalBranch); err != nil {
		if g.progress != nil {
			g.progress(fmt.Sprintf("warning: could not restore branch %s: %v", g.originalBranch, err))
		}
		return
	}
	if g.progress != nil {
		g.progress("Restored branch " + g.originalBranch)
	}
	// Delete the safety branch if it is now fully merged into the original.
	if safetyBranch != "" && safetyBranch != g.originalBranch {
		merged, err := gitutil.IsBranchMerged(g.workDir, safetyBranch, g.originalBranch)
		if err == nil && merged {
			if delErr := gitutil.DeleteBranch(g.workDir, safetyBranch); delErr == nil {
				if g.progress != nil {
					g.progress("Deleted merged branch " + safetyBranch)
				}
			}
		}
	}
}

func safeBranchName(seed string, now time.Time) string {
	slug := branchSlug(seed)
	if slug == "" {
		slug = "task"
	}
	stamp := now.UTC().Format("20060102150405")
	return "forge/" + slug + "-" + stamp
}

func branchSlug(input string) string {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range input {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlphaNum {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
		if b.Len() >= 32 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

func isProtectedBranch(branch string) bool {
	branch = strings.TrimSpace(branch)
	return branch == "main" || branch == "master"
}

func checkoutBranch(dir, branch string) error {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return fmt.Errorf("branch name is required")
	}
	cmd := exec.Command("git", "checkout", branch)
	cmd.Dir = dir
	return cmd.Run()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
