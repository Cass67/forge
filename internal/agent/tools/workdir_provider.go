package tools

import "strings"

type WorkDirProvider func() string

func FixedWorkDirProvider(workDir string) WorkDirProvider {
	return func() string { return workDir }
}

func currentWorkDir(provider WorkDirProvider, fallback string) string {
	if provider == nil {
		return fallback
	}
	if workDir := strings.TrimSpace(provider()); workDir != "" {
		return workDir
	}
	return fallback
}
