package react

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (r *Runner) markTurnContractSatisfiedIfComplete(turn int) {
	if r == nil || r.session == nil {
		return
	}
	r.session.UpdateTurnContract(func(contract *TurnContract) {
		if contract == nil || contract.Status != ContractStatusActive || contract.Intent == TurnIntentAnswerOnly {
			return
		}
		if turnContractFinalEvidenceFeedback(contract) != "" {
			return
		}
		contract.Status = ContractStatusSatisfied
	})
}

func (r *Runner) finalSideEffectGateMayBlock() bool {
	if r == nil || r.session == nil {
		return false
	}
	snap := r.session.Snapshot()
	return len(unresolvedSideEffectGates(snap.SideEffectIntent)) > 0 || turnContractHasPendingArtifactGate(snap.TurnContract) || planStateHasUnresolvedStep(snap.PlanState)
}

func (r *Runner) validateFinalCompletion(ctx context.Context, turn int, finalText string, requireToolCall bool) (bool, error) {
	// Successful finalization must pass this central point before assistant text
	// is appended or the turn is completed as a success.
	if err := r.ensureFinalValidationTurnCurrent(ctx, turn); err != nil {
		return false, err
	}
	if err := r.rejectRawToolMarkupFinalText(ctx, turn, finalText); err != nil {
		return false, err
	}
	if requireToolCall {
		return false, NewRetryableCompletionError(
			"react runtime: required tool call missing",
			"A tool call is required for this step. Use one of the available tools instead of answering with prose.",
		)
	}
	if blocked, err := r.blockFinalCompletionGates(turn, finalText); blocked || err != nil {
		if err != nil {
			return false, err
		}
		return false, nil
	}
	return true, nil
}

func (r *Runner) ensureFinalValidationTurnCurrent(ctx context.Context, turn int) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if r == nil || r.session == nil {
		return nil
	}
	active, ok := r.session.ActiveTurnSnapshot()
	if !ok {
		return staleTurnError(fmt.Sprintf("turn-%d", turn))
	}
	turnID := fmt.Sprintf("turn-%d", turn)
	if active.ID != turnID || !r.session.IsActiveTurn(turnID) {
		return staleTurnError(turnID)
	}
	return nil
}

func (r *Runner) blockFinalCompletionGates(turn int, finalText string) (bool, error) {
	if r == nil || r.session == nil {
		return false, nil
	}
	// Gates are advisory nudges, not walls: after a bounded number of
	// rejections the final answer goes through. The gates are derived from
	// keyword heuristics and an uncapped rejection loop burns the whole step
	// budget when they misclassify the user's intent.
	if r.completionGateRejections >= maxCompletionGateRejectionsPerTurn {
		return false, nil
	}
	blocked, err := r.blockFinalCompletionGatesOnce(turn, finalText)
	if blocked {
		r.completionGateRejections++
	}
	return blocked, err
}

func (r *Runner) blockFinalCompletionGatesOnce(turn int, finalText string) (bool, error) {
	snap := r.session.Snapshot()
	step, ok := unresolvedPlanStep(snap.PlanState)
	if ok || contractHasPlanStateGate(snap.TurnContract) {
		turnID := fmt.Sprintf("turn-%d", turn)
		if turn > 0 && !r.session.IsActiveTurn(turnID) {
			return false, staleTurnError(turnID)
		}
	}
	sideFeedback, hasArtifactFeedback, sideFailure := r.finalSideEffectGateFeedback(finalText)
	if !ok {
		r.passResolvedPlanStateGate()
		if blocked, err := r.blockWithSideEffectFeedback(sideFeedback, hasArtifactFeedback, sideFailure); blocked || err != nil {
			return blocked, err
		}
		return r.blockWithTurnContractFeedback(turn, finalText)
	}
	feedback := planStateInconsistencyFeedback(step)
	r.recordPlanStateContractViolation(feedback)
	feedbackParts := append([]string(nil), sideFeedback...)
	feedbackParts = append(feedbackParts, feedback)
	combinedFeedback := strings.Join(feedbackParts, "\n")
	if combinedFeedback != "" {
		if err := r.session.AppendUserMessage(combinedFeedback); err != nil {
			return false, err
		}
	}
	if hasArtifactFeedback {
		return true, NewRetryableCompletionError("react runtime: artifact gate unresolved", combinedFeedback)
	}
	return true, NewRetryableCompletionError("react runtime: plan state inconsistent", combinedFeedback)
}

