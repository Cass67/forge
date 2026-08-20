package tools

import (
	"path/filepath"
	"strings"
)

// blockedPathTarget resolves each target of a recursive, destructive path
// operation and returns the first that names a protected root. Deleting a
// directory and recursively rewriting its ownership or permissions are the
// same catastrophe when aimed at /, so they share one rule.
func blockedPathTarget(fields []string, workDir, home string) string {
	switch filepath.Base(fields[0]) {
	case "rm", "chmod", "chown", "chgrp", "shred":
	default:
		return ""
	}
	recursive := false
	var targets []string
	for _, field := range fields[1:] {
		switch {
		case field == "--recursive":
			recursive = true
		case strings.HasPrefix(field, "--"):
			// Another long option; not a target.
		case strings.HasPrefix(field, "-"):
			if strings.ContainsAny(field, "rR") {
				recursive = true
			}
		default:
			targets = append(targets, field)
		}
	}
	if !recursive {
		return ""
	}
	for _, target := range targets {
		if resolved := resolveDeleteTarget(target, workDir, home); isProtectedRoot(resolved, home) {
			return resolved
		}
	}
	return ""
}

// resolveDeleteTarget expands the forms a shell would expand before deleting,
// then resolves the result against the working directory.
func resolveDeleteTarget(target, workDir, home string) string {
	target = strings.Trim(target, `"'`)
	// A trailing glob deletes the directory's contents, which for a protected
	// root is the same catastrophe as deleting the root itself.
	target = strings.TrimSuffix(target, "*")
	if home != "" {
		if target == "~" || strings.HasPrefix(target, "~/") {
			target = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(target, "~"), "/"))
		}
		for _, form := range []string{"$HOME", "${HOME}"} {
			if target == form || strings.HasPrefix(target, form+"/") {
				target = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(target, form), "/"))
			}
		}
	}
	if target == "" {
		return ""
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(workDir, target)
	}
	return filepath.Clean(target)
}

// isProtectedRoot reports paths whose removal breaks the machine or the user's
// account. Anything below one of these stays allowed.
func isProtectedRoot(path, home string) bool {
	if path == "" {
		return false
	}
	if path == string(filepath.Separator) {
		return true
	}
	if home != "" {
		if path == filepath.Clean(home) {
			return true
		}
		for _, secret := range []string{".ssh", ".gnupg", ".aws", ".kube"} {
			if path == filepath.Join(home, secret) {
				return true
			}
		}
	}
	switch path {
	case "/bin", "/boot", "/dev", "/etc", "/home", "/lib", "/opt", "/private",
		"/proc", "/root", "/sbin", "/srv", "/sys", "/usr", "/var",
		"/Applications", "/Library", "/System", "/Users", "/Volumes":
		return true
	}
	// One level under the system prefixes is still infrastructure: /usr/lib,
	// /Users/someone, /Volumes/disk.
	for _, prefix := range []string{"/usr", "/Library", "/System", "/Users", "/Volumes", "/home"} {
		if rest, ok := strings.CutPrefix(path, prefix+"/"); ok && !strings.Contains(rest, "/") {
			return true
		}
	}
	return false
}

// splitCommandSegments splits on shell operators so a destructive command
// chained after something harmless is still examined.
func splitCommandSegments(command string) []string {
	replacer := strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n", "|", "\n", "&", "\n")
	return strings.Split(replacer.Replace(command), "\n")
}

// stripCommandPrefixes drops leading environment assignments and privilege
// wrappers so the real program is examined.
func stripCommandPrefixes(fields []string) []string {
	for len(fields) > 0 {
		head := fields[0]
		if strings.Contains(head, "=") && !strings.HasPrefix(head, "-") {
			fields = fields[1:]
			continue
		}
		switch filepath.Base(head) {
		case "sudo", "doas", "env", "nohup", "time", "command", "xargs":
			fields = fields[1:]
			continue
		}
		return fields
	}
	return fields
}
