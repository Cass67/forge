package copilot

import (
	"fmt"
	"strings"

	"forge/internal/llm"
)

func FormatQuota(q llm.CopilotQuota) string {
	parts := []string{}
	if q.Unlimited {
		parts = append(parts, "unlimited")
	} else {
		if q.Remaining > 0 {
			parts = append(parts, fmt.Sprintf("%d remaining", q.Remaining))
		} else if q.PercentRemaining > 0 {
			parts = append(parts, fmt.Sprintf("%.0f%% remaining", q.PercentRemaining))
		} else {
			parts = append(parts, "quota snapshot available")
		}
		if q.Used > 0 || q.Included > 0 {
			parts = append(parts, fmt.Sprintf("used %d", q.Used))
			if q.Included > 0 {
				parts[len(parts)-1] = fmt.Sprintf("used %d / %d", q.Used, q.Included)
			}
		}
	}
	if q.ResetAt != "" {
		parts = append(parts, "resets "+q.ResetAt)
	}
	return strings.Join(parts, " • ")
}
