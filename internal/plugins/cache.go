package plugins

import "path/filepath"

type InstallStore struct {
	Root string
}

func NewInstallStore(root string) *InstallStore {
	return &InstallStore{Root: root}
}

func (s *InstallStore) CachePath(name, version string) string {
	return filepath.Join(s.Root, "plugins", "cache", name, version)
}

func (s *InstallStore) MetadataPath() string {
	return filepath.Join(s.Root, "plugins", "installed_plugins.json")
}
