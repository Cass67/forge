package tools

import (
	"path/filepath"
	"strings"
)

// Commands refused outright, including under --yolo.
//
// The bar for this list is deliberately high: a command belongs here only when
// it has no legitimate use inside a coding session AND its effect is either
// machine-wide or unrecoverable. Anything a developer might reasonably want --
// force pushes, hard resets, pruning containers, piping an install script --
// stays available, because refusing ordinary work is its own failure and a
// refusal the model cannot act on is worse than the risk it averts.
//
// Where a command is only dangerous for certain targets (delete, chmod, chown,
// writes to a device) the decision is made by resolving the path, not by
// matching the text.

// haltCommands change the machine's power state.
var haltCommands = map[string]bool{
	"shutdown": true, "reboot": true, "halt": true, "poweroff": true, "init": true,
}

// diskFormatCommands make a filesystem or destroy a partition table.
var diskFormatCommands = []string{"mkfs", "mke2fs", "newfs", "fdisk", "parted", "wipefs", "sgdisk", "gdisk"}

// devicePathPrefixes name whole disks. Character devices such as /dev/null and
// /dev/urandom are ordinary tools and are not covered.
var devicePathPrefixes = []string{"/dev/sd", "/dev/hd", "/dev/nvme", "/dev/disk", "/dev/rdisk", "/dev/vd", "/dev/mmcblk"}

// blockedCommand returns the reason a command is refused, or "" to run it.
func blockedCommand(command, workDir, home string) string {
	if isForkBomb(command) {
		return "fork bomb"
	}
	for _, segment := range splitCommandSegments(command) {
		fields := strings.Fields(segment)
		if len(fields) == 0 {
			continue
		}
		if target := redirectedDeviceTarget(segment); target != "" {
			return "raw write to the block device " + target
		}
		fields = stripCommandPrefixes(fields)
		if len(fields) == 0 {
			continue
		}
		if reason := blockedProgram(fields); reason != "" {
			return reason
		}
		if target := blockedPathTarget(fields, workDir, home); target != "" {
			return "it would destroy " + target + ", which is outside the workspace and not recoverable"
		}
	}
	return ""
}

// blockedProgram refuses whole programs whose effect is machine-wide.
func blockedProgram(fields []string) string {
	name := filepath.Base(fields[0])
	args := fields[1:]

	if haltCommands[name] {
		// `init` only changes runlevel for 0 (halt) and 6 (reboot).
		if name == "init" && (len(args) == 0 || (args[0] != "0" && args[0] != "6")) {
			return ""
		}
		return "it would halt or restart the machine"
	}
	if name == "systemctl" || name == "launchctl" {
		for _, arg := range args {
			switch arg {
			case "reboot", "poweroff", "halt", "kexec", "shutdown", "suspend", "hibernate":
				return "it would halt or restart the machine"
			}
		}
		return ""
	}
	for _, prog := range diskFormatCommands {
		if name == prog || strings.HasPrefix(name, prog+".") || strings.HasPrefix(name, prog+"_") {
			return "it would format or repartition a disk"
		}
	}
	if name == "diskutil" && len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "erasedisk", "erasevolume", "eraseoptical", "reformat", "partitiondisk", "zerodisk":
			return "it would erase a disk"
		}
	}
	if name == "dd" {
		for _, arg := range args {
			if value, ok := strings.CutPrefix(arg, "of="); ok && isBlockDevicePath(value) {
				return "it would write directly to the block device " + value
			}
		}
		return ""
	}
	if name == "tee" {
		for _, arg := range args {
			if isBlockDevicePath(arg) {
				return "it would write directly to the block device " + arg
			}
		}
		return ""
	}
	if reason := blockedProcessKill(name, args); reason != "" {
		return reason
	}
	return blockedSecurityDisable(name, args)
}

// blockedProcessKill refuses signalling every process rather than a target.
func blockedProcessKill(name string, args []string) string {
	switch name {
	case "killall5":
		return "it would kill every process on the machine"
	case "kill":
		for _, arg := range args {
			if arg == "-1" {
				return "it would kill every process on the machine"
			}
		}
	case "pkill", "killall":
		for _, arg := range args {
			if arg == "-u" || arg == "--user" || arg == "-1" {
				return "it would kill every process for a user"
			}
		}
	}
	return ""
}

// blockedSecurityDisable refuses turning off platform integrity protections.
func blockedSecurityDisable(name string, args []string) string {
	joined := strings.ToLower(strings.Join(args, " "))
	switch name {
	case "csrutil":
		if strings.Contains(joined, "disable") {
			return "it would disable System Integrity Protection"
		}
	case "spctl":
		if strings.Contains(joined, "--master-disable") || strings.Contains(joined, "--global-disable") {
			return "it would disable Gatekeeper"
		}
	case "nvram":
		if strings.Contains(joined, "-c") || strings.Contains(joined, "-d") {
			return "it would clear firmware variables"
		}
	}
	return ""
}

// isForkBomb matches the classic self-replicating shell function.
func isForkBomb(command string) bool {
	collapsed := strings.NewReplacer(" ", "", "\t", "").Replace(command)
	return strings.Contains(collapsed, ":(){:|:&};:") || strings.Contains(collapsed, ":(){:|:&};:&")
}

// redirectedDeviceTarget finds a shell redirect that writes to a whole disk.
func redirectedDeviceTarget(segment string) string {
	idx := strings.Index(segment, ">")
	for idx >= 0 {
		rest := strings.TrimLeft(segment[idx+1:], "> ")
		if field := strings.Fields(rest); len(field) > 0 && isBlockDevicePath(field[0]) {
			return field[0]
		}
		next := strings.Index(segment[idx+1:], ">")
		if next < 0 {
			break
		}
		idx = idx + 1 + next
	}
	return ""
}

// isBlockDevicePath reports whole-disk device nodes, never /dev/null and the
// other character devices that ordinary commands write to every day.
func isBlockDevicePath(path string) bool {
	path = strings.Trim(path, `"'`)
	for _, prefix := range devicePathPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}
