package agent

import (
	"context"

	"github.com/AutumnsGrove/nova/message"
)

// Run runs the agent loop over history as the prototype did: it builds an Agent
// and appends nothing, so history is the whole model-facing transcript. New code
// should use Agent.Run, which also takes the prompt message.
func Run(ctx context.Context, cfg Config, history []message.Message) (*Result, error) {
	a := New(cfg)
	return a.run(ctx, history, nil)
}
