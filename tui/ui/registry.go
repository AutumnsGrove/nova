//go:build !notui

package ui

import (
	"encoding/json"

	"github.com/AutumnsGrove/nova/bundled/ask"
	"github.com/AutumnsGrove/nova/bundled/plan"
	"github.com/AutumnsGrove/nova/bundled/subagent"
	"github.com/AutumnsGrove/nova/bundled/tasks"
	"github.com/AutumnsGrove/nova/bundled/webfetch"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/dialog"
	"github.com/AutumnsGrove/nova/tui/render"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// Registry holds the TUI pieces the bundled extensions contribute: a card
// renderer per tool, and a dialog builder per command. It is separate from the
// transcript's render registry so a bundled extension can add to it without
// owning the transcript.
type Registry struct {
	renderers *render.Registry
	dialogs   map[string]func(args string, st any) dialog.Dialog
}

// NewRegistry wraps a render registry and returns an empty command-dialog map.
func NewRegistry(renderers *render.Registry) *Registry {
	return &Registry{renderers: renderers, dialogs: map[string]func(args string, st any) dialog.Dialog{}}
}

// Renderer registers a card renderer for a tool name.
func (r *Registry) Renderer(tool string, rd render.Renderer) {
	if r == nil || r.renderers == nil {
		return
	}
	r.renderers.Register(tool, rd)
}

// Dialog registers a dialog builder for a slash command.
func (r *Registry) Dialog(command string, build func(args string, st any) dialog.Dialog) {
	if r == nil || r.dialogs == nil {
		return
	}
	r.dialogs[command] = build
}

// CommandDialog returns the dialog builder for a command, if any.
func (r *Registry) CommandDialog(command string) (func(args string, st any) dialog.Dialog, bool) {
	if r == nil {
		return nil, false
	}
	build, ok := r.dialogs[command]
	return build, ok
}

// Lookup returns the renderer for a tool name, or the generic renderer.
func (r *Registry) Lookup(tool string) render.Renderer {
	if r == nil || r.renderers == nil {
		return nil
	}
	return r.renderers.Lookup(tool)
}

// funcRenderer adapts a header function to render.Renderer.
type funcRenderer struct {
	header func(c render.Call) string
}

func (f funcRenderer) Header(c render.Call, _ styles.Styles, _ int) string { return f.header(c) }

func (f funcRenderer) Body(render.Call, styles.Styles, int, bool) string { return "" }

// details decodes a tool result's Details object.
func details(c render.Call) map[string]any {
	out := map[string]any{}
	if len(c.Details) > 0 {
		_ = json.Unmarshal(c.Details, &out)
	}
	return out
}

// Bundled registers the card renderers and command dialogs for the bundled
// extensions. A bundled extension's Details shape is the contract.
func (r *Registry) Bundled() {
	r.Renderer("todo", funcRenderer{header: func(c render.Call) string {
		d := details(c)
		var list []tasks.Task
		if raw, ok := d["tasks"].([]any); ok {
			for _, item := range raw {
				if m, ok := item.(map[string]any); ok {
					list = append(list, tasks.Task{ID: str(m["id"]), Title: str(m["title"]), Status: str(m["status"])})
				}
			}
		}
		return "todo: " + tasks.Card(list)
	}})
	r.Renderer("subagent", funcRenderer{header: func(c render.Call) string {
		d := details(c)
		turns := intOf(d["turns"])
		var tools []string
		if raw, ok := d["tools"].([]any); ok {
			for _, item := range raw {
				tools = append(tools, str(item))
			}
		}
		cost := costOf(d["usage"])
		return subagent.Card(turns, tools, cost)
	}})
	r.Renderer("web_fetch", funcRenderer{header: func(c render.Call) string {
		d := details(c)
		return webfetch.Card(str(d["url"]), intOf(d["status"]), intOf(d["bytes"]))
	}})
	r.Renderer("plan_submit", funcRenderer{header: func(c render.Call) string {
		return "plan_submit: " + plan.Card(c.Live)
	}})
	r.Renderer("ask", funcRenderer{header: func(render.Call) string {
		return "ask: " + ask.Card(0)
	}})
	r.Dialog("todos", func(string, any) dialog.Dialog { return dialog.NewInput(inputRequest("Todos")) })
	r.Dialog("plan", func(string, any) dialog.Dialog { return dialog.NewInput(inputRequest("Plan mode")) })
}

// inputRequest builds a minimal input request for a local command dialog.
func inputRequest(title string) proto.UIRequest {
	return proto.UIRequest{Type: proto.FrameUIRequest, Title: title}
}

// str reads a string field.
func str(v any) string {
	s, _ := v.(string)
	return s
}

// intOf reads an integer-ish field.
func intOf(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

// costOf reads usage.cost_usd from a details object.
func costOf(v any) float64 {
	m, ok := v.(map[string]any)
	if !ok {
		return 0
	}
	cost, _ := m["cost_usd"].(float64)
	return cost
}
