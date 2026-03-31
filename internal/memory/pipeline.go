package memory

import reactruntime "forge/internal/react"

type Pipeline struct {
	MaxRecords int
}

func (p Pipeline) Process(current State, snapshot reactruntime.SessionSnapshot) (State, bool) {
	record, ok := ExtractSessionMemory(snapshot)
	if !ok {
		return current, false
	}
	records := append(append([]Record(nil), current.Records...), record)
	return ConsolidateRecords(records, p.maxRecords()), true
}

func (p Pipeline) maxRecords() int {
	if p.MaxRecords < 1 {
		return 10
	}
	return p.MaxRecords
}
