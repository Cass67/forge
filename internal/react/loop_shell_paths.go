package react

import (
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
