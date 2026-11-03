// Package roles resolves an application role name to a model chain and runs one
// attempt per model under the role's timeout.
//
// It exists because more than one primitive needs the same rules: a role maps an
// application name ("oracle") to a model id, an optional fallback and a
// per-attempt timeout, an unknown bare name is a configuration error rather than
// a wire error, and an alias is refused unless it is allowed. decide wraps it;
// the embeddings primitive needs it more strictly, because a fallback that
// changes vector dimensions corrupts a stored index.
package roles

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AutumnsGrove/nova/provider"
)

// Clock is the time seam for a role's timeout, so tests can drive it without
// sleeping on real time. It is deliberately narrower than agent.Clock.
type Clock interface {
	After(d time.Duration) <-chan time.Time
}

// realClock is the wall-clock Clock used when none is injected.
type realClock struct{}

func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Role maps one application role to a model id and its bounds. Timeout is per
// attempt; zero means only the caller's context bounds the call. Fallback is
// tried when the primary fails and the caller's context is still live.
type Role struct {
	Model    string
	Fallback string
	Timeout  time.Duration
}

// Config builds a Table.
type Config struct {
	// Roles maps an application role name to its model and bounds.
	Roles map[string]Role
	// Clock drives a role's timeout; nil means the wall clock.
	Clock Clock
	// AllowAliases permits ids that end in "-latest" or start with "~".
	// False is the default: an alias moves without notice, so a caller that
	// records a model id would record one that no longer names a build.
	AllowAliases bool
	// Subsystem prefixes every error, so a caller still reads "decide: unknown
	// role" rather than "roles: unknown role". Empty means "roles".
	Subsystem string
}

// Chain is the resolved call order for one role or direct model id.
type Chain struct {
	// Models is the primary followed by its optional fallback.
	Models []string
	// Timeout is the per-attempt deadline; zero means the caller's context only.
	Timeout time.Duration
}

// Table resolves role names against a configuration. Building one does not
// validate the table, because a bad role is a configuration error the caller
// should see when it is used.
type Table struct {
	roles        map[string]Role
	clock        Clock
	allowAliases bool
	subsystem    string
}

// New builds a Table with its own copy of the role map, so a later config change
// cannot move a call in flight.
func New(cfg Config) *Table {
	clock := cfg.Clock
	if clock == nil {
		clock = realClock{}
	}
	subsystem := cfg.Subsystem
	if subsystem == "" {
		subsystem = "roles"
	}
	roles := make(map[string]Role, len(cfg.Roles))
	for name, role := range cfg.Roles {
		roles[name] = role
	}
	return &Table{roles: roles, clock: clock, allowAliases: cfg.AllowAliases, subsystem: subsystem}
}

// Resolve returns the model chain and the per-attempt timeout for a role name or
// a direct model id. A name containing a slash is a direct id and passes
// through; a bare unknown name is a configuration error naming the role, never a
// request error from the wire.
func (t *Table) Resolve(name string) (Chain, error) {
	if strings.TrimSpace(name) == "" {
		return Chain{}, fmt.Errorf("%s: model is required", t.subsystem)
	}
	role, ok := t.roles[name]
	if !ok {
		if !strings.Contains(name, "/") {
			return Chain{}, fmt.Errorf("%s: unknown role %q", t.subsystem, name)
		}
		if err := t.checkModel(fmt.Sprintf("model %q", name), name); err != nil {
			return Chain{}, err
		}
		return Chain{Models: []string{name}}, nil
	}
	if role.Model == "" {
		return Chain{}, fmt.Errorf("%s: role %q has no model", t.subsystem, name)
	}
	if err := t.checkModel(fmt.Sprintf("role %q", name), role.Model); err != nil {
		return Chain{}, err
	}
	models := []string{role.Model}
	if role.Fallback != "" {
		if err := t.checkModel(fmt.Sprintf("role %q fallback", name), role.Fallback); err != nil {
			return Chain{}, err
		}
		models = append(models, role.Fallback)
	}
	return Chain{Models: models, Timeout: role.Timeout}, nil
}