func (r *Runner) blockWithTurnContractFeedback(turn int, finalText string) (bool, error) {
	if r == nil || r.session == nil {
		return false, nil
	}
	snap := r.session.Snapshot()
	contract := snap.TurnContract
	if !turnContractRequiresFinalEvidence(contract, turn) {
		return false, nil
	}
	feedback := turnContractFinalEvidenceFeedback(contract)
	if feedback == "" {
		return false, nil
	}
	if err := r.session.AppendUserMessage(feedback); err != nil {
		return false, err
	}
	return true, nil
}

func turnContractRequiresFinalEvidence(contract *TurnContract, turn int) bool {
	if contract == nil || contract.Status == ContractStatusSatisfied || contract.Status == ContractStatusCleared || contract.Intent == TurnIntentAnswerOnly {
		return false
	}
	if contract.SourceTurn != 0 && turn != 0 && contract.SourceTurn != turn {
		return false
	}
	return len(contract.RequiredActions) > 0 || len(contract.RequiredArtifacts) > 0 || len(contract.RequiredVerification) > 0 || turnContractHasEvidenceKind(contract, EvidenceDelegationFailure)
}

func turnContractFinalEvidenceFeedback(contract *TurnContract) string {
	if contract == nil {
		return ""
	}
	var missing []string
	for _, action := range contract.RequiredActions {
		if !turnContractActionSatisfied(contract, action.Kind) {
			missing = append(missing, string(action.Kind))
		}
	}
	if len(contract.RequiredVerification) > 0 && !turnContractRequiredVerificationPassed(contract) {
		missing = append(missing, "verification")
	}
	for _, artifact := range contract.RequiredArtifacts {
		path := strings.TrimSpace(artifact.Path)
		if path == "" {
			path = "<empty>"
		}
		if !turnContractRequiredArtifactSatisfied(contract, artifact) {
			missing = append(missing, "artifact "+path)
		}
	}
	if len(missing) > 0 {
		feedback := "Runtime feedback: required turn contract evidence missing: " + strings.Join(uniqueStrings(missing), ", ") + ". Use the required tools and successful verification evidence before claiming completion."
		if turnContractHasEvidenceKind(contract, EvidenceDelegationFailure) {
			feedback += " delegation failed; provide parent-owned recovery evidence satisfying the required actions/artifacts or report the failure/blocker."
		}
		return feedback
	}
	if turnContractHasEvidenceKind(contract, EvidenceDelegationFailure) && len(contract.RequiredActions) == 0 && len(contract.RequiredArtifacts) == 0 && len(contract.RequiredVerification) == 0 {
		return "Runtime feedback: delegation failed. Provide parent-owned recovery evidence satisfying the required actions/artifacts or report the failure/blocker instead of claiming successful completion."
	}
	return ""
}

func turnContractActionSatisfied(contract *TurnContract, kind ContractActionKind) bool {
	switch kind {
	case ContractActionEdit:
		return turnContractHasWriteEvidence(contract) || contractGatePassed(contract, string(SideEffectActionWrite)) || contractGatePassed(contract, "artifact")
	case ContractActionRead:
		return turnContractHasEvidenceKind(contract, EvidenceRead) || (turnContractHasEvidenceKind(contract, EvidenceDelegation) && !turnContractHasEvidenceKind(contract, EvidenceDelegationFailure))
	case ContractActionRun:
		return turnContractAnyVerificationPassed(contract) || contractHasPassedToolEvidence(contract, "run_command")
	case ContractActionCommit:
		return contractGatePassed(contract, string(SideEffectActionCommit)) || contractHasPassedToolEvidence(contract, "git_commit")
	case ContractActionPush:
		return contractGatePassed(contract, string(SideEffectActionPush)) || contractHasPassedToolEvidence(contract, "git_push")
	case ContractActionReport:
		return true
	default:
		return false
	}
}

