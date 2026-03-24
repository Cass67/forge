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

func ForgeConfigDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "forge")
	}
	return filepath.Join(UserHomeDir(), ".config", "forge")
}

func ForgeConfigPath(name string) string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "forge", name)
	}
	return filepath.Join(UserHomeDir(), ".config", "forge", name)
}
