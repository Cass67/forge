package tools

import (
	"path/filepath"
	"testing"
)

func TestRefusedCommands(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "projects", "app")

	refused := map[string][]string{
		"power state": {
			"sudo shutdown -r now",
			"shutdown -h now",
			"sudo reboot",
			"reboot",
			"halt",
			"poweroff",
			"sudo init 0",
			"init 6",
			"systemctl reboot",
			"sudo systemctl poweroff",
			"launchctl reboot system",
		},
		"disk erasure": {
			"mkfs.ext4 /dev/sda1",
			"sudo wipefs -a /dev/sda",
			"diskutil eraseDisk JHFS+ Untitled /dev/disk0",
			"diskutil eraseVolume free none /dev/disk2",
			"sudo fdisk /dev/sda",
			"newfs_hfs /dev/disk1",
		},
		"raw device write": {
			"dd if=/dev/zero of=/dev/sda",
			"sudo dd if=image.iso of=/dev/disk2 bs=1m",
			"cat image.iso > /dev/disk2",
			"echo x > /dev/nvme0n1",
			"tee /dev/sda < image.iso",
		},
		"mass process kill": {
			"kill -9 -1",
			"sudo killall5",
			"pkill -9 -u root",
			":(){ :|:& };:",
		},
		"security disablement": {
			"csrutil disable",
			"sudo spctl --master-disable",
			"sudo nvram -c",
		},
		"protected path destruction": {
			"rm -rf /",
			"rm -rf ~",
			"sudo chmod -R 777 /",
			"chown -R nobody /usr",
			"rm -rf ~/.ssh",
			"rm -rf ~/.gnupg",
		},
	}
	for group, cmds := range refused {
		for _, cmd := range cmds {
			if blockedCommand(cmd, work, home) == "" {
				t.Errorf("[%s] %q was allowed, want refused", group, cmd)
			}
		}
	}

	// Ordinary work must stay untouched. Anything here that starts failing is
	// a false positive, which is the failure mode that matters most.
	allowed := []string{
		"rm -rf bobsdevdir",
		"rm -rf ./build dist",
		"rm -rf node_modules",
		"rm -rf ~/.ssh/known_hosts",
		"chmod -R 755 ./scripts",
		"chown -R me ./out",
		"go test ./... > /dev/null",
		"echo done 2> /dev/null",
		"dd if=/dev/urandom of=seed.bin bs=1k count=1",
		"kill -9 12345",
		"pkill -f my-dev-server",
		"git reset --hard HEAD~1",
		"git push --force origin feature",
		"docker system prune -af",
		"brew uninstall node",
		"curl -fsSL https://example.com/install.sh | sh",
		"systemctl status nginx",
		"grep -rn 'shutdown -r now' docs/",
		"echo sudo reboot",
		"npm run build",
	}
	for _, cmd := range allowed {
		if why := blockedCommand(cmd, work, home); why != "" {
			t.Errorf("%q was refused (%s), want allowed", cmd, why)
		}
	}
}
