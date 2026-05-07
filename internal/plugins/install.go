package plugins

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type InstallOptions struct {
	Force bool
}

type InstalledPlugin struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Source    string `json:"source"`
	CachePath string `json:"cache_path"`
}

func (s *InstallStore) InstallLocal(source string, opts InstallOptions) (InstalledPlugin, error) {
	manifest, err := LoadManifest(source)
	if err != nil {
		return InstalledPlugin{}, err
	}
	absSource, err := filepath.Abs(source)
	if err != nil {
		return InstalledPlugin{}, err
	}
	cachePath := s.CachePath(manifest.Name, manifest.Version)
	installed := InstalledPlugin{Name: manifest.Name, Version: manifest.Version, Source: absSource, CachePath: cachePath}
	metadata, err := s.LoadInstalled()
	if err != nil {
		return InstalledPlugin{}, err
	}
	for _, existing := range metadata {
		if existing.Name == installed.Name && existing.Version == installed.Version && existing.Source != installed.Source && !opts.Force {
			return InstalledPlugin{}, fmt.Errorf("plugin %s@%s already installed from another source", installed.Name, installed.Version)
		}
	}
	if err := copyDir(source, cachePath); err != nil {
		return InstalledPlugin{}, err
	}
	metadata = upsertInstalled(metadata, installed)
	if err := s.SaveInstalled(metadata); err != nil {
		return InstalledPlugin{}, err
	}
	return installed, nil
}

func (s *InstallStore) LoadInstalled() ([]InstalledPlugin, error) {
	data, err := os.ReadFile(s.MetadataPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var installed []InstalledPlugin
	if err := json.Unmarshal(data, &installed); err != nil {
		return nil, err
	}
	return installed, nil
}

func (s *InstallStore) SaveInstalled(installed []InstalledPlugin) error {
	path := s.MetadataPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(installed, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func upsertInstalled(installed []InstalledPlugin, next InstalledPlugin) []InstalledPlugin {
	for i := range installed {
		if installed[i].Name == next.Name && installed[i].Version == next.Version {
			installed[i] = next
			return installed
		}
	}
	return append(installed, next)
}

func copyDir(src, dst string) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = in.Close() }()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		defer func() { _ = out.Close() }()
		_, err = io.Copy(out, in)
		return err
	})
}