func turnContractHasWriteEvidence(contract *TurnContract) bool {
	return turnContractHasEvidenceKind(contract, EvidenceWrite)
}

func turnContractRequiredVerificationPassed(contract *TurnContract) bool {
	if contract == nil {
		return false
	}
	for _, required := range contract.RequiredVerification {
		command := strings.TrimSpace(required.Command)
		if command == "" {
			if !turnContractAnyVerificationPassed(contract) {
				return false
			}
			continue
		}
		if !turnContractVerificationCommandPassed(contract, command) {
			return false
		}
	}
	return true
}

func turnContractAnyVerificationPassed(contract *TurnContract) bool {
	if contract == nil {
		return false
	}
	for _, evidence := range contract.Evidence {
		if evidence.Kind == EvidenceVerification && strings.Contains(strings.ToLower(evidence.Summary), "passed") {
			return true
		}
	}
	return false
}

func turnContractVerificationCommandPassed(contract *TurnContract, command string) bool {
	if contract == nil {
		return false
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return turnContractAnyVerificationPassed(contract)
	}
	for _, evidence := range contract.Evidence {
		if evidence.Kind != EvidenceVerification || !strings.Contains(strings.ToLower(evidence.Summary), "passed") {
			continue
		}
		if strings.TrimSpace(strings.TrimPrefix(evidence.Summary, "verification passed:")) == command {
			return true
		}
	}
	return false
}

func turnContractRequiredArtifactSatisfied(contract *TurnContract, artifact ArtifactRequirement) bool {
	path := normalizeIntentPath(artifact.Path)
	if path == "" {
		return false
	}
	if status, ok := latestArtifactWriteEvidenceStatus(contract, path); ok {
		return status == ContractGatePassed
	}
	for _, gate := range contract.Gates {
		if gate.Name == "artifact" && gate.Status == ContractGatePassed && evidenceSummaryHasExactPath(gate.Evidence, path) {
			return true
		}
	}
	return false
}

func contractGatePassed(contract *TurnContract, name string) bool {
	if contract == nil || strings.TrimSpace(name) == "" {
		return false
	}
	for _, gate := range contract.Gates {
		if gate.Name == name && gate.Status == ContractGatePassed {
			return true
		}
	}
	return false
}

func contractHasPassedToolEvidence(contract *TurnContract, toolName string) bool {
	if contract == nil || strings.TrimSpace(toolName) == "" {
		return false
	}
	for _, evidence := range contract.Evidence {
		lower := strings.ToLower(evidence.Summary)
		if evidence.Kind == EvidenceTool && strings.Contains(lower, strings.ToLower(toolName)) && strings.Contains(lower, "passed") {
			return true
		}
	}
	return false
}

func turnContractHasEvidenceKind(contract *TurnContract, kind EvidenceKind) bool {
	if contract == nil {
		return false
	}
	for _, evidence := range contract.Evidence {
		if evidence.Kind == kind {
			return true
		}
	}
	return false
}

func (r *Runner) blockWithSideEffectFeedback(feedbackParts []string, hasArtifactFeedback bool, failure error) (bool, error) {
	if failure != nil {
		return true, failure
	}
	feedback := strings.Join(feedbackParts, "\n")
	if feedback == "" {
		return false, nil
	}
	if err := r.session.AppendUserMessage(feedback); err != nil {
		return false, err
	}
	if hasArtifactFeedback {
		return true, NewRetryableCompletionError("react runtime: artifact gate unresolved", feedback)
	}
	return true, nil
}

func planStateHasUnresolvedStep(plan *PlanState) bool {
	_, ok := unresolvedPlanStep(plan)
	return ok
}

func unresolvedPlanStep(plan *PlanState) (PlanStep, bool) {
	if plan == nil {
		return PlanStep{}, false
	}
	return plan.ActiveStep()
}

