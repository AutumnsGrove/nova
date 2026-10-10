package mode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/testkit"
)

// rpcHarness drives ServeRPC over two pipes.
type rpcHarness struct {
	in    *io.PipeWriter
	outR  *io.PipeReader
	out   *bufio.Reader
	errCh chan error
}

// startRPC starts a server loop over pipes.
func startRPC(t *testing.T, s *Server) *rpcHarness {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		err := ServeRPC(context.Background(), s, inR, outW)
		_ = outW.Close()
		errCh <- err
	}()
	t.Cleanup(func() {
		_ = inW.Close()
		_ = outR.Close()
	})
	return &rpcHarness{in: inW, outR: outR, out: bufio.NewReader(outR), errCh: errCh}
}

// disconnect closes both directions of the connection.
func (h *rpcHarness) disconnect() {
	_ = h.in.Close()
	_ = h.outR.Close()
}

// send writes one JSON frame.
func (h *rpcHarness) send(t *testing.T, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := h.in.Write(append(data, '\n')); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// next reads and decodes one frame.
func (h *rpcHarness) next(t *testing.T) proto.Frame {
	t.Helper()
	line, err := h.out.ReadString('\n')
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	frame, err := proto.Decode([]byte(strings.TrimRight(line, "\n")))
	if err != nil {
		t.Fatalf("decode frame %q: %v", line, err)
	}
	return frame
}

// readResponse reads the next response frame.
func (h *rpcHarness) readResponse(t *testing.T) *proto.Response {
	t.Helper()
	for {
		frame := h.next(t)
		if frame.Response != nil {
			return frame.Response
		}
	}
}

// awaitRunEnd reads until run_end and returns it.
func (h *rpcHarness) awaitRunEnd(t *testing.T) *proto.Event {
	t.Helper()
	for {
		frame := h.next(t)
		if frame.Event != nil && frame.Event.Type == proto.EventRunEnd {
			return frame.Event
		}
	}
}

// runRPC drives ServeRPC over a finite input and returns every output frame.
func runRPC(t *testing.T, s *Server, input string) []proto.Frame {
	t.Helper()
	var out bytes.Buffer
	if err := ServeRPC(context.Background(), s, strings.NewReader(input), &out); err != nil {
		t.Fatalf("ServeRPC: %v", err)
	}
	var frames []proto.Frame
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		frame, err := proto.Decode([]byte(line))
		if err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		frames = append(frames, frame)
	}
	return frames
}

func TestRPC_RPC02_ClientSkipsHello(t *testing.T) {
	s, _ := testServer(t)
	frames := runRPC(t, s, `{"type":"get_state","id":"1"}`+"\n")
	if len(frames) < 2 || frames[0].Hello == nil {
		t.Fatalf("frames = %+v, want hello then a response", frames)
	}
	if resp := frames[1].Response; resp == nil || resp.ID != "1" {
		t.Fatalf("response = %+v", frames[1])
	}
}

func TestRPC_RPC03_PromptRoundTrip(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Text: "42"})
	h := startRPC(t, s)
	h.next(t) // hello
	h.send(t, proto.Command{Type: proto.CmdPrompt, ID: "1", Text: "hi"})
	if resp := h.readResponse(t); !resp.OK {
		t.Fatalf("prompt response = %+v", resp)
	}
	if end := h.awaitRunEnd(t); end.Reason != "done" {
		t.Fatalf("run_end = %+v", end)
	}
}

func TestRPC_RPC05_LineTooLong(t *testing.T) {
	s, _ := testServer(t)
	huge := strings.Repeat("a", (17<<20)+1)
	frames := runRPC(t, s, huge+"\n")
	for _, frame := range frames {
		if frame.Response != nil {
			if frame.Response.Error.Code != proto.CodeBadRequest {
				t.Fatalf("response = %+v, want bad_request", frame.Response)
			}
			return
		}
	}
	t.Fatal("no response frame")
}

func TestRPC_RPC06_UnknownType(t *testing.T) {
	s, _ := testServer(t)
	frames := runRPC(t, s, `{"type":"nope","id":"9"}`+"\n")
	for _, frame := range frames {
		if frame.Response != nil {
			if frame.Response.Error.Code != proto.CodeUnsupported {
				t.Fatalf("response = %+v, want unsupported", frame.Response)
			}
			return
		}
	}
	t.Fatal("no response frame")
}

func TestRPC_RPC07_AbortDuringRun(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Hang: true})
	h := startRPC(t, s)
	h.next(t)
	h.send(t, proto.Command{Type: proto.CmdPrompt, ID: "1", Text: "hi"})
	if resp := h.readResponse(t); !resp.OK {
		t.Fatalf("prompt = %+v", resp)
	}
	// Wait for run_start before aborting.
	for {
		frame := h.next(t)
		if frame.Event != nil && frame.Event.Type == proto.EventRunStart {
			break
		}
	}
	h.send(t, proto.Command{Type: proto.CmdAbort, ID: "2"})
	if end := h.awaitRunEnd(t); end.Reason != "aborted" {
		t.Fatalf("run_end = %+v, want aborted", end)
	}
}

