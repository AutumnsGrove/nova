package webfetch

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// errBlocked is wrapped by every refused address so callers can test for it.
var errBlocked = errors.New("blocked address")

// reservedNet is a range that is never a legitimate fetch target. The netip
// predicates do not know about these, and allow_private does not lift them.
type reservedNet struct {
	prefix netip.Prefix
	name   string
}

// reservedNets lists the non-global ranges outside the private, loopback, and
// link-local classes.
var reservedNets = []reservedNet{
	{netip.MustParsePrefix("0.0.0.0/8"), "this network"},
	{netip.MustParsePrefix("100.64.0.0/10"), "CGNAT"},
	{netip.MustParsePrefix("192.0.0.0/24"), "protocol assignments"},
	{netip.MustParsePrefix("192.0.2.0/24"), "documentation"},
	{netip.MustParsePrefix("198.18.0.0/15"), "benchmarking"},
	{netip.MustParsePrefix("198.51.100.0/24"), "documentation"},
	{netip.MustParsePrefix("203.0.113.0/24"), "documentation"},
	{netip.MustParsePrefix("240.0.0.0/4"), "reserved"},
	{netip.MustParsePrefix("100::/64"), "discard-only"},
	{netip.MustParsePrefix("2001::/32"), "Teredo"},
	{netip.MustParsePrefix("2001:db8::/32"), "documentation"},
	{netip.MustParsePrefix("fec0::/10"), "site-local"},
}

// Prefixes of the IPv6 forms that carry an IPv4 address, which a gateway on
// the network may translate into a connection to that address.
var (
	nat64Prefix = netip.MustParsePrefix("64:ff9b::/96")
	sixToFour   = netip.MustParsePrefix("2002::/16")
)

// embeddedIPv4 returns the IPv4 address inside a NAT64 or 6to4 address, so the
// translated destination is judged by the rules for the address it reaches.
func embeddedIPv4(addr netip.Addr) (netip.Addr, bool) {
	raw := addr.As16()
	switch {
	case nat64Prefix.Contains(addr):
		return netip.AddrFrom4([4]byte(raw[12:])), true
	case sixToFour.Contains(addr):
		return netip.AddrFrom4([4]byte(raw[2:6])), true
	}
	return netip.Addr{}, false
}

// blockedReason classifies an address. It returns "" for a globally routable
// address and the name of the range that refuses it otherwise. allowPrivate
// lifts the private, loopback, and link-local refusals.
func blockedReason(ip net.IP, allowPrivate bool) string {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return "invalid"
	}
	// Unmap normalizes IPv4-mapped IPv6 forms such as ::ffff:127.0.0.1, and a
	// zone is irrelevant to which network the address is on.
	addr = addr.Unmap().WithZone("")
	if v4, ok := embeddedIPv4(addr); ok {
		addr = v4
	}
	switch {
	case addr.IsUnspecified():
		return "unspecified"
	case addr.IsMulticast():
		return "multicast"
	}
	for _, r := range reservedNets {
		if r.prefix.Contains(addr) {
			return r.name
		}
	}
	if allowPrivate {
		return ""
	}
	switch {
	case addr.IsLoopback():
		return "loopback"
	case addr.IsLinkLocalUnicast():
		return "link-local"
	case addr.IsPrivate():
		return "private"
	}
	return ""
}

// hostAllowed reports whether a host is on the explicit allow-list. Matching is
// exact, or a suffix on a dot boundary for a leading-dot entry. A trailing dot
// (a fully qualified name) is ignored on both sides.
func hostAllowed(host string, allowHosts []string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, pattern := range allowHosts {
		pattern = strings.ToLower(strings.TrimSuffix(pattern, "."))
		if pattern == "" {
			continue
		}
		if pattern == host || (strings.HasPrefix(pattern, ".") && strings.HasSuffix(host, pattern)) {
			return true
		}
	}
	return false
}

// checkAddr classifies one resolved address and wraps errBlocked, naming the
// range, for a refusal.
func checkAddr(addr net.IP, allowPrivate bool) error {
	if reason := blockedReason(addr, allowPrivate); reason != "" {
		return fmt.Errorf("%w (%s)", errBlocked, reason)
	}
	return nil
}