func planStateInconsistencyFeedback(step PlanStep) string {
	status := strings.ToLower(strings.TrimSpace(step.Status))
	if status == "" {
		status = "unresolved"
	}
	name := strings.TrimSpace(step.Step)
	if name == "" {
		name = "<unnamed step>"
	}
	feedback := "Runtime feedback: plan state inconsistent: step " + strconv.Quote(name) + " is " + status
	if blocker := strings.TrimSpace(step.Blocker); blocker != "" {
		feedback += " (blocker: " + blocker + ")"
	}
	return feedback + ". Update the plan state or report the blocker/failure instead of claiming successful completion."
}

func (r *Runner) recordPlanStateContractViolation(feedback string) {
	if r == nil || r.session == nil || strings.TrimSpace(feedback) == "" {
		return
	}
	r.session.UpdateTurnContract(func(contract *TurnContract) {
		updateContractGate(contract, "plan_state", ContractGateFailed, feedback)
		summary := "plan state inconsistent: " + feedback
		for _, evidence := range contract.Evidence {
			if evidence.Kind == EvidenceModelViolation && evidence.Summary == summary {
				return
			}
		}
		contract.Evidence = append(contract.Evidence, EvidenceRecord{Kind: EvidenceModelViolation, Summary: summary})
	})
}

func (r *Runner) passResolvedPlanStateGate() {
	if r == nil || r.session == nil {
		return
	}
	r.session.UpdateTurnContract(func(contract *TurnContract) {
		for _, gate := range contract.Gates {
			if gate.Name == "plan_state" && gate.Status != ContractGatePassed {
				updateContractGate(contract, "plan_state", ContractGatePassed, "plan state resolved")
				return
			}
		}
	})
}

func contractHasPlanStateGate(contract *TurnContract) bool {
	if contract == nil {
		return false
	}
	for _, gate := range contract.Gates {
		if gate.Name == "plan_state" {
			return true
		}
	}
	return false
}

func (r *Runner) finalSideEffectGateFeedback(finalText string) ([]string, bool, error) {
	if r == nil || r.session == nil {
		return nil, false, nil
	}
	var feedbackParts []string
	hasArtifactFeedback := false
	if artifactFeedback := r.turnContractArtifactGateFeedback(finalText); artifactFeedback != "" {
		feedbackParts = append(feedbackParts, artifactFeedback)
		hasArtifactFeedback = true
	}
	ignored := map[string]bool(nil)
	if hasArtifactFeedback {
		ignored = map[string]bool{string(SideEffectActionWrite): true}
	}
	if feedback := sideEffectGateFeedbackExcept(r.session.Snapshot().SideEffectIntent, ignored); feedback != "" {
		feedbackParts = append(feedbackParts, feedback)
	}
	return feedbackParts, hasArtifactFeedback, nil
}

func (r *Runner) turnContractArtifactGateFeedback(finalText string) string {
	if r == nil || r.session == nil {
		return ""
	}
	snap := r.session.Snapshot()
	contract := snap.TurnContract
	if contract == nil || contract.Intent != TurnIntentWriteArtifact || len(contract.RequiredArtifacts) == 0 {
		return ""
	}
	_ = finalText
	root := strings.TrimSpace(snap.ActiveWorkspaceRoot)
	if root == "" && snap.SideEffectIntent != nil {
		root = strings.TrimSpace(snap.SideEffectIntent.WorkspaceRoot)
	}
	if root == "" {
		root = "."
	}
	root = filepath.Clean(root)
	var failures []string
	updates := make(map[string]ContractGate)
	for _, artifact := range contract.RequiredArtifacts {
		path := strings.TrimSpace(artifact.Path)
		if path == "" {
			path = "<empty>"
		}
		status, evidence := validateContractArtifactRequirement(contract, snap.SideEffectIntent, root, artifact)
		updates[path] = ContractGate{Name: "artifact", Status: status, Evidence: evidence}
		if status != ContractGatePassed {
			failures = append(failures, path+" ("+evidence+")")
		}
	}
	if len(updates) > 0 {
		r.session.UpdateTurnContract(func(contract *TurnContract) {
			status := ContractGatePassed
			var evidence []string
			for _, artifact := range contract.RequiredArtifacts {
				path := strings.TrimSpace(artifact.Path)
				if path == "" {
					path = "<empty>"
				}
				update, ok := updates[path]
				if !ok {
					continue
				}
				if update.Status != ContractGatePassed {
					status = update.Status
				}
				evidence = append(evidence, path+": "+update.Evidence)
			}
			updateContractGate(contract, "artifact", status, strings.Join(evidence, "; "))
		})
	}
	if len(failures) == 0 {
		return ""
	}
	return "Runtime feedback: artifact gate unresolved: required artifact must exist at exact path " + strings.Join(failures, ", ") + ". Write the requested file before claiming completion."
}

