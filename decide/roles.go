package decide

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

// RolesConfig builds a Roles decorator.
type RolesConfig struct {
	Decider Decider
	// Roles maps an application role name to its model and bounds.
	Roles map[string]Role
	// Clock drives a role's timeout; nil means the wall clock.
	Clock Clock
	// AllowAliases permits ids that end in "-latest" or start with "~".
	// False is the default: an alias moves without notice, so a decision that
	// fires on every turn could change model and nobody would see it.
	AllowAliases bool
}

// Roles resolves a role name to a model id and applies the role's timeout and
// fallback. A request whose Model contains a slash is treated as a direct model
// id and passes through; a bare unknown name is a configuration error naming the
// role, never a 400 from the wire.
type Roles struct {
	decider      Decider
	roles        map[string]Role
	clock        Clock
	allowAliases bool
}

// NewRoles builds the decorator. It does not validate the table, because a bad
// role is a configuration error the caller should see when it is used, not a
// crash at startup.
func NewRoles(cfg RolesConfig) *Roles {
	clock := cfg.Clock
	if clock == nil {
		clock = realClock{}
	}
	roles := make(map[string]Role, len(cfg.Roles))
	for name, role := range cfg.Roles {
		roles[name] = role
	}
	return &Roles{
		decider:      cfg.Decider,
		roles:        roles,
		clock:        clock,
		allowAliases: cfg.AllowAliases,
	}
}

// Decide resolves req.Model, then calls the decider with the role's timeout and
// fallback. Usage from a failed attempt is never lost: it is summed into the
// response, which is returned even alongside the first error.
func (r *Roles) Decide(ctx context.Context, req Request) (*Response, error) {
	models, timeout, err := r.resolve(req.Model)
	if err != nil {
		return nil, err
	}
	var firstErr error
	var usage provider.Usage
	for _, model := range models {
		attempt := req
		attempt.Model = model
		resp, err := r.attempt(ctx, attempt, timeout)
		if resp != nil {
			usage.Add(resp.Usage)
		}
		if err == nil && resp == nil {
			err = errors.New("decide: decider returned no response")
		}
		if err == nil {
			out := *resp
			out.Usage = usage
			return &out, nil
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
		firstErr = errors.New("decide: no model resolved")
	}
	return &Response{Usage: usage}, firstErr
}

// resolve returns the model chain and the per-attempt timeout for a role name or
// a direct model id.
func (r *Roles) resolve(name string) ([]string, time.Duration, error) {
	if strings.TrimSpace(name) == "" {
		return nil, 0, errors.New("decide: model is required")
	}
	role, ok := r.roles[name]
	if !ok {
		if !strings.Contains(name, "/") {
			return nil, 0, fmt.Errorf("decide: unknown role %q", name)
		}
		if err := r.checkModel(fmt.Sprintf("model %q", name), name); err != nil {
			return nil, 0, err
		}
		return []string{name}, 0, nil
	}
	if role.Model == "" {
		return nil, 0, fmt.Errorf("decide: role %q has no model", name)
	}
	if err := r.checkModel(fmt.Sprintf("role %q", name), role.Model); err != nil {
		return nil, 0, err
	}
	models := []string{role.Model}
	if role.Fallback != "" {
		if err := r.checkModel(fmt.Sprintf("role %q fallback", name), role.Fallback); err != nil {
			return nil, 0, err
		}
		models = append(models, role.Fallback)
	}
	return models, role.Timeout, nil
}

// checkModel refuses an alias unless the caller explicitly allowed one.
func (r *Roles) checkModel(where, id string) error {
	if r.allowAliases || !isAlias(id) {
		return nil
	}
	return fmt.Errorf("decide: %s uses alias %q; pin a version-level id, or set decide.allow_aliases to permit it", where, id)
}

// isAlias reports the two alias spellings OpenRouter uses: a "~" prefix and a
// "-latest" suffix.
func isAlias(id string) bool {
	return strings.HasPrefix(id, "~") || strings.HasSuffix(id, "-latest")
}

// errTimeout marks the role's own timer, so a context cancellation caused by it
// is reported as a retryable timeout rather than a caller cancellation.
var errTimeout = errors.New("decide: role timeout")

// attempt runs one model under the role's timeout. The timer is driven by the
// injected clock, so a test never sleeps on real time. The decider must honor
// ctx; waiting for it after a cancel is what keeps the goroutine from leaking.
func (r *Roles) attempt(ctx context.Context, req Request, timeout time.Duration) (*Response, error) {
	if r.decider == nil {
		return nil, errors.New("decide: no decider configured")
	}
	if timeout <= 0 {
		return r.decider.Decide(ctx, req)
	}

	child, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-r.clock.After(timeout):
			cancel(errTimeout)
		case <-stop:
		case <-child.Done():
		}
	}()

	resp, err := r.decider.Decide(child, req)
	if err == nil {
		return resp, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			return resp, timeoutError(ctxErr)
		}
		return resp, ctxErr
	}
	if errors.Is(context.Cause(child), errTimeout) {
		return resp, timeoutError(context.DeadlineExceeded)
	}
	return resp, err
}

// timeoutError is the typed, retryable error a timeout produces. It reuses
// provider.Error so a caller classifies a decision timeout the same way it
// classifies a chat timeout.
func timeoutError(cause error) *provider.Error {
	return &provider.Error{
		Code:      "timeout",
		Message:   "decision request timed out",
		Retryable: true,
		Err:       cause,
	}
}
