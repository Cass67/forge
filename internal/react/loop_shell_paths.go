package react

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

func mutationPathsFromShellCommand(command string) []string {
	fields := shellLikeFields(command)
	if len(fields) == 0 {
		return nil
	}
	var paths []string
	for i, field := range fields {
		switch {
		case field == ">" || field == ">>":
			if i+1 < len(fields) {
				paths = append(paths, fields[i+1])
			}
		case strings.HasPrefix(field, ">>") && len(field) > 2:
			paths = append(paths, strings.TrimSpace(field[2:]))
		case strings.HasPrefix(field, ">") && len(field) > 1:
			paths = append(paths, strings.TrimSpace(field[1:]))
		}
	}
	if len(fields) >= 3 && fields[0] == "sed" {
		for _, field := range fields[1:] {
			if field == "-i" || strings.HasPrefix(field, "-i") {
				paths = append(paths, fields[len(fields)-1])
				break
			}
		}
	}
	return uniqueStrings(paths)
}

func shellLikeFields(command string) []string {
	var fields []string
	var b strings.Builder
	inSingle := false
	inDouble := false
	escaped := false
	flush := func() {
		if b.Len() == 0 {
			return
		}
		fields = append(fields, b.String())
		b.Reset()
	}
	for _, r := range command {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		switch r {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
				continue
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
				continue
			}
		case ' ', '\t', '\n', '\r':
			if !inSingle && !inDouble {
				flush()
				continue
			}
		}
		b.WriteRune(r)
	}
	flush()
	return fields
}

func outOfScopeSideEffectBlockMessage(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "unknown path"
	}
	return "blocked: refusing workspace mutation outside active side-effect intent allowed paths: " + path
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

func normalizeArtifactToolPath(path string, intent *SideEffectIntent) string {
	path = strings.TrimSpace(strings.Trim(path, "`'\".,:;()[]{}<>"))
	if path == "" || looksLikeWindowsAbsolutePath(path) || strings.Contains(path, "\\") || strings.Contains(path, ":") {
		return ""
	}
	if normalized := normalizeIntentPath(path); normalized != "" {
		return normalized
	}
	if filepath.IsAbs(path) {
		if intent == nil || strings.TrimSpace(intent.WorkspaceRoot) == "" {
			return ""
		}
		root := filepath.Clean(strings.TrimSpace(intent.WorkspaceRoot))
		rel, err := filepath.Rel(root, filepath.Clean(path))
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return ""
		}
		return normalizeIntentPath(filepath.ToSlash(rel))
	}
	return normalizeIntentPath(filepath.ToSlash(filepath.Clean(path)))
}

func patchArtifactTarget(patch string, intent *SideEffectIntent) string {
	for _, path := range pathsFromPatch(patch) {
		path = normalizeArtifactToolPath(path, intent)
		if path == "" {
			continue
		}
		for _, artifact := range intent.ArtifactPaths {
			artifact = normalizeIntentPath(artifact)
			if artifact != "" && artifact == path {
				return artifact
			}
		}
	}
	for _, line := range strings.Split(patch, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"*** Add File:", "*** Update File:", "*** Delete File:"} {
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			path := normalizeArtifactToolPath(strings.TrimSpace(strings.TrimPrefix(line, prefix)), intent)
			if path == "" {
				continue
			}
			for _, artifact := range intent.ArtifactPaths {
				artifact = normalizeIntentPath(artifact)
				if artifact != "" && artifact == path {
					return artifact
				}
			}
		}
	}
	return ""
}

func commandWriteArtifactTarget(command string, intent *SideEffectIntent) string {
	if intent == nil {
		return ""
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	for _, artifact := range append(append([]string(nil), intent.ArtifactPaths...), intent.AllowedPaths...) {
		normalized := normalizeIntentPath(artifact)
		if normalized == "" || !commandLooksLikeWriteToPath(command, commandWritePathRefs(normalized, intent)) {
			continue
		}
		return normalized
	}
	return ""
}

func commandWritePathRefs(path string, intent *SideEffectIntent) []string {
	refs := []string{path, "./" + path}
	if intent != nil && strings.TrimSpace(intent.WorkspaceRoot) != "" {
		refs = append(refs, filepath.Join(strings.TrimSpace(intent.WorkspaceRoot), filepath.FromSlash(path)))
	}
	return uniqueStrings(refs)
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
