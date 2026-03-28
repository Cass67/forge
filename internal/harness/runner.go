package harness

import (
	"context"
	"fmt"
	"strings"

	"forge/internal/skills"
)

type Runner struct {
	session         *Session
	trace           *Recorder
	local           LocalExecutor
	strictLocal     LocalExecutor
	workers         WorkerExecutor
	workspacePolicy WorkspacePolicy
	policyAction    string
	workerSkills    []skills.Skill
	workerAutoMode  string
}

type RunnerConfig struct {
	Session         *Session
	Trace           *Recorder
	Local           LocalExecutor
	StrictLocal     LocalExecutor
	Workers         WorkerExecutor
	WorkspacePolicy WorkspacePolicy
	WorkerSkills    []skills.Skill
	WorkerAutoMode  string
}

func NewRunner(cfg RunnerConfig) *Runner {
	session := cfg.Session
	if session == nil {
		session = NewSession()
	}
	trace := cfg.Trace
	if trace == nil {
		trace = NewRecorder()
	}
	return &Runner{
		session:         session,
		trace:           trace,
		local:           cfg.Local,
		strictLocal:     cfg.StrictLocal,
		workers:         cfg.Workers,
		workspacePolicy: cfg.WorkspacePolicy,
		workerSkills:    append([]skills.Skill(nil), cfg.WorkerSkills...),
		workerAutoMode:  cfg.WorkerAutoMode,
	}
}

