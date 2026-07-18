package tools

import "testing"

func TestReviewApprovalActionCommands(t *testing.T) {
	cases := []struct {
		name       string
		transcript string
		command    string
		want       GuardianDecision
	}{
		{"rm root", "task: cleanup", "rm -rf /", GuardianBlock},
		{"rm root no-preserve", "ctx", "rm -rf --no-preserve-root /", GuardianBlock},
		{"rm home", "ctx", "rm -rf ~", GuardianBlock},
		{"rm tmp", "ctx", "sudo rm -rf /tmp", GuardianBlock},
		{"rm chained", "ctx", "cd /x && rm -rf /", GuardianBlock},
		{"force push", "ctx", "git push --force origin main", GuardianBlock},
		{"force push short", "ctx", "git push -f", GuardianBlock},
		{"force with lease", "ctx", "git push --force-with-lease", GuardianBlock},
		{"push delete", "ctx", "git push origin --delete feature", GuardianBlock},
		{"reset hard", "ctx", "git reset --hard HEAD~3", GuardianBlock},
		{"git clean", "ctx", "git clean -fdx", GuardianBlock},
		{"docker prune forced", "ctx", "docker system prune -af", GuardianBlock},
		{"truncate zero", "ctx", "truncate -s 0 app.log", GuardianBlock},
		{"chmod 777", "ctx", "chmod -R 777 /srv", GuardianBlock},
		{"dd to device", "ctx", "dd if=img.iso of=/dev/sda", GuardianBlock},

		// False positives the old substring matcher would have blocked.
		{"rm targeted abs path", "ctx", "rm -rf /var/tmp/build-cache", GuardianAllow},
		{"dangerous string quoted", "ctx", "echo 'never run rm -rf /'", GuardianAllow},
		{"dangerous string in heredoc", "ctx", "cat > patch.diff << 'PATCH'\n+git push --force\n+rm -rf /\nPATCH", GuardianAllow},
		{"push follow-tags", "ctx", "git push --follow-tags", GuardianAllow},
		{"redirect to dev null", "ctx", "make test > /dev/null 2>&1", GuardianAllow},

		{"merge no context", "", "git merge feature/runtime", GuardianWarn},
		{"mutating no context", "", "mv a.go b.go", GuardianWarn},
		{"redirect no context", "", "echo hi >> notes.md", GuardianWarn},
		{"mutating with context", "task: rename", "mv a.go b.go", GuardianAllow},
		{"read only", "", "ls -la", GuardianAllow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			review := ReviewApprovalAction(tc.transcript, Action{
				Tool:    "run_command",
				Summary: tc.command,
				Detail:  tc.command,
			})
			if review.Decision != tc.want {
				t.Fatalf("decision = %q (%s), want %q", review.Decision, review.Reason, tc.want)
			}
		})
	}
}

func TestReviewApprovalActionFileToolsNeverTripCommandRules(t *testing.T) {
	review := ReviewApprovalAction("ctx", Action{
		Tool:    "apply_patch",
		Summary: "edit guardian_review.go",
		Detail:  "+case strings.Contains(combined, \"rm -rf /\"), strings.Contains(combined, \"git push --force\"):",
	})
	if review.Decision != GuardianAllow {
		t.Fatalf("decision = %q, want allow", review.Decision)
	}
}

func TestReviewApprovalActionWarnsOnEmptyFileMutation(t *testing.T) {
	review := ReviewApprovalAction("ctx", Action{Tool: "write_file", Summary: "write x.go"})
	if review.Decision != GuardianWarn {
		t.Fatalf("decision = %q, want warn", review.Decision)
	}
}

func TestReviewApprovalActionAllowsEditWithDetail(t *testing.T) {
	review := ReviewApprovalAction("task: patch runtime flow", Action{
		Tool:    "edit_file",
		Summary: "edit internal/runtime/chat.go",
		Detail:  "--- a/internal/runtime/chat.go",
	})
	if review.Decision != GuardianAllow {
		t.Fatalf("decision = %q, want allow", review.Decision)
	}
}