func TestRPC_RPC08_OrderedStateChanges(t *testing.T) {
	s, _ := testServer(t)
	input := `{"type":"set_model","id":"1","model":"openai/gpt-6-luna"}` + "\n" +
		`{"type":"set_model","id":"2","model":"anthropic/claude-opus-5.5"}` + "\n"
	frames := runRPC(t, s, input)
	var ids []string
	for _, frame := range frames {
		if frame.Response != nil {
			ids = append(ids, frame.Response.ID)
		}
	}
	if len(ids) != 2 || ids[0] != "1" || ids[1] != "2" {
		t.Fatalf("response ids = %v, want [1 2] in order", ids)
	}
	if s.cfg.Model != "anthropic/claude-opus-5.5" {
		t.Fatalf("model = %q, want the second value applied last", s.cfg.Model)
	}
}

func TestRPC_RPC09_EOF(t *testing.T) {
	s, _ := testServer(t)
	h := startRPC(t, s)
	h.next(t) // hello
	_ = h.in.Close()
	select {
	case err := <-h.errCh:
		if err != nil {
			t.Fatalf("ServeRPC = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ServeRPC did not return on EOF")
	}
}

func TestRPC_RPC10_DisconnectMidRun(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Hang: true})
	h := startRPC(t, s)
	h.next(t)
	h.send(t, proto.Command{Type: proto.CmdPrompt, ID: "1", Text: "hi"})
	h.readResponse(t)
	for {
		frame := h.next(t)
		if frame.Event != nil && frame.Event.Type == proto.EventRunStart {
			break
		}
	}
	_ = h.in.Close()
	_ = h.outR.Close()
	select {
	case <-h.errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("ServeRPC did not return after the client disconnected")
	}
}

func TestRPC_RPC12_CorrelationIDs(t *testing.T) {
	s, _ := testServer(t)
	input := `{"type":"get_state","id":"a"}` + "\n" + `{"type":"get_state","id":"b"}` + "\n"
	frames := runRPC(t, s, input)
	var ids []string
	for _, frame := range frames {
		if frame.Response != nil {
			ids = append(ids, frame.Response.ID)
		}
	}
	if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("response ids = %v, want [a b]", ids)
	}
}

func TestRPC_RPC11_StdoutOnlyFrames(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Text: "42"})
	h := startRPC(t, s)
	h.next(t) // hello
	h.send(t, proto.Command{Type: proto.CmdPrompt, ID: "1", Text: "hi"})
	for {
		frame := h.next(t)
		if frame.Event != nil && frame.Event.Type == proto.EventRunEnd {
			return
		}
	}
}

func TestRPC_RPC13_Backpressure(t *testing.T) {
	chunks := make([]string, 30)
	for i := range chunks {
		chunks[i] = "x"
	}
	s, _ := testServer(t, testkit.Turn{TextChunks: chunks})
	h := startRPC(t, s)
	h.next(t) // hello
	h.send(t, proto.Command{Type: proto.CmdPrompt, ID: "1", Text: "hi"})
	// Read one frame at a time; the server must keep up without deadlocking.
	for {
		frame := h.next(t)
		if frame.Event != nil && frame.Event.Type == proto.EventRunEnd {
			return
		}
	}
}

func TestRPC_RPC14_LargeEvent(t *testing.T) {
	bashArgs := `{"command":"yes x | head -c 1200000"}`
	srv := testkit.NewServer(t,
		testkit.ServerTurn{Turn: testkit.Turn{ToolCalls: []testkit.Call{{ID: "c1", Name: "bash", Args: bashArgs}}}},
		testkit.ServerTurn{Turn: testkit.Turn{Text: "done"}},
	)
	s := serverWithHTTP(t, srv.URL)
	h := startRPC(t, s)
	h.next(t)
	h.send(t, proto.Command{Type: proto.CmdPrompt, ID: "1", Text: "run it"})
	h.readResponse(t)
	sawLarge := false
	maxLen := 0
	for {
		frame := h.next(t)
		if frame.Event == nil {
			continue
		}
		if frame.Event.Type == proto.EventToolEnd && frame.Event.Result != nil {
			if n := len(frame.Event.Result.Content); n > maxLen {
				maxLen = n
			}
			if maxLen >= 1<<20 {
				sawLarge = true
			}
		}
		if frame.Event.Type == proto.EventRunEnd {
			break
		}
	}
	if !sawLarge {
		t.Fatalf("large tool result not intact; largest was %d bytes", maxLen)
	}
}