func (r *Runner) Run(ctx context.Context, input string) (TurnResult, error) {
	r.trace.Reset()
	r.policyAction = ""

	turn := r.session.BeginTurn(input)
	snapshot := r.session.Snapshot()
	r.trace.Add(StateIntake, "", "", WorkerNone, "user turn received", "")

	class := Classify(turn, snapshot)
	r.trace.Record(TraceRecord{
		State:                 StateClassify,
		Family:                class.Family,
		Reason:                class.Reason,
		TopicKey:              class.TopicKey,
		ThreadID:              snapshot.ActiveThread().ID,
		ThreadKind:            snapshot.ActiveThread().Kind,
		ThreadStatus:          snapshot.ActiveThread().Status,
		ThreadPhase:           snapshot.ActiveThread().Phase,
		ThreadIntent:          class.ThreadIntent,
		WorkspacePolicyAction: r.policyAction,
	})

	policyMilestone, policyErr := r.ensureWorkspacePolicy(ctx, turn, class, snapshot)
	if policyErr != nil {
		r.trace.Record(TraceRecord{
			State:                 StateBlocked,
			Family:                class.Family,
			Reason:                "workspace policy failed: " + strings.TrimSpace(policyErr.Error()),
			TopicKey:              class.TopicKey,
			ThreadPhase:           snapshot.ActiveThread().Phase,
			WorkspacePolicyAction: r.policyAction,
		})
		return r.blockedResultForPolicyFailure(class, snapshot, policyErr), nil
	}
	r.policyAction = strings.TrimSpace(policyMilestone.Message)
	if strings.TrimSpace(policyMilestone.Message) != "" {
		r.trace.Record(TraceRecord{
			State:                 StatePlanStep,
			Family:                class.Family,
			Reason:                "workspace policy: " + strings.TrimSpace(policyMilestone.Message),
			TopicKey:              class.TopicKey,
			ThreadPhase:           snapshot.ActiveThread().Phase,
			WorkspacePolicyAction: r.policyAction,
		})
	}

	planned := Plan(class, snapshot)
	r.trace.Record(TraceRecord{
		State:                 StatePlanStep,
		Family:                class.Family,
		Lane:                  planned.Lane,
		Step:                  planned.Kind,
		Worker:                planned.Worker,
		Reason:                planned.Reason,
		TopicKey:              class.TopicKey,
		ThreadPhase:           snapshot.ActiveThread().Phase,
		WorkspacePolicyAction: r.policyAction,
	})

	step, obs, err := r.executeStep(ctx, turn, class, snapshot, planned)
	obs = enrichObservation(turn, class, snapshot, obs)
	if strings.TrimSpace(policyMilestone.Message) != "" {
		obs.Progress = append([]ProgressMilestone{policyMilestone}, obs.Progress...)
	}
	decision := Decide(class, obs)
	r.trace.Record(TraceRecord{
		State:                 StateDecide,
		Family:                class.Family,
		Lane:                  step.Lane,
		Step:                  step.Kind,
		Worker:                step.Worker,
		Reason:                decision.Reason,
		TopicKey:              class.TopicKey,
		ThreadPhase:           activeThreadPhase(snapshot),
		ClaimGuardStatus:      claimGuardStatus(obs),
		WorkspacePolicyAction: r.policyAction,
		ToolCallCount:         len(obs.ToolCalls),
		OutcomeKind:           obs.Outcome.Kind,
		DeliverableKind:       obs.Outcome.DeliverableKind,
		DeliverableStatus:     obs.Outcome.DeliverableStatus,
	})

	obs.Response = buildForgeResponse(step, obs)
	if decision.FinalState == StateComplete || decision.FinalState == StateAwaitingFeedback {
		obs.Response = appendResponsePostlude(obs.Response, class.ResponsePostlude)
		r.session.Apply(class, obs)
		if threadRecord, ok := threadTraceRecord(snapshot, r.session.Snapshot(), class); ok {
			r.trace.Record(threadRecord)
		}
		r.trace.Record(TraceRecord{
			State:                 StateRespond,
			Family:                class.Family,
			Lane:                  step.Lane,
			Step:                  StepRespond,
			Reason:                "emit final forge response",
			TopicKey:              class.TopicKey,
			ThreadPhase:           activeThreadPhase(r.session.Snapshot()),
			WorkspacePolicyAction: r.policyAction,
		})
		r.trace.Record(TraceRecord{
			State:                 decision.FinalState,
			Family:                class.Family,
			Lane:                  step.Lane,
			Step:                  StepRespond,
			Reason:                decision.Reason,
			TopicKey:              class.TopicKey,
			ThreadPhase:           activeThreadPhase(r.session.Snapshot()),
			ClaimGuardStatus:      claimGuardStatus(obs),
			WorkspacePolicyAction: r.policyAction,
			ToolCallCount:         len(obs.ToolCalls),
			OutcomeKind:           decision.Outcome,
			DeliverableKind:       obs.Outcome.DeliverableKind,
			DeliverableStatus:     obs.Outcome.DeliverableStatus,
		})
	} else {
		if strings.TrimSpace(obs.Response) != "" {
			r.trace.Record(TraceRecord{
				State:                 StateRespond,
				Family:                class.Family,
				Lane:                  step.Lane,
				Step:                  StepRespond,
				Reason:                "emit blocked forge response",
				TopicKey:              class.TopicKey,
				ThreadPhase:           activeThreadPhase(snapshot),
				WorkspacePolicyAction: r.policyAction,
			})
		}
		r.trace.Record(TraceRecord{
			State:                 decision.FinalState,
			Family:                class.Family,
			Lane:                  step.Lane,
			Step:                  step.Kind,
			Worker:                step.Worker,
			Reason:                decision.Reason,
			TopicKey:              class.TopicKey,
			ThreadPhase:           activeThreadPhase(snapshot),
			ClaimGuardStatus:      claimGuardStatus(obs),
			WorkspacePolicyAction: r.policyAction,
			ToolCallCount:         len(obs.ToolCalls),
			OutcomeKind:           decision.Outcome,
			DeliverableKind:       obs.Outcome.DeliverableKind,
			DeliverableStatus:     obs.Outcome.DeliverableStatus,
		})
	}

	return TurnResult{
		Response:       obs.Response,
		Classification: class,
		Step:           step,
		Observation:    obs,
		Decision:       decision,
		Trace:          r.trace.Records(),
	}, err
}

