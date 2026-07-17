package cli

import (
	"fmt"
	"os"
)

type Command struct {
	Name string
	Run  func(args []string)
}

func RequireArg(args []string, usage string) string {
	if len(args) == 0 || args[0] == "" {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}
	return args[0]
}
