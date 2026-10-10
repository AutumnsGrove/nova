// Package sdk is the Go SDK for writing a Nova subprocess extension. It
// handles framing, request correlation, deadlines, and panics, so an extension
// only describes its tools, commands, and hooks.
//
// A minimal extension:
//
//	func main() {
//		sdk.Run(func(h *sdk.Host) error {
//			h.Tool(sdk.Tool{
//				Name:        "hello",
//				Description: "Greets someone.",
//				Params:      sdk.Object(sdk.String("name", "Who to greet", sdk.Required)),
//				ReadOnly:    true,
//			}, func(c *sdk.Call) (sdk.Result, error) {
//				return sdk.Text("Hello, %s!", c.String("name")), nil
//			})
//			return nil
//		})
//	}
package sdk

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// maxLine is the protocol's 16 MiB line cap.
const maxLine = 16 << 20

// Host collects an extension's registrations and talks to the host process.
type Host struct {
	mu       sync.Mutex
	in       io.Reader
	out      io.Writer
	next     int64
	tools    []Tool
	handlers []handlerEntry
	commands []CommandSpec
	hooks    map[string]HookHandler
	flags    []Flag
	entries  []string
	replay   map[string][]json.RawMessage
	pending  map[string]chan json.RawMessage
	UI       *UI
}

// Tool is a model-callable tool description.
type Tool struct {
	Name        string
	Description string
	Params      map[string]any
	ReadOnly    bool
	Destructive bool
	OpenWorld   bool
	Parallel    bool
	TimeoutMS   int
}

// CommandSpec is a slash command description.
type CommandSpec struct {
	Name        string
	Description string
	Run         func(*Command) error
}

// Flag is a CLI flag the extension reads from init.
type Flag struct {
	Name        string
	Description string
	Default     string
}

// Result is a tool result.
type Result struct {
	Content string
	IsError bool
	Details any
	Render  string
}

// Text builds a successful result. With no args, format is used verbatim.
func Text(format string, args ...any) Result {
	if len(args) == 0 {
		return Result{Content: format}
	}
	return Result{Content: fmt.Sprintf(format, args...)}
}

// Error builds a failed result.
func Error(format string, args ...any) Result {
	return Result{Content: fmt.Sprintf(format, args...), IsError: true}
}

// Call is one tool invocation.
type Call struct {
	ctx  context.Context
	UI   *UI
	name string
	args map[string]any
	host *Host
}

// Context returns the call's deadline-carrying context.
func (c *Call) Context() context.Context { return c.ctx }

// String reads a string argument.
func (c *Call) String(name string) string { v, _ := c.args[name].(string); return v }

// Int reads an integer argument.
func (c *Call) Int(name string) int { n, _ := c.args[name].(float64); return int(n) }

// Bool reads a boolean argument.
func (c *Call) Bool(name string) bool { v, _ := c.args[name].(bool); return v }

// Command is one slash-command invocation.
type Command struct {
	ctx  context.Context
	Args string
	UI   *UI
	out  strings.Builder
	host *Host
}

// Context returns the command's context.
func (c *Command) Context() context.Context { return c.ctx }

// Write appends command output.
func (c *Command) Write(text string) { c.out.WriteString(text) }

// Hook is one hook invocation.
type Hook struct {
	ctx      context.Context
	Hook     string
	Event    json.RawMessage
	host     *Host
	deadline time.Duration
}

// Context returns the hook's deadline-carrying context.
func (h *Hook) Context() context.Context { return h.ctx }

// Decode unmarshals the event into v.
func (h *Hook) Decode(v any) error { return json.Unmarshal(h.Event, v) }

// HookDecision is what a hook handler returns.
type HookDecision struct {
	Block     bool
	Reason    string
	Arguments json.RawMessage
	Result    *Result
	End       bool
	Continue  bool
	Inject    any
}

// HookHandler answers one hook.
type HookHandler func(*Hook) (*HookDecision, error)

// Tool registers a tool handler.
func (h *Host) Tool(t Tool, fn func(*Call) (Result, error)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tools = append(h.tools, t)
	h.handlers = append(h.handlers, handlerEntry{tool: t.Name, fn: fn})
}

// Command registers a slash command.
func (h *Host) Command(name, description string, fn func(*Command) error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.commands = append(h.commands, CommandSpec{Name: name, Description: description, Run: fn})
}

// On registers a hook handler.
func (h *Host) On(hook string, fn HookHandler) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.hooks == nil {
		h.hooks = map[string]HookHandler{}
	}
	h.hooks[hook] = fn
}

// Flag declares a CLI flag.
func (h *Host) Flag(name, description, def string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.flags = append(h.flags, Flag{Name: name, Description: description, Default: def})
}

