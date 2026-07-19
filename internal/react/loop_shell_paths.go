package react

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// clearBlockingHandoffsAfterWrite clears blocking child-agent handoffs once the
// parent has written to a path one of their remaining actions named.
func (r *Runner) clearBlockingHandoffsAfterWrite(toolName string, args map[string]any) {
	if r == nil || r.session == nil {
		return
	}
	toolName = strings.TrimSpace(toolName)
	if toolName != "write_file" && toolName != "edit_file" && toolName != "apply_patch" {
		return
	}
	writtenPaths := checkpointScopePaths(toolName, args)
	if len(writtenPaths) == 0 {
		return
	}
	for _, task := range blockingAgentHandoffs(r.session.Snapshot()) {
		for _, action := range task.Handoff.RemainingActions {
			target := normalizeIntentPath(action.TargetPath)
			if target == "" {
				continue
			}
			for _, written := range writtenPaths {
				if normalizeIntentPath(written) == target {
					r.session.ClearBlockingAgentHandoffs()
					return
				}
			}
		}
	}
}

func checkpointScopePaths(toolName string, args map[string]any) []string {
	switch toolName {
	case "write_file", "edit_file", "artifact_write":
		if path, _ := args["path"].(string); strings.TrimSpace(path) != "" {
			return []string{strings.TrimSpace(path)}
		}
	case "apply_patch":
		patch, _ := args["patch"].(string)
		return pathsFromPatch(patch)
	}
	return nil
}

func commandLooksLikeWriteToPath(command string, paths []string) bool {
	pathRefs := nonEmptyShellPathRefs(paths)
	for _, segment := range shellCommandSegments(command) {
		for _, nested := range nestedShellCommandsFromSegment(segment) {
			if commandLooksLikeWriteToPath(nested, pathRefs) {
				return true
			}
		}
		if shellSegmentRedirectsToPath(segment, pathRefs) || commandSegmentWritesToDestination(segment, pathRefs) {
			return true
		}
	}
	return false
}

func nestedShellCommandsFromSegment(segment string) []string {
	tokens := shellFields(segment)
	commands := make([]string, 0, 1)
	for i, token := range tokens {
		if !isShellInterpreter(token) {
			continue
		}
		commandIndex := shellCommandStringOptionIndex(tokens[i+1:])
		if commandIndex < 0 || i+commandIndex+2 >= len(tokens) {
			continue
		}
		nested := expandShellPositionalParameters(tokens[i+commandIndex+2], tokens[i+commandIndex+3:])
		if nested = strings.TrimSpace(nested); nested != "" {
			commands = append(commands, nested)
		}
	}
	return commands
}

func expandShellPositionalParameters(command string, args []string) string {
	for i := 1; i < len(args); i++ {
		value := args[i]
		placeholder := fmt.Sprintf("$%d", i)
		command = strings.ReplaceAll(command, placeholder, value)
		command = strings.ReplaceAll(command, "${"+strconv.Itoa(i)+"}", value)
	}
	return command
}

func nonEmptyShellPathRefs(paths []string) []string {
	refs := make([]string, 0, len(paths))
	for _, path := range paths {
		if path = strings.TrimSpace(path); path != "" {
			refs = append(refs, path)
		}
	}
	return uniqueStrings(refs)
}

func shellSegmentRedirectsToPath(segment string, refs []string) bool {
	inSingleQuote := false
	inDoubleQuote := false
	for i := 0; i < len(segment); i++ {
		if shellCharEscaped(segment, i) {
			continue
		}
		switch segment[i] {
		case '\'':
			if !inDoubleQuote {
				inSingleQuote = !inSingleQuote
			}
			continue
		case '"':
			if !inSingleQuote {
				inDoubleQuote = !inDoubleQuote
			}
			continue
		case '>':
			if inSingleQuote || inDoubleQuote {
				continue
			}
			if i+1 < len(segment) && segment[i+1] == '>' {
				i++
			}
			j := i + 1
			for j < len(segment) && (segment[j] == ' ' || segment[j] == '\t') {
				j++
			}
			target, _ := nextShellToken(segment, j)
			if shellTokenMatchesPath(target, refs) {
				return true
			}
		}
	}
	return false
}

