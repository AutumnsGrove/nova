package agent

import (
	"context"

	"github.com/AutumnsGrove/nova/message"
)

// Run runs the agent loop over history alone: it builds an Agent and appends no
// prompt, so history is the whole model-facing transcript. Code that has a
// prompt should use Agent.Run, which also takes it.
func Run(ctx context.Context, cfg Config, history []message.Message) (*Result, error) {
	a := New(cfg)
	return a.run(ctx, history, nil)
}
