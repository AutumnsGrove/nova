package config

import (
	"strings"
	"testing"
)

// TestSetUser_CFW12_ChangedValueKeepsItsComment checks that replacing a value
// keeps the comment written on the same line, since users annotate the very
// settings they later change from /settings.
func TestSetUser_CFW12_ChangedValueKeepsItsComment(t *testing.T) {
	home := t.TempDir()
	write(t, userConfigPath(home), "# top\nmodel: old # my favourite\ntheme: dark\n")
	if err := SetUser(home, "model", "new"); err != nil {
		t.Fatalf("SetUser: %v", err)
	}
	got := readUser(t, home)
	if !strings.Contains(got, "model: new # my favourite") {
		t.Errorf("the line comment was lost:\n%s", got)
	}
	if !strings.Contains(got, "# top") {
		t.Errorf("the head comment was lost:\n%s", got)
	}
}
