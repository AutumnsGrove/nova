package embed

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/roles"
)

// Role maps an application role to a model, an optional fallback, a per-attempt
// timeout, and the dimensions the role's vectors must have.
//
// Dims is the reason embeddings need their own decorator rather than the plain
// resolver: a fallback that silently returns different dimensions does not give
// a wrong answer, it corrupts a stored index, so a mismatch is refused instead
// of swapped.
type Role struct {
	Model    string
	Fallback string
	// Timeout is per attempt; zero means only the caller's context bounds it.
	Timeout time.Duration
	// Dims is the expected component count. Zero means unset: nothing is
	// checked, which is only sensible for a direct model id.
	Dims int
}

// RolesConfig builds an embedding Roles decorator.
type RolesConfig struct {
	Embedder Embedder
	// Roles maps an application role name to its model and bounds.
	Roles map[string]Role
	// Clock drives a role's timeout; nil means the wall clock.
	Clock roles.Clock
	// AllowAliases permits ids that end in "-latest" or start with "~". False is
	// the default, and stricter here than for decisions: an alias can move to a
	// model with different dimensions without anything in the request naming it.
	AllowAliases bool
}

// Roles resolves a role name to a model and applies its timeout, fallback and
// dimension check. A request whose Model contains a slash is a direct model id
// and passes through.
type Roles struct {
	embedder Embedder
	table    *roles.Table
	roles    map[string]Role
}

// NewRoles builds the decorator. It does not validate the table, because a bad
// role is a configuration error the caller should see when it is used.
func NewRoles(cfg RolesConfig) *Roles {
	table := make(map[string]roles.Role, len(cfg.Roles))
	copied := make(map[string]Role, len(cfg.Roles))
	for name, role := range cfg.Roles {
		table[name] = roles.Role{Model: role.Model, Fallback: role.Fallback, Timeout: role.Timeout}
		copied[name] = role
	}
	return &Roles{
		embedder: cfg.Embedder,
		roles:    copied,
		table: roles.New(roles.Config{
			Roles:        table,
			Clock:        cfg.Clock,
			AllowAliases: cfg.AllowAliases,
			Subsystem:    "embed",
		}),
	}
}

// DimsError reports a response whose component count disagrees with the role's
// Dims. It names the role, the model that answered, and the role's primary, so
// a mismatch in a fallback is not mistaken for a first model.
type DimsError struct {
	Role    string
	Primary string
	Model   string
	Want    int
	Got     int
}

func (e *DimsError) Error() string {
	if e.Role == "" {
		return fmt.Sprintf("embed: model %q returned %d dimensions, want %d", e.Model, e.Got, e.Want)
	}
	return fmt.Sprintf("embed: role %q: %s returned %d dimensions, want %d (primary %s)", e.Role, e.Model, e.Got, e.Want, e.Primary)
}

// Embed resolves req.Model and calls the embedder with the role's timeout and
// fallback. A dimension mismatch stops the chain immediately: falling back to a
// model with the right shape would mix two vector spaces in one index.
func (r *Roles) Embed(ctx context.Context, req Request) (*Response, error) {
	if r.embedder == nil {
		return nil, errors.New("embed: no embedder configured")
	}
	chain, err := r.table.Resolve(req.Model)
	if err != nil {
		return nil, err
	}
	role := r.roles[req.Model]
	want := role.Dims
	primary := chain.Models[0]

	var answer *Response
	var firstErr error
	var usage provider.Usage
	for _, model := range chain.Models {
		attempt := req
		attempt.Model = model
		u, err := r.table.Attempt(ctx, model, chain.Timeout, func(ctx context.Context, model string) (provider.Usage, error) {
			resp, err := r.embedder.Embed(ctx, attempt)
			if resp == nil {
				if err == nil {
					err = errors.New("embed: embedder returned no response")
				}
				return provider.Usage{}, err
			}
			answer = resp
			if err == nil {
				err = ValidateResponse(attempt, resp)
			}
			if err == nil && want > 0 && resp.Dims != want {
				err = &DimsError{Role: req.Model, Primary: primary, Model: model, Want: want, Got: resp.Dims}
			}
			return resp.Usage, err
		})
		usage.Add(u)
		if err == nil {
			out := *answer
			out.Usage = usage
			return &out, nil
		}
		if firstErr == nil {
			firstErr = err
		}
		// A mismatch is the failure the caller must see: reporting the primary's
		// earlier error would hide the reason the chain stopped.
		var dimsErr *DimsError
		if errors.As(err, &dimsErr) {
			firstErr = err
			break
		}
		if ctx.Err() != nil {
			break
		}
	}
	if firstErr == nil {
		firstErr = errors.New("embed: no model resolved")
	}
	return &Response{Usage: usage}, firstErr
}
