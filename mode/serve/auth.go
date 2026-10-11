package serve

import (
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// constantTimeEqual compares the presented token with the configured one
// without an early exit on the first differing byte. It is a named wrapper so a
// test can prove that the token check goes through subtle.ConstantTimeCompare.
func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// loopbackListen reports whether a listen address names only the loopback
// interface. An empty host, a wildcard address, or a hostname is not loopback.
func loopbackListen(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	return isLoopbackHost(host)
}

// isLoopbackHost reports whether a bare host (no port) is a loopback name or
// address. "localhost" is accepted by name; every other name must parse as a
// loopback IP.
func isLoopbackHost(host string) bool {
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// hostAllowed reports whether the Host header is trusted. Loopback names pass;
// anything else must be listed in allowed. The port never matters.
func hostAllowed(header string, allowed []string) bool {
	host := header
	if h, _, err := net.SplitHostPort(header); err == nil {
		host = h
	}
	if isLoopbackHost(host) {
		return true
	}
	for _, a := range allowed {
		if strings.EqualFold(a, host) || strings.EqualFold(a, header) {
			return true
		}
	}
	return false
}

// bearer extracts the token from an Authorization header. A missing, malformed,
// or wrong-scheme header yields the empty string, never an error that would
// reveal why it failed.
func bearer(header string) string {
	const prefix = "Bearer "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return header[len(prefix):]
}

// validateCORS rejects a wildcard or malformed origin at configuration time.
func validateCORS(origin string) error {
	if origin == "" {
		return nil
	}
	if origin == "*" {
		return fmt.Errorf("serve: CORSOrigin %q is not allowed; name one exact origin", origin)
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("serve: CORSOrigin %q is not an origin", origin)
	}
	return nil
}

// corsHeaders writes the CORS headers for a matching request. Only the exact
// configured origin is echoed, and Vary: Origin keeps caches honest.
func (s *server) corsHeaders(w http.ResponseWriter, r *http.Request) bool {
	if s.opt.CORSOrigin == "" {
		return false
	}
	w.Header().Add("Vary", "Origin")
	if r.Header.Get("Origin") != s.opt.CORSOrigin {
		return false
	}
	w.Header().Set("Access-Control-Allow-Origin", s.opt.CORSOrigin)
	return true
}
