package mode

import (
	"context"
	"fmt"
	"io"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
)

// RunPrint runs one prompt and writes only the final assistant answer to out.
// Progress goes to errOut. The return value is the process exit code.
func RunPrint(ctx context.Context, s *Server, prompt string, out, errOut io.Writer) int {
	events, cancel := s.Subscribe(256)
	defer cancel()

	resp := s.Handle(ctx, proto.Command{Type: proto.CmdPrompt, Text: prompt})
	if !resp.OK {
		// A progress line that cannot be written has nowhere better to go.
		_, _ = fmt.Fprintln(errOut, resp.Error.Message)
		return exitForError(resp.Error.Code)
	}

	var answer string
	return driveRun(ctx, s, events, func(ev proto.Event) {
		switch ev.Type {
		case proto.EventToolCall:
			if ev.Call != nil {
				_, _ = fmt.Fprintf(errOut, "· %s\n", ev.Call.Name)
			}
		case proto.EventError:
			if ev.Error != nil {
				_, _ = fmt.Fprintln(errOut, ev.Error.Message)
			}
		case proto.EventMessageEnd:
			if ev.Message != nil && ev.Message.Role == message.RoleAssistant && ev.Message.Content != "" {
				answer = ev.Message.Content
			}
		case proto.EventRunEnd:
			_, _ = fmt.Fprintln(out, answer)
		}
	})
}

// driveRun hands each event to onEvent until run_end and returns the exit code.
// A cancelled ctx aborts the run once and the loop keeps reading, so the run
// still ends with its run_end and the code becomes 130.
func driveRun(ctx context.Context, s *Server, events <-chan proto.Event, onEvent func(proto.Event)) int {
	interrupted := false
	ctxDone := ctx.Done()
	for {
		select {
		case <-ctxDone:
			s.agent.Abort()
			ctxDone = nil
			interrupted = true
		case ev, ok := <-events:
			if !ok {
				return 1
			}
			onEvent(ev)
			if ev.Type == proto.EventRunEnd {
				if interrupted {
					return 130
				}
				return exitForReason(ev.Reason)
			}
		}
	}
}

// exitForReason maps a run_end reason to an exit code.
func exitForReason(reason string) int {
	switch reason {
	case "done":
		return 0
	case "exhausted":
		return 3
	case "aborted":
		return 130
	default:
		return 1
	}
}

// exitForError maps a protocol error code to an exit code.
func exitForError(code string) int {
	if code == proto.CodeBadRequest {
		return 2
	}
	return 1
}
