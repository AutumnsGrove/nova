package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/tool"
)

// Host owns every configured MCP server and the tools they contribute. It is
// the piece mode wires into the tool registry; a server that fails is a warning
// and contributes nothing, never a broken run.
type Host struct {
	opt HostOptions

	mu      sync.Mutex
	servers []*serverState
	closed  bool
}

// HostOptions are what a host needs from the harness.
type HostOptions struct {
	// Registry receives the tools. It is required.
	Registry *tool.Registry
	// Inherit is the environment every server starts from. Empty means
	// os.Environ() minus Scrub.
	Inherit []string
	// Scrub names the variables a server never inherits: Nova's own credentials
	// and anything the user asked to hide. A server is a third-party program,
	// and a key it never needed is a key it can leak. A name in Pass is kept
	// even when a pattern matches it.
	Scrub []string
	Pass  []string
	// Version names this client in clientInfo.
	Version string
	// Warn receives one warning per problem: a server that will not start, a
	// handshake that fails, a tool name that is already taken. It may be nil.
	Warn func(error)
}

// serverState is one configured server: its connection, its tools, and why it
// is not running when it is not.
type serverState struct {
	cfg        config.MCPServer
	client     *Client
	names      []string // the registry names it contributed
	err        error
	connecting bool // between the start of a dial and its outcome
	reported   bool // this server's failure has been reported once
	pid        int  // the last process, kept after the connection is dropped
}

// State names for Status.
const (
	StateStarting = "starting"
	StateRunning  = "running"
	StateFailed   = "failed"
	StateStopped  = "stopped"
)

// Status is one server's state, for get_extensions and nova doctor.
type Status struct {
	Name    string `json:"name"`
	State   string `json:"state"`
	Tools   int    `json:"tools"`
	Command string `json:"command"`
	Error   string `json:"error,omitempty"`
}

// NewHost builds a host for the configured servers without starting anything.
func NewHost(cfg config.MCPConfig, opt HostOptions) *Host {
	// The scrub applies to whatever base the caller gave, so a server never
	// sees a variable the host was told to hide.
	base := opt.Inherit
	if len(base) == 0 {
		base = osEnv()
	}
	opt.Inherit = tool.ScrubEnvPatterns(base, opt.Scrub, opt.Pass)
	h := &Host{opt: opt}
	for _, srv := range cfg.Servers {
		h.servers = append(h.servers, &serverState{cfg: srv})
	}
	return h
}

// Servers reports how many servers were configured.
func (h *Host) Servers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.servers)
}

// Start connects to every server in parallel and registers its tools. It
// returns the warnings it reported, so a caller that shows them can, and a
// server that fails never stops the others.
func (h *Host) Start(ctx context.Context) []error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	states := slices.Clone(h.servers)
	h.mu.Unlock()

	// Connect failures are returned, not also handed to Warn: a caller that
	// reports the return value would otherwise report every dead server twice.
	var (
		mu    sync.Mutex
		warns []error
		wg    sync.WaitGroup
	)
	for _, st := range states {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := h.connect(ctx, st)
			// A death during the first listing already reported itself, so only
			// the first reporter speaks for this server.
			if err != nil && h.claimReport(st) {
				mu.Lock()
				warns = append(warns, err)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return warns
}

// connect dials one server and registers everything it offers.
func (h *Host) connect(ctx context.Context, st *serverState) error {
	h.mu.Lock()
	st.connecting = true
	st.reported = false
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		st.connecting = false
		h.mu.Unlock()
	}()
	client, err := Dial(ctx, Options{
		Name: st.cfg.Name, Command: st.cfg.Command, Args: st.cfg.Args,
		Env: st.cfg.Env, Dir: st.cfg.CWD, Inherit: h.opt.Inherit,
		Timeout: time.Duration(st.cfg.TimeoutMS) * time.Millisecond,
		Version: h.opt.Version,
		// The reader starts inside Dial, so it must have these before it runs:
		// setting them afterwards is a data race, and a death it missed.
		OnToolsChanged: func() { h.relisted(ctx, st) },
		OnClosed:       func(err error) { h.died(st, err) },
	})
	if err != nil {
		h.mu.Lock()
		st.err = err
		h.mu.Unlock()
		return err
	}
	h.mu.Lock()
	st.pid = client.Pid()
	h.mu.Unlock()
	if err := client.Err(); err != nil {
		// The reader stopped before the connection was stored, so died had
		// nothing to close: the process may still be alive and must not outlive
		// the host.
		_ = client.Close()
		lost := fmt.Errorf("mcp: %s: connection lost: %w", st.cfg.Name, err)
		h.mu.Lock()
		if st.err == nil {
			st.err = lost
		}
		h.mu.Unlock()
		return lost
	}
	h.mu.Lock()
	st.client = client
	st.err = nil
	h.mu.Unlock()
	if err := h.refresh(ctx, st); err != nil {
		h.mu.Lock()
		st.err = err
		h.mu.Unlock()
		return err
	}
	return nil
}

