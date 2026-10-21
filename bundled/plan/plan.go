// Package plan is the bundled plan-mode extension: a `/plan` toggle, a
// read-only guard, plan-mode instructions, and a `plan_submit` approval tool.
package plan

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/AutumnsGrove/nova/agent"
	"github.com/AutumnsGrove/nova/ext"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/tool"
)

// State kind and limits.
const (
	stateKind    = "plan.mode"
	stateVersion = 1
	maxPlan      = 20000

	approveText = "The user approved the plan. Leave plan mode and carry it out."
	reviseText  = "Revise the plan and call plan_submit again."
	cancelText  = "The user cancelled the plan. Stop and ask what they would like to do."
	noUIText    = "Plan recorded. No approval is possible here; present the plan as your final answer."
	blockReason = "plan mode: only read-only tools are allowed until the plan is approved"

	instructions = "\n\nPlan mode is on. Investigate with read-only tools only, then write a concrete " +
		"step-by-step plan and call plan_submit. Do not modify anything until the plan is approved."
)

// Extension is the plan bundled extension.
type Extension struct {
	mu         sync.Mutex
	env        *ext.Env
	on         bool
	pendingEnd bool
	registry   toolInfos
}

// toolInfos is the slice of the tool registry plan mode checks.
type toolInfos interface {
	Info(name string) (tool.Info, bool)
}

// New returns a fresh plan extension.
func New() *Extension { return &Extension{} }

// Name is the extension name.
func (e *Extension) Name() string { return "plan" }

// Init registers the tool, command, and hooks.
func (e *Extension) Init(env *ext.Env) error {
	e.env = env
	if env.Extra != nil {
		if reg, ok := env.Extra["tools"].(toolInfos); ok {
			e.registry = reg
		}
	}
	if err := env.Tool(e.submitTool()); err != nil {
		return err
	}
	if err := env.Command(ext.InProcCommand{Name: "plan", Description: "Toggle plan mode", Run: e.command}); err != nil {
		return err
	}
	env.Hooks(agent.Hooks{
		ToolCall:              e.hookToolCall,
		BeforeProviderRequest: e.hookBeforeProviderRequest,
		TurnEnd:               e.hookTurnEnd,
	})
	env.Events(func(ev proto.Event) {
		if ev.Type == proto.EventSessionChanged {
			e.rebuild()
			e.publish()
		}
	})
	e.rebuild()
	if env.Extra != nil {
		if start, ok := env.Extra["plan"].(bool); ok && start {
			e.setOn(true)
		}
	}
	if e.isOn() {
		e.publish()
	}
	return nil
}

// submitTool is the plan_submit tool.
func (e *Extension) submitTool() tool.Tool {
	return tool.New(tool.Info{
		Name:        "plan_submit",
		Description: "Submit the plan for approval. Only valid in plan mode.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"plan": map[string]any{"type": "string"}},
			"required":   []string{"plan"},
		},
		ReadOnly: true,
	}, e.submit)
}

