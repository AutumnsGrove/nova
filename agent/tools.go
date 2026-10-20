package agent

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/AutumnsGrove/nova/diag"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/permission"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/tool"
)

// emptyResult is recorded in place of an empty tool result, because providers
// reject a tool message with no content.
const emptyResult = "(empty result)"

// defaultMaxParallel is the concurrency cap when Config.MaxParallel is unset.
const defaultMaxParallel = 8

// maxParallel reports the effective parallel-call cap.
func (r *run) maxParallel() int {
	n := r.cfg.MaxParallel
	if n <= 0 {
		n = defaultMaxParallel
	}
	return n
}

// parallelCapable reports whether a call's tool opts into parallel execution.
func (r *run) parallelCapable(call message.ToolCall) bool {
	if r.cfg.Tools == nil {
		return false
	}
	info, ok := r.cfg.Tools.Info(call.Function.Name)
	return ok && info.Parallel
}

// callOutcome is one dispatched call's result plus any messages the tool asked
// the loop to inject after the batch, and the usage its nested calls reported.
type callOutcome struct {
	result   tool.Result
	injected []message.Message
	usage    provider.Usage
}

// dispatch runs every call and returns the outcomes in call order, whatever the
// completion order. Parallel-capable runs run in groups; a non-parallel call
// runs alone as a barrier. Sequential mode runs one at a time. A call that
// starts after the run was cancelled still gets a "cancelled" result from
// runCall, so every call of the recorded assistant message is answered.
func (r *run) dispatch(calls []message.ToolCall) []callOutcome {
	outcomes := make([]callOutcome, len(calls))
	if r.cfg.ToolExecution == Sequential || r.maxParallel() == 1 {
		for i := range calls {
			outcomes[i] = r.runCall(calls[i])
		}
		return outcomes
	}
	for i := 0; i < len(calls); {
		if !r.parallelCapable(calls[i]) {
			outcomes[i] = r.runCall(calls[i])
			i++
			continue
		}
		j := i
		for j < len(calls) && r.parallelCapable(calls[j]) {
			j++
		}
		r.runGroup(calls[i:j], outcomes[i:j])
		i = j
	}
	return outcomes
}

// runGroup runs parallel-capable calls concurrently, at most maxParallel at a
// time, and returns when all have finished. outcomes is the matching slice of
// the caller's results; each goroutine writes only its own element.
func (r *run) runGroup(calls []message.ToolCall, outcomes []callOutcome) {
	sem := make(chan struct{}, r.maxParallel())
	var wg sync.WaitGroup
	for k := range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			outcomes[k] = r.runCall(calls[k])
		}()
	}
	wg.Wait()
}

// runCall runs one tool call: cancellation check, permission, the ToolCall
// hook, tool_start, execution with progress, and the ToolResult hook. A panic
// anywhere is recovered into that call's error result.
func (r *run) runCall(call message.ToolCall) (out callOutcome) {
	var injected []message.Message
	var usage provider.Usage
	defer func() {
		if rec := recover(); rec != nil {
			_, _ = diag.Report(rec, "tool "+call.Function.Name)
			out.result = tool.Errorf("internal error running %s: %v", call.Function.Name, rec)
			out.injected = injected
			out.usage = usage
			r.emitToolEnd(call, out.result)
		}
	}()

	if r.ctx.Err() != nil {
		out.result = tool.Errorf("cancelled")
		return out
	}

	args := json.RawMessage(call.Function.Arguments)
	if blocked, blockedResult := r.checkPermission(call, args); blocked {
		out.result = r.finishCall(call, blockedResult)
		return out
	}

	if h := r.cfg.Hooks.ToolCall; h != nil {
		decision, ok := runHook(r, "tool_call", func(ctx context.Context) *CallDecision {
			return h(ctx, ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: args})
		})
		if ok && decision != nil {
			if decision.Block {
				out.result = r.finishCall(call, tool.Errorf("blocked: %s", decision.Reason))
				return out
			}
			if decision.Arguments != nil {
				args = decision.Arguments
			}
		}
	}

	r.emit(proto.Event{Type: proto.EventToolStart, CallID: call.ID})
	diag.SetOp("tool " + call.Function.Name)
	progress := newProgress(r, call.ID)
	// Closed explicitly below so updates precede tool_end; this covers a panic.
	defer progress.close()
	tctx := r.toolContext()
	tctx.Emit = progress.emit
	tctx.Inject = func(m message.Message) { injected = append(injected, m) }
	tctx.AddUsage = func(u provider.Usage) { usage.Add(u) }
	var res tool.Result
	if r.cfg.Tools != nil {
		res = r.cfg.Tools.Execute(r.ctx, call.Function.Name, args, tctx)
	} else {
		res = tool.Errorf("no tools are registered")
	}
	progress.close()
	out.result = r.finishCall(call, res)
	out.injected = injected
	out.usage = usage
	return out
}

