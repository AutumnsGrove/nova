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
	timeout := r.cfg.HookTimeout
	if timeout <= 0 {
		timeout = defaultHookTimeout
	}
	ctx, cancel := context.WithTimeout(r.ctx, timeout)
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

	select {
	case v := <-ch:
		if panicked {
			r.extensionError(name, "hook panicked")
			var zero R
			return zero, false
		}
		return v, true
	case <-ctx.Done():
		// A cancelled run is not an extension fault; a deadline is.
		if r.ctx.Err() == nil {
			r.extensionError(name, "hook exceeded its deadline")
		}
		var zero R
		return zero, false
	}
}
