package react

import (
	"path/filepath"
	"strings"
)

func normalizeIntentPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, "`'\".,:;()[]{}<>")
	path = strings.TrimPrefix(path, "@")
	if path == "" || filepath.IsAbs(path) || looksLikeWindowsAbsolutePath(path) || strings.Contains(path, "\\") || strings.Contains(path, ":") {
		return ""
	}
	for _, segment := range strings.Split(filepath.ToSlash(path), "/") {
		if segment == ".." {
			return ""
		}
	}
	cleaned := filepath.Clean(path)
	if cleaned == "." || strings.HasPrefix(cleaned, "..") || strings.Contains(cleaned, string(filepath.Separator)+".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(cleaned)
}

func looksLikeWindowsAbsolutePath(path string) bool {
	if len(path) >= 3 && ((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) && path[1] == ':' && (path[2] == '\\' || path[2] == '/') {
		return true
	}
	return strings.HasPrefix(path, `\\`)
}