func (r *Runner) ensureWorkspacePolicy(ctx context.Context, turn UserTurn, class Classification, session SessionState) (ProgressMilestone, error) {
	if !requiresWorkspacePolicy(class) || r.workspacePolicy == nil {
		return ProgressMilestone{}, nil
	}
	return r.workspacePolicy.EnsureExecutionContext(ctx, turn, class, session)
}

func (r *Runner) blockedResultForPolicyFailure(class Classification, session SessionState, err error) TurnResult {
	reason := firstNonEmpty(errorString(err), "workspace policy blocked execution")
	step := Step{
		Lane:    LaneStrictAction,
		Kind:    StepBlocked,
		Worker:  WorkerNone,
		Reason:  reason,
		Summary: reason,
	}
	obs := normalizeObservation(step, class, session, Observation{
		Status:   ObservationBlocked,
		Lane:     step.Lane,
		Summary:  reason,
		TopicKey: class.TopicKey,
		Outcome: ActionOutcome{
			Lane:              step.Lane,
			Kind:              OutcomeBlocked,
			DeliverableKind:   resolveExpectedDeliverable(class, session, step.Lane),
			DeliverableStatus: DeliverableMissing,
			Reason:            reason,
		},
		Err: err,
	})
	decision := Decide(class, obs)
	obs.Response = buildForgeResponse(step, obs)
	r.trace.Record(TraceRecord{
		State:                 decision.FinalState,
		Family:                class.Family,
		Lane:                  step.Lane,
		Step:                  step.Kind,
		Reason:                decision.Reason,
		TopicKey:              class.TopicKey,
		ThreadPhase:           activeThreadPhase(session),
		ClaimGuardStatus:      claimGuardStatus(obs),
		WorkspacePolicyAction: r.policyAction,
		ToolCallCount:         len(obs.ToolCalls),
		OutcomeKind:           decision.Outcome,
		DeliverableKind:       obs.Outcome.DeliverableKind,
		DeliverableStatus:     obs.Outcome.DeliverableStatus,
	})
	return TurnResult{
		Response:       obs.Response,
		Classification: class,
		Step:           step,
		Observation:    obs,
		Decision:       decision,
		Trace:          r.trace.Records(),
	}
}

func (r *Runner) Trace() []TraceRecord {
	return r.trace.Records()
}

func (r *Runner) executeStep(ctx context.Context, turn UserTurn, class Classification, session SessionState, step Step) (Step, Observation, error) {
	if step.Kind == StepWorker {
		return r.executeWorker(ctx, turn, class, session, step)
	}
	if step.Kind == StepStrictLocal {
		return r.executeStrictLocal(ctx, turn, class, step)
	}
	return r.executeLocal(ctx, turn, class, step)
}

