package mode

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/AutumnsGrove/nova/proto"
)

// ServeRPC runs the stdio JSONL loop: a hello frame, then commands and events.
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

	events, cancel := s.Subscribe(0)
	defer cancel()

	// Until the client's hello arrives it can answer nothing, so permission
	// asks resolve headless.
	s.uiMu.Lock()
	s.uiCaps = map[string]bool{}
	s.uiHandler = func(ctx context.Context, req proto.UIRequest) (proto.UIResponse, error) {
		if err := write(req); err != nil {
			return proto.UIResponse{}, err
		}
		// The answer arrives as a separate ui_response frame and is routed by
		// AnswerUI, so return nothing for callUI to use now.
		return proto.UIResponse{}, nil
	}
	s.uiMu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range events {
			if err := write(ev); err != nil {
				return
			}
		}
	}()

	reader := proto.NewReader(in, 0)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			line, err := reader.Next()
			if err != nil {
				return
			}
			frame, err := proto.Decode(line)
			if err != nil {
				var unknown *proto.UnknownTypeError
				if errors.As(err, &unknown) {
					_ = write(failResponse(proto.Command{Type: unknown.Type, ID: unknown.ID}, proto.CodeUnsupported, "unsupported frame type "+unknown.Type))
					continue
				}
				_ = write(failResponse(proto.Command{}, proto.CodeBadRequest, err.Error()))
				continue
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
	}()

	select {
	case <-ctx.Done():
	case <-readDone:
	}
	s.agent.Abort()
	cancel()
	<-done
	_ = s.Close()
	return nil
}
