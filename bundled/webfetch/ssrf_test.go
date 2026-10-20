package webfetch

import (
	"net"
	"testing"
)

func TestWebfetch_WFT47_ReservedRangesBlocked(t *testing.T) {
	always := []string{
		"0.1.2.3", "192.0.0.9", "192.0.2.1", "198.18.0.1", "198.51.100.7", "203.0.113.5",
		"240.0.0.1", "255.255.255.255", "2001:db8::1", "fec0::1", "100::1",
		"2001:0:4136:e378:8000:63bf:3fff:fdd2", // Teredo
	}
	for _, s := range always {
		if reason := blockedReason(net.ParseIP(s), true); reason == "" {
			t.Errorf("%s should be blocked even with allow_private", s)
		}
	}
	// A tunnelled address is judged by the IPv4 address inside it.
	tunnelled := []string{"64:ff9b::7f00:1", "64:ff9b::a00:1", "64:ff9b::a9fe:a9fe", "2002:7f00:1::", "2002:c0a8:101::"}
	for _, s := range tunnelled {
		if reason := blockedReason(net.ParseIP(s), false); reason == "" {
			t.Errorf("%s should be blocked by default", s)
		}
	}
	if reason := blockedReason(net.ParseIP("64:ff9b::7f00:1"), true); reason != "" {
		t.Errorf("a NAT64 loopback follows the loopback rule, got %q with allow_private", reason)
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "64:ff9b::808:808", "2002:808:808::", "2606:4700:4700::1111"} {
		if reason := blockedReason(net.ParseIP(s), false); reason != "" {
			t.Errorf("%s should be allowed, got %q", s, reason)
		}
	}
}

func TestWebfetch_WFT48_AllowHostsNormalised(t *testing.T) {
	if !hostAllowed("Docs.Example.com.", []string{"docs.example.com."}) {
		t.Error("a trailing dot on either side should not defeat an exact entry")
	}
	if !hostAllowed("a.example.com", []string{".example.com"}) || hostAllowed("evilexample.com", []string{".example.com"}) {
		t.Error("a leading-dot entry must match on a dot boundary only")
	}
	if hostAllowed("example.com", []string{".example.com"}) {
		t.Error("a leading-dot entry does not match the bare domain")
	}
}
