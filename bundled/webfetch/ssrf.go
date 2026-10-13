package webfetch

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// errBlocked is wrapped by every refused address so callers can test for it.
var errBlocked = errors.New("blocked address")

// cgnat is the carrier-grade NAT range, which net.IP does not know about.
var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// blockedReason classifies an address. It returns "" for a globally routable
// address and a short reason otherwise. allowPrivate lifts the private,
// loopback, and link-local refusals.
func blockedReason(ip net.IP, allowPrivate bool) string {
	if ip == nil {
		return "blocked address"
	}
	// To4 normalizes IPv4-mapped IPv6 forms such as ::ffff:127.0.0.1.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsUnspecified() {
		return "blocked address (unspecified)"
	}
	if ip.IsMulticast() {
		return "blocked address (multicast)"
	}
	if cgnat.Contains(ip) {
		return "blocked address (CGNAT)"
	}
	if ip.IsLoopback() {
		if allowPrivate {
			return ""
		}
		return "blocked address (loopback)"
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		if allowPrivate {
			return ""
		}
		return "blocked address (link-local)"
	}
	if ip.IsPrivate() {
		if allowPrivate {
			return ""
		}
		return "blocked address (private)"
	}
	return ""
}

// hostAllowed reports whether a host is on the explicit allow-list. Matching is
// exact, or a suffix on a dot boundary for a leading-dot entry.
func hostAllowed(host string, allowHosts []string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, pattern := range allowHosts {
		pattern = strings.ToLower(pattern)
		if pattern == "" {
			continue
		}
		if pattern == host {
			return true
		}
		if strings.HasPrefix(pattern, ".") && strings.HasSuffix(host, pattern) {
			return true
		}
	}
	return false
}

// checkAddr parses and classifies one resolved address.
func checkAddr(addr net.IP, allowPrivate bool) error {
	if reason := blockedReason(addr, allowPrivate); reason != "" {
		return fmt.Errorf("%w: %s", errBlocked, reason)
	}
	return nil
}
