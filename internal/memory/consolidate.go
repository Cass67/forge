package memory

import "strings"

type State struct {
	Records []Record
	Summary string
}

func ConsolidateRecords(records []Record, maxRecords int) State {
	if maxRecords < 1 {
		maxRecords = 1
	}
	bounded := dedupeRecords(records)
	if len(bounded) > maxRecords {
		bounded = append([]Record(nil), bounded[len(bounded)-maxRecords:]...)
	}
	return State{
		Records: bounded,
		Summary: summarizeRecords(bounded),
	}
}

func dedupeRecords(records []Record) []Record {
	seen := make(map[string]struct{}, len(records))
	out := make([]Record, 0, len(records))
	for _, record := range records {
		key := record.Mode + "\x00" + record.Objective + "\x00" + record.Summary
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, record)
	}
	return out
}

func summarizeRecords(records []Record) string {
	lines := make([]string, 0, len(records))
	for _, record := range records {
		summary := strings.TrimSpace(record.Summary)
		if summary == "" {
			summary = strings.TrimSpace(record.Objective)
		}
		if summary == "" {
			continue
		}
		lines = append(lines, "- "+summary)
	}
	return strings.Join(lines, "\n")
}
