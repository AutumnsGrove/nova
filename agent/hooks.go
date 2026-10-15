package agent

import (
	"context"
	"time"
)

// defaultHookTimeout bounds every hook call when Config.HookTimeout is unset.
const defaultHookTimeout = 5 * time.Second

// runHook invokes one hook under its own deadline and a panic guard. It runs the
// hook in a goroutine so a hung hook cannot stall the loop, and it returns
// ok=false (with the zero value R) when the hook panics, times out, or the run
// is cancelled. A panic or timeout is reported with an extension_error.
func runHook[R any](r *run, name string, fn func(context.Context) R) (R, bool) {
	return runHookUnder(r, r.ctx, name, fn)
}

// runHookUnder is runHook with an explicit parent context. AgentEnd uses it to
// keep running after an abort: the run's own context is cancelled by then, and a
// hook that is skipped for being cancelled is a hook that cannot observe the end.
func runHookUnder[R any](r *run, parent context.Context, name string, fn func(context.Context) R) (R, bool) {
	timeout := r.cfg.HookTimeout
	if timeout <= 0 {
		timeout = defaultHookTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	ch := make(chan R, 1)
	panicked := false
	go func() {
		defer func() {
			if recover() != nil {
				panicked = true
				var zero R
				ch <- zero
			}
		}()
		ch <- fn(ctx)
	}()

	var zero R
	select {
	case v := <-ch:
		if panicked {
			r.extensionError(name, "hook panicked")
			return zero, false
		}
		return v, true
	case <-ctx.Done():
		// A cancelled run is not an extension fault; a deadline is.
		if parent.Err() == nil {
			r.extensionError(name, "hook exceeded its deadline")
		}
		return zero, false
	}
}
