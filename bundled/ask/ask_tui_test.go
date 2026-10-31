//go:build !notui

package ask

import "testing"

func TestAsk_ASK18_Card(t *testing.T) {
	cases := []struct {
		name   string
		labels []string
		want   string
	}{
		{"three", []string{"Ambiguity", "Explanations", "Off-topic"}, "3 questions (Ambiguity, Explanations, Off-topic)"},
		{"one", []string{"Scope"}, "1 question (Scope)"},
		{"none", nil, "no questions"},
	}
	for _, c := range cases {
		if got := Card(c.labels); got != c.want {
			t.Errorf("%s: Card = %q, want %q", c.name, got, c.want)
		}
	}
}