// relisted re-lists one server after it announces a changed tool list. A
// notification can arrive before the connect has stored the connection, in
// which case there is nothing to list yet and the connect's own listing covers
// it.
func (h *Host) relisted(ctx context.Context, st *serverState) {
	h.mu.Lock()
	ready := st.client != nil
	h.mu.Unlock()
	if !ready {
		return
	}
	if err := h.refresh(ctx, st); err != nil {
		h.mu.Lock()
		gone := st.client == nil || st.client.Err() != nil
		h.mu.Unlock()
		if gone {
			// The connection is gone, so this is the death's warning to make, not
			// a second one; the one-shot report stays unclaimed for it.
			return
		}
		h.warn(err)
	}
}

// refresh re-lists one server's tools and makes the registry match. It is the
// path both the initial connect and notifications/tools/list_changed take, so a
// server that adds or removes a tool is followed without a reload.
func (h *Host) refresh(ctx context.Context, st *serverState) error {
	h.mu.Lock()
	client := st.client
	h.mu.Unlock()
	if client == nil {
		return fmt.Errorf("mcp: %s: not connected", st.cfg.Name)
	}
	tools, err := client.ListTools(ctx)
	if err != nil {
		return fmt.Errorf("mcp: %s: tools/list: %w", st.cfg.Name, err)
	}
	h.mu.Lock()
	h.unregister(st)
	// The listing took a round trip, and the process may have died during it: a
	// server that answered and then exited must not leave its tools registered.
	// The death is recorded and reported once, by whichever path noticed it
	// first, so this returns nil rather than reporting it a second time.
	if err := client.Err(); err != nil {
		lost := fmt.Errorf("mcp: %s: connection lost: %w", st.cfg.Name, err)
		if st.err == nil {
			st.err = lost
		}
		closing := st.client == client
		if closing {
			st.client = nil
		}
		h.mu.Unlock()
		if closing {
			// The connection is out of the state, so this path owns closing it: a
			// server whose output went bad is usually still running.
			_ = client.Close()
		}
		if h.claimReport(st) {
			h.warn(lost)
		}
		return nil
	}
	if st.client != client {
		// Another path already dropped this connection; its death is reported.
		h.mu.Unlock()
		return nil
	}
	defer h.mu.Unlock()
	var (
		names []string
		warns []error
	)
	for _, t := range tools {
		name := ToolName(st.cfg.Name, t.Name)
		if err := h.opt.Registry.Register(served{name: name, info: tool.Info{
			Name:        name,
			Description: Describe(st.cfg.Name, t.Description),
			Parameters:  t.InputSchema,
			Parallel:    t.ReadOnly,
			ReadOnly:    t.ReadOnly,
			Destructive: t.Destructive,
			OpenWorld:   t.OpenWorld,
		}, client: client, tool: t.Name}); err != nil {
			warns = append(warns, fmt.Errorf("mcp: %s: tool %q is not registered as %q: %w", st.cfg.Name, t.Name, name, err))
			continue
		}
		names = append(names, name)
	}
	st.names = names
	for _, err := range warns {
		if h.opt.Warn != nil {
			h.opt.Warn(err)
		}
	}
	return nil
}