// AppendEntry writes a custom session entry and declares its kind for replay.
func (h *Host) AppendEntry(kind string, data any, keep bool) error {
	h.mu.Lock()
	h.entries = append(h.entries, kind)
	h.mu.Unlock()
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return h.send(map[string]any{"type": "append_entry", "kind": kind, "data": json.RawMessage(raw), "keep": keep})
}

// Entries returns the entries of kind replayed by the host.
func (h *Host) Entries(kind string) []json.RawMessage {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]json.RawMessage(nil), h.replay[kind]...)
}

// handlerEntry keeps a tool handler next to its name.
type handlerEntry struct {
	tool string
	fn   func(*Call) (Result, error)
}

// Complete makes a nested model call.
func (h *Host) Complete(ctx context.Context, req any) (json.RawMessage, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	return h.request(ctx, map[string]any{"type": "complete", "request": json.RawMessage(raw)})
}

// Run reads init, calls fn, registers, and serves until shutdown or EOF.
func Run(fn func(*Host) error) {
	if err := run(fn, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "sdk:", err)
		os.Exit(1)
	}
}

// run is Run over explicit streams, so tests can drive it in process.
func run(fn func(*Host) error, in io.Reader, out io.Writer) error {
	h := &Host{in: in, out: out, replay: map[string][]json.RawMessage{}, pending: map[string]chan json.RawMessage{}}
	r := bufio.NewReaderSize(in, 4096)
	line, err := readLine(r)
	if err != nil {
		return err
	}
	var init struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(line, &init) != nil || init.Type != "init" {
		return fmt.Errorf("first frame is not init")
	}
	if err := fn(h); err != nil {
		return err
	}
	h.UI = &UI{host: h}
	if err := h.sendRegister(); err != nil {
		return err
	}
	return h.serve(r)
}

// sendRegister writes the collected registrations.
func (h *Host) sendRegister() error {
	h.mu.Lock()
	tools := make([]map[string]any, 0, len(h.tools))
	for _, t := range h.tools {
		tools = append(tools, map[string]any{
			"name": t.Name, "description": t.Description, "parameters": t.Params,
			"read_only": t.ReadOnly, "destructive": t.Destructive,
			"open_world": t.OpenWorld, "parallel": t.Parallel, "timeout_ms": t.TimeoutMS,
		})
	}
	commands := make([]map[string]any, 0, len(h.commands))
	for _, c := range h.commands {
		commands = append(commands, map[string]any{"name": c.Name, "description": c.Description})
	}
	hooks := make([]string, 0, len(h.hooks))
	for name := range h.hooks {
		hooks = append(hooks, name)
	}
	sort.Strings(hooks)
	entries := append([]string(nil), h.entries...)
	flags := append([]Flag(nil), h.flags...)
	h.mu.Unlock()
	return h.send(map[string]any{"type": "register", "tools": tools, "commands": commands, "hooks": hooks, "flags": flags, "entries": entries})
}

// serve reads frames until shutdown or EOF.
func (h *Host) serve(r *bufio.Reader) error {
	for {
		line, err := readLine(r)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		var probe struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if json.Unmarshal(line, &probe) != nil {
			continue
		}
		switch probe.Type {
		case "shutdown":
			return nil
		case "ui_response":
			h.deliver(probe.ID, line)
		case "complete_result":
			h.deliver(probe.ID, line)
		case "replay":
			var f struct {
				Kind string          `json:"kind"`
				Data json.RawMessage `json:"data"`
			}
			if json.Unmarshal(line, &f) == nil {
				h.mu.Lock()
				h.replay[f.Kind] = append(h.replay[f.Kind], f.Data)
				h.mu.Unlock()
			}
		case "tool_call":
			go h.handleTool(line)
		case "hook":
			go h.handleHook(line)
		case "command":
			go h.handleCommand(line)
		}
	}
}

// handleTool runs one tool handler and replies.
func (h *Host) handleTool(line []byte) {
	var f struct {
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Deadline  int             `json:"deadline_ms"`
	}
	_ = json.Unmarshal(line, &f)
	ctx, cancel := deadlineContext(f.Deadline)
	defer cancel()
	call := &Call{ctx: ctx, UI: h.UI, name: f.Name, host: h, args: decodeArgs(f.Arguments)}
	h.mu.Lock()
	var fn func(*Call) (Result, error)
	for _, e := range h.handlers {
		if e.tool == f.Name {
			fn = e.fn
		}
	}
	h.mu.Unlock()
	res := invokeTool(fn, call)
	h.reply("tool_result", f.ID, map[string]any{"content": res.Content, "is_error": res.IsError, "details": res.Details})
}

// invokeTool runs a tool handler, turning a panic into an error result.
func invokeTool(fn func(*Call) (Result, error), call *Call) (res Result) {
	if fn == nil {
		return Error("no handler for tool %q", call.name)
	}
	defer func() {
		if rec := recover(); rec != nil {
			res = Error("internal error in %s: %v", call.name, rec)
		}
	}()
	out, err := fn(call)
	if err != nil {
		return Error("%v", err)
	}
	return out
}

