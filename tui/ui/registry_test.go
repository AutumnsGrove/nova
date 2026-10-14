//go:build !notui

package ui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/tui/render"
)

// TestRegistry_BundledCards proves the transcript's render registry is the
// bundled one: bundled tools render through their cards, not the generic card.
func TestRegistry_BundledCards(t *testing.T) {
	m, _, _ := newModel(t)
	cases := []struct {
		tool    string
		details string
		want    string
	}{
		{"todo", `{}`, "todo:"},
		{"subagent", `{}`, "subagent:"},
		{"web_fetch", `{"url":"https://example.com","status":200,"bytes":10}`, "https://example.com"},
	}
	for _, tc := range cases {
		rd := m.registry.Lookup(tc.tool)
		if rd == nil {
			t.Fatalf("no renderer for %s", tc.tool)
		}
		call := render.Call{Name: tc.tool, Details: json.RawMessage(tc.details)}
		if header := rd.Header(call, m.st, 80); !strings.Contains(header, tc.want) {
			t.Fatalf("%s header = %q, want %q", tc.tool, header, tc.want)
		}
	}
}