func (r *Runner) executeWorker(ctx context.Context, turn UserTurn, class Classification, session SessionState, step Step) (Step, Observation, error) {
	if r.workers == nil {
		r.trace.Record(TraceRecord{
			State:                 StateBlocked,
			Family:                class.Family,
			Lane:                  step.Lane,
			Step:                  step.Kind,
			Worker:                step.Worker,
			Reason:                "worker executor unavailable; recovering locally",
			TopicKey:              class.TopicKey,
			ThreadPhase:           activeThreadPhase(session),
			WorkspacePolicyAction: r.policyAction,
		})
		return r.executeLocal(ctx, turn, class, localRecoveryStep())
	}

	r.trace.Record(TraceRecord{
		State:                 StateAct,
		Family:                class.Family,
		Lane:                  step.Lane,
		Step:                  step.Kind,
		Worker:                step.Worker,
		Reason:                step.Summary,
		TopicKey:              class.TopicKey,
		ThreadPhase:           activeThreadPhase(session),
		WorkspacePolicyAction: r.policyAction,
	})
	deadline, _ := ctx.Deadline()
	obs, err := r.workers.Execute(ctx, WorkerTask{
		Kind:                              step.Worker,
		Objective:                         turn.Text,
		Context:                           workerContext(class, session, step),
		TopicKey:                          class.TopicKey,
		StopCondition:                     step.Reason,
		RequireRepresentativeFileEvidence: readerTaskNeedsRepresentativeFile(step, class),
		RequireNonReadmeFileEvidence:      readerTaskNeedsNonReadmeFile(step, class),
		SkillContext: WorkerSkillContext{
			Loaded:   append([]skills.Skill(nil), r.workerSkills...),
			AutoMode: r.workerAutoMode,
		},
		PermissionProfile: append([]string(nil), workerToolAllowlist(step.Worker)...),
		Deadline:          deadline,
	})
	obs = normalizeObservation(step, class, session, obs)
	r.trace.Record(TraceRecord{
		State:                 StateObserve,
		Family:                class.Family,
		Lane:                  step.Lane,
		Step:                  step.Kind,
		Worker:                step.Worker,
		Reason:                firstNonEmpty(obs.Outcome.Reason, obs.Summary, "worker execution complete"),
		TopicKey:              class.TopicKey,
		ThreadPhase:           activeThreadPhase(session),
		ClaimGuardStatus:      claimGuardStatus(obs),
		WorkspacePolicyAction: r.policyAction,
		ToolCallCount:         len(obs.ToolCalls),
		OutcomeKind:           obs.Outcome.Kind,
		DeliverableKind:       obs.Outcome.DeliverableKind,
		DeliverableStatus:     obs.Outcome.DeliverableStatus,
	})
	if err == nil && obs.Status != ObservationBlocked {
		return step, obs, nil
	}

	reason := firstNonEmpty(obs.Outcome.Reason, obs.Summary, errorString(err), "worker failed closed; recovering locally")
	r.trace.Record(TraceRecord{
		State:                 StateBlocked,
		Family:                class.Family,
		Lane:                  step.Lane,
		Step:                  step.Kind,
		Worker:                step.Worker,
		Reason:                reason,
		TopicKey:              class.TopicKey,
		ThreadPhase:           activeThreadPhase(session),
		WorkspacePolicyAction: r.policyAction,
	})

	fallback := localRecoveryStep()
	r.trace.Record(TraceRecord{
		State:                 StatePlanStep,
		Family:                class.Family,
		Lane:                  fallback.Lane,
		Step:                  fallback.Kind,
		Worker:                fallback.Worker,
		Reason:                fallback.Reason,
		TopicKey:              class.TopicKey,
		ThreadPhase:           activeThreadPhase(session),
		WorkspacePolicyAction: r.policyAction,
	})
	return r.executeLocal(ctx, turn, class, fallback)
}

func (r *Runner) executeLocal(ctx context.Context, turn UserTurn, class Classification, step Step) (Step, Observation, error) {
	return r.executeLocalExecutor(ctx, turn, class, r.session.Snapshot(), step, r.local)
}

func (r *Runner) executeStrictLocal(ctx context.Context, turn UserTurn, class Classification, step Step) (Step, Observation, error) {
	if r.strictLocal == nil {
		r.trace.Record(TraceRecord{
			State:                 StateBlocked,
			Family:                class.Family,
			Lane:                  step.Lane,
			Step:                  step.Kind,
			Worker:                step.Worker,
			Reason:                "strict local executor unavailable; recovering locally",
			TopicKey:              class.TopicKey,
			ThreadPhase:           activeThreadPhase(r.session.Snapshot()),
			WorkspacePolicyAction: r.policyAction,
		})
		fallback := localRecoveryStep()
		r.trace.Record(TraceRecord{
			State:                 StatePlanStep,
			Family:                class.Family,
			Lane:                  fallback.Lane,
			Step:                  fallback.Kind,
			Worker:                fallback.Worker,
			Reason:                fallback.Reason,
			TopicKey:              class.TopicKey,
			ThreadPhase:           activeThreadPhase(r.session.Snapshot()),
			WorkspacePolicyAction: r.policyAction,
		})
		return r.executeLocal(ctx, turn, class, fallback)
	}
	return r.executeLocalExecutor(ctx, turn, class, r.session.Snapshot(), step, r.strictLocal)
}