// handleHook runs one hook handler and replies.
func (h *Host) handleHook(line []byte) {
	var f struct {
		ID       string          `json:"id"`
		Hook     string          `json:"hook"`
		Event    json.RawMessage `json:"event"`
		Deadline int             `json:"deadline_ms"`
	}
	_ = json.Unmarshal(line, &f)
	h.mu.Lock()
	fn := h.hooks[f.Hook]
	h.mu.Unlock()
	if fn == nil {
		h.reply("hook_result", f.ID, map[string]any{"result": map[string]any{}})
		return
	}
	ctx, cancel := deadlineContext(f.Deadline)
	defer cancel()
	dec, err := safeHook(fn, &Hook{ctx: ctx, Hook: f.Hook, Event: f.Event, host: h})
	if err != nil || dec == nil {
		h.reply("hook_result", f.ID, map[string]any{"result": map[string]any{}})
		return
	}
	h.reply("hook_result", f.ID, map[string]any{"result": decisionJSON(dec)})
}

// safeHook runs a hook handler, converting a panic into an error.
func safeHook(fn HookHandler, hook *Hook) (dec *HookDecision, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			dec, err = nil, fmt.Errorf("panic: %v", rec)
		}
	}()
	return fn(hook)
}

// decisionJSON renders a hook decision.
func decisionJSON(d *HookDecision) map[string]any {
	m := map[string]any{}
	if d.Block {
		m["block"] = true
		m["reason"] = d.Reason
	}
	if len(d.Arguments) > 0 {
		m["arguments"] = json.RawMessage(d.Arguments)
	}
	if d.Result != nil {
		m["result"] = map[string]any{"content": d.Result.Content, "is_error": d.Result.IsError}
	}
	if d.End {
		m["end"] = true
	}
	if d.Continue {
		m["continue"] = true
	}
	if d.Inject != nil {
		m["inject"] = d.Inject
	}
	return m
}

// handleCommand runs one command handler and replies.
func (h *Host) handleCommand(line []byte) {
	var f struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Args string `json:"args"`
	}
	_ = json.Unmarshal(line, &f)
	h.mu.Lock()
	var spec *CommandSpec
	for i := range h.commands {
		if h.commands[i].Name == f.Name {
			spec = &h.commands[i]
		}
	}
	h.mu.Unlock()
	cmd := &Command{ctx: context.Background(), Args: f.Args, UI: h.UI, host: h}
	code := 0
	if spec == nil || spec.Run == nil {
		code = 1
	} else if err := runCommand(spec.Run, cmd); err != nil {
		code = 1
		cmd.Write(err.Error())
	}
	h.reply("command_result", f.ID, map[string]any{"content": cmd.out.String(), "is_error": code != 0})
}

// runCommand runs a command handler with panic recovery.
func runCommand(fn func(*Command) error, cmd *Command) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("panic: %v", rec)
		}
	}()
	return fn(cmd)
}

// deadlineContext builds a context with the host's deadline, if any.
func deadlineContext(ms int) (context.Context, context.CancelFunc) {
	if ms > 0 {
		return context.WithTimeout(context.Background(), time.Duration(ms)*time.Millisecond)
	}
	return context.WithCancel(context.Background())
}

// decodeArgs parses tool arguments into a map.
func decodeArgs(raw json.RawMessage) map[string]any {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return map[string]any{}
	}
	return m
}

// reply sends a result frame.
func (h *Host) reply(typ, id string, fields map[string]any) {
	frame := map[string]any{"type": typ, "id": id}
	for k, v := range fields {
		frame[k] = v
	}
	_ = h.send(frame)
}

// send writes one frame.
func (h *Host) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err = h.out.Write(b)
	return err
}

// request sends a frame and waits for the reply with its id.
func (h *Host) request(ctx context.Context, frame map[string]any) (json.RawMessage, error) {
	h.mu.Lock()
	h.next++
	id := fmt.Sprintf("sdk_%d", h.next)
	frame["id"] = id
	ch := make(chan json.RawMessage, 1)
	h.pending[id] = ch
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.pending, id)
		h.mu.Unlock()
	}()
	if err := h.send(frame); err != nil {
		return nil, err
	}
	select {
	case raw := <-ch:
		return raw, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// deliver routes a response to the waiting request.
func (h *Host) deliver(id string, line []byte) {
	h.mu.Lock()
	ch := h.pending[id]
	h.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- append(json.RawMessage(nil), line...):
	default:
	}
}

// readLine reads one protocol line.
func readLine(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		buf = append(buf, chunk...)
		if len(buf) > maxLine {
			return nil, fmt.Errorf("line too long")
		}
		if err == nil {
			return buf[:len(buf)-1], nil
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err == io.EOF && len(buf) > 0 {
			return buf, nil
		}
		return nil, err
	}
}
