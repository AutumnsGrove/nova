package decide

import (
	"context"
	"errors"

	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/roles"
)

// Clock is the time seam for a role's timeout. It is the shared roles.Clock, so
// a caller can inject one clock for every primitive.
type Clock = roles.Clock

// Role maps one application role to a model id and its bounds. Timeout is per
// attempt; zero means only the caller's context bounds the call. Fallback is
// tried when the primary fails and the caller's context is still live.
type Role = roles.Role

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
// role, never a 400 from the wire. The resolver itself lives in roles, because
// every primitive that maps an application name to a model needs the same rules.
type Roles struct {
	decider Decider
	table   *roles.Table
}

// NewRoles builds the decorator. It does not validate the table, because a bad
// role is a configuration error the caller should see when it is used, not a
// crash at startup.
func NewRoles(cfg RolesConfig) *Roles {
	table := make(map[string]roles.Role, len(cfg.Roles))
	for name, role := range cfg.Roles {
		table[name] = role
	}
	return &Roles{
		decider: cfg.Decider,
		table: roles.New(roles.Config{
			Roles:        table,
			Clock:        cfg.Clock,
			AllowAliases: cfg.AllowAliases,
			Subsystem:    "decide",
		}),
	}
}

// Decide resolves req.Model, then calls the decider with the role's timeout and
// fallback. Usage from a failed attempt is never lost: it is summed into the
// response, which is returned even alongside the first error.
func (r *Roles) Decide(ctx context.Context, req Request) (*Response, error) {
	var answer *Response
	usage, err := r.table.Do(ctx, req.Model, func(ctx context.Context, model string) (provider.Usage, error) {
		if r.decider == nil {
			return provider.Usage{}, errors.New("decide: no decider configured")
		}
		attempt := req
		attempt.Model = model
		resp, err := r.decider.Decide(ctx, attempt)
		if resp == nil {
			if err == nil {
				err = errors.New("decide: decider returned no response")
			}
			return provider.Usage{}, err
		}
		answer = resp
		return resp.Usage, err
	})
	if err != nil {
		return &Response{Usage: usage}, err
	}
	out := *answer
	out.Usage = usage
	return &out, nil
}
