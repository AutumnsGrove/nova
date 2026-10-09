// Package tool defines the tool contract and registry.
//
// A tool bundles a model-facing definition (name, description, JSON Schema) with
// an execution function. Tools are added without modifying the harness: define
// one, register it on a Registry, and the agent loop will offer it to the model
// and dispatch calls to it.
package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/AutumnsGrove/nova/provider"
)

// Context carries per-call dependencies and application state to a tool.
//
// WorkDir is the directory relative paths resolve against. Data is an opaque
// slot for the embedding application: her-go, polaris, and ivy can hang their
// own dependency bundles (stores, clients, callbacks) off it without the
// harness knowing about any of them.
type Context struct {
	WorkDir string
	Data    any
}

// Result is what a tool returns to the model.
type Result struct {
	Content string
	IsError bool
}

// Info is a tool's model-facing definition plus behavioral annotations.
//
// The annotations mirror MCP's tool hints and are what a permission hook uses
// to decide whether a call needs confirmation. They are advisory, not enforced
// by the registry.
type Info struct {
	Name        string
	Description string
	Parameters  map[string]any

	// Parallel reports whether this tool may run concurrently with other
	// parallel tools in the same batch. Pure-read tools should set true.
	Parallel bool
	// ReadOnly reports that the tool does not mutate state.
	ReadOnly bool
	// Destructive reports that the tool may destroy data.
	Destructive bool
	// OpenWorld reports that the tool reaches beyond the local machine.
	OpenWorld bool
}

// Def converts Info into the provider's tool definition shape.
func (i Info) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.ToolFunction{
			Name:        i.Name,
			Description: i.Description,
			Parameters:  i.Parameters,
		},
	}
}

// Func is the execution signature of a simple tool.
type Func func(ctx context.Context, args json.RawMessage, tctx *Context) (Result, error)

// Tool is a callable capability.
type Tool interface {
	// Info returns the model-facing definition.
	Info() Info
	// Run executes one call. Returning an error makes the harness surface a
	// failed tool result to the model; it does not abort the loop.
	Run(ctx context.Context, args json.RawMessage, tctx *Context) (Result, error)
}

type funcTool struct {
	info Info
	fn   Func
}

func (t funcTool) Info() Info { return t.info }

func (t funcTool) Run(ctx context.Context, args json.RawMessage, tctx *Context) (Result, error) {
	return t.fn(ctx, args, tctx)
}

// New builds a Tool from an Info and an execution function.
func New(info Info, fn Func) Tool { return funcTool{info: info, fn: fn} }

// Text builds a successful text result.
func Text(format string, args ...any) Result {
	if len(args) == 0 {
		return Result{Content: format}
	}
	return Result{Content: fmt.Sprintf(format, args...)}
}

// Errorf builds a failed result whose content is prefixed with "error:".
func Errorf(format string, args ...any) Result {
	return Result{Content: "error: " + fmt.Sprintf(format, args...), IsError: true}
}