func validateContractArtifactRequirement(contract *TurnContract, intent *SideEffectIntent, root string, artifact ArtifactRequirement) (ContractGateStatus, string) {
	rawPath := strings.TrimSpace(artifact.Path)
	path := normalizeIntentPath(rawPath)
	if path == "" || path != rawPath {
		return ContractGateFailed, "invalid required artifact path"
	}
	writeStatus, ok := latestArtifactWriteEvidenceStatus(contract, path)
	if !ok {
		return ContractGatePending, "missing same-turn successful write evidence"
	}
	if writeStatus != ContractGatePassed {
		return ContractGateFailed, "latest write evidence is a tool error"
	}
	if !contractArtifactPathAllowed(intent, path) {
		return ContractGateFailed, "path is outside allowed artifact scope"
	}
	fullPath, ok := contractArtifactFullPath(root, path)
	if !ok {
		return ContractGateFailed, "path is outside active workspace"
	}
	info, err := os.Lstat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ContractGatePending, "missing"
		}
		return ContractGateFailed, "stat failed: " + err.Error()
	}
	if info.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(fullPath)
		if err != nil {
			return ContractGateFailed, "symlink resolution failed: " + err.Error()
		}
		if !resolvedPathInsideRoot(root, resolved) {
			return ContractGateFailed, "symlink target is outside active workspace"
		}
		info, err = os.Stat(fullPath)
		if err != nil {
			return ContractGateFailed, "stat failed: " + err.Error()
		}
	}
	resolved, err := filepath.EvalSymlinks(fullPath)
	if err != nil {
		return ContractGateFailed, "symlink resolution failed: " + err.Error()
	}
	if !resolvedPathInsideRoot(root, resolved) {
		return ContractGateFailed, "resolved path is outside active workspace"
	}
	if info.IsDir() {
		return ContractGateFailed, "path is a directory"
	}
	if info.Size() == 0 {
		return ContractGateFailed, "file is empty"
	}
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return ContractGateFailed, "read failed: " + err.Error()
	}
	if !artifactContentPlausible(contract, artifact, string(content)) {
		return ContractGateFailed, "markdown plan/spec lacks heading and substantive section"
	}
	return ContractGatePassed, "exists and content is plausible"
}

func turnContractHasPendingArtifactGate(contract *TurnContract) bool {
	if contract == nil || contract.Intent != TurnIntentWriteArtifact {
		return false
	}
	for _, gate := range contract.Gates {
		if gate.Name == "artifact" && gate.Status != ContractGatePassed {
			return true
		}
	}
	return len(contract.RequiredArtifacts) > 0
}

func updateContractGate(contract *TurnContract, name string, status ContractGateStatus, evidence string) {
	if contract == nil || strings.TrimSpace(name) == "" {
		return
	}
	for i := range contract.Gates {
		if contract.Gates[i].Name == name {
			contract.Gates[i].Status = status
			contract.Gates[i].Evidence = evidence
			return
		}
	}
	contract.Gates = append(contract.Gates, ContractGate{Name: name, Status: status, Evidence: evidence})
}

func contractArtifactPathAllowed(intent *SideEffectIntent, path string) bool {
	path = normalizeIntentPath(path)
	if path == "" || filepath.IsAbs(path) || strings.HasPrefix(path, "../") || path == ".." {
		return false
	}
	if intent == nil {
		return true
	}
	return sideEffectPathMatchesIntent(path, intent)
}

