package permissions

type Scope string

const (
	ScopeManaged Scope = "managed"
	ScopeUser    Scope = "user"
	ScopeProject Scope = "project"
	ScopeLocal   Scope = "local"
	ScopeSession Scope = "session"
	ScopeCLI     Scope = "cli"
)

func scopeRank(scope Scope) int {
	switch scope {
	case ScopeManaged:
		return 1
	case ScopeUser:
		return 2
	case ScopeProject:
		return 3
	case ScopeLocal:
		return 4
	case ScopeSession:
		return 5
	case ScopeCLI:
		return 6
	default:
		return 0
	}
}
