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
		// drop oldest unpinned first; pinned records never rotate out
		kept := make([]Record, 0, maxRecords)
		drop := len(bounded) - maxRecords
		for _, record := range bounded {
			if drop > 0 && !record.Pinned {
				drop--
				continue
			}
			kept = append(kept, record)
		}
		bounded = kept
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
		record = normalizeRecord(record)
		if record.Objective == "" && record.Summary == "" {
			continue
		}
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
		lines = append(lines, "- "+clipText(summary, maxSummaryLine-2))
	}
	return strings.Join(lines, "\n")
}

func normalizeRecord(record Record) Record {
	record.Mode = normalizeMemoryText(record.Mode)
	record.Objective = clipText(RedactText(normalizeMemoryText(record.Objective)), maxObjectiveLen)
	record.Summary = clipText(RedactText(normalizeMemoryText(record.Summary)), maxRecordLen)
	return record
}
