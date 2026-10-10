package message

import (
	"strings"
	"testing"
)

// assistantCall builds an assistant message with one tool call.
func assistantCall(id string) Message {
	return Message{
		Role:      RoleAssistant,
		ToolCalls: []ToolCall{{ID: id, Type: "function", Function: FunctionCall{Name: "echo"}}},
	}
}

func TestValidate_MSG06_Empty(t *testing.T) {
	if err := Validate(nil); err != nil {
		t.Errorf("Validate(nil) = %v, want nil", err)
	}
}

func TestValidate_MSG07_UserAssistant(t *testing.T) {
	if err := Validate([]Message{User("hi"), Assistant("hello")}); err != nil {
		t.Errorf("Validate = %v, want nil", err)
	}
}

func TestValidate_MSG08_PairedCallsOutOfOrder(t *testing.T) {
	msgs := []Message{
		{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{
				{ID: "a", Function: FunctionCall{Name: "read"}},
				{ID: "b", Function: FunctionCall{Name: "write"}},
			},
		},
		ToolResult("b", "second"),
		ToolResult("a", "first"),
	}
	if err := Validate(msgs); err != nil {
		t.Errorf("Validate = %v, want nil", err)
	}
}

func TestValidate_MSG09_ToolWithoutCall(t *testing.T) {
	err := Validate([]Message{ToolResult("c1", "orphan")})
	if err == nil {
		t.Fatal("Validate = nil, want error")
	}
	if !strings.Contains(err.Error(), "0") {
		t.Errorf("error %q should name index 0", err)
	}
}

func TestValidate_MSG10_EmptyToolCallID(t *testing.T) {
	err := Validate([]Message{ToolResult("", "orphan")})
	if err == nil {
		t.Fatal("Validate = nil, want error")
	}
}

func TestValidate_MSG11_CallWithoutResult(t *testing.T) {
	err := Validate([]Message{assistantCall("c1"), User("next")})
	if err == nil {
		t.Fatal("Validate = nil, want error")
	}
	if !strings.Contains(err.Error(), "c1") {
		t.Errorf("error %q should name the call id c1", err)
	}
}

func TestValidate_MSG12_TwoResultsForOneCall(t *testing.T) {
	msgs := []Message{
		assistantCall("c1"),
		ToolResult("c1", "first"),
		ToolResult("c1", "second"),
	}
	if err := Validate(msgs); err == nil {
		t.Fatal("Validate = nil, want error")
	}
}

func TestValidate_MSG13_SystemAtTwo(t *testing.T) {
	if err := Validate([]Message{User("hi"), Assistant("ok"), System("late")}); err == nil {
		t.Fatal("Validate = nil, want error")
	}
}

func TestValidate_MSG14_SystemAtZero(t *testing.T) {
	if err := Validate([]Message{System("you are helpful"), User("hi"), Assistant("ok")}); err != nil {
		t.Errorf("Validate = %v, want nil", err)
	}
}

func TestValidate_MSG15_ResultFromEarlierTurn(t *testing.T) {
	msgs := []Message{
		assistantCall("c1"),
		User("next"),
		ToolResult("c1", "late"),
	}
	if err := Validate(msgs); err == nil {
		t.Fatal("Validate = nil, want error")
	}
}

func FuzzValidate(f *testing.F) {
	f.Add([]byte("user"))
	f.Add([]byte("tool"))
	f.Fuzz(func(t *testing.T, data []byte) {
		// Build a deterministic message slice from the bytes; the point is only
		// that Validate never panics on arbitrary content.
		roles := []Role{RoleSystem, RoleUser, RoleAssistant, RoleTool}
		msgs := make([]Message, 0, len(data))
		for i, b := range data {
			m := Message{Role: roles[int(b)%len(roles)], Content: string(b)}
			switch i % 3 {
			case 0:
				m.ToolCalls = []ToolCall{{ID: string(rune('a' + int(b)%26)), Function: FunctionCall{Name: "x"}}}
			case 1:
				m.ToolCallID = string(rune('a' + int(b)%26))
			}
			msgs = append(msgs, m)
		}
		_ = Validate(msgs)
	})
}
