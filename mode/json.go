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
	// A line that cannot be written (a closed pipe) must not stop the run from
	// being drained to its run_end, so the encode error is dropped on purpose.
	return driveRun(ctx, s, events, func(ev proto.Event) { _ = enc.Encode(ev) })
}
