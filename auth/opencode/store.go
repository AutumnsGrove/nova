package opencode

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/AutumnsGrove/nova/auth"
)

// DefaultKeyPath is where the provisioned Go key is stored under home.
func DefaultKeyPath(home string) string {
	return filepath.Join(home, ".config", "nova", "opencode-go.json")
}

// SaveKey writes the key as JSON with mode 0600, creating the directory 0700.
// The write is atomic and never leaves the key in a readable file.
func SaveKey(path, key string) error {
	data, err := json.Marshal(map[string]string{"api_key": key})
	if err != nil {
		return fmt.Errorf("opencode: encode key: %w", err)
	}
	if err := auth.WriteSecretFile(path, data, "OpenCode Go key"); err != nil {
		return fmt.Errorf("opencode: save key: %w", err)
	}
	return nil
}

// DeleteKey removes a stored key. Deleting a missing key is not an error.
func DeleteKey(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("opencode: delete key: %w", err)
	}
	return nil
}

// LoadKey reads the stored key, returning auth.ErrNoKey when none is stored. A
// parse error never quotes the file, which holds the key.
func LoadKey(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", auth.ErrNoKey
	}
	if err != nil {
		return "", fmt.Errorf("opencode: read key: %w", err)
	}
	var parsed struct {
		APIKey string `json:"api_key"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", fmt.Errorf("opencode: key file %s is not valid JSON", path)
	}
	if parsed.APIKey == "" {
		return "", auth.ErrNoKey
	}
	return parsed.APIKey, nil
}
