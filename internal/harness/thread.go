package harness

import (
	"fmt"
	"strings"
)

func classifyActiveThreadTurn(text string, session SessionState) (Classification, bool) {
	if !session.HasActiveThread() {
		return Classification{}, false
	}

	active := session.ActiveThread()
	lower := strings.ToLower(strings.TrimSpace(text))
	tokens := tokenize(lower)
	ordered := tokenList(lower)
	scope := inferRequestScope(lower, tokens)

	if looksLikeThreadCancellation(lower, tokens, ordered) {
		return Classification{
			Family:                  FamilyAnswer,
			PrefersVisibleExecution: active.Kind == ThreadPreviewCollaboration,
			CanStayLocal:            true,
			IsFollowUp:              true,
			TopicKey:                strings.TrimSpace(active.TopicKey),
			TaskText:                strings.TrimSpace(text),
			ThreadIntent:            TurnIntentCancelThread,
			Reason:                  "active thread cancellation",
		}, true
	}

	if active.Kind != ThreadPreviewCollaboration {
		return Classification{}, false
	}

	if looksLikeActivePreviewReplay(lower, tokens, ordered) {
		return Classification{
			Family:                  FamilyAnswer,
			PrefersVisibleExecution: true,
			CanStayLocal:            true,
			IsFollowUp:              true,
			TopicKey:                strings.TrimSpace(active.TopicKey),
			TaskText:                strings.TrimSpace(text),
			ThreadIntent:            TurnIntentReplayThread,
			Reason:                  "active thread replay",
		}, true
	}

	if looksLikeActivePreviewContinuation(text, lower, ordered, tokens, scope) {
		taskText := strings.TrimSpace(text)
		if onlyContinuationScaffolding(ordered) || looksLikeOpaqueShortContinuation(ordered) {
			taskText = firstNonEmpty(strings.TrimSpace(active.TaskText), strings.TrimSpace(active.Goal), taskText)
		}
		family := FamilyImplement
		if containsAny(tokens, debugTokens) {
			family = FamilyDebug
		}
		return Classification{
			Family:                  family,
			WantsAction:             true,
			PrefersVisibleExecution: true,
			CanStayLocal:            true,
			IsFollowUp:              true,
			TopicKey:                strings.TrimSpace(active.TopicKey),
			TaskText:                taskText,
			ThreadIntent:            TurnIntentContinueThread,
			Reason:                  "active thread continuation",
		}, true
	}

	return Classification{}, false
}

func looksLikeThreadCancellation(lower string, tokens map[string]struct{}, ordered []string) bool {
	if len(ordered) == 0 || len(ordered) > 8 {
		return false
	}
	if strings.Contains(lower, "never mind") || strings.Contains(lower, "nevermind") || strings.Contains(lower, "forget it") {
		return true
	}
	return hasToken(tokens, "cancel") || hasToken(tokens, "stop")
}

func looksLikeActivePreviewReplay(lower string, tokens map[string]struct{}, ordered []string) bool {
	if strings.Contains(lower, "where can i see") {
		return true
	}
	return looksLikeRuntimeReplayFollowUp(tokens, lower, ordered) || looksLikeRuntimeThreadQuestion(lower, tokens, ordered)
}

func looksLikeActivePreviewContinuation(text, lower string, ordered []string, tokens map[string]struct{}, scope requestScope) bool {
	if looksLikeRuntimeThreadRevision(lower, tokens, ordered) ||
		looksLikeContextualContinuation(lower) ||
		onlyContinuationScaffolding(ordered) ||
		looksLikeOpaqueShortContinuation(ordered) {
		return true
	}
	if wantsImplementation(scope, ordered, tokens, lower, text) {
		return true
	}
	return containsAny(tokens, debugTokens) && !looksQuestionLike(text)
}

func applyThreadLedger(state *SessionState, class Classification, obs Observation) {
	if state == nil {
		return
	}

	if class.ThreadIntent == TurnIntentCancelThread {
		cancelActiveThread(state)
		return
	}
	if obs.Status != ObservationComplete || !shouldTrackThread(class) {
		return
	}

	current := state.Threads.Active
	deliverable := resolveExpectedDeliverable(class, *state, inferredLane(class))
	kind := resolveThreadKind(class, deliverable)
	continuing := current.IsOpen() && class.IsFollowUp && current.Kind == kind
	if class.PrefersVisibleExecution && current.IsOpen() && current.Kind == ThreadPreviewCollaboration && class.ThreadIntent != TurnIntentSupersedeThread {
		continuing = true
	}

	if continuing {
		thread := current
		updateThreadState(&thread, class, obs, state.Turn)
		state.Threads.Active = thread
		return
	}

	supersedes := ""
	if current.IsOpen() {
		supersedes = current.ID
		archiveThread(state, current, ThreadSuperseded)
	}

	thread := ThreadState{
		ID:                 nextThreadID(state),
		Kind:               kind,
		Deliverable:        deliverable,
		SupersedesThreadID: supersedes,
	}
	updateThreadState(&thread, class, obs, state.Turn)
	state.Threads.Active = thread
}

func shouldTrackThread(class Classification) bool {
	if class.NeedsPolicyGuard || class.NeedsTerseAnswer {
		return false
	}
	if class.PrefersVisibleExecution {
		return true
	}
	switch class.Family {
	case FamilyInspect, FamilyImplement, FamilyDebug, FamilyVerify, FamilyResearch:
		return true
	default:
		return false
	}
}

