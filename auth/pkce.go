// Package auth implements OpenRouter's OAuth PKCE login and the small key
// store behind `nova login`. It owns credential resolution order too, so the
// provider never reads the environment itself.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// bytesOfEntropy is the input size for a verifier and for a state value. 32
// random bytes encode to 43 base64url characters, the RFC 7636 minimum, and
// leave plenty of entropy for uniqueness.
const bytesOfEntropy = 32

// NewVerifier returns a fresh PKCE code verifier: 43 characters from the
// unreserved set, drawn from crypto/rand.
func NewVerifier() (string, error) {
	return randomToken()
}

// NewState returns a fresh opaque value to bind a login callback to its
// request, so a callback that did not originate this flow is rejected.
func NewState() (string, error) {
	return randomToken()
}

// Challenge returns the S256 code challenge for a verifier:
// base64url(SHA-256(verifier)) without padding. The verifier is never logged.
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// randomToken returns a base64url string of bytesOfEntropy random bytes.
func randomToken() (string, error) {
	b := make([]byte, bytesOfEntropy)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: read random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
