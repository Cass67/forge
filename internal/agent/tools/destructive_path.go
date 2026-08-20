package tools

import (
	"path/filepath"
	"strings"
)

// deviceWriteCommands have no legitimate agent use at all: unlike a delete,
// there is no target that makes them safe, so they are matched by name.
var deviceWriteCommands = []string{"mkfs", "mke2fs", "fdisk", "parted"}

// blockedDestructiveTarget reports the path a command would destroy when that
// path is one nothing should destroy, or "" when the command may run.
//
// The question asked is deliberately narrow and decidable: not "does this look
// dangerous", which no amount of pattern matching answers, but "which path does
// this resolve to, and is that path a system or home root". Deleting inside the
// workspace is ordinary work and stays untouched.
func blockedDestructiveTarget(command, workDir, home string) string {
	for _, segment := range splitCommandSegments(command) {
		fields := strings.Fields(segment)
		if len(fields) == 0 {
			continue
		}
		fields = stripCommandPrefixes(fields)
		if len(fields) == 0 {
			continue
		}
		if target := blockedDeviceWrite(fields); target != "" {
			return target
		}
		if target := blockedRecursiveDelete(fields, workDir, home); target != "" {
			return target
		}
	}
	return ""
}

// blockedDeviceWrite catches writes that address a block device directly.
func blockedDeviceWrite(fields []string) string {
	name := filepath.Base(fields[0])
	for _, blocked := range deviceWriteCommands {
		if name == blocked || strings.HasPrefix(name, blocked+".") {
			return strings.Join(fields, " ")
		}
	}
	if name != "dd" {
		return ""
	}
	for _, field := range fields[1:] {
		if value, ok := strings.CutPrefix(field, "of="); ok && strings.HasPrefix(value, "/dev/") {
			return value
		}
	}
	return ""
}

// blockedRecursiveDelete resolves each target of a recursive delete and returns
// the first that names a protected root.
func blockedRecursiveDelete(fields []string, workDir, home string) string {
	if filepath.Base(fields[0]) != "rm" {
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
	if home != "" && path == filepath.Clean(home) {
		return true
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
