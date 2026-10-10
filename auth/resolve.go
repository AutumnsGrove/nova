package auth

import (
	"errors"
	"strings"
)

// EnvAPIKey is the environment variable that can carry the key.
const EnvAPIKey = "OPENROUTER_API_KEY"

// Resolve returns the first credential found in the order config, environment,
// store, together with the source name ("config", "env", or "store"). Keys are
// trimmed, so surrounding whitespace never reaches the wire, and a
// whitespace-only environment value counts as unset. When nothing is set it
// returns ErrNoKey.
//
// A store that returns a key together with a *PermissionWarning still resolves;
// the warning is for the caller's own Load, not a reason to refuse the key.
func Resolve(cfgKey string, env func(string) string, store Store) (key, source string, err error) {
	if k := strings.TrimSpace(cfgKey); k != "" {
		return k, "config", nil
	}
	if env != nil {
		if k := strings.TrimSpace(env(EnvAPIKey)); k != "" {
			return k, "env", nil
		}
	}
	if store != nil {
		k, loadErr := store.Load()
		if k = strings.TrimSpace(k); k != "" {
			return k, "store", nil
		}
		if loadErr != nil && !errors.Is(loadErr, ErrNoKey) {
			return "", "", loadErr
		}
	}
	return "", "", ErrNoKey
}