func localRecoveryStep() Step {
	return Step{
		Lane:    LaneConversational,
		Kind:    StepLocal,
		Worker:  WorkerNone,
		Reason:  "worker failed closed; recover locally",
		Summary: "run locally",
	}
}

func workerContext(class Classification, session SessionState, step Step) string {
	switch step.Worker {
	case WorkerReader:
		if class.Family != FamilyInspect {
			return ""
		}
		lines := []string{`Gather concrete workspace evidence before you conclude.
For a directory or repository walkthrough:
- inspect the top-level structure with list_dir
- inspect one or two representative files such as README.md, go.mod, package.json, or a relevant entrypoint when present
- use git_status or git_log only when they materially help explain the state
- stop once you can explain what the directory is and how it is organized`}
		if class.WantsEvaluation {
			lines = append(lines,
				"The user wants evidence-backed findings or cleanup recommendations, not just a neutral walkthrough.",
				"Make each evidence summary explain the concrete observation and why it matters.",
			)
			if isInspectableWorkspaceTopic(class.TopicKey) {
				lines = append(lines,
					"For an evaluative repository or directory review, README-only evidence is not enough.",
					"Inspect at least one grounded implementation, config, or entrypoint file beyond README before you conclude.",
				)
			}
		}
		if session.HasRecentEvidence() && strings.TrimSpace(session.LastEvidence.TopicKey) != "" {
			lines = append(lines,
				"Recent evidence topic: "+strings.TrimSpace(session.LastEvidence.TopicKey),
				"Recent evidence summary: "+strings.TrimSpace(session.LastEvidence.Summary),
			)
		}
		return strings.TrimSpace(strings.Join(lines, "\n"))
	case WorkerEditor:
		lines := []string{
			"Implement the requested change in the workspace instead of drafting code in chat.",
			"Inspect relevant files before editing, then create or update the actual file that delivers the request.",
			"When the user asks for a script, tool, helper, or test, return a real file change with its path in the JSON result.",
			"Run a focused verification command for the touched files when possible.",
		}
		if strings.TrimSpace(class.TopicKey) != "" {
			lines = append(lines, "Primary scope: "+strings.TrimSpace(class.TopicKey))
		}
		if session.HasRecentEvidence() && strings.TrimSpace(session.LastEvidence.TopicKey) != "" {
			lines = append(lines,
				"Recent evidence topic: "+strings.TrimSpace(session.LastEvidence.TopicKey),
				"Recent evidence summary: "+strings.TrimSpace(session.LastEvidence.Summary),
			)
		}
		return strings.TrimSpace(strings.Join(lines, "\n"))
	default:
		return ""
	}
}

func readerTaskNeedsRepresentativeFile(step Step, class Classification) bool {
	return step.Worker == WorkerReader && class.Family == FamilyInspect && isInspectableWorkspaceTopic(class.TopicKey)
}

func readerTaskNeedsNonReadmeFile(step Step, class Classification) bool {
	return step.Worker == WorkerReader &&
		class.Family == FamilyInspect &&
		class.WantsEvaluation &&
		isInspectableWorkspaceTopic(class.TopicKey)
}

func buildForgeResponse(step Step, obs Observation) string {
	if step.Kind != StepWorker {
		return firstNonEmpty(strings.TrimSpace(obs.Response), strings.TrimSpace(obs.Summary))
	}

	switch artifact := obs.Artifact.(type) {
	case ReaderResult:
		return firstNonEmpty(joinReaderEvidence(artifact.Evidence), artifact.Coverage, obs.Summary)
	case EditorResult:
		return firstNonEmpty(joinChangeSummaries(artifact.Changes), strings.Join(trimmedStrings(artifact.RemainingIssues), " "), obs.Summary, "Edit complete.")
	case VerifierResult:
		if failures := trimmedStrings(artifact.Failures); len(failures) > 0 {
			return strings.Join(failures, " ")
		}
		if len(artifact.Checks) > 0 {
			return fmt.Sprintf("Verified %d checks.", len(artifact.Checks))
		}
		return firstNonEmpty(obs.Summary, "Verification complete.")
	case ResearcherResult:
		return composeResearchResponse(artifact, obs.Summary)
	default:
		return firstNonEmpty(obs.Summary, obs.Response)
	}
}

