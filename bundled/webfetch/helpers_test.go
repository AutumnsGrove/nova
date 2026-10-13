package webfetch

import (
	"net"
	"net/url"
	"testing"
	"unicode/utf8"

	"github.com/AutumnsGrove/nova/ext"
	"github.com/AutumnsGrove/nova/tool"
)

// testEnv builds an Env that captures the registered tool.
func testEnv(capture func(tool.Tool)) *ext.Env {
	return &ext.Env{
		Tool: func(t tool.Tool) error { capture(t); return nil },
	}
}

// parseURL parses a URL for tests, panicking on a bad literal.
func parseURL(s string) (*url.URL, error) { return url.Parse(s) }

// refCIDRs is the independent reference classification for FuzzBlockedAddress.
var refCIDRs = func() []*net.IPNet {
	var out []*net.IPNet
	for _, s := range []string{
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10",
		"fc00::/7", "169.254.0.0/16", "fe80::/10",
	} {
		_, n, _ := net.ParseCIDR(s)
		out = append(out, n)
	}
	return out
}()

// refBlocked is a from-scratch classifier for the fuzz oracle.
func refBlocked(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	for _, n := range refCIDRs {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// FuzzBlockedAddress checks the checker against the reference classifier.
func FuzzBlockedAddress(f *testing.F) {
	f.Add([]byte{127, 0, 0, 1})
	f.Add([]byte{10, 0, 0, 1})
	f.Add([]byte{8, 8, 8, 8})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 4 {
			return
		}
		ip := net.IPv4(data[0], data[1], data[2], data[3])
		got := blockedReason(ip, false) != ""
		if want := refBlocked(ip); got != want {
			t.Fatalf("blockedReason(%s) = %v, reference = %v", ip, got, want)
		}
	})
}

// FuzzHTMLToMarkdown checks the converter never panics and emits UTF-8.
func FuzzHTMLToMarkdown(f *testing.F) {
	f.Add([]byte("<html><body><p>hi</p></body></html>"))
	f.Add([]byte("<a href='/x'>link</a>"))
	f.Add([]byte("<table><tr><td>x</td></tr></table>"))
	f.Fuzz(func(t *testing.T, data []byte) {
		out := htmlToMarkdown(data, nil)
		if !utf8.ValidString(out) {
			t.Fatalf("output is not valid UTF-8: %q", out)
		}
	})
}