func commandSegmentWritesToDestination(segment string, refs []string) bool {
	tokens := shellFields(segment)
	if len(tokens) < 2 {
		return false
	}
	cmd := filepath.Base(tokens[0])
	switch cmd {
	case "cp", "mv", "install", "rsync":
		if len(tokens) < 3 {
			return false
		}
		if cmd == "rsync" && commandTokensIncludeRsyncDryRun(tokens[1:]) {
			return false
		}
		last := strings.TrimRight(tokens[len(tokens)-1], ";")
		return shellTokenMatchesPath(last, refs) || commandDirectoryDestinationMatchesPath(tokens, refs)
	case "tee", "touch":
		skipNext := false
		for _, token := range tokens[1:] {
			if skipNext {
				skipNext = false
				continue
			}
			if token == "<" {
				skipNext = true
				continue
			}
			if strings.HasPrefix(token, "<") {
				continue
			}
			if strings.HasPrefix(token, "-") {
				continue
			}
			if shellTokenMatchesPath(token, refs) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func commandDirectoryDestinationMatchesPath(tokens []string, refs []string) bool {
	if len(tokens) < 3 {
		return false
	}
	destination := strings.TrimSuffix(strings.Trim(strings.TrimRight(tokens[len(tokens)-1], ";"), "'\""), "/")
	for _, ref := range refs {
		ref = strings.Trim(strings.TrimSpace(ref), "'\"")
		if ref == "" {
			continue
		}
		for _, sourceToken := range tokens[1 : len(tokens)-1] {
			source := strings.Trim(sourceToken, "'\"")
			if source == "" || strings.HasPrefix(source, "-") || shellTokenMatchesPath(source, refs) || filepath.Base(ref) != filepath.Base(source) {
				continue
			}
			dir := strings.TrimSuffix(filepath.ToSlash(filepath.Dir(ref)), "/")
			if destination == dir {
				return true
			}
		}
	}
	return false
}

func commandTokensIncludeRsyncDryRun(tokens []string) bool {
	for _, token := range tokens {
		if token == "--dry-run" || token == "-n" {
			return true
		}
		if strings.HasPrefix(token, "-") && !strings.HasPrefix(token, "--") && strings.Contains(token, "n") {
			return true
		}
	}
	return false
}

func shellFields(segment string) []string {
	var fields []string
	var current strings.Builder
	inSingleQuote := false
	inDoubleQuote := false
	flush := func() {
		if current.Len() > 0 {
			fields = append(fields, current.String())
			current.Reset()
		}
	}
	for i := 0; i < len(segment); i++ {
		if segment[i] == '\\' && !inSingleQuote && i+1 < len(segment) {
			current.WriteByte(segment[i+1])
			i++
			continue
		}
		switch segment[i] {
		case '\'':
			if !inDoubleQuote {
				inSingleQuote = !inSingleQuote
				continue
			}
		case '"':
			if !inSingleQuote {
				inDoubleQuote = !inDoubleQuote
				continue
			}
		case ' ', '\t', '\n', '\r':
			if !inSingleQuote && !inDoubleQuote {
				flush()
				continue
			}
		}
		current.WriteByte(segment[i])
	}
	flush()
	return fields
}

func nextShellToken(segment string, start int) (string, int) {
	fields := shellFields(segment[start:])
	if len(fields) == 0 {
		return "", len(segment)
	}
	return fields[0], start + len(fields[0])
}

func shellTokenMatchesPath(token string, refs []string) bool {
	token = strings.Trim(strings.TrimSpace(token), "'\"")
	normalizedToken := normalizeIntentPath(token)
	for _, ref := range refs {
		ref = strings.Trim(strings.TrimSpace(ref), "'\"")
		if token == ref {
			return true
		}
		if normalizedToken != "" && normalizedToken == normalizeIntentPath(ref) {
			return true
		}
	}
	return false
}

func pathsFromPatch(patch string) []string {
	var paths []string
	for _, line := range strings.Split(patch, "\n") {
		for _, prefix := range []string{"+++ b/", "--- a/"} {
			if strings.HasPrefix(line, prefix) {
				path := strings.TrimSpace(strings.TrimPrefix(line, prefix))
				if path != "/dev/null" {
					paths = append(paths, path)
				}
			}
		}
		for _, prefix := range []string{"*** Add File:", "*** Update File:", "*** Delete File:", "*** Move to:"} {
			if strings.HasPrefix(line, prefix) {
				path := strings.TrimSpace(strings.TrimPrefix(line, prefix))
				if path != "" && path != "/dev/null" {
					paths = append(paths, path)
				}
			}
		}
	}
	return uniqueStrings(paths)
}