func (r *Runner) executeLocalExecutor(ctx context.Context, turn UserTurn, class Classification, session SessionState, step Step, exec LocalExecutor) (Step, Observation, error) {
	attempts := 0
	for {
		r.trace.Record(TraceRecord{
			State:                 StateAct,
			Family:                class.Family,
			Lane:                  step.Lane,
			Step:                  step.Kind,
			Worker:                step.Worker,
			Reason:                step.Summary,
			TopicKey:              class.TopicKey,
			ThreadPhase:           activeThreadPhase(session),
			WorkspacePolicyAction: r.policyAction,
		})

		obs, err := exec.Execute(ctx, turn, class, r.session.Snapshot())
		obs = normalizeObservation(step, class, session, obs)
		r.trace.Record(TraceRecord{
			State:                 StateObserve,
			Family:                class.Family,
			Lane:                  step.Lane,
			Step:                  step.Kind,
			Worker:                step.Worker,
			Reason:                firstNonEmpty(obs.Outcome.Reason, obs.Summary, "local execution complete"),
			TopicKey:              class.TopicKey,
			ThreadPhase:           activeThreadPhase(session),
			ClaimGuardStatus:      claimGuardStatus(obs),
			WorkspacePolicyAction: r.policyAction,
			ToolCallCount:         len(obs.ToolCalls),
			OutcomeKind:           obs.Outcome.Kind,
			DeliverableKind:       obs.Outcome.DeliverableKind,
			DeliverableStatus:     obs.Outcome.DeliverableStatus,
		})
		if err != nil {
			return step, obs, err
		}
		if obs.Outcome.Kind == OutcomeRetry && step.Lane == LaneStrictAction && attempts < maxStrictActionRetries {
			attempts++
			r.trace.Record(TraceRecord{
				State:                 StateRetry,
				Family:                class.Family,
				Lane:                  step.Lane,
				Step:                  step.Kind,
				Worker:                step.Worker,
				Reason:                obs.Outcome.Reason,
				TopicKey:              class.TopicKey,
				ThreadPhase:           activeThreadPhase(session),
				ClaimGuardStatus:      claimGuardStatus(obs),
				WorkspacePolicyAction: r.policyAction,
				ToolCallCount:         len(obs.ToolCalls),
				OutcomeKind:           obs.Outcome.Kind,
				DeliverableKind:       obs.Outcome.DeliverableKind,
				DeliverableStatus:     obs.Outcome.DeliverableStatus,
			})
			continue
		}
		if obs.Outcome.Kind == OutcomeRetry {
			obs = exhaustObservationRetry(obs)
		}
		return step, obs, nil
	}
}

func joinReaderEvidence(evidence []ReaderEvidence) string {
	parts := make([]string, 0, len(evidence))
	for _, item := range evidence {
		if summary := strings.TrimSpace(item.Summary); summary != "" {
			parts = append(parts, summary)
		}
	}
	return strings.Join(parts, " ")
}

func joinChangeSummaries(changes []ChangeRecord) string {
	parts := make([]string, 0, len(changes))
	for _, item := range changes {
		if summary := strings.TrimSpace(item.Summary); summary != "" {
			parts = append(parts, summary)
		}
	}
	return strings.Join(parts, " ")
}

