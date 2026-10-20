package mode

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/AutumnsGrove/nova/diag"
	"github.com/AutumnsGrove/nova/proto"
)

// serverCapabilities is what the server advertises in its hello frame.
var serverCapabilities = []string{"steer", "fork", "compact"}

// rpcQueueSize bounds the commands waiting behind a slow one. A client that
// sends more waits, because the loop stops reading until there is room.
// rpcEventBuffer is how many events a client may fall behind before it is
// dropped: one text delta is one event, so a burst from a fast model can pass
// the default of 64 while a healthy client is only a moment behind.
const (
	rpcQueueSize   = 64
	rpcEventBuffer = 1024
)

// rpcConn is one stdio connection. Everything it writes goes through write, so
// frames never interleave and none is written once the connection is closed.
type rpcConn struct {
	mu     sync.Mutex
	w      *proto.Writer
	closed bool
}

func (c *rpcConn) write(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return io.ErrClosedPipe
	}
	return c.w.Write(v)
}

// ServeRPC runs the stdio JSONL loop: a hello frame, then commands and events.
// Events are written by their own goroutine, so a slow command (a reload that
// waits for a run, a long bash) never stalls them and never gets the
// subscriber dropped. Commands run one at a time in arrival order; abort alone
// skips the line, so it can end the run a blocked command is waiting for. No
// frame is written after ServeRPC returns.
func ServeRPC(ctx context.Context, s *Server, in io.Reader, out io.Writer) error {
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	c := &rpcConn{w: proto.NewWriter(out)}
	hello := proto.Hello{
		Type:         proto.FrameHello,
		Version:      proto.Version,
		Server:       "nova " + s.opt.Version,
		Session:      s.sessionID(),
		Modes:        []string{"rpc"},
		Capabilities: serverCapabilities,
	}
	if err := c.write(hello); err != nil {
		return err
	}

	// Until the client's hello arrives it can answer nothing, so permission
	// asks resolve headless. The answer to a request is a separate ui_response
	// frame that AnswerUI routes, so the handler has nothing to return.
	s.attachUI(map[string]bool{}, func(_ context.Context, req proto.UIRequest) (proto.UIResponse, error) {
		return proto.UIResponse{}, c.write(req)
	})

	events, unsubscribe := s.Subscribe(rpcEventBuffer)
	var wg sync.WaitGroup
	wg.Go(func() {
		defer stop()
		for ev := range events {
			if c.write(ev) != nil {
				return
			}
		}
	})
	cmds := make(chan proto.Command, rpcQueueSize)
	cmdsDone := make(chan struct{})
	go func() {
		defer close(cmdsDone)
		for cmd := range cmds {
			// After the connection ends, commands still queued are dropped:
			// starting a run now would outlive the server.
			if ctx.Err() == nil {
				_ = c.write(s.Handle(ctx, cmd))
			}
		}
	}()

	lines, readErr := readLines(ctx, in)
	for done := false; !done; {
		select {
		case <-ctx.Done():
			done = true
		case err := <-readErr:
			if errors.Is(err, proto.ErrLineTooLong) {
				_ = c.write(failResponse(proto.Command{}, proto.CodeBadRequest, "protocol line too long"))
			}
			done = true
		case line := <-lines:
			s.handleFrame(ctx, line, c, cmds)
		}
	}

	// At end of input the queued commands still run and get their responses. A
	// cancelled context releases a command blocked in Handle. Only once the
	// command goroutine is done is it safe to abort: a queued prompt could
	// otherwise start a run after the abort.
	close(cmds)
	<-cmdsDone
	s.agent.Abort()
	stop()
	unsubscribe()
	_ = s.Close()
	wg.Wait()
	c.mu.Lock() // from here every write fails
	c.closed = true
	c.mu.Unlock()
	return nil
}

// readLines reads protocol lines on its own goroutine. The goroutine ends at
// the first read error, or when ctx ends while a line waits to be taken.
func readLines(ctx context.Context, in io.Reader) (<-chan []byte, <-chan error) {
	lines := make(chan []byte)
	errs := make(chan error, 1)
	go func() {
		defer diag.Catch("rpc reader")
		reader := proto.NewReader(in, 0)
		for {
			line, err := reader.Next()
			if err != nil {
				errs <- err
				return
			}
			select {
			case lines <- line:
			case <-ctx.Done():
				return
			}
		}
	}()
	return lines, errs
}

// handleFrame decodes and routes one input line. A command other than abort
// joins the queue behind any command still running.
func (s *Server) handleFrame(ctx context.Context, line []byte, c *rpcConn, cmds chan<- proto.Command) {
	frame, err := proto.Decode(line)
	if err != nil {
		var unknown *proto.UnknownTypeError
		if errors.As(err, &unknown) {
			_ = c.write(failResponse(proto.Command{Type: unknown.Type, ID: unknown.ID}, proto.CodeUnsupported, "unsupported frame type "+unknown.Type))
			return
		}
		_ = c.write(failResponse(proto.Command{}, proto.CodeBadRequest, err.Error()))
		return
	}
	switch {
	case frame.Command != nil && frame.Command.Type == proto.CmdAbort:
		_ = c.write(s.Handle(ctx, *frame.Command))
	case frame.Command != nil:
		select {
		case cmds <- *frame.Command:
		case <-ctx.Done():
		}
	case frame.Hello != nil:
		caps := map[string]bool{}
		for _, cap := range frame.Hello.Capabilities {
			caps[cap] = true
		}
		s.uiMu.Lock()
		s.uiCaps = caps
		s.uiMu.Unlock()
	case frame.UIResponse != nil:
		s.AnswerUI(*frame.UIResponse)
	default:
		_ = c.write(failResponse(proto.Command{}, proto.CodeUnsupported, "only commands are accepted"))
	}
}