// Do resolves name, then runs attempt for each model in order under the role's
// timeout. It stops at the first success and stops immediately when the caller's
// context is done. Usage from a failed attempt is never lost: it is summed into
// the result, which is returned even alongside the first error.
func (t *Table) Do(ctx context.Context, name string, attempt func(ctx context.Context, model string) (provider.Usage, error)) (provider.Usage, error) {
	if attempt == nil {
		return provider.Usage{}, fmt.Errorf("%s: no attempt function", t.subsystem)
	}
	chain, err := t.Resolve(name)
	if err != nil {
		return provider.Usage{}, err
	}
	var firstErr error
	var usage provider.Usage
	for _, model := range chain.Models {
		u, err := t.Attempt(ctx, model, chain.Timeout, attempt)
		usage.Add(u)
		if err == nil {
			return usage, nil
		}
		if firstErr == nil {
			firstErr = err
		}
		// A cancelled caller is not an availability problem; do not spend a
		// second request on it.
		if ctx.Err() != nil {
			break
		}
	}
	if firstErr == nil {
		firstErr = fmt.Errorf("%s: no model resolved", t.subsystem)
	}
	return usage, firstErr
}

// Attempt runs one model under a per-attempt timeout. It is the single-model
// half of Do, for a caller that needs its own stop rule between attempts (the
// embeddings decorator must not fall back after a dimension mismatch).
func (t *Table) Attempt(ctx context.Context, model string, timeout time.Duration, attempt func(ctx context.Context, model string) (provider.Usage, error)) (provider.Usage, error) {
	if attempt == nil {
		return provider.Usage{}, fmt.Errorf("%s: no attempt function", t.subsystem)
	}
	return t.attempt(ctx, model, timeout, attempt)
}

// checkModel refuses an alias unless the caller explicitly allowed one.
func (t *Table) checkModel(where, id string) error {
	if t.allowAliases || !isAlias(id) {
		return nil
	}
	return fmt.Errorf("%s: %s uses alias %q; pin a version-level id, or set %s.allow_aliases to permit it", t.subsystem, where, id, t.subsystem)
}

// isAlias reports the two alias spellings OpenRouter uses: a "~" prefix and a
// "-latest" suffix.
func isAlias(id string) bool {
	return strings.HasPrefix(id, "~") || strings.HasSuffix(id, "-latest")
}

// errTimeout marks the role's own timer, so a context cancellation caused by it
// is reported as a retryable timeout rather than a caller cancellation.
var errTimeout = errors.New("role timeout")

// attempt runs one model under the role's timeout. The timer is driven by the
// injected clock, so a test never sleeps on real time. The attempt function must
// honor ctx; waiting for it after a cancel is what keeps the goroutine from
// leaking.
func (t *Table) attempt(ctx context.Context, model string, timeout time.Duration, fn func(context.Context, string) (provider.Usage, error)) (provider.Usage, error) {
	if timeout <= 0 {
		return fn(ctx, model)
	}

	child, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-t.clock.After(timeout):
			cancel(errTimeout)
		case <-stop:
		case <-child.Done():
		}
	}()

	usage, err := fn(child, model)
	if err == nil {
		return usage, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			return usage, t.timeoutError(ctxErr)
		}
		return usage, ctxErr
	}
	if errors.Is(context.Cause(child), errTimeout) {
		return usage, t.timeoutError(context.DeadlineExceeded)
	}
	return usage, err
}

// timeoutError is the typed, retryable error a timeout produces. It reuses
// provider.Error so a caller classifies a role timeout the same way it
// classifies a chat timeout.
func (t *Table) timeoutError(cause error) *provider.Error {
	return &provider.Error{
		Code:      "timeout",
		Message:   t.subsystem + ": request timed out",
		Retryable: true,
		Err:       cause,
	}
}
