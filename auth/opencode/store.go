package opencode

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/AutumnsGrove/nova/auth"
)

// DefaultKeyPath is where the provisioned Go key is stored under home.
func DefaultKeyPath(home string) string {
	return filepath.Join(home, ".config", "nova", "opencode-go.json")
}

// SaveKey writes the key as JSON with mode 0600, creating the directory.
func SaveKey(path, key string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(map[string]string{"api_key": key})
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadKey reads the stored key, returning auth.ErrNoKey when none is stored.
func LoadKey(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", auth.ErrNoKey
	}
	if err != nil {
		return "", err
	}
	var parsed struct {
		APIKey string `json:"api_key"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", err
	}
	if parsed.APIKey == "" {
		return "", auth.ErrNoKey
	}
	return parsed.APIKey, nil
}
