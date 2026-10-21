package proto

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
)

// roundTrip marshals v, unmarshals it into a fresh value, and compares the two
// encodings. Comparing encodings avoids time.Time location and monotonic-clock
// differences that reflect.DeepEqual would flag.
func roundTrip[T any](t *testing.T, v T) {
	t.Helper()
	first, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got T
	if err := json.Unmarshal(first, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	second, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("round trip differs:\n first: %s\nsecond: %s", first, second)
	}
}

func TestProto_PRT12_RoundTrip(t *testing.T) {
	zero := 0
	turn := 2
	one := 1
	roundTrip(t, Command{
		Type: CmdPrompt, ID: "1", Text: "hi", Model: "m", Level: "low",
		Tokens: &one, Active: []string{"read"}, Instructions: "i", Session: "s",
		Entry: "e", Name: "n", Args: "a", Command: "ls", Queue: true,
		Attachments: []Attachment{{Kind: "file", Name: "x", Path: "/x", Text: "t"}},
	})
	roundTrip(t, Response{
		Type: FrameResponse, ID: "1", Command: CmdGetState, OK: false,
		Error: &Error{Code: CodeBusy, Message: "busy"},
	})
	roundTrip(t, Response{
		Type: FrameResponse, Command: CmdGetState, OK: true,
		Data: json.RawMessage(`{"model":"m"}`),
	})
	roundTrip(t, Event{
		Type: EventToolEnd, Seq: 7, TS: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC),
		Run: "r", Turn: &turn, CallID: "c1",
		Result:  &ToolResult{Content: "ok", IsError: false, Details: json.RawMessage(`{"a":1}`)},
		Message: &message.Message{Role: message.RoleAssistant, Content: "hi"},
		Usage:   &provider.Usage{PromptTokens: 1, CompletionTokens: 2, ReasoningTokens: 3, TotalTokens: 3},
		Total:   &provider.Usage{TotalTokens: 3},
		Reason:  "done", Attempt: 1, Max: 3, DelayMS: 100,
		Error: &Error{Code: CodeTimeout, Message: "t"},
		Entry: "e", Replaced: []string{"a", "b"}, TokensBefore: 10, TokensAfter: 5,
		Steering: &zero, FollowUp: &zero, Ms: 5, Tokens: &one,
		Extension: "ext", Hook: "tool_call", Session: "s", Leaf: "l",
		State: json.RawMessage(`{"x":1}`), Decision: "allow",
	})
	roundTrip(t, UIRequest{
		Type: FrameUIRequest, ID: "u", Method: "select", Title: "t", Message: "m",
		Default: boolPtr(true), TimeoutMS: &one, Remember: true,
		Options: []UIOption{{ID: "a", Label: "A", Description: "d"}},
		Multi:   true, Placeholder: "p",
		Questions: []UIQuestion{{ID: "q", Prompt: "p", Kind: "single", Options: []UIOption{{ID: "a", Label: "A"}}, Notes: true}},
		AllowSkip: true, Text: "x", Level: "info", Key: "k",
		Segments: []UISegment{{Text: "s", Style: "bold"}}, Placement: "sidebar",
		Content: json.RawMessage(`{"kind":"text"}`),
	})
	roundTrip(t, UIResponse{
		Type: FrameUIResponse, ID: "u", OK: true, IDs: []string{"a"}, Text: "t",
		Answers:   []UIAnswer{{ID: "q", Value: "v", Values: []string{"a"}, Notes: "n", Skipped: true}},
		Cancelled: false, Remember: "session",
	})
	roundTrip(t, Hello{
		Type: FrameHello, Version: 1, Client: "c", Server: "s",
		Capabilities: []string{"ui.confirm"}, Session: "sess", Modes: []string{"tui"},
	})
}

func boolPtr(b bool) *bool { return &b }

// A server-originated event (an error, a session change) carries no timestamp.
// omitempty does not omit a zero time.Time, so without omitzero every such
// event goes out with "ts":"0001-01-01T00:00:00Z", a date no client can use.
func TestProto_PRT19_ZeroTimestampOmitted(t *testing.T) {
	b, err := json.Marshal(Event{Type: EventSessionChanged, Session: "s"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(b, []byte(`"ts"`)) {
		t.Fatalf("event = %s, want no ts for a zero time", b)
	}
	stamped, err := json.Marshal(Event{Type: EventSessionChanged, TS: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)})
	if err != nil || !bytes.Contains(stamped, []byte(`"ts":"2026-10-09T12:00:00Z"`)) {
		t.Fatalf("stamped event = %s err = %v, want its ts kept", stamped, err)
	}
}
