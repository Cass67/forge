package cli

import (
	"fmt"
	"os"
)

type Command struct {
	Name string
	Run  func(args []string)
}

func Dispatch(args []string, commands map[string]Command, fallback func()) {
	if len(args) == 0 {
		fallback()
		return
	}
	cmd, ok := commands[args[0]]
	if !ok {
		fallback()
		return
	}
	cmd.Run(args[1:])
}

func RequireArg(args []string, usage string) string {
	if len(args) == 0 || args[0] == "" {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}
	return args[0]
}
