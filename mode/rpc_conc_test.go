package mode

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
)

// frameWithin reads the next frame, failing the test if none arrives in d. A
// server that stops writing would otherwise hang the test until its timeout.
func (h *rpcHarness) frameWithin(t *testing.T, d time.Duration) proto.Frame {
	t.Helper()
	type result struct {
		frame proto.Frame
		err   error
	}
	got := make(chan result, 1)
	go func() {
		line, err := h.out.ReadString('\n')
		if err != nil {
			got <- result{err: err}
			return
		}
		frame, err := proto.Decode([]byte(strings.TrimRight(line, "\n")))
		got <- result{frame: frame, err: err}
	}()
	select {
	case r := <-got:
		if r.err != nil {
			t.Fatalf("read frame: %v", r.err)
		}
		return r.frame
	case <-time.After(d):
		t.Fatal("no frame arrived: the server stopped writing")
		return proto.Frame{}
	}
}

// gatedProvider emits n text deltas, tells the test it has, then holds the run
// open until released or cancelled.
type gatedProvider struct {
	n       int
	emitted chan struct{}
	release chan struct{}
}

func (g *gatedProvider) Name() string { return "gated" }

func (g *gatedProvider) Stream(ctx context.Context, _ provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error) {
	for range g.n {
		onEvent(provider.StreamEvent{Type: provider.StreamTextDelta, Text: "x"})
	}
	close(g.emitted)
	select {
	case <-g.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &provider.Response{Message: message.Assistant(strings.Repeat("x", g.n)), FinishReason: provider.NormalizeFinish("stop")}, nil
}

// gatedServer builds a server over a gatedProvider.
func gatedServer(t *testing.T, g *gatedProvider) *Server {
	t.Helper()
	cfg := config.Defaults()
	cfg.Model = "test/model"
	cfg.Tools.Active = []string{"read"}
	cfg.Compaction.Enabled = boolPtr(false)
	cfg.Extensions.Disable = bundledNames()
	s, err := New(Options{Config: &cfg, CWD: t.TempDir(), Home: t.TempDir(), Provider: g, NoSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// A reload waits for the active run. While it waits, events must keep flowing
// to the client: if the loop that writes them is the one that is blocked, the
// event buffer overflows and the server drops its own connection.
func TestRPC_RPC15_SlowCommandDoesNotStallEvents(t *testing.T) {
	g := &gatedProvider{n: 3 * 64, emitted: make(chan struct{}), release: make(chan struct{})}
	h := startRPC(t, gatedServer(t, g))
	h.next(t) // hello
	h.send(t, proto.Command{Type: proto.CmdPrompt, ID: "1", Text: "go"})
	h.send(t, proto.Command{Type: proto.CmdReload, ID: "2"})

	deltas := 0
	for deltas < g.n {
		frame := h.frameWithin(t, 5*time.Second)
		if frame.Event != nil && frame.Event.Type == proto.EventError {
			t.Fatalf("server raised an error event: %+v", frame.Event.Error)
		}
		if frame.Event != nil && frame.Event.Type == proto.EventTextDelta {
			deltas++
		}
	}
	close(g.release)

	var reloaded, ended bool
	for !reloaded || !ended {
		frame := h.frameWithin(t, 5*time.Second)
		switch {
		case frame.Response != nil && frame.Response.ID == "2":
			reloaded = frame.Response.OK
			if !reloaded {
				t.Fatalf("reload = %+v", frame.Response)
			}
		case frame.Event != nil && frame.Event.Type == proto.EventRunEnd:
			ended = true
		case frame.Event != nil && frame.Event.Type == proto.EventError:
			t.Fatalf("server raised an error event: %+v", frame.Event.Error)
		}
	}
}

// abort is the one command that must not wait in line: the reload ahead of it
// is waiting for the very run the abort ends.
func TestRPC_RPC16_AbortOvertakesBlockedCommand(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Hang: true})
	h := startRPC(t, s)
	h.next(t)
	h.send(t, proto.Command{Type: proto.CmdPrompt, ID: "1", Text: "go"})
	for {
		if frame := h.frameWithin(t, 5*time.Second); frame.Event != nil && frame.Event.Type == proto.EventRunStart {
			break
		}
	}
	h.send(t, proto.Command{Type: proto.CmdReload, ID: "2"})
	h.send(t, proto.Command{Type: proto.CmdAbort, ID: "3"})

	seen := map[string]bool{}
	for len(seen) < 3 {
		frame := h.frameWithin(t, 5*time.Second)
		switch {
		case frame.Response != nil && frame.Response.ID == "2" && frame.Response.OK:
			seen["reload"] = true
		case frame.Response != nil && frame.Response.ID == "3" && frame.Response.OK:
			seen["abort"] = true
		case frame.Event != nil && frame.Event.Type == proto.EventRunEnd && frame.Event.Reason == "aborted":
			seen["run_end"] = true
		}
	}
}

// Input that ends, or a context that is cancelled, must leave no goroutine
// behind, including the reader that was holding lines the loop never took.
func TestRPC_RPC17_NoGoroutineAfterReturn(t *testing.T) {
	testkit.NoLeaks(t)
	s, _ := testServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	input := strings.Repeat(`{"type":"get_state"}`+"\n", 200)
	var out strings.Builder
	if err := ServeRPC(ctx, s, strings.NewReader(input), &out); err != nil {
		t.Fatalf("ServeRPC = %v", err)
	}
}

// The server hello advertises what the server can do.
func TestRPC_RPC18_HelloCapabilities(t *testing.T) {
	s, _ := testServer(t)
	frames := runRPC(t, s, "")
	if len(frames) == 0 || frames[0].Hello == nil {
		t.Fatalf("frames = %+v, want a hello first", frames)
	}
	if got := strings.Join(frames[0].Hello.Capabilities, ","); got != "steer,fork,compact" {
		t.Fatalf("capabilities = %q, want steer,fork,compact", got)
	}
}

// A UI request raised after the connection closed must not be written to it.
func TestRPC_RPC19_NoWriteAfterReturn(t *testing.T) {
	s, _ := testServer(t)
	var mu sync.Mutex
	var out strings.Builder
	w := &lockedWriter{mu: &mu, b: &out}
	if err := ServeRPC(context.Background(), s, strings.NewReader(""), w); err != nil {
		t.Fatalf("ServeRPC = %v", err)
	}
	mu.Lock()
	before := out.Len()
	mu.Unlock()
	s.uiMu.Lock()
	h := s.uiHandler
	s.uiMu.Unlock()
	if h == nil {
		t.Fatal("the connection removed its UI handler")
	}
	if _, err := h(context.Background(), proto.UIRequest{Type: proto.FrameUIRequest, ID: "late", Method: "ui.confirm"}); err == nil {
		t.Fatal("a late UI request was accepted by a closed connection")
	}
	mu.Lock()
	defer mu.Unlock()
	if out.Len() != before {
		t.Fatalf("wrote %q after ServeRPC returned", out.String()[before:])
	}
}

// lockedWriter lets the test read the buffer while another goroutine may write.
type lockedWriter struct {
	mu *sync.Mutex
	b  *strings.Builder
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}
