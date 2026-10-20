//go:build !notui

package sanitize

import "testing"

func TestText(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain text is untouched", "echo hello world", "echo hello world"},
		{"newline and tab are kept", "a\nb\tc", "a\nb\tc"},
		{"unicode and emoji are kept", "héllo wörld 日本語 👍🏽 👨‍👩‍👧", "héllo wörld 日本語 👍🏽 👨‍👩‍👧"},
		{"sgr colour", "a\x1b[31mred\x1b[0m", "ared"},
		{"osc title with bell", "a\x1b]0;title\x07b", "ab"},
		{"c1 csi", "a\x9b31mb", "ab"},
		{"lone escape", "a\x1bb", "a"},
		// A carriage return moves the cursor to the start of the line, so text after
		// it is drawn over what came before.
		{"carriage return", "rm -rf /tmp/x\recho ok", "rm -rf /tmp/xecho ok"},
		{"crlf becomes lf", "a\r\nb", "a\nb"},
		{"backspace", "abc\b\b\bxyz", "abcxyz"},
		{"bell and nul and del", "a\ab\x00c\x7fd", "abcd"},
		{"vertical tab and form feed", "a\vb\fc", "abc"},
		{"c1 control", "a\u0085b\u009fc", "abc"},
		// Bidirectional overrides reorder what is drawn without changing the bytes.
		{"right-to-left override", "safe ‮txt.exe", "safe txt.exe"},
		{"bidi isolates and marks", "a⁦b⁩c‎d‏e؜f", "abcdef"},
		{"line and paragraph separators", "a b c", "abc"},
		{"invalid utf-8", "a\xffb\xc3", "ab"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		if got := Text(c.in); got != c.want {
			t.Errorf("%s: Text(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}
