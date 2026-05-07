package permissions

const (
	DefaultMaxConsecutiveDenials = 3
	DefaultMaxTotalDenials       = 20
)

type DenialTracker struct {
	maxConsecutive int
	maxTotal       int
	consecutive    int
	total          int
}

func NewDenialTracker(maxConsecutive, maxTotal int) *DenialTracker {
	if maxConsecutive < 1 {
		maxConsecutive = DefaultMaxConsecutiveDenials
	}
	if maxTotal < 1 {
		maxTotal = DefaultMaxTotalDenials
	}
	return &DenialTracker{maxConsecutive: maxConsecutive, maxTotal: maxTotal}
}

func (t *DenialTracker) RecordDenied() {
	if t == nil {
		return
	}
	t.consecutive++
	t.total++
}

func (t *DenialTracker) RecordAllowed() {
	if t == nil {
		return
	}
	t.consecutive = 0
}

func (t *DenialTracker) ShouldFallback() bool {
	if t == nil {
		return false
	}
	return t.consecutive >= t.maxConsecutive || t.total >= t.maxTotal
}

func (t *DenialTracker) Reset() {
	if t == nil {
		return
	}
	t.consecutive = 0
	t.total = 0
}
