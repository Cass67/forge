package memory

import (
	"strings"

	reactruntime "forge/internal/react"
)

type Record struct {
	Mode      string
	Objective string
	Summary   string
	Pinned    bool
}

const (
	maxObjectiveLen = 160
	maxRecordLen    = 220
	maxSummaryLine  = 160
)

func ExtractSessionMemory(snapshot reactruntime.SessionSnapshot) (Record, bool) {
	if snapshot.HookOutput.Block != nil {
		return Record{}, false
	}
	objective := ""
	if snapshot.TaskState != nil {
		objective = strings.TrimSpace(snapshot.TaskState.Objective)
	}
	if objective == "" {
		objective = strings.TrimSpace(snapshot.LastInput)
	}
	summary := latestSummary(snapshot)
	if objective == "" && summary == "" {
		return Record{}, false
	}
	objective = clipText(RedactText(normalizeMemoryText(objective)), maxObjectiveLen)
	summary = clipText(RedactText(normalizeMemoryText(summary)), maxRecordLen)
	if summary == "" {
		return Record{}, false
	}
	return Record{
		Mode:      strings.TrimSpace(string(snapshot.Mode)),
		Objective: objective,
		Summary:   summary,
	}, true
}

func latestSummary(snapshot reactruntime.SessionSnapshot) string {
	if len(snapshot.Turns) > 0 {
		turn := snapshot.Turns[len(snapshot.Turns)-1]
		if strings.TrimSpace(turn.Error) != "" {
			return ""
		}
		if text := normalizeMemoryText(turn.FinalResponse); text != "" {
			return text
		}
		return ""
	}
	for i := len(snapshot.History) - 1; i >= 0; i-- {
		if snapshot.History[i].Role == "assistant" {
			if text := normalizeMemoryText(snapshot.History[i].Content); text != "" {
				return text
			}
		}
	}
	return ""
}

func normalizeMemoryText(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
}

func clipText(text string, limit int) string {
	if limit < 1 {
		return ""
	}
	if len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	return strings.TrimSpace(text[:limit-3]) + "..."
}