func resolveThreadKind(class Classification, deliverable DeliverableKind) ThreadKind {
	if deliverable == DeliverablePreviewAvailableAndRenderable {
		return ThreadPreviewCollaboration
	}
	switch class.Family {
	case FamilyInspect:
		return ThreadWorkspaceInspect
	case FamilyImplement, FamilyDebug:
		return ThreadWorkspaceChange
	case FamilyVerify:
		return ThreadVerification
	case FamilyResearch:
		return ThreadExternalResearch
	case FamilyAnswer:
		return ThreadDirectAnswer
	default:
		return ThreadDirectAnswer
	}
}

func updateThreadState(thread *ThreadState, class Classification, obs Observation, turn int) {
	if thread == nil {
		return
	}
	if thread.Kind == "" {
		thread.Kind = resolveThreadKind(class, thread.Deliverable)
	}
	if thread.Deliverable == "" {
		thread.Deliverable = resolveExpectedDeliverable(class, SessionState{}, inferredLane(class))
	}
	thread.Family = class.Family
	thread.TopicKey = firstNonEmpty(strings.TrimSpace(class.TopicKey), strings.TrimSpace(obs.TopicKey), strings.TrimSpace(thread.TopicKey))
	if thread.Goal == "" {
		thread.Goal = firstNonEmpty(strings.TrimSpace(class.TaskText), strings.TrimSpace(obs.Response), strings.TrimSpace(obs.Summary))
	}
	if strings.TrimSpace(class.TaskText) != "" {
		thread.TaskText = strings.TrimSpace(class.TaskText)
	}
	if thread.CreatedTurn <= 0 {
		thread.CreatedTurn = turn
	}
	thread.UpdatedTurn = turn
	if !obs.Runtime.Artifact.IsZero() {
		thread.Artifact = finalizeArtifactSnapshot(obs.Runtime.Artifact, turn)
	}
	if !obs.Runtime.Preview.IsZero() {
		thread.Preview = finalizePreviewSnapshot(obs.Runtime.Preview, turn)
	}
	switch obs.Outcome.Kind {
	case OutcomeAwaitingFeedback:
		thread.Status = ThreadAwaitingUserFeedback
	case OutcomeBlocked:
		thread.Status = ThreadBlocked
	default:
		thread.Status = ThreadActive
	}
}

func inferredLane(class Classification) ExecutionLane {
	if class.PrefersVisibleExecution {
		return LaneStrictAction
	}
	return LaneConversational
}

func cancelActiveThread(state *SessionState) {
	if state == nil || !state.Threads.Active.IsOpen() {
		return
	}
	archiveThread(state, state.Threads.Active, ThreadCanceled)
	state.Threads.Active = ThreadState{}
}

func archiveThread(state *SessionState, thread ThreadState, status ThreadStatus) {
	if state == nil || thread.IsZero() {
		return
	}
	thread.Status = status
	thread.UpdatedTurn = state.Turn
	state.Threads.Last = thread
}

func nextThreadID(state *SessionState) string {
	state.Threads.NextID++
	return fmt.Sprintf("thread-%d", state.Threads.NextID)
}

func threadTraceRecord(before, after SessionState, class Classification) (TraceRecord, bool) {
	switch class.ThreadIntent {
	case TurnIntentCancelThread:
		if after.Threads.Last.Status == ThreadCanceled {
			return TraceRecord{
				State:        StateRespond,
				Family:       class.Family,
				Step:         StepRespond,
				Reason:       "thread canceled",
				TopicKey:     after.Threads.Last.TopicKey,
				ThreadID:     after.Threads.Last.ID,
				ThreadKind:   after.Threads.Last.Kind,
				ThreadStatus: after.Threads.Last.Status,
				ThreadIntent: class.ThreadIntent,
			}, true
		}
	case TurnIntentReplayThread:
		if after.HasActiveThread() {
			thread := after.ActiveThread()
			return TraceRecord{
				State:        StateRespond,
				Family:       class.Family,
				Step:         StepRespond,
				Reason:       "thread replayed",
				TopicKey:     thread.TopicKey,
				ThreadID:     thread.ID,
				ThreadKind:   thread.Kind,
				ThreadStatus: thread.Status,
				ThreadIntent: class.ThreadIntent,
			}, true
		}
	case TurnIntentContinueThread:
		if after.HasActiveThread() {
			thread := after.ActiveThread()
			return TraceRecord{
				State:        StateRespond,
				Family:       class.Family,
				Step:         StepRespond,
				Reason:       "thread continued",
				TopicKey:     thread.TopicKey,
				ThreadID:     thread.ID,
				ThreadKind:   thread.Kind,
				ThreadStatus: thread.Status,
				ThreadIntent: class.ThreadIntent,
			}, true
		}
	}

	beforeThread := before.ActiveThread()
	afterThread := after.ActiveThread()
	if beforeThread.ID == "" && afterThread.ID != "" {
		return TraceRecord{
			State:        StateRespond,
			Family:       class.Family,
			Step:         StepRespond,
			Reason:       "thread created",
			TopicKey:     afterThread.TopicKey,
			ThreadID:     afterThread.ID,
			ThreadKind:   afterThread.Kind,
			ThreadStatus: afterThread.Status,
			ThreadIntent: class.ThreadIntent,
		}, true
	}
	if beforeThread.ID != "" && afterThread.ID != "" && beforeThread.ID != afterThread.ID && afterThread.SupersedesThreadID == beforeThread.ID {
		return TraceRecord{
			State:        StateRespond,
			Family:       class.Family,
			Step:         StepRespond,
			Reason:       "thread superseded",
			TopicKey:     afterThread.TopicKey,
			ThreadID:     afterThread.ID,
			ThreadKind:   afterThread.Kind,
			ThreadStatus: afterThread.Status,
			ThreadIntent: TurnIntentSupersedeThread,
		}, true
	}
	return TraceRecord{}, false
}
