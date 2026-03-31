package memory

import (
	"strings"

	reactruntime "forge/internal/react"
)

type Record struct {
	Mode      string
	Objective string
	Summary   string
}

func ExtractSessionMemory(snapshot reactruntime.SessionSnapshot) (Record, bool) {
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
	return Record{
		Mode:      strings.TrimSpace(string(snapshot.Mode)),
		Objective: RedactText(objective),
		Summary:   RedactText(summary),
	}, true
}

func latestSummary(snapshot reactruntime.SessionSnapshot) string {
	for i := len(snapshot.Turns) - 1; i >= 0; i-- {
		if text := strings.TrimSpace(snapshot.Turns[i].FinalResponse); text != "" {
			return text
		}
	}
	for i := len(snapshot.History) - 1; i >= 0; i-- {
		if snapshot.History[i].Role == "assistant" {
			if text := strings.TrimSpace(snapshot.History[i].Content); text != "" {
				return text
			}
		}
	}
	return ""
}