func composeResearchResponse(result ResearcherResult, fallback string) string {
	findings := make([]string, 0, len(result.Findings))
	for _, finding := range result.Findings {
		if summary := strings.TrimSpace(finding.Summary); summary != "" {
			findings = append(findings, summary)
		}
	}

	response := strings.Join(findings, " ")
	labels := make([]string, 0, len(result.Sources))
	for _, source := range result.Sources {
		if label := strings.TrimSpace(source.Label); label != "" {
			labels = append(labels, label)
		}
	}
	if len(labels) > 0 {
		suffix := "Sources checked: " + humanList(labels) + "."
		response = firstNonEmpty(strings.TrimSpace(response+" "+suffix), suffix)
	}
	return firstNonEmpty(response, fallback, "Research complete.")
}

func humanList(values []string) string {
	values = trimmedStrings(values)
	switch len(values) {
	case 0:
		return ""
	case 1:
		return values[0]
	case 2:
		return values[0] + " and " + values[1]
	default:
		return strings.Join(values[:len(values)-1], ", ") + ", and " + values[len(values)-1]
	}
}

func trimmedStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func activeThreadPhase(state SessionState) ThreadPhase {
	if !state.HasActiveThread() {
		return ThreadPhaseNone
	}
	phase := state.ActiveThread().Phase
	if phase == ThreadPhaseNone && state.ActiveThread().Kind == ThreadPreviewCollaboration {
		return ThreadPhaseIdeate
	}
	return phase
}

func claimGuardStatus(obs Observation) string {
	reason := strings.ToLower(firstNonEmpty(obs.Outcome.Reason, obs.Summary))
	if strings.Contains(reason, "without supporting tool evidence") ||
		strings.Contains(reason, "without git_commit evidence") ||
		strings.Contains(reason, "without verified preview runtime evidence") {
		return "blocked"
	}
	if len(obs.ToolCalls) > 0 {
		return "evidence_present"
	}
	return "not_applicable"
}

func enrichObservation(turn UserTurn, class Classification, _ SessionState, obs Observation) Observation {
	if obs.PendingAction.IsZero() {
		obs.PendingAction = inferPendingAction(turn, class, obs)
	}
	return obs
}

func inferPendingAction(turn UserTurn, class Classification, obs Observation) PendingAction {
	if pending := inferPendingReviewAction(turn, class); !pending.IsZero() {
		return pending
	}
	return inferPendingInspectOffer(turn, class, obs)
}

func inferPendingReviewAction(turn UserTurn, class Classification) PendingAction {
	if class.Family != FamilyAnswer ||
		class.NeedsPolicyGuard ||
		class.NeedsTerseAnswer ||
		class.WantsAction ||
		class.WantsInterpretation ||
		!class.WantsEvaluation ||
		!looksLikeQuestionishReviewPrompt(turn.Text) {
		return PendingAction{}
	}

	lower := strings.ToLower(strings.TrimSpace(turn.Text))
	tokens := tokenize(lower)
	if !mentionsImprovementReviewIntent(tokens, lower) {
		return PendingAction{}
	}
	scope := inferRequestScope(lower, tokens)
	if !scope.Inspectable() {
		scope = requestScope{Kind: scopeRepository, TopicKey: "workspace:repository"}
	}
	return PendingAction{
		Family:          FamilyInspect,
		TopicKey:        resolveTopicKey(turn.Text, scope),
		TaskText:        reviewTaskTextForScope(scope),
		WantsEvaluation: true,
		CanStayLocal:    true,
	}
}

func inferPendingInspectOffer(turn UserTurn, class Classification, obs Observation) PendingAction {
	if obs.Status != ObservationComplete ||
		(class.Family != FamilyAnswer && class.Family != FamilyInspect) ||
		class.NeedsPolicyGuard ||
		class.NeedsTerseAnswer ||
		class.WantsAction ||
		class.WantsInterpretation {
		return PendingAction{}
	}

	response := strings.TrimSpace(obs.Response)
	if response == "" {
		return PendingAction{}
	}

	lower := strings.ToLower(response)
	tokens := tokenize(lower)
	if !looksLikeAssistantOffer(lower) || !mentionsInspectOffer(lower, tokens) {
		return PendingAction{}
	}

	topicKey := resolvePendingInspectOfferTopic(turn, class, response)
	if topicKey == "" {
		return PendingAction{}
	}

	return PendingAction{
		Family:          FamilyInspect,
		TopicKey:        topicKey,
		TaskText:        inspectTaskTextForTopic(topicKey),
		WantsEvaluation: wantsEvaluation(tokens, lower),
		CanStayLocal:    true,
	}
}

