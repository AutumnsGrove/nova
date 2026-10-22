package proto

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/decide"
)

func TestCommand_DecideRoundTrip(t *testing.T) {
	cmd := Command{
		Type: CmdDecide,
		ID:   "1",
		Decision: &decide.Request{
			Model: "oracle",
			State: []decide.Source{{Source: "one", Text: "first"}},
			Questions: map[string]decide.Question{
				"q": {Kind: decide.KindNoul, Instructions: "is it?"},
			},
		},
	}
	data, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, want := range []string{`"type":"decide"`, `"decision":{"model":"oracle"`, `"questions":{"q":{"kind":"noul"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("frame = %s, want it to contain %s", data, want)
		}
	}

	var got Command
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Decision == nil || got.Decision.Model != "oracle" {
		t.Fatalf("Decision = %+v", got.Decision)
	}
	if q := got.Decision.Questions["q"]; q.Kind != decide.KindNoul || q.Instructions != "is it?" {
		t.Fatalf("question = %+v", q)
	}
}
