package mode

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/AutumnsGrove/nova/diag"
	"github.com/AutumnsGrove/nova/proto"
)

// ServeRPC runs the stdio JSONL loop: a hello frame, then commands and events.
// All writes happen in this goroutine (or under one mutex for UI requests), so
// no frame is written after ServeRPC returns.
func ServeRPC(ctx context.Context, s *Server, in io.Reader, out io.Writer) error {
	writer := proto.NewWriter(out)
	var writeMu sync.Mutex
	write := func(v any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return writer.Write(v)
	}

	hello := proto.Hello{
		Type:    proto.FrameHello,
		Version: proto.Version,
		Server:  "nova " + s.opt.Version,
		Session: s.sessionID(),
		Modes:   []string{"rpc"},
	}
	if err := write(hello); err != nil {
		return err
	}

	// Until the client's hello arrives it can answer nothing, so permission
	// asks resolve headless.
	s.uiMu.Lock()
	s.uiCaps = map[string]bool{}
	s.uiHandler = func(_ context.Context, req proto.UIRequest) (proto.UIResponse, error) {
		if err := write(req); err != nil {
			return proto.UIResponse{}, err
		}
		// The answer arrives as a separate ui_response frame and is routed by
		// AnswerUI, so return nothing for callUI to use now.
		return proto.UIResponse{}, nil
	}
	s.uiMu.Unlock()

	events, cancel := s.Subscribe(0)
	defer cancel()

	type readResult struct {
		line []byte
		err  error
	}
	readCh := make(chan readResult, 16)
	go func() {
		defer diag.Catch("rpc reader")
		reader := proto.NewReader(in, 0)
		for {
			line, err := reader.Next()
			if err != nil {
				readCh <- readResult{err: err}
				return
			}
			readCh <- readResult{line: line}
		}
	}()

	stop := func() {
		s.agent.Abort()
		cancel()
		_ = s.Close()
	}

	for {
		select {
		case <-ctx.Done():
			stop()
			return nil
		case result := <-readCh:
			if result.err != nil {
				if errors.Is(result.err, proto.ErrLineTooLong) {
					_ = write(failResponse(proto.Command{}, proto.CodeBadRequest, "protocol line too long"))
				}
				stop()
				return nil
			}
			s.handleFrame(ctx, result.line, write)
		case ev, ok := <-events:
			if !ok {
				stop()
				return nil
			}
			if err := write(ev); err != nil {
				stop()
				return nil
			}
		}
	}
}

// handleFrame decodes and runs one input line.
func (s *Server) handleFrame(ctx context.Context, line []byte, write func(any) error) {
	frame, err := proto.Decode(line)
	if err != nil {
		var unknown *proto.UnknownTypeError
		if errors.As(err, &unknown) {
			_ = write(failResponse(proto.Command{Type: unknown.Type, ID: unknown.ID}, proto.CodeUnsupported, "unsupported frame type "+unknown.Type))
			return
		}
		_ = write(failResponse(proto.Command{}, proto.CodeBadRequest, err.Error()))
		return
	}
	switch {
	case frame.Command != nil:
		_ = write(s.Handle(ctx, *frame.Command))
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
		_ = write(failResponse(proto.Command{}, proto.CodeUnsupported, "only commands are accepted"))
	}
}
