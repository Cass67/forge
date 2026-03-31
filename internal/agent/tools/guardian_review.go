package tools

import "strings"

type GuardianDecision string

const (
	GuardianAllow GuardianDecision = "allow"
	GuardianWarn  GuardianDecision = "warn"
	GuardianBlock GuardianDecision = "block"
)

type GuardianReview struct {
	Decision GuardianDecision
	Reason   string
}

// ReviewApprovalAction performs a compact, deterministic pre-approval review.
// It is intentionally conservative: obviously destructive commands are blocked,
// risky mutations without session context are warned, and ordinary edits pass.
func ReviewApprovalAction(transcript string, action Action) GuardianReview {
	summary := strings.ToLower(strings.TrimSpace(action.Summary))
	detail := strings.ToLower(strings.TrimSpace(action.Detail))
	combined := strings.TrimSpace(summary + "\n" + detail)
	hasContext := strings.TrimSpace(transcript) != ""

	switch {
	case strings.Contains(combined, "rm -rf /"), strings.Contains(combined, "git push --force"), strings.Contains(combined, "git reset --hard"):
		return GuardianReview{
			Decision: GuardianBlock,
			Reason:   "action looks destructive and should not be auto-approved",
		}
	case action.Tool == "run_command" && (strings.Contains(combined, "git push") || strings.Contains(combined, "git rebase") || strings.Contains(combined, "git merge")) && !hasContext:
		return GuardianReview{
			Decision: GuardianWarn,
			Reason:   "high-impact command has no compact task context",
		}
	case action.Tool == "run_command" && commandLooksMutating(combined) && !hasContext:
		return GuardianReview{
			Decision: GuardianWarn,
			Reason:   "mutating command is missing task context",
		}
	case action.Tool == "write_file" || action.Tool == "edit_file" || action.Tool == "apply_patch" || action.Tool == "artifact_write":
		if strings.TrimSpace(action.Detail) == "" {
			return GuardianReview{
				Decision: GuardianWarn,
				Reason:   "file mutation has no diff or content detail",
			}
		}
	}

	return GuardianReview{Decision: GuardianAllow}
}

func commandLooksMutating(text string) bool {
	markers := []string{
		"git add", "git commit", "git checkout", "git switch", "git merge", "git rebase", "git push",
		"rm ", "mv ", "cp ", "sed -i", "perl -i", "tee ", "cat >", ">>",
	}
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}
