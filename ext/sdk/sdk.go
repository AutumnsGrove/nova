package sdk

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

// maxLine is the protocol's 16 MiB line cap.
const maxLine = 16 << 20

// Host collects an extension's registrations and talks to the host process.
type Host struct {
	// wmu serialises writes to out. It is separate from mu so a host that has
	// stopped reading cannot also stop handlers from looking up their tools.
	wmu sync.Mutex
	out io.Writer

	mu       sync.Mutex
	next     int64
	tools    []Tool
	handlers map[string]func(*Call) (Result, error)
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

// Part is one image an extension's tool result shows the model. Type is
// "image"; Data is base64 bytes.
type Part struct {
	Type     string `json:"type"`
	MimeType string `json:"mime_type,omitempty"`
	Data     string `json:"data,omitempty"`
	URL      string `json:"url,omitempty"`
	Name     string `json:"name,omitempty"`
}

// Result is a tool result.
type Result struct {
	Content string
	IsError bool
	// Details is machine-readable data sent beside Content.
	Details any
	// Render is text for the tool's card. It is sent as details.render, the way
	// a declarative tool's render template is, so it needs Details to be nil or
	// a JSON object; it is dropped for any other kind of Details.
	Render string
	// Parts are images the model should see; the host turns them into one
	// synthetic user message after the tool batch.
	Parts []Part
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

// wire renders the result's fields as a frame fragment. Details and parts are
// left out when empty so the host does not see a literal null.
func (r Result) wire() map[string]any {
	m := map[string]any{"content": r.Content, "is_error": r.IsError}
	if details := r.details(); details != nil {
		m["details"] = details
	}
	if len(r.Parts) > 0 {
		m["parts"] = r.Parts
	}
	return m
}

// details returns Details with Render merged in.
func (r Result) details() any {
	if r.Render == "" {
		return r.Details
	}
	merged := map[string]any{}
	if r.Details != nil {
		raw, err := json.Marshal(r.Details)
		if err != nil || json.Unmarshal(raw, &merged) != nil {
			return r.Details
		}
		if merged == nil { // Details was a nil map, which encodes as null.
			merged = map[string]any{}
		}
	}
	merged["render"] = r.Render
	return merged
}

// Call is one tool invocation.
type Call struct {
	ctx  context.Context
	UI   *UI
	name string
	args map[string]any
}

// Context returns the call's deadline-carrying context.
func (c *Call) Context() context.Context { return c.ctx }

// String reads a string argument.
func (c *Call) String(name string) string { v, _ := c.args[name].(string); return v }

// Int reads an integer argument.
func (c *Call) Int(name string) int { n, _ := c.args[name].(float64); return int(n) }

// Bool reads a boolean argument.
func (c *Call) Bool(name string) bool { v, _ := c.args[name].(bool); return v }

// Raw returns the arguments re-encoded as JSON, for a handler that wants to
// share a decoder with the in-process implementation.
func (c *Call) Raw() json.RawMessage {
	raw, _ := json.Marshal(c.args)
	return raw
}

// Command is one slash-command invocation.
type Command struct {
	ctx  context.Context
	Args string
	UI   *UI
	out  strings.Builder
}

// Context returns the command's context.
func (c *Command) Context() context.Context { return c.ctx }

// Write appends command output.
func (c *Command) Write(text string) { c.out.WriteString(text) }

// Hook is one hook invocation.
type Hook struct {
	ctx   context.Context
	Hook  string
	Event json.RawMessage
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
	h.handlers[t.Name] = fn
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
	h.hooks[hook] = fn
}

// Flag declares a CLI flag.
func (h *Host) Flag(name, description, def string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.flags = append(h.flags, Flag{Name: name, Description: description, Default: def})
}

// AppendEntry writes a custom session entry. Called while registering, it also
// declares the entry's kind for replay. The entry is visible to Entries at
// once, as it will be after a reload.
func (h *Host) AppendEntry(kind string, data any, keep bool) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if err := h.send(map[string]any{"type": "append_entry", "kind": kind, "data": json.RawMessage(raw), "keep": keep}); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.declareKind(kind)
	h.replay[kind] = append(h.replay[kind], raw)
	return nil
}

// EntryKind declares a session entry kind during registration, so the host
// replays the session's entries of that kind on reload and resume. The host
// reads the kinds from the register frame, so a kind first used after
// registration is not replayed unless it is declared here.
func (h *Host) EntryKind(kind string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.declareKind(kind)
}

// declareKind records an entry kind once. The caller holds h.mu.
func (h *Host) declareKind(kind string) {
	if !slices.Contains(h.entries, kind) {
		h.entries = append(h.entries, kind)
	}
}

// Entries returns the session's entries of kind: those the host replayed and
// those this extension has appended since.
func (h *Host) Entries(kind string) []json.RawMessage {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.replay[kind])
}

