package harness

import "context"

type WorkspacePolicy interface {
	EnsureExecutionContext(ctx context.Context, turn UserTurn, class Classification, session SessionState) (ProgressMilestone, error)
}

func requiresWorkspacePolicy(class Classification) bool {
	if !class.WantsAction {
		return false
	}
	switch class.Family {
	case FamilyImplement, FamilyDebug, FamilyVerify:
		return true
	default:
		return false
	}
}
