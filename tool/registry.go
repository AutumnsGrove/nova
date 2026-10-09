package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/AutumnsGrove/nova/provider"
)

// Registry holds the tools available to an agent.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
	order []string
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

// Register adds a tool. It returns an error for a nil tool, an empty name, or a
// duplicate name, so wiring mistakes are reported to the caller.
func (r *Registry) Register(t Tool) error {
	if t == nil {
		return fmt.Errorf("tool: cannot register nil tool")
	}
	name := t.Info().Name
	if name == "" {
		return fmt.Errorf("tool: name is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool: duplicate registration for %q", name)
	}
	r.tools[name] = t
	r.order = append(r.order, name)
	return nil
}

// MustRegister is Register with a panic on error, for startup wiring.
func (r *Registry) MustRegister(t Tool) {
	if err := r.Register(t); err != nil {
		panic(err)
	}
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Defs returns the model-facing definitions in registration order.
func (r *Registry) Defs() []provider.ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	defs := make([]provider.ToolDef, 0, len(r.order))
	for _, name := range r.order {
		defs = append(defs, r.tools[name].Info().Def())
	}
	return defs
}

// Names returns the registered tool names in registration order.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.order...)
}

// Execute dispatches one call. Unknown tools and handler errors become failed
// results rather than loop-fatal errors, so the model can self-correct.
func (r *Registry) Execute(ctx context.Context, name string, args json.RawMessage, tctx *Context) Result {
	t, ok := r.Get(name)
	if !ok {
		return Errorf("unknown tool %q", name)
	}
	res, err := t.Run(ctx, args, tctx)
	if err != nil {
		return Errorf("%s: %v", name, err)
	}
	return res
}
