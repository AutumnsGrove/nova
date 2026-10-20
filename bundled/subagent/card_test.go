//go:build !notui

package subagent

import (
	"strings"
	"testing"
)

func TestSubagent_SUB24_CardRenders(t *testing.T) {
	got := Card(4, []string{"read", "grep"}, 0.0123)
	if !strings.Contains(got, "4 turn") || !strings.Contains(got, "2 tool") || !strings.Contains(got, "$0.0123") {
		t.Fatalf("card = %q", got)
	}
}
