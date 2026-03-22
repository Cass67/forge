package agent

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"forge/internal/agent/tools"
)

// YoloApproval returns an ApprovalFunc that approves everything.
func YoloApproval() tools.ApprovalFunc {
	return func(action tools.Action) (bool, error) {
		return true, nil
	}
}

// InteractiveApproval returns an ApprovalFunc that prompts the user.
func InteractiveApproval(in io.Reader, out io.Writer) tools.ApprovalFunc {
	scanner := bufio.NewScanner(in)
	return func(action tools.Action) (bool, error) {
		fmt.Fprintf(out, "\n● %s\n", action.Summary)
		if action.Detail != "" {
			for _, line := range strings.Split(action.Detail, "\n") {
				fmt.Fprintf(out, "  %s\n", line)
			}
		}

		prompt := "apply? [y/n] "
		if action.Tool == "run_command" {
			prompt = "run? [y/n] "
		}
		fmt.Fprint(out, prompt)

		if !scanner.Scan() {
			return false, nil
		}
		answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
		return answer == "y" || answer == "yes", nil
	}
}