// finishCall applies the empty-result rule and the ToolResult hook, then emits
// tool_end. It is the single place a call becomes a recorded result.
func (r *run) finishCall(call message.ToolCall, res tool.Result) tool.Result {
	if h := r.cfg.Hooks.ToolResult; h != nil {
		if out, ok := runHook(r, "tool_result", func(ctx context.Context) tool.Result {
			return h(ctx, ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: json.RawMessage(call.Function.Arguments)}, res)
		}); ok {
			res = out
		}
	}
	// After the hook, so a hook that blanks the result is normalised too and
	// tool_end shows what the transcript records.
	if res.Content == "" {
		res.Content = emptyResult
	}
	r.emitToolEnd(call, res)
	return res
}

// checkPermission applies the policy. It returns blocked=true with the error
// result when the call must not run, and emits a permission event for every
// decision including an allow.
func (r *run) checkPermission(call message.ToolCall, args json.RawMessage) (blocked bool, out tool.Result) {
	if r.cfg.Policy == nil {
		return false, tool.Result{}
	}
	info := tool.Info{}
	if r.cfg.Tools != nil {
		info, _ = r.cfg.Tools.Info(call.Function.Name)
	}
	d := r.cfg.Policy.Decide(permission.Call{Tool: call.Function.Name, Args: args, Info: info, WorkDir: r.cfg.WorkDir}, r.cfg.Ask != nil)
	r.emit(proto.Event{Type: proto.EventPermission, CallID: call.ID, Decision: string(d.Action), Reason: d.Reason})
	switch d.Action {
	case permission.Allow:
		return false, tool.Result{}
	case permission.Ask:
		if r.cfg.Ask == nil {
			return true, tool.Errorf("not approved: %s", d.Reason)
		}
		allow, err := r.cfg.Ask(r.ctx, Question{Tool: call.Function.Name, Reason: d.Reason, Args: wireArgs(args), Rule: d.Rule})
		if err != nil || !allow {
			return true, tool.Errorf("not approved: %s", d.Reason)
		}
		return false, tool.Result{}
	default:
		// Deny, and any action a hand-built policy mistyped: an unknown
		// decision must never run the tool.
		return true, tool.Errorf("blocked by policy: %s", d.Reason)
	}
}

// toolContext builds a fresh tool context per call so progress routes to the
// right call id.
func (r *run) toolContext() *tool.Context {
	return &tool.Context{WorkDir: r.cfg.WorkDir, Roots: r.cfg.Roots, Data: r.cfg.ToolData, SessionID: r.cfg.SessionID, Complete: r.cfg.Complete}
}

// progress forwards tool updates as tool_update events without ever blocking
// the tool: a full buffer drops the update. A tool that leaked a goroutine may
// report after it returned, so emit and close share a lock and a late update is
// dropped instead of being sent on a closed channel.
type progress struct {
	r      *run
	id     string
	ch     chan tool.Update
	done   chan struct{}
	mu     sync.Mutex
	closed bool
}

func newProgress(r *run, callID string) *progress {
	p := &progress{r: r, id: callID, ch: make(chan tool.Update, 32), done: make(chan struct{})}
	go func() {
		defer close(p.done)
		defer diag.Catch("tool progress")
		for u := range p.ch {
			p.r.emit(proto.Event{Type: proto.EventToolUpdate, CallID: p.id, Text: u.Text})
		}
	}()
	return p
}

func (p *progress) emit(u tool.Update) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	select {
	case p.ch <- u:
	default:
	}
}

// close stops accepting updates and waits until every accepted one has been
// emitted, so tool_update events always precede the call's tool_end. It is
// safe to call more than once.
func (p *progress) close() {
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		close(p.ch)
	}
	p.mu.Unlock()
	<-p.done
}
