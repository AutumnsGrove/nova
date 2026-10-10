package compaction

import (
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/message"
)

func msgRunes(role message.Role, n int) message.Message {
	return message.Message{Role: role, Content: strings.Repeat("a", n)}
}

// msgTokens builds one message whose Estimate is exactly tokens (>=4).
func msgTokens(role message.Role, tokens int) message.Message {
	if tokens < 4 {
		tokens = 4
	}
	return msgRunes(role, (tokens-4)*4)
}

func Test_EST01_Empty(t *testing.T) {
	if got := Estimate(nil); got != 0 {
		t.Fatalf("Estimate(nil) = %d, want 0", got)
	}
	if got := Estimate([]message.Message{}); got != 0 {
		t.Fatalf("Estimate(empty) = %d, want 0", got)
	}
}

func Test_EST02_KnownText(t *testing.T) {
	msgs := []message.Message{msgRunes(message.RoleUser, 400)}
	// 100 content tokens plus 4 tokens of per-message overhead.
	if got := Estimate(msgs); got != 104 {
		t.Fatalf("Estimate = %d, want 104", got)
	}
}

func Test_EST03_MultibyteCountedByRunes(t *testing.T) {
	ascii := []message.Message{msgRunes(message.RoleUser, 400)}
	multi := []message.Message{{Role: message.RoleUser, Content: strings.Repeat("é", 400)}}
	if Estimate(ascii) != Estimate(multi) {
		t.Fatalf("ascii %d != multibyte %d", Estimate(ascii), Estimate(multi))
	}
}

func Test_EST04_ToolCallsCounted(t *testing.T) {
	args := strings.Repeat("a", 400)
	m := message.Message{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{
		ID:       "c1",
		Function: message.FunctionCall{Arguments: args},
	}}}
	// Overhead 4 plus about 100 tokens for the 400-char argument.
	if got := Estimate([]message.Message{m}); got != 104 {
		t.Fatalf("Estimate = %d, want 104", got)
	}
}

func Test_EST05_Monotonic(t *testing.T) {
	base := []message.Message{
		msgRunes(message.RoleUser, 40),
		{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "c", Function: message.FunctionCall{Name: "read", Arguments: `{"path":"a"}`}}}},
		{Role: message.RoleTool, ToolCallID: "c", Content: "result"},
	}
	prev := Estimate(nil)
	for i := range base {
		got := Estimate(base[:i+1])
		if got < prev {
			t.Fatalf("Estimate dropped from %d to %d at %d", prev, got, i)
		}
		prev = got
	}
}

func Test_EST06_VeryLargeInput(t *testing.T) {
	big := strings.Repeat("x", 50<<20)
	if got := Estimate([]message.Message{{Role: message.RoleUser, Content: big}}); got <= 0 {
		t.Fatalf("Estimate = %d, want positive", got)
	}
}

func Test_EST07_LinearTime(t *testing.T) {
	one := msgRunes(message.RoleUser, 400)
	if Estimate([]message.Message{one})*2 != Estimate([]message.Message{one, one}) {
		t.Fatal("doubling the messages should double the estimate")
	}
}
