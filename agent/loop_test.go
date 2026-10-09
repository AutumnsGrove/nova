package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/tool"
)

// fakeProvider replays a fixed sequence of responses.
type fakeProvider struct {
	steps []*provider.Response
	i     int
	reqs  []provider.Request
}

func (f *fakeProvider) Name() string { return "fake" }

func (f *fakeProvider) Stream(_ context.Context, req provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error) {
	f.reqs = append(f.reqs, req)
	if f.i >= len(f.steps) {
		return &provider.Response{Message: message.Assistant("(no more scripted responses)")}, nil
	}
	resp := f.steps[f.i]
	f.i++
	if onEvent != nil && resp.Message.Content != "" {
		onEvent(provider.StreamEvent{Type: provider.StreamTextDelta, Text: resp.Message.Content})
	}
	return resp, nil
}

func echoTool(t *testing.T) tool.Tool {
	t.Helper()
	return tool.New(tool.Info{
		Name:        "echo",
		Description: "echo text",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, args json.RawMessage, _ *tool.Context) (tool.Result, error) {
		var in struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return tool.Result{}, err
		}
		return tool.Text("echo:%s", in.Text), nil
	})
}

func TestRunExecutesToolThenAnswers(t *testing.T) {
	first := &provider.Response{
		Message: message.Message{
			Role: message.RoleAssistant,
			ToolCalls: []message.ToolCall{{
				ID:       "call_1",
				Type:     "function",
				Function: message.FunctionCall{Name: "echo", Arguments: `{"text":"hi"}`},
			}},
		},
		FinishReason: "tool_calls",
		Usage:        provider.Usage{PromptTokens: 10, CompletionTokens: 2},
	}
	second := &provider.Response{
		Message:      message.Assistant("done"),
		FinishReason: "stop",
		Usage:        provider.Usage{PromptTokens: 5, CompletionTokens: 1},
	}
	fake := &fakeProvider{steps: []*provider.Response{first, second}}

	reg := tool.NewRegistry()
	if err := reg.Register(echoTool(t)); err != nil {
		t.Fatal(err)
	}

	var results []string
	res, err := Run(context.Background(), Config{
		Provider: fake,
		Model:    "test",
		System:   "sys",
		Tools:    reg,
		OnEvent: func(e Event) {
			if e.Type == EventToolResult {
				results = append(results, e.Result)
			}
		},
	}, []message.Message{message.User("go")})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Answer != "done" {
		t.Fatalf("answer = %q, want %q", res.Answer, "done")
	}
	if res.Turns != 2 {
		t.Fatalf("turns = %d, want 2", res.Turns)
	}
	if res.Usage.PromptTokens != 15 || res.Usage.CompletionTokens != 3 {
		t.Fatalf("usage = %+v, want prompt=15 completion=3", res.Usage)
	}
	if len(results) != 1 || results[0] != "echo:hi" {
		t.Fatalf("tool results = %v, want [echo:hi]", results)
	}

	// The tool result must be in the transcript as a tool-role message.
	found := false
	for _, m := range res.Messages {
		if m.Role == message.RoleTool && m.ToolCallID == "call_1" && m.Content == "echo:hi" {
			found = true
		}
	}
	if !found {
		t.Fatalf("tool result message not found in transcript: %+v", res.Messages)
	}

	// The second request must carry the first assistant turn and the tool result.
	if len(fake.reqs) != 2 {
		t.Fatalf("provider requests = %d, want 2", len(fake.reqs))
	}
	if len(fake.reqs[1].Tools) != 1 || fake.reqs[1].Tools[0].Function.Name != "echo" {
		t.Fatalf("second request tools = %+v, want echo", fake.reqs[1].Tools)
	}
}

func TestRunBeforeToolCallBlocks(t *testing.T) {
	first := &provider.Response{
		Message: message.Message{
			Role: message.RoleAssistant,
			ToolCalls: []message.ToolCall{{
				ID:       "call_1",
				Function: message.FunctionCall{Name: "echo", Arguments: `{"text":"hi"}`},
			}},
		},
	}
	fake := &fakeProvider{steps: []*provider.Response{first}}

	reg := tool.NewRegistry()
	if err := reg.Register(echoTool(t)); err != nil {
		t.Fatal(err)
	}

	res, err := Run(context.Background(), Config{
		Provider: fake,
		Model:    "test",
		Tools:    reg,
		BeforeToolCall: func(ToolCall) *Block {
			return &Block{Reason: "not allowed"}
		},
	}, []message.Message{message.User("go")})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var toolMsg string
	for _, m := range res.Messages {
		if m.Role == message.RoleTool {
			toolMsg = m.Content
		}
	}
	if toolMsg != "error: blocked: not allowed" {
		t.Fatalf("tool message = %q, want blocked error", toolMsg)
	}
}

func TestRunNoToolsAnswersDirectly(t *testing.T) {
	fake := &fakeProvider{steps: []*provider.Response{{Message: message.Assistant("hello")}}}
	res, err := Run(context.Background(), Config{Provider: fake, Model: "test"}, []message.Message{message.User("hi")})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Answer != "hello" || res.Turns != 1 || res.Exhausted {
		t.Fatalf("unexpected result: %+v", res)
	}
}