// submit validates the plan and asks for a decision.
func (e *Extension) submit(ctx context.Context, args json.RawMessage, _ *tool.Context) (tool.Result, error) {
	var in struct {
		Plan string `json:"plan"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return tool.Errorf("invalid arguments: %v", err), nil
	}
	if !e.isOn() {
		return tool.Errorf("not in plan mode"), nil
	}
	plan := strings.TrimSpace(in.Plan)
	if plan == "" || utf8.RuneCountInString(plan) > maxPlan {
		return tool.Errorf("a plan must be 1 to %d characters", maxPlan), nil
	}
	e.recordPlan(plan)
	if e.env == nil || e.env.UI == nil || !e.env.HasUI("form") {
		e.mu.Lock()
		e.pendingEnd = true
		e.mu.Unlock()
		return tool.Text("%s", noUIText), nil
	}
	resp, err := e.env.UI.Request(ctx, approvalRequest(plan))
	if err != nil {
		return tool.Errorf("the plan could not be shown: %v", err), nil
	}
	if resp.Cancelled {
		return e.finish(cancelText), nil
	}
	choice, note := firstAnswer(resp)
	switch choice {
	case "approve":
		return e.finish(approveText), nil
	case "revise":
		if strings.TrimSpace(note) != "" {
			return tool.Text("%s", note+"\n"+reviseText), nil
		}
		return tool.Text("%s", reviseText), nil
	default:
		return e.finish(cancelText), nil
	}
}

// finish turns plan mode off, publishes, and returns text.
func (e *Extension) finish(text string) tool.Result {
	e.setOn(false)
	e.persist()
	e.publish()
	return tool.Text("%s", text)
}

// firstAnswer returns the chosen option id and the note.
func firstAnswer(resp proto.UIResponse) (choice, note string) {
	for _, a := range resp.Answers {
		note = a.Notes
		if len(a.Values) > 0 {
			choice = a.Values[0]
		} else {
			choice = a.Value
		}
	}
	return choice, note
}

// approvalRequest builds the three-way approval form.
func approvalRequest(plan string) proto.UIRequest {
	return proto.UIRequest{
		Type:      proto.FrameUIRequest,
		Method:    "form",
		Title:     "Approve the plan?",
		AllowSkip: true,
		Questions: []proto.UIQuestion{{
			ID:     "plan",
			Prompt: plan,
			Kind:   "single",
			Notes:  true,
			Options: []proto.UIOption{
				{ID: "approve", Label: "Approve and execute"},
				{ID: "revise", Label: "Revise (with a note)"},
				{ID: "cancel", Label: "Cancel plan"},
			},
		}},
	}
}

// command toggles plan mode.
func (e *Extension) command(_ context.Context, _ string) (string, error) {
	e.mu.Lock()
	e.on = !e.on
	on := e.on
	e.mu.Unlock()
	e.persist()
	e.publish()
	if on {
		return "Plan mode on.", nil
	}
	return "Plan mode off.", nil
}

// hookToolCall blocks a non-read-only tool while plan mode is on.
func (e *Extension) hookToolCall(_ context.Context, c agent.ToolCall) *agent.CallDecision {
	if !e.isOn() {
		return nil
	}
	if c.Name == "plan_submit" || e.readOnly(c.Name) {
		return nil
	}
	return &agent.CallDecision{Block: true, Reason: blockReason}
}

// hookBeforeProviderRequest appends the plan-mode instructions to the system
// message.
func (e *Extension) hookBeforeProviderRequest(_ context.Context, req *provider.Request) {
	if !e.isOn() {
		return
	}
	for i := range req.Messages {
		if req.Messages[i].Role == message.RoleSystem {
			req.Messages[i].Content += instructions
			return
		}
	}
}

// hookTurnEnd ends the run after a no-UI plan submission.
func (e *Extension) hookTurnEnd(context.Context, agent.Turn) agent.TurnDecision {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.pendingEnd {
		e.pendingEnd = false
		return agent.TurnDecision{End: true}
	}
	return agent.TurnDecision{}
}

// readOnly reports whether a tool name is safe in plan mode.
func (e *Extension) readOnly(name string) bool {
	if e.registry != nil {
		if info, ok := e.registry.Info(name); ok {
			return info.ReadOnly
		}
	}
	switch name {
	case "read", "list", "glob", "grep", "web_fetch", "ask":
		return true
	}
	return false
}

// isOn reports whether plan mode is on.
func (e *Extension) isOn() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.on
}

// setOn sets the mode flag.
func (e *Extension) setOn(on bool) {
	e.mu.Lock()
	e.on = on
	e.mu.Unlock()
}

// recordPlan appends the submitted plan as a custom entry.
func (e *Extension) recordPlan(plan string) {
	if e.env == nil || e.env.Session == nil {
		return
	}
	data, _ := json.Marshal(map[string]string{"plan": plan})
	_ = e.env.Session.AppendEntry("plan.submitted", data, false)
}

// persist writes the mode snapshot to the session.
func (e *Extension) persist() {
	e.mu.Lock()
	on := e.on
	e.mu.Unlock()
	if e.env == nil || e.env.Session == nil {
		return
	}
	data, _ := json.Marshal(map[string]any{"v": stateVersion, "on": on})
	_ = e.env.Session.AppendEntry(stateKind, data, true)
}

// rebuild restores the mode from the last snapshot on the active branch.
func (e *Extension) rebuild() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.on = false
	if e.env == nil || e.env.Session == nil {
		return
	}
	entries := e.env.Session.Entries(stateKind)
	if len(entries) == 0 {
		return
	}
	var s struct {
		V  int  `json:"v"`
		On bool `json:"on"`
	}
	if err := json.Unmarshal(entries[len(entries)-1], &s); err != nil || s.V != stateVersion {
		return
	}
	e.on = s.On
}

// publish sends the footer segment, or clears it when plan mode is off.
func (e *Extension) publish() {
	if e.env == nil || e.env.UI == nil {
		return
	}
	if e.isOn() {
		e.env.UI.Notify(proto.UIRequest{Method: "status", Key: "plan", Segments: []proto.UISegment{{Text: "plan", Style: "accent"}}})
		return
	}
	e.env.UI.Notify(proto.UIRequest{Method: "status", Key: "plan"})
}

// fmtPlan summarises a plan for the TUI card.
func fmtPlan(plan string) string {
	return fmt.Sprintf("%d characters", utf8.RuneCountInString(plan))
}

// ToolNames lists the tool Init registers.
func (e *Extension) ToolNames() []string { return []string{"plan_submit"} }
