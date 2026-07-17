package react

import (
	"context"
	"fmt"
	"strings"
)

func (r *Runner) failedAgentFallbackErrorText() string {
	if r == nil || r.session == nil {
		return ""
	}
	snap := r.session.Snapshot()
	if len(outstandingSpawnedAgents(snap)) > 0 {
		return ""
	}
	var parts []string
	for _, agent := range snap.AgentTasks {
		if agent.Status != AgentStatusFailed {
			continue
		}
		label := strings.TrimSpace(agent.ID)
		if role := strings.TrimSpace(agent.Role); role != "" {
			label = strings.TrimSpace(label + " (" + role + ")")
		}
		if label == "" {
			label = "child agent"
		}
		detail := strings.TrimSpace(firstNonEmpty(agent.Error, agent.Result))
		if detail == "" {
			detail = "failed without details"
		}
		parts = append(parts, label+": "+detail)
	}
	if len(parts) == 0 {
		return ""
	}
	return "react runtime: child agent failed before parent could complete: " + strings.Join(parts, "; ")
}

func (r *Runner) activeAgentFallbackText() string {
	if r == nil || r.session == nil {
		return ""
	}
	agents := outstandingSpawnedAgents(r.session.Snapshot())
	if len(agents) == 0 {
		return ""
	}
	parts := make([]string, 0, len(agents))
	for _, agent := range agents {
		id := strings.TrimSpace(agent.ID)
		if id == "" {
			continue
		}
		label := id
		if role := strings.TrimSpace(agent.Role); role != "" {
			label += " (" + role + ")"
		}
		status := strings.TrimSpace(string(agent.Status))
		if status == "" {
			status = string(AgentStatusRunning)
		}
		parts = append(parts, label+" is "+status)
	}
	if len(parts) == 0 {
		return ""
	}
	return "Child agent work is still in progress: " + strings.Join(parts, "; ") + ". Ask for status or tell me to continue waiting."
}

func (r *Runner) tryCompletedAgentResultFallbackAfterError(ctx context.Context, turn int) (bool, error) {
	return r.tryCompletedAgentResultFallbackWithOptions(ctx, turn, true)
}

func (r *Runner) tryCompletedAgentResultFallbackWithOptions(ctx context.Context, turn int, allowBoundedMultiAgent bool) (bool, error) {
	if r == nil || r.session == nil {
		return false, nil
	}
	snap := r.session.Snapshot()
	_ = allowBoundedMultiAgent
	content := completedAgentResultFallbackContent(snap)
	if content == "" {
		return false, nil
	}
	if err := r.ensureFallbackTurnCurrent(ctx, turn); err != nil {
		return false, err
	}
	fallback := "Parent model connection failed while composing the final response. Showing completed child-agent result instead.\n\n" + content
	r.pendingRetryPrompt = ""
	if ok, err := r.validateFinalCompletion(ctx, turn, fallback, false); !ok || err != nil {
		if err != nil && r.hasTurnSnapshot(turn) {
			r.recordModelViolation("completed-agent fallback blocked", fallbackBlockDetail(err))
		}
		return false, err
	}
	if err := r.appendFinalAssistantMessageAndCompleteTurn(ctx, turn, fallback, nil); err != nil {
		return true, err
	}
	r.notifyTurnComplete()
	if r.renderer != nil {
		r.renderer.AgentText(fallback)
	}
	return true, nil
}

func (r *Runner) ensureFallbackTurnCurrent(ctx context.Context, turn int) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if r == nil || r.session == nil {
		return nil
	}
	turnID := fmt.Sprintf("turn-%d", turn)
	if active, ok := r.session.ActiveTurnSnapshot(); ok {
		if active.ID != turnID {
			return staleTurnError(turnID)
		}
		return nil
	}
	return staleTurnError(turnID)
}

func fallbackBlockDetail(err error) string {
	if err == nil {
		return "unresolved completion gate"
	}
	return err.Error()
}

func completedAgentResultFallbackContent(snap SessionSnapshot) string {
	resultTurn := completedAgentResultTurn(snap)
	if sameTurnAgentStillOutstanding(snap.AgentTasks, resultTurn) {
		return ""
	}
	var parts []string
	for _, task := range snap.AgentTasks {
		if task.Status != AgentStatusCompleted || task.ParentTurn != resultTurn {
			continue
		}
		result := strings.TrimSpace(task.Result)
		if result == "" {
			continue
		}
		label := strings.TrimSpace(task.Role)
		if label == "" {
			label = strings.TrimSpace(task.ID)
		}
		if label == "" {
			label = "child agent"
		}
		parts = append(parts, "## "+label+"\n"+result)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n")
}

func completedAgentResultTurn(snap SessionSnapshot) int {
	if agentTaskCompletedResultForTurn(snap.AgentTasks, snap.Turn) || sameTurnAgentStillOutstanding(snap.AgentTasks, snap.Turn) {
		return snap.Turn
	}
	if !pendingDelegationWriteAction(snap) {
		return snap.Turn
	}
	if snap.PendingDelegationAction != nil {
		sourceAgent := strings.TrimSpace(snap.PendingDelegationAction.SourceAgent)
		if sourceAgent != "" {
			for _, task := range snap.AgentTasks {
				if task.ID == sourceAgent && task.ParentTurn != 0 {
					return task.ParentTurn
				}
			}
		}
	}
	latest := 0
	for _, task := range snap.AgentTasks {
		if task.Status != AgentStatusCompleted || strings.TrimSpace(task.Result) == "" || task.ParentTurn == 0 {
			continue
		}
		if task.ParentTurn > latest {
			latest = task.ParentTurn
		}
	}
	if latest != 0 {
		return latest
	}
	return snap.Turn
}

func agentTaskCompletedResultForTurn(tasks []AgentTaskState, turn int) bool {
	for _, task := range tasks {
		if task.ParentTurn == turn && task.Status == AgentStatusCompleted && strings.TrimSpace(task.Result) != "" {
			return true
		}
	}
	return false
}

func sameTurnAgentStillOutstanding(tasks []AgentTaskState, turn int) bool {
	for _, task := range tasks {
		if task.ParentTurn == turn && agentTaskFallbackOutstanding(task.Status) {
			return true
		}
	}
	return false
}

func agentTaskFallbackOutstanding(status AgentStatus) bool {
	if status == AgentStatusPending {
		return true
	}
	return agentStillOutstanding(status)
}
