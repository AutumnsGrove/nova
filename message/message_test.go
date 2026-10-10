package message

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestMessage_MSG01_MarshalOmitsEmptyFields(t *testing.T) {
	got, err := json.Marshal(User("hi"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `{"role":"user","content":"hi"}`; string(got) != want {
		t.Errorf("JSON = %s, want %s", got, want)
	}
}

func TestMessage_MSG02_RoundTripWithToolCalls(t *testing.T) {
	original := Message{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{
			{ID: "c1", Type: "function", Function: FunctionCall{Name: "read", Arguments: `{"path":"a"}`}},
			{ID: "c2", Type: "function", Function: FunctionCall{Name: "write", Arguments: `{"path":"b"}`}},
		},
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var restored Message
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(original, restored) {
		t.Errorf("round trip = %+v, want %+v", restored, original)
	}
}

func TestMessage_MSG03_UnknownFieldsIgnored(t *testing.T) {
	var m Message
	if err := json.Unmarshal([]byte(`{"role":"user","content":"x","extra":1}`), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Role != RoleUser || m.Content != "x" {
		t.Errorf("message = %+v, want user/x", m)
	}
}

func TestMessage_MSG04_CloneIsolatesToolCalls(t *testing.T) {
	original := Message{
		Role:      RoleAssistant,
		ToolCalls: []ToolCall{{ID: "c1", Function: FunctionCall{Name: "read", Arguments: "{}"}}},
	}
	clone := original.Clone()
	clone.ToolCalls = append(clone.ToolCalls, ToolCall{ID: "c2", Function: FunctionCall{Name: "write"}})
	if len(original.ToolCalls) != 1 {
		t.Errorf("original ToolCalls len = %d, want 1", len(original.ToolCalls))
	}
	if len(clone.ToolCalls) != 2 {
		t.Errorf("clone ToolCalls len = %d, want 2", len(clone.ToolCalls))
	}
}

func TestMessage_MSG05_ReasoningAndNameSerialize(t *testing.T) {
	m := Message{Role: RoleAssistant, Reasoning: "hmm", Name: "assistant"}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"reasoning":"hmm"`) || !strings.Contains(string(data), `"name":"assistant"`) {
		t.Errorf("JSON should carry reasoning and name: %s", data)
	}

	empty, err := json.Marshal(Message{Role: RoleUser})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(empty), "reasoning") || strings.Contains(string(empty), "name") {
		t.Errorf("empty reasoning and name should be omitted: %s", empty)
	}
}
