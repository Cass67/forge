package tui

import "strings"

func normalizePlanStepStatus(status string) planStepStatus {
	switch strings.ToLower(status) {
	case planStepCompleted, "done", "finished", "complete":
		return planStepCompleted
	case planStepInProgress, "active", "running", "doing":
		return planStepInProgress
	case planStepBlocked, "waiting", "stuck":
		return planStepBlocked
	case planStepFailed, "error", "errored":
		return planStepFailed
	default:
		return planStepPending
	}
}
