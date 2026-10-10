package testkit

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/provider"
)

// wireChunk is the subset of the SSE payload the tests inspect.
type wireChunk struct {
	Choices []struct {
		Delta struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			Reasoning string `json:"reasoning_content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage map[string]any `json:"usage"`
}

// postTurn sends one request to the server and returns the status, headers,
// and body.
func postTurn(t *testing.T, s *Server) (int, http.Header, string) {
	t.Helper()
	resp, err := http.Post(s.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"m"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, resp.Header, string(body)
}

// dataLines returns the payload of each `data:` line.
func dataLines(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "data: ") {
			out = append(out, strings.TrimPrefix(line, "data: "))
		}
	}
	return out
}

func TestServer_TKS01_ServesValidSSE(t *testing.T) {
	s := NewServer(t, ServerTurn{Turn: Turn{Text: "hello"}})
	status, header, body := postTurn(t, s)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if ct := header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	lines := dataLines(body)
	if len(lines) == 0 || lines[len(lines)-1] != "[DONE]" {
		t.Fatalf("data lines = %v, want a final [DONE]", lines)
	}
	content := ""
	for _, line := range lines {
		if line == "[DONE]" {
			continue
		}
		var chunk wireChunk
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			t.Fatalf("data line is not JSON: %v\n%s", err, line)
		}
		for _, c := range chunk.Choices {
			content += c.Delta.Content
		}
	}
	if content != "hello" {
		t.Errorf("streamed content = %q, want hello", content)
	}
}

func TestServer_TKS02_ToolCallsStreamedAsDeltas(t *testing.T) {
	s := NewServer(t, ServerTurn{Turn: Turn{ToolCalls: []Call{{
		ID: "c1", Name: "read", SplitArgs: []string{`{"a":`, "1", "}"},
	}}}})
	_, _, body := postTurn(t, s)

	var names, args []string
	for _, line := range dataLines(body) {
		if line == "[DONE]" {
			continue
		}
		var chunk wireChunk
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			t.Fatalf("bad chunk: %v", err)
		}
		for _, c := range chunk.Choices {
			for _, tc := range c.Delta.ToolCalls {
				if tc.Index != 0 {
					t.Errorf("tool call index = %d, want 0", tc.Index)
				}
				if tc.Function.Name != "" {
					names = append(names, tc.ID+":"+tc.Function.Name)
				}
				if tc.Function.Arguments != "" {
					args = append(args, tc.Function.Arguments)
				}
			}
		}
	}
	if len(names) != 1 || names[0] != "c1:read" {
		t.Errorf("name chunks = %v, want [c1:read]", names)
	}
	if len(args) != 3 {
		t.Errorf("argument fragments = %v, want 3", args)
	}
	if joined := strings.Join(args, ""); joined != `{"a":1}` {
		t.Errorf("joined arguments = %q, want {\"a\":1}", joined)
	}
}

func TestServer_TKS03_UsageChunk(t *testing.T) {
	s := NewServer(t, ServerTurn{Turn: Turn{
		Text:  "hi",
		Usage: &provider.Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7},
	}})
	_, _, body := postTurn(t, s)
	found := false
	for _, line := range dataLines(body) {
		if line == "[DONE]" {
			continue
		}
		var chunk wireChunk
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			t.Fatalf("bad chunk: %v", err)
		}
		if chunk.Usage != nil {
			found = true
			if len(chunk.Choices) != 0 {
				t.Errorf("usage chunk choices = %v, want empty", chunk.Choices)
			}
			if chunk.Usage["prompt_tokens"] != float64(3) {
				t.Errorf("usage prompt_tokens = %v, want 3", chunk.Usage["prompt_tokens"])
			}
		}
	}
	if !found {
		t.Error("no usage chunk was sent")
	}
}

func TestServer_TKS04_FaultCutMidEvent(t *testing.T) {
	s := NewServer(t, ServerTurn{Turn: Turn{Text: "hi"}, Fault: FaultCutMidEvent})
	_, _, body := postTurn(t, s)
	if strings.Contains(body, "[DONE]") {
		t.Errorf("cut-mid-event stream should not complete:\n%s", body)
	}
	lines := dataLines(body)
	if len(lines) == 0 {
		t.Fatal("expected a partial data line")
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &wireChunk{}); err == nil {
		t.Error("final data line parsed as JSON; expected it to be cut off")
	}
}

func TestServer_TKS05_FaultNoDone(t *testing.T) {
	s := NewServer(t, ServerTurn{Turn: Turn{Text: "hi"}, Fault: FaultNoDone})
	_, _, body := postTurn(t, s)
	if strings.Contains(body, "[DONE]") {
		t.Errorf("FaultNoDone should omit [DONE]:\n%s", body)
	}
	if len(dataLines(body)) == 0 {
		t.Error("FaultNoDone should still send events")
	}
}

func TestServer_TKS06_FaultMalformedJSON(t *testing.T) {
	s := NewServer(t, ServerTurn{Turn: Turn{Text: "hi"}, Fault: FaultMalformedJSON})
	_, _, body := postTurn(t, s)
	malformed := false
	for _, line := range dataLines(body) {
		if line == "[DONE]" {
			continue
		}
		if json.Unmarshal([]byte(line), &wireChunk{}) != nil {
			malformed = true
		}
	}
	if !malformed {
		t.Errorf("expected a malformed data line:\n%s", body)
	}
}

func TestServer_TKS07_FaultStall(t *testing.T) {
	s := NewServer(t, ServerTurn{Turn: Turn{Text: "hi"}, Fault: FaultStall})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.URL+"/v1/chat/completions", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("headers did not arrive: %v", err)
	}
	defer resp.Body.Close()
	if _, err := io.ReadAll(resp.Body); err == nil {
		t.Error("expected the body read to time out")
	}
}

func TestServer_TKS08_Fault429(t *testing.T) {
	s := NewServer(t, ServerTurn{Turn: Turn{Text: "hi"}, Fault: Fault429})
	status, header, _ := postTurn(t, s)
	if status != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", status)
	}
	if got := header.Get("Retry-After"); got != "1" {
		t.Errorf("Retry-After = %q, want 1", got)
	}
}

func TestServer_TKS09_Fault500(t *testing.T) {
	s := NewServer(t, ServerTurn{Turn: Turn{Text: "hi"}, Fault: Fault500})
	status, _, body := postTurn(t, s)
	if status != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", status)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Errorf("500 body is not JSON: %v\n%s", err, body)
	}
}

func TestServer_TKS10_FaultHTML(t *testing.T) {
	s := NewServer(t, ServerTurn{Turn: Turn{Text: "hi"}, Fault: FaultHTML})
	status, header, body := postTurn(t, s)
	if status != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", status)
	}
	if ct := header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(body, "<html") {
		t.Errorf("body is not HTML: %s", body)
	}
}

func TestServer_TKS11_FaultHugeChunk(t *testing.T) {
	s := NewServer(t, ServerTurn{Turn: Turn{Text: "hi"}, Fault: FaultHugeChunk})
	_, _, body := postTurn(t, s)
	big := 0
	for _, line := range dataLines(body) {
		if len(line) > big {
			big = len(line)
		}
	}
	if big < 4<<20 {
		t.Errorf("largest data line = %d bytes, want at least 4 MiB", big)
	}
}

func TestServer_TKS12_RequestsRecorded(t *testing.T) {
	s := NewServer(t,
		ServerTurn{Turn: Turn{Text: "one"}},
		ServerTurn{Turn: Turn{Text: "two"}},
	)
	for i := 0; i < 2; i++ {
		req, err := http.NewRequest(http.MethodPost, s.URL+"/v1/chat/completions", strings.NewReader(`{"model":"m"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Test", "yes")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	got := s.Requests()
	if len(got) != 2 {
		t.Fatalf("recorded %d requests, want 2", len(got))
	}
	for _, r := range got {
		if r.Method != http.MethodPost || r.Path != "/v1/chat/completions" {
			t.Errorf("recorded %s %s", r.Method, r.Path)
		}
		if r.Header.Get("X-Test") != "yes" {
			t.Errorf("header not recorded: %v", r.Header)
		}
		if string(r.Body) != `{"model":"m"}` {
			t.Errorf("body = %s", r.Body)
		}
	}
}

func TestServer_TKS13_ClosesOnCleanup(t *testing.T) {
	var url string
	t.Run("inner", func(t *testing.T) {
		s := NewServer(t, ServerTurn{Turn: Turn{Text: "hi"}})
		url = s.URL
		resp, err := http.Get(url)
		if err != nil {
			t.Fatalf("server not reachable inside the sub-test: %v", err)
		}
		resp.Body.Close()
	})
	if _, err := http.Get(url); err == nil {
		t.Error("server still accepting connections after its cleanup ran")
	}
}

func TestServer_TKS14_UnknownPath(t *testing.T) {
	s := NewServer(t, ServerTurn{Turn: Turn{Text: "hi"}})
	resp, err := http.Get(s.URL + "/nope")
	if err != nil {
		t.Fatalf("GET /nope: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}