// died records a connection that ended after the handshake and takes its tools
// out of the registry, so a dead server is reported as failed and the model is
// never offered a tool that cannot run. A shutdown is not a death: Shutdown
// clears the client first and sets closed.
func (h *Host) died(st *serverState, err error) {
	lost := fmt.Errorf("mcp: %s: connection lost: %w", st.cfg.Name, err)
	h.mu.Lock()
	client := st.client
	shutting := h.closed
	if !shutting {
		h.unregister(st)
		if client != nil {
			st.err = lost
			st.client = nil
		} else if st.err == nil {
			// The reader stopped before connect stored the connection: the
			// server is failed, and saying so must not depend on which side of
			// that assignment the death landed.
			st.err = lost
		}
	}
	h.mu.Unlock()
	if shutting {
		return
	}
	// Report before closing: Close can spend three grace windows escalating a
	// server that will not die, and the warning is already true.
	if h.claimReport(st) {
		h.warn(lost)
	}
	// The process may still be alive — a malformed frame or a full pipe does not
	// kill it — so the connection is closed here. Nothing else holds it now, and
	// leaving it open would outlive the host.
	if client != nil {
		_ = client.Close()
	}
}

// claimReport records that one server's failure has been reported, so a death
// noticed twice — by the reader and by the listing that was in flight — is one
// warning. It reports whether this caller is the first.
func (h *Host) claimReport(st *serverState) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if st.reported {
		return false
	}
	st.reported = true
	return true
}

// unregister removes the names one server contributed. It is safe when the
// registry never took them.
func (h *Host) unregister(st *serverState) {
	for _, name := range st.names {
		h.opt.Registry.Unregister(name)
	}
	st.names = nil
}

// warn reports one warning.
func (h *Host) warn(err error) {
	if h.opt.Warn != nil {
		h.opt.Warn(err)
	}
}

// Reload closes every connection and starts again, which is what a config
// reload does: a changed command line, environment or tool list all arrive this
// way. No old process survives it.
func (h *Host) Reload(ctx context.Context) []error {
	if err := h.Shutdown(ctx); err != nil {
		return []error{err}
	}
	h.mu.Lock()
	h.closed = false
	for _, st := range h.servers {
		st.client = nil
		st.err = nil
	}
	h.mu.Unlock()
	return h.Start(ctx)
}

// Shutdown closes every connection in parallel and removes its tools. A server
// that ignores the polite shutdown is escalated by Client.Close.
func (h *Host) Shutdown(ctx context.Context) error {
	h.mu.Lock()
	h.closed = true
	states := slices.Clone(h.servers)
	h.mu.Unlock()
	var wg sync.WaitGroup
	for _, st := range states {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.mu.Lock()
			h.unregister(st)
			client := st.client
			st.client = nil
			h.mu.Unlock()
			if client != nil {
				_ = client.Close()
			}
		}()
	}
	wg.Wait()
	return ctx.Err()
}

// Status reports every server, in config order, for get_extensions and doctor.
func (h *Host) Status() []Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Status, 0, len(h.servers))
	for _, st := range h.servers {
		status := Status{Name: st.cfg.Name, Command: st.cfg.Command, Tools: len(st.names), State: StateRunning}
		switch {
		case h.closed:
			status.State = StateStopped
		case st.connecting:
			// Between dialling and the handshake's answer: not failed, just not
			// ready, and a /mcp or doctor read must not call it dead.
			status.State = StateStarting
		case st.err != nil:
			// A dial that failed, or a death the host has already recorded: its
			// reason is the one a user acts on.
			status.State = StateFailed
			status.Error = st.err.Error()
			status.Tools = 0
		case st.client == nil:
			status.State = StateFailed
			status.Error = "not connected"
		case st.client.Err() != nil:
			// The reader stopped and the death has not been recorded yet: report
			// what the connection says rather than call it healthy.
			status.State = StateFailed
			status.Error = st.client.Err().Error()
			status.Tools = 0
		}
		out = append(out, status)
	}
	return out
}

// served is one server tool in the registry's shape.
type served struct {
	name   string
	info   tool.Info
	client *Client
	tool   string
}

func (s served) Info() tool.Info { return s.info }

// Run forwards one call to the server. The permission policy has already
// decided whether it may run: this is not a way around it.
func (s served) Run(ctx context.Context, args json.RawMessage, _ *tool.Context) (tool.Result, error) {
	return s.client.Call(ctx, s.tool, args)
}
