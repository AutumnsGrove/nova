package auth

import (
	"regexp"
	"testing"
)

// pkceVerifierPattern is the RFC 7636 unreserved alphabet and length range.
var pkceVerifierPattern = regexp.MustCompile(`^[A-Za-z0-9\-._~]{43,128}$`)

// TestPKCE_PKC01_VerifierShape checks 1,000 verifiers are the right length and
// alphabet.
func TestPKCE_PKC01_VerifierShape(t *testing.T) {
	for i := 0; i < 1000; i++ {
		v, err := NewVerifier()
		if err != nil {
			t.Fatalf("NewVerifier: %v", err)
		}
		if !pkceVerifierPattern.MatchString(v) {
			t.Fatalf("verifier %q does not match %s", v, pkceVerifierPattern)
		}
	}
}

// TestPKCE_PKC02_VerifiersUnique checks 1,000 verifiers are all distinct.
func TestPKCE_PKC02_VerifiersUnique(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		v, err := NewVerifier()
		if err != nil {
			t.Fatalf("NewVerifier: %v", err)
		}
		if seen[v] {
			t.Fatalf("duplicate verifier %q", v)
		}
		seen[v] = true
	}
}

// TestPKCE_PKC03_RFCVector checks the challenge against RFC 7636 appendix B.
func TestPKCE_PKC03_RFCVector(t *testing.T) {
	const (
		verifier  = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
		challenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	)
	if got := Challenge(verifier); got != challenge {
		t.Fatalf("Challenge() = %q, want %q", got, challenge)
	}
}

// TestPKCE_PKC04_ChallengeEncoding checks the challenge is unpadded base64url.
func TestPKCE_PKC04_ChallengeEncoding(t *testing.T) {
	c := Challenge("some-verifier-value")
	for _, bad := range []string{"=", "+", "/"} {
		if regexp.MustCompile(regexp.QuoteMeta(bad)).MatchString(c) {
			t.Errorf("challenge %q contains %q", c, bad)
		}
	}
	if c == "" {
		t.Fatal("challenge is empty")
	}
}
