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

// The ask card must report the questions the call carried, from its Details.
func TestRegistry_REG01_AskCardFromDetails(t *testing.T) {
	m, _, _ := newModel(t)
	rd := m.registry.Lookup("ask")
	if rd == nil {
		t.Fatal("no renderer for ask")
	}
	cases := []struct {
		name    string
		details string
		want    string
	}{
		{"three questions", `{"questions":[{"id":"ambiguity"},{"id":"explanations"},{"id":"off-topic"}],"answers":[]}`,
			"ask: 3 questions (Ambiguity, Explanations, Off-topic)"},
		{"one question", `{"questions":[{"id":"scope"}]}`, "ask: 1 question (Scope)"},
		{"no details", `{}`, "ask: no questions"},
		{"hostile id is cleaned", `{"questions":[{"id":"\u001b]0;pwned\u0007bad"}]}`, "ask: 1 question (Bad)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			call := render.Call{Name: "ask", Details: json.RawMessage(tc.details)}
			if got := rd.Header(call, m.st, 100); got != tc.want {
				t.Fatalf("header = %q, want %q", got, tc.want)
			}
		})
	}
}
