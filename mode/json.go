package mode

import (
	"context"
	"encoding/json"
	"io"

	"github.com/AutumnsGrove/nova/proto"
)

// RunJSON writes every event as one JSONL line until run_end. Nothing else is
// written to out. The return value is the process exit code.
func RunJSON(ctx context.Context, s *Server, prompt string, out io.Writer) int {
	events, cancel := s.Subscribe(256)
	defer cancel()

	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)

	resp := s.Handle(ctx, proto.Command{Type: proto.CmdPrompt, Text: prompt})
	if !resp.OK {
		_ = enc.Encode(proto.Event{Type: proto.EventError, Error: resp.Error})
		return exitForError(resp.Error.Code)
	}

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
			_ = enc.Encode(ev)
			if ev.Type == proto.EventRunEnd {
				if interrupted {
					return 130
				}
				return exitForReason(ev.Reason)
			}
		}
	}
}
