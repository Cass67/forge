package fsutil

import (
	"os"
	"path/filepath"
)

func UserHomeDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home
	}
	if home := os.Getenv("HOME"); home != "" {
		return home
	}
	return "."
}

func ForgeConfigPath(name string) string {
	return filepath.Join(UserHomeDir(), ".config", "forge", name)
}
