package react

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

func (r *Runner) finalPlanGateMayBlock() bool {
	if r == nil || r.session == nil {
		return false
	}
	return planStateHasUnresolvedStep(r.session.Snapshot().PlanState)
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
	// The plan gate is an advisory nudge, not a wall: after a bounded number of
	// rejections the final answer goes through, so a stale plan step cannot burn
	// the whole step budget.
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
	_ = finalText
	snap := r.session.Snapshot()
	step, ok := unresolvedPlanStep(snap.PlanState)
	if !ok {
		return false, nil
	}
	turnID := fmt.Sprintf("turn-%d", turn)
	if turn > 0 && !r.session.IsActiveTurn(turnID) {
		return false, staleTurnError(turnID)
	}
	feedback := planStateInconsistencyFeedback(step)
	if err := r.session.AppendUserMessage(feedback); err != nil {
		return false, err
	}
	return true, NewRetryableCompletionError("react runtime: unresolved plan step", feedback)
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
	// Naming the actual problem matters: an earlier wording called this an
	// inconsistent plan, which models read as a false alarm — the plan is
	// well-formed, it just still has work in it — and then argued with the
	// gate instead of acting on it.
	feedback := "Runtime feedback: you are finishing with unresolved plan work: step " + strconv.Quote(name) + " is " + status
	if blocker := strings.TrimSpace(step.Blocker); blocker != "" {
		feedback += " (blocker: " + blocker + ")"
	}
	return feedback + ". Mark it completed with update_plan if it is done, or report the blocker/failure, instead of claiming successful completion."
}
