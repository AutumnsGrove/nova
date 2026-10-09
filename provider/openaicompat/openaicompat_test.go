package openaicompat

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AutumnsGrove/nova/provider"
)

func TestStreamParsesTextAndToolCalls(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing auth header, got %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("response writer is not a flusher")
			return
		}
		write := func(s string) {
			fmt.Fprint(w, s)
			flusher.Flush()
		}
		write(`data: {"choices":[{"delta":{"content":"Hel"}}]}` + "\n\n")
		write(`data: {"choices":[{"delta":{"content":"lo"}}]}` + "\n\n")
		write(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"echo","arguments":"{\"text\":"}}]}}]}` + "\n\n")
		write(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"hi\"}"}}]}}]}` + "\n\n")
		write(`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"cost":0.0001,"prompt_tokens_details":{"cached_tokens":3}}}` + "\n\n")
		write("data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "test-key"})

	var text string
	var sawToolCall bool
	resp, err := p.Stream(context.Background(), provider.Request{Model: "test/model"}, func(ev provider.StreamEvent) {
		switch ev.Type {
		case provider.StreamTextDelta:
			text += ev.Text
		case provider.StreamToolCall:
			sawToolCall = true
		}
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if gotPath != "/chat/completions" {
		t.Fatalf("path = %q, want /chat/completions", gotPath)
	}
	if text != "Hello" {
		t.Fatalf("streamed text = %q, want Hello", text)
	}
	if resp.Message.Content != "Hello" {
		t.Fatalf("content = %q, want Hello", resp.Message.Content)
	}
	if resp.FinishReason != "tool_calls" {
		t.Fatalf("finish reason = %q", resp.FinishReason)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v, want 1", resp.Message.ToolCalls)
	}
	tc := resp.Message.ToolCalls[0]
	if tc.ID != "call_1" || tc.Function.Name != "echo" || tc.Function.Arguments != `{"text":"hi"}` {
		t.Fatalf("tool call = %+v", tc)
	}
	if !sawToolCall {
		t.Fatal("expected a StreamToolCall event")
	}
	if resp.Usage.PromptTokens != 10 || resp.Usage.CompletionTokens != 5 || resp.Usage.TotalTokens != 15 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	if resp.Usage.CostUSD != 0.0001 || resp.Usage.CachedTokens != 3 {
		t.Fatalf("cost/cached = %+v", resp.Usage)
	}
}

func TestStreamReturnsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad model", http.StatusBadRequest)
	}))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL})
	if _, err := p.Stream(context.Background(), provider.Request{Model: "x"}, nil); err == nil {
		t.Fatal("expected an error for HTTP 400")
	}
}
