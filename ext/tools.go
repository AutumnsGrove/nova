package ext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/AutumnsGrove/nova/tool"
)

// protocolTool adapts a registered subprocess tool to tool.Tool.
type protocolTool struct {
	host *Host
	inst *instance
	def  RegisterTool
}

// Info returns the model-facing definition from the register frame.
func (t *protocolTool) Info() tool.Info {
	return tool.Info{
		Name:        t.def.Name,
		Description: t.def.Description,
		Parameters:  t.def.Parameters,
		ReadOnly:    t.def.ReadOnly,
		Destructive: t.def.Destructive,
		OpenWorld:   t.def.OpenWorld,
		Parallel:    t.def.Parallel,
	}
}

// Run sends one tool_call and waits for its result. A timeout, a crash, or a
// cancellation is an error result, never a hang.
func (t *protocolTool) Run(ctx context.Context, args json.RawMessage, tctx *tool.Context) (tool.Result, error) {
	timeout := defaultToolCallTimeout
	if t.def.TimeoutMS > 0 {
		timeout = time.Duration(t.def.TimeoutMS) * time.Millisecond
	}
	callID := ""
	if tctx != nil {
		callID = tctx.SessionID
	}
	reply, err := t.inst.conn.request(ctx, &outFrame{
		Type:       "tool_call",
		Name:       t.def.Name,
		Arguments:  args,
		CallID:     callID,
		DeadlineMS: int(timeout / time.Millisecond),
	}, timeout)
	if err != nil {
		return tool.Errorf("%s", t.failure(err, timeout)), nil
	}
	if reply.Error != nil {
		return tool.Errorf("extension %s: %s", t.inst.manifest.Name, reply.Error.Message), nil
	}
	return tool.Result{Content: reply.Content, IsError: reply.IsError, Details: reply.Details, Parts: reply.Parts}, nil
}

// failure renders the error result text for a failed request.
func (t *protocolTool) failure(err error, timeout time.Duration) string {
	switch {
	case errors.Is(err, errDeadline):
		return fmt.Sprintf("extension %s did not answer in %s", t.inst.manifest.Name, timeout)
	case errors.Is(err, errCancelled):
		return "cancelled"
	default:
		return fmt.Sprintf("extension %s exited", t.inst.manifest.Name)
	}
}