func contractArtifactFullPath(root, path string) (string, bool) {
	path = normalizeIntentPath(path)
	if path == "" || filepath.IsAbs(path) {
		return "", false
	}
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" {
		root = "."
	}
	fullPath := filepath.Clean(filepath.Join(root, filepath.FromSlash(path)))
	if !pathInsideRoot(root, fullPath) {
		return "", false
	}
	return fullPath, true
}

func pathInsideRoot(root, path string) bool {
	root = filepath.Clean(strings.TrimSpace(root))
	path = filepath.Clean(strings.TrimSpace(path))
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".."
}

func resolvedPathInsideRoot(root, path string) bool {
	resolvedRoot, err := filepath.EvalSymlinks(filepath.Clean(strings.TrimSpace(root)))
	if err != nil {
		resolvedRoot = filepath.Clean(strings.TrimSpace(root))
	}
	return pathInsideRoot(resolvedRoot, filepath.Clean(strings.TrimSpace(path)))
}

func latestArtifactWriteEvidenceStatus(contract *TurnContract, path string) (ContractGateStatus, bool) {
	if contract == nil {
		return ContractGatePending, false
	}
	path = normalizeIntentPath(path)
	foundFailed := false
	for i := len(contract.Evidence) - 1; i >= 0; i-- {
		evidence := contract.Evidence[i]
		if path == "" || !evidenceSummaryHasExactPath(evidence.Summary, path) {
			continue
		}
		if evidence.Kind == EvidenceWrite {
			return ContractGatePassed, true
		}
		if evidence.Kind == EvidenceTool && strings.Contains(strings.ToLower(evidence.Summary), "failed write") {
			foundFailed = true
		}
	}
	if foundFailed {
		return ContractGateFailed, true
	}
	return ContractGatePending, false
}

func evidenceSummaryHasExactPath(summary, path string) bool {
	path = normalizeIntentPath(path)
	if path == "" {
		return false
	}
	for _, got := range evidenceSummaryPaths(summary) {
		if normalizeIntentPath(got) == path {
			return true
		}
	}
	return false
}

func evidenceSummaryPaths(summary string) []string {
	idx := strings.Index(summary, ":")
	if idx < 0 {
		return nil
	}
	rest := strings.TrimSpace(summary[idx+1:])
	fields := strings.Fields(rest)
	if len(fields) < 2 {
		return nil
	}
	pathsText := strings.TrimSpace(strings.TrimPrefix(rest, fields[0]))
	var paths []string
	for _, path := range strings.Split(pathsText, ",") {
		if path = strings.TrimSpace(path); path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

func artifactContentPlausible(contract *TurnContract, artifact ArtifactRequirement, content string) bool {
	if strings.TrimSpace(content) == "" {
		return false
	}
	lowerPath := strings.ToLower(strings.TrimSpace(artifact.Path))
	if !strings.HasSuffix(lowerPath, ".md") || !artifactRequiresMarkdownPlanSpecStructure(contract, artifact) {
		return true
	}
	lines := strings.Split(content, "\n")
	hasHeading := false
	hasSection := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			hasHeading = true
		}
		if strings.HasPrefix(trimmed, "## ") {
			for _, body := range lines[i+1:] {
				if strings.TrimSpace(body) != "" {
					hasSection = true
					break
				}
			}
		}
	}
	return hasHeading && hasSection
}

func artifactRequiresMarkdownPlanSpecStructure(contract *TurnContract, artifact ArtifactRequirement) bool {
	lowerPath := strings.ToLower(strings.TrimSpace(artifact.Path))
	lowerDescription := strings.ToLower(strings.TrimSpace(artifact.Description))
	if strings.Contains(lowerDescription, "plan") || strings.Contains(lowerDescription, "spec") {
		return true
	}
	if strings.Contains(lowerPath, "plan") || strings.Contains(lowerPath, "spec") {
		return true
	}
	return false
}
