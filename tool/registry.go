package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
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

// validateTool rejects the wiring mistakes Register and Replace share.
func validateTool(t Tool) error {
	if t == nil {
		return fmt.Errorf("tool: cannot register nil tool")
	}
	if t.Info().Name == "" {
		return fmt.Errorf("tool: name is required")
	}
	return nil
}

// Register adds a tool. It returns an error for a nil tool, an empty name, or a
// duplicate name, so wiring mistakes are reported to the caller.
func (r *Registry) Register(t Tool) error {
	if err := validateTool(t); err != nil {
		return err
	}
	name := t.Info().Name
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

// Replace registers a tool, replacing any existing tool with the same name. An
// existing tool keeps its position in the registration order.
func (r *Registry) Replace(t Tool) error {
	if err := validateTool(t); err != nil {
		return err
	}
	name := t.Info().Name
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[name]; !exists {
		r.order = append(r.order, name)
	}
	r.tools[name] = t
	return nil
}

// Unregister removes a tool by name and reports whether it was present.
func (r *Registry) Unregister(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[name]; !exists {
		return false
	}
	delete(r.tools, name)
	r.order = slices.DeleteFunc(r.order, func(n string) bool { return n == name })
	return true
}

// View returns a registry holding only the named tools, in the parent's
// registration order. Unknown names are ignored. The view shares the tool
// values but has its own registration state, so changing one does not change
// the other.
func (r *Registry) View(names ...string) *Registry {
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	view := NewRegistry()
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, name := range r.order {
		if !wanted[name] {
			continue
		}
		view.tools[name] = r.tools[name]
		view.order = append(view.order, name)
	}
	return view
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Info returns a tool's definition by name.
func (r *Registry) Info(name string) (Info, bool) {
	t, ok := r.Get(name)
	if !ok {
		return Info{}, false
	}
	return t.Info(), true
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

// Execute dispatches one call. Unknown tools, invalid arguments, cancellation,
// panics, and handler errors all become failed results rather than loop-fatal
// errors, so the model can self-correct and a misbehaving tool cannot kill the
// run.
func (r *Registry) Execute(ctx context.Context, name string, args json.RawMessage, tctx *Context) Result {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Errorf("cancelled")
	}
	t, ok := r.Get(name)
	if !ok {
		return r.unknownTool(name)
	}
	norm, err := argsObject(args)
	if err != nil {
		return Errorf("invalid arguments: %v", err)
	}
	if err := ValidateArgs(t.Info().Parameters, norm); err != nil {
		return Errorf("invalid arguments: %v", err)
	}
	return runGuarded(ctx, name, t, norm, tctx)
}

// unknownTool builds the error result for a missing tool, naming up to ten
// registered tools so the model can pick a real one.
func (r *Registry) unknownTool(name string) Result {
	names := r.Names()
	const maxListed = 10
	if len(names) > maxListed {
		names = names[:maxListed]
	}
	return Errorf("unknown tool %q; known tools: %s", name, strings.Join(names, ", "))
}

// runGuarded runs a tool and converts a panic into an error result. It is the
// only recover on the tool path, for both sequential and parallel callers.
func runGuarded(ctx context.Context, name string, t Tool, args json.RawMessage, tctx *Context) (res Result) {
	defer func() {
		if rec := recover(); rec != nil {
			res = Errorf("internal error in %s: %v", name, rec)
		}
	}()
	out, err := t.Run(ctx, args, tctx)
	if err != nil {
		return Errorf("%s: %v", name, err)
	}
	return out
}
