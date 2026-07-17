package react

import (
	"path/filepath"
	"strings"
)

func shellCommandHasGitMutation(command string) bool {
	for _, segment := range shellCommandSegments(command) {
		if shellSegmentIsGitMutation(segment) {
			return true
		}
	}
	return false
}

func shellCommandSegments(command string) []string {
	var segments []string
	start := 0
	inSingleQuote := false
	inDoubleQuote := false
	inBacktick := false
	for i := 0; i < len(command); i++ {
		if shellCharEscaped(command, i) {
			continue
		}
		switch command[i] {
		case '\'':
			if !inDoubleQuote && !inBacktick {
				inSingleQuote = !inSingleQuote
			}
			continue
		case '"':
			if !inSingleQuote && !inBacktick {
				inDoubleQuote = !inDoubleQuote
			}
			continue
		case '`':
			if !inSingleQuote {
				inBacktick = !inBacktick
			}
			continue
		}
		if inSingleQuote || inDoubleQuote || inBacktick {
			continue
		}
		skip := 1
		switch command[i] {
		case ';', '\n', '\r':
		case '&':
			if i+1 < len(command) && command[i+1] == '&' {
				skip = 2
			}
		case '|':
			if i+1 < len(command) && command[i+1] == '|' {
				skip = 2
			}
		default:
			continue
		}
		if segment := strings.TrimSpace(command[start:i]); segment != "" {
			segments = append(segments, segment)
		}
		i += skip - 1
		start = i + 1
	}
	if segment := strings.TrimSpace(command[start:]); segment != "" {
		segments = append(segments, segment)
	}
	return segments
}

func shellSegmentIsGitMutation(segment string) bool {
	if shellSegmentCommandSubstitutionsHaveGitMutation(segment) {
		return true
	}
	tokens := shellFields(strings.TrimSpace(segment))
	if shellTokensInvokeNestedGitMutation(tokens) {
		return true
	}
	subcommand, args, ok := gitSubcommandFromShellTokens(tokens)
	if !ok {
		return false
	}
	return gitShellSubcommandMayMutate(subcommand, args)
}

func shellSegmentCommandSubstitutionsHaveGitMutation(segment string) bool {
	inSingleQuote := false
	for i := 0; i < len(segment); i++ {
		if segment[i] == '\'' && !shellCharEscaped(segment, i) {
			inSingleQuote = !inSingleQuote
			continue
		}
		if inSingleQuote || shellCharEscaped(segment, i) {
			continue
		}
		if segment[i] == '$' && i+1 < len(segment) && segment[i+1] == '(' {
			end := shellCommandSubstitutionEnd(segment, i+2)
			if end < 0 {
				continue
			}
			if shellCommandHasGitMutation(segment[i+2 : end]) {
				return true
			}
			i = end
			continue
		}
		if segment[i] == '`' {
			end := shellBacktickSubstitutionEnd(segment, i+1)
			if end < 0 {
				continue
			}
			if shellCommandHasGitMutation(segment[i+1 : end]) {
				return true
			}
			i = end
		}
	}
	return false
}

