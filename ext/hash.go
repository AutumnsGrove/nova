package ext

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

// ManifestHash hashes a manifest file's bytes for change detection.
func ManifestHash(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "extension.yaml"))
	if err != nil {
		return ""
	}
	return HashBytes(data)
}

// HashBytes returns the hex SHA-256 of b. The installer records it in the
// lockfile and reload compares against it, so both must use this one function.
func HashBytes(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