func looksLikeAssistantOffer(lower string) bool {
	return strings.Contains(lower, "if you want") ||
		strings.Contains(lower, "want me to") ||
		strings.Contains(lower, "i can ") ||
		strings.Contains(lower, "i could ") ||
		strings.Contains(lower, "happy to")
}

func mentionsInspectOffer(lower string, tokens map[string]struct{}) bool {
	for token := range tokens {
		if hasInspectOfferStem(token) {
			return true
		}
	}
	return strings.Contains(lower, "look at") || strings.Contains(lower, "go over")
}

func hasInspectOfferStem(token string) bool {
	switch {
	case strings.HasPrefix(token, "inspect"),
		strings.HasPrefix(token, "check"),
		strings.HasPrefix(token, "review"),
		strings.HasPrefix(token, "examin"),
		strings.HasPrefix(token, "read"):
		return true
	default:
		return false
	}
}

func resolvePendingInspectOfferTopic(turn UserTurn, class Classification, response string) string {
	responseLower := strings.ToLower(response)
	responseTokens := tokenize(responseLower)
	if topicKey := resolveTopicKey(response, inferRequestScope(responseLower, responseTokens)); topicKey != "" {
		return topicKey
	}
	if topicKey := strings.TrimSpace(class.TopicKey); topicKey != "" {
		return topicKey
	}
	turnLower := strings.ToLower(strings.TrimSpace(turn.Text))
	turnTokens := tokenize(turnLower)
	return resolveTopicKey(turn.Text, inferRequestScope(turnLower, turnTokens))
}

func inspectTaskTextForTopic(topicKey string) string {
	switch {
	case strings.HasPrefix(topicKey, "path:"):
		return "inspect `" + strings.TrimSpace(strings.TrimPrefix(topicKey, "path:")) + "`"
	case topicKey == "workspace:repository":
		return "inspect the repository"
	case topicKey == "workspace:directory":
		return "inspect the current directory"
	case strings.HasPrefix(topicKey, "files:"):
		return "inspect the matching files"
	default:
		return ""
	}
}

func mentionsImprovementReviewIntent(tokens map[string]struct{}, lower string) bool {
	return strings.Contains(lower, "improvement") ||
		strings.Contains(lower, "improvements") ||
		strings.Contains(lower, "review") ||
		strings.Contains(lower, "audit") ||
		hasToken(tokens, "recommend") ||
		hasToken(tokens, "recommendations") ||
		hasToken(tokens, "issue") ||
		hasToken(tokens, "issues") ||
		hasToken(tokens, "problem") ||
		hasToken(tokens, "problems") ||
		hasToken(tokens, "fix") ||
		hasToken(tokens, "fixes")
}

func reviewTaskTextForScope(scope requestScope) string {
	switch scope.Kind {
	case scopeDirectory:
		return "review the current directory for improvement opportunities"
	case scopeFocusedFiles:
		return "review the matching files for improvement opportunities"
	default:
		return "review the whole repo for improvement opportunities"
	}
}

func looksLikeQuestionishReviewPrompt(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	if looksQuestionLike(text) {
		return true
	}
	return startsWithAny(lower, "how can", "how do", "can you", "could you", "what would", "what could", "why would")
}

func appendResponsePostlude(response, postlude string) string {
	response = strings.TrimSpace(response)
	postlude = strings.TrimSpace(postlude)
	switch {
	case response == "":
		return postlude
	case postlude == "":
		return response
	default:
		return response + "\n\n" + postlude
	}
}