func shellCharEscaped(text string, index int) bool {
	backslashes := 0
	for i := index - 1; i >= 0 && text[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 1
}

func shellCommandSubstitutionEnd(text string, start int) int {
	depth := 1
	for i := start; i < len(text); i++ {
		if shellCharEscaped(text, i) {
			continue
		}
		switch text[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func shellBacktickSubstitutionEnd(text string, start int) int {
	for i := start; i < len(text); i++ {
		if text[i] == '`' && !shellCharEscaped(text, i) {
			return i
		}
	}
	return -1
}

func shellTokensInvokeNestedGitMutation(tokens []string) bool {
	for i, raw := range tokens {
		if shellTokenStartsCommandSubstitution(raw) {
			nested := strings.Join(cleanShellNestedCommandArgs(tokens[i:]), " ")
			if shellCommandHasGitMutation(nested) {
				return true
			}
		}
		name := filepath.Base(cleanShellGitToken(raw))
		if name == "eval" {
			nested := strings.Join(cleanShellGitArgs(tokens[i+1:]), " ")
			if shellCommandHasGitMutation(nested) {
				return true
			}
			continue
		}
		if !isShellInterpreter(name) {
			continue
		}
		commandIndex := shellCommandStringOptionIndex(tokens[i+1:])
		if commandIndex < 0 || i+commandIndex+2 >= len(tokens) {
			continue
		}
		nested := strings.Join(cleanShellGitArgs(tokens[i+commandIndex+2:]), " ")
		if shellCommandHasGitMutation(nested) {
			return true
		}
	}
	return false
}

func shellTokenStartsCommandSubstitution(token string) bool {
	token = strings.TrimSpace(token)
	return strings.HasPrefix(token, "$(") || strings.HasPrefix(token, "`")
}

func cleanShellNestedCommandArgs(tokens []string) []string {
	args := make([]string, 0, len(tokens))
	for _, token := range tokens {
		cleaned := cleanShellGitToken(strings.TrimPrefix(strings.TrimPrefix(token, "$("), "`"))
		cleaned = strings.TrimSuffix(strings.TrimSuffix(cleaned, ")"), "`")
		cleaned = cleanShellGitToken(cleaned)
		if cleaned != "" {
			args = append(args, cleaned)
		}
	}
	return args
}

func isShellInterpreter(token string) bool {
	switch filepath.Base(token) {
	case "sh", "bash", "zsh", "dash", "ksh":
		return true
	default:
		return false
	}
}

func shellCommandStringOptionIndex(tokens []string) int {
	for i, raw := range tokens {
		token := cleanShellGitToken(raw)
		if !strings.HasPrefix(token, "-") {
			continue
		}
		if token == "-c" || strings.Contains(strings.TrimPrefix(token, "-"), "c") {
			return i
		}
	}
	return -1
}

func gitShellSubcommandMayMutate(subcommand string, args []string) bool {
	switch subcommand {
	case "status", "diff", "log", "show", "rev-parse", "rev-list", "ls-files", "ls-tree", "ls-remote", "describe", "grep", "blame", "cat-file", "diff-tree", "diff-index", "diff-files", "for-each-ref", "merge-base", "name-rev", "shortlog", "whatchanged":
		return false
	case "branch":
		return gitBranchShellArgsMayMutate(args)
	case "config":
		return gitConfigShellArgsMayMutate(args)
	case "remote":
		return gitRemoteShellArgsMayMutate(args)
	case "tag":
		return gitTagShellArgsMayMutate(args)
	default:
		return true
	}
}

func gitBranchShellArgsMayMutate(args []string) bool {
	if len(args) == 0 {
		return false
	}
	for _, arg := range args {
		switch {
		case arg == "--show-current" || arg == "--list" || arg == "--all" || arg == "--remotes" || arg == "-a" || arg == "-r" || arg == "-v" || arg == "-vv":
			continue
		case strings.HasPrefix(arg, "--contains") || strings.HasPrefix(arg, "--no-contains") || strings.HasPrefix(arg, "--merged") || strings.HasPrefix(arg, "--no-merged") || strings.HasPrefix(arg, "--points-at") || strings.HasPrefix(arg, "--format=") || strings.HasPrefix(arg, "--sort=") || strings.HasPrefix(arg, "--column") || strings.HasPrefix(arg, "--no-column"):
			continue
		default:
			return true
		}
	}
	return false
}

func gitConfigShellArgsMayMutate(args []string) bool {
	if len(args) == 0 {
		return false
	}
	readMode := false
	for _, arg := range args {
		if arg == "--get" || arg == "--get-all" || arg == "--get-regexp" || arg == "--list" || arg == "-l" || strings.HasPrefix(arg, "--get-") {
			readMode = true
			continue
		}
		if arg == "--show-origin" || arg == "--show-scope" || arg == "--name-only" || arg == "--null" {
			continue
		}
		if strings.HasPrefix(arg, "--") {
			return true
		}
		return !readMode && len(args) > 1
	}
	return false
}

func gitRemoteShellArgsMayMutate(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "-v", "--verbose", "get-url", "show":
		return false
	default:
		return true
	}
}

func gitTagShellArgsMayMutate(args []string) bool {
	if len(args) == 0 {
		return false
	}
	for _, arg := range args {
		if arg == "-l" || arg == "--list" || arg == "-n" || strings.HasPrefix(arg, "-n") || strings.HasPrefix(arg, "--sort=") || strings.HasPrefix(arg, "--format=") || strings.HasPrefix(arg, "--points-at") || strings.HasPrefix(arg, "--contains") || strings.HasPrefix(arg, "--no-contains") || strings.HasPrefix(arg, "--merged") || strings.HasPrefix(arg, "--no-merged") {
			continue
		}
		return true
	}
	return false
}

func gitSubcommandFromShellTokens(tokens []string) (string, []string, bool) {
	for i, raw := range tokens {
		if filepath.Base(cleanShellGitToken(raw)) != "git" || !allowedShellGitPrefix(tokens[:i]) {
			continue
		}
		for j := i + 1; j < len(tokens); {
			token := cleanShellGitToken(tokens[j])
			if token == "" {
				j++
				continue
			}
			if strings.HasPrefix(token, "-") {
				if gitGlobalOptionTakesValue(token) && j+1 < len(tokens) {
					j += 2
				} else {
					j++
				}
				continue
			}
			return token, cleanShellGitArgs(tokens[j+1:]), true
		}
	}
	return "", nil, false
}

func allowedShellGitPrefix(tokens []string) bool {
	for i := 0; i < len(tokens); {
		raw := tokens[i]
		token := cleanShellGitToken(raw)
		name := filepath.Base(token)
		if token == "" || token == "then" || token == "do" || token == "if" {
			i++
			continue
		}
		if strings.Contains(token, "=") && !strings.HasPrefix(token, "-") {
			i++
			continue
		}
		switch name {
		case "command":
			i++
		case "exec":
			i = skipShellWrapperPrefixArgs(tokens, i+1, execWrapperOptionTakesValue)
		case "env":
			i = skipShellWrapperPrefixArgs(tokens, i+1, envWrapperOptionTakesValue)
		case "sudo":
			i = skipShellWrapperPrefixArgs(tokens, i+1, sudoWrapperOptionTakesValue)
		case "time":
			i = skipShellWrapperPrefixArgs(tokens, i+1, timeWrapperOptionTakesValue)
		default:
			return false
		}
	}
	return true
}

func skipShellWrapperPrefixArgs(tokens []string, start int, optionTakesValue func(string) bool) int {
	for i := start; i < len(tokens); {
		token := cleanShellGitToken(tokens[i])
		if token == "" || (strings.Contains(token, "=") && !strings.HasPrefix(token, "-")) {
			i++
			continue
		}
		if !strings.HasPrefix(token, "-") {
			return i
		}
		if optionTakesValue(token) && i+1 < len(tokens) {
			i += 2
		} else {
			i++
		}
	}
	return len(tokens)
}

func envWrapperOptionTakesValue(option string) bool {
	if strings.Contains(option, "=") {
		return false
	}
	switch option {
	case "-u", "--unset", "-C", "--chdir", "-S", "--split-string", "--block-signal", "--default-signal", "--ignore-signal":
		return true
	default:
		return false
	}
}

func sudoWrapperOptionTakesValue(option string) bool {
	if strings.Contains(option, "=") {
		return false
	}
	switch option {
	case "-A", "-a", "-b", "-C", "-c", "-D", "-g", "-h", "-p", "-R", "-r", "-T", "-t", "-U", "-u":
		return true
	default:
		return false
	}
}

func timeWrapperOptionTakesValue(option string) bool {
	if strings.Contains(option, "=") {
		return false
	}
	switch option {
	case "-f", "--format", "-o", "--output":
		return true
	default:
		return false
	}
}

func execWrapperOptionTakesValue(option string) bool {
	if strings.Contains(option, "=") {
		return false
	}
	switch option {
	case "-a":
		return true
	default:
		return false
	}
}

func cleanShellGitArgs(tokens []string) []string {
	args := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if cleaned := cleanShellGitToken(token); cleaned != "" {
			args = append(args, cleaned)
		}
	}
	return args
}

func cleanShellGitToken(token string) string {
	return strings.Trim(strings.TrimSpace(token), "(){}[]'\"")
}

func gitGlobalOptionTakesValue(option string) bool {
	if strings.Contains(option, "=") {
		return false
	}
	switch option {
	case "-C", "-c", "--git-dir", "--work-tree", "--namespace":
		return true
	default:
		return false
	}
}