// Complete makes a nested model call.
func (h *Host) Complete(ctx context.Context, req any) (json.RawMessage, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	return h.request(ctx, map[string]any{"type": "complete", "request": json.RawMessage(raw)})
}

// Decide makes a decision-model call. The request is a decide.Request-shaped
// object; the raw result is the decide_result frame.
func (h *Host) Decide(ctx context.Context, req any) (json.RawMessage, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	return h.request(ctx, map[string]any{"type": "decide", "request": json.RawMessage(raw)})
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
	h := &Host{
		out:      out,
		handlers: map[string]func(*Call) (Result, error){},
		hooks:    map[string]HookHandler{},
		replay:   map[string][]json.RawMessage{},
		pending:  map[string]chan json.RawMessage{},
	}
	h.UI = &UI{host: h}
	r := bufio.NewReaderSize(in, 4096)
	line, err := readLine(r)
	if err != nil {
		return err
	}
	var init struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(line, &init) != nil || init.Type != "init" {
		return errors.New("first frame is not init")
	}
	if err := fn(h); err != nil {
		return err
	}
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
	slices.Sort(hooks)
	entries := slices.Clone(h.entries)
	flags := slices.Clone(h.flags)
	h.mu.Unlock()
	return h.send(map[string]any{"type": "register", "tools": tools, "commands": commands, "hooks": hooks, "flags": flags, "entries": entries})
}

// serve reads frames until shutdown or EOF.
func (h *Host) serve(r *bufio.Reader) error {
	for {
		line, err := readLine(r)
		if err != nil {
			if errors.Is(err, io.EOF) {
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
		case "ui_response", "complete_result", "decide_result", "error":
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
	call := &Call{ctx: ctx, UI: h.UI, name: f.Name, args: decodeArgs(f.Arguments)}
	h.mu.Lock()
	fn := h.handlers[f.Name]
	h.mu.Unlock()
	h.reply("tool_result", f.ID, invokeTool(fn, call).wire())
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
	result := map[string]any{}
	if fn != nil {
		ctx, cancel := deadlineContext(f.Deadline)
		defer cancel()
		if dec, err := safeHook(fn, &Hook{ctx: ctx, Hook: f.Hook, Event: f.Event}); err == nil && dec != nil {
			result = decisionJSON(dec)
		}
	}
	h.reply("hook_result", f.ID, map[string]any{"result": result})
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
		m["arguments"] = d.Arguments
	}
	if d.Result != nil {
		m["result"] = d.Result.wire()
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
	i := slices.IndexFunc(h.commands, func(c CommandSpec) bool { return c.Name == f.Name })
	var spec CommandSpec
	if i >= 0 {
		spec = h.commands[i]
	}
	h.mu.Unlock()
	cmd := &Command{ctx: context.Background(), Args: f.Args, UI: h.UI}
	failed := false
	if spec.Run == nil {
		failed = true
	} else if err := runCommand(spec.Run, cmd); err != nil {
		failed = true
		cmd.Write(err.Error())
	}
	h.reply("command_result", f.ID, map[string]any{"content": cmd.out.String(), "is_error": failed})
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

// decodeArgs parses tool arguments into a map. It is not shared with the host's
// stricter decoder because this package imports nothing from Nova, so an
// extension does not link the host to use it.
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
	h.wmu.Lock()
	defer h.wmu.Unlock()
	_, err = h.out.Write(b)
	return err
}

// request sends a frame and waits for the reply with its id. A reply that is an
// error frame, which is how the host refuses a request, comes back as an error.
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
		if err := refusal(raw); err != nil {
			return nil, err
		}
		return raw, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// refusal returns the error an error frame carries, or nil for any other frame.
func refusal(raw json.RawMessage) error {
	var f struct {
		Type  string `json:"type"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &f); err == nil && f.Type == "error" {
		return fmt.Errorf("host refused the request: %s: %s", f.Error.Code, f.Error.Message)
	}
	return nil
}

// deliver routes a response to the waiting request. A frame with no id, or one
// nobody waits for, is dropped.
func (h *Host) deliver(id string, line []byte) {
	h.mu.Lock()
	ch := h.pending[id]
	h.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- slices.Clone(line):
	default:
	}
}

// readLine reads one protocol line of at most maxLine bytes. It does not use
// the host's proto.Reader, which would make this package import Nova.
func readLine(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		buf = append(buf, chunk...)
		if len(buf) > maxLine {
			return nil, errors.New("line too long")
		}
		switch {
		case err == nil:
			return buf[:len(buf)-1], nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(buf) > 0:
			return buf, nil
		}
		return nil, err
	}
}
