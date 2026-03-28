package react

func CompactSessionHistory(session *Session, keep int) bool {
	if session == nil {
		return false
	}
	return session.compact(keep)
}
