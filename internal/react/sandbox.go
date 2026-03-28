package react

import "forge/internal/agent/tools"

type SandboxPolicy string

const (
	SandboxReadOnly       SandboxPolicy = "read_only"
	SandboxWorkspaceWrite SandboxPolicy = "workspace_write"
	SandboxDangerFull     SandboxPolicy = "danger_full_access"
)

func (p SandboxPolicy) Allows(action tools.Action) bool {
	switch p {
	case SandboxReadOnly:
		return !actionMutates(action)
	case SandboxWorkspaceWrite, SandboxDangerFull:
		return true
	default:
		return false
	}
}

