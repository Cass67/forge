package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

// ponytail: one JSON file per workdir; move into sessionstore if memory ever becomes per-thread
func stateFile(workDir string) (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(workDir))
	return filepath.Join(configDir, "forge", "memory", hex.EncodeToString(sum[:8])+".json"), nil
}

func LoadState(workDir string) State {
	path, err := stateFile(workDir)
	if err != nil {
		return State{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return State{}
	}
	var s State
	if json.Unmarshal(data, &s) != nil {
		return State{}
	}
	return s
}

func SaveState(workDir string, s State) error {
	path, err := stateFile(workDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
