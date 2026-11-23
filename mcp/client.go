package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"slices"
	"sync"
	"time"

	"github.com/AutumnsGrove/nova/proc"
	"github.com/AutumnsGrove/nova/tool"
)

// defaultCallTimeout bounds one call when the config does not say.
const defaultCallTimeout = 60 * time.Second

// closeGrace is how long a server gets at each shutdown step: after its stdin
// closes, and again after SIGTERM, before the next, harsher one.
const closeGrace = 2 * time.Second

// Options configure one server connection.
type Options struct {
	// Name is the server's name from config; it prefixes every tool.
	Name string
	// Command, Args, Env and Dir describe the child process. Env is added to
	// the inherited environment.
	Command string
	Args    []string
	Env     map[string]string
	Dir     string
	// Inherit is the environment to start from, normally os.Environ().
	Inherit []string
	// Timeout bounds the handshake and one tool call.
	Timeout time.Duration
	// Version names this client in clientInfo.
	Version string
	// OnToolsChanged is called when the server reports that its tool list
	// changed. It runs on its own goroutine, so it may call back into Client.
	OnToolsChanged func()
}

// Client is a live connection to one MCP server.
type Client struct {
	opt   Options
	cmd   *exec.Cmd
	stdin io.WriteCloser
	out   *bufio.Reader

	wmu sync.Mutex // serialises writes: one message per line, never interleaved

	mu      sync.Mutex
	next    int64
	pending map[int64]chan *frame
	err     error // why the reader stopped; set before done closes
	version string

	done   chan struct{} // closed when the reader stops
	exited chan struct{} // closed when the process is gone

	closeOnce sync.Once
	closeErr  error
}

// Dial starts a server, performs the initialize handshake, and returns the
// connection. The server is in its own process group, so closing the client
// reaches anything it started.
func Dial(ctx context.Context, opt Options) (*Client, error) {
	if opt.Command == "" {
		return nil, fmt.Errorf("mcp: server %q has no command", opt.Name)
	}
	if opt.Timeout <= 0 {
		opt.Timeout = defaultCallTimeout
	}
	cmd := exec.Command(opt.Command, opt.Args...)
	cmd.Dir = opt.Dir
	cmd.Env = append(append([]string(nil), opt.Inherit...), envPairs(opt.Env)...)
	proc.SetGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: %s: stdin: %w", opt.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: %s: stdout: %w", opt.Name, err)
	}
	// A server's stderr is not captured and never becomes part of an error: it
	// is another program's output, which may contain anything, including a
	// credential. A server that will not start is run by hand to see why.
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp: %s: start %s: %w", opt.Name, opt.Command, err)
	}
	c := &Client{
		opt: opt, cmd: cmd, stdin: stdin, out: bufio.NewReaderSize(stdout, 64<<10),
		pending: map[int64]chan *frame{},
		done:    make(chan struct{}), exited: make(chan struct{}),
	}
	go c.read()
	go func() {
		_ = cmd.Wait()
		close(c.exited)
	}()
	if err := c.handshake(ctx); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// handshake performs initialize and notifications/initialized. The version the
// server answers with is the one in force: the protocol says the client adopts
// it rather than insisting on its own.
func (c *Client) handshake(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.opt.Timeout)
	defer cancel()
	raw, err := c.request(ctx, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "nova", "version": c.opt.Version},
	})
	if err != nil {
		return fmt.Errorf("mcp: %s: initialize: %w", c.opt.Name, err)
	}
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(raw, &init); err != nil {
		return fmt.Errorf("mcp: %s: initialize result: %w", c.opt.Name, err)
	}
	c.mu.Lock()
	c.version = init.ProtocolVersion
	c.mu.Unlock()
	if err := c.notify("notifications/initialized", map[string]any{}); err != nil {
		return fmt.Errorf("mcp: %s: initialized: %w", c.opt.Name, err)
	}
	return nil
}

// Version returns the protocol version negotiated by the handshake.
func (c *Client) Version() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.version
}

// Name returns the server's configured name.
func (c *Client) Name() string { return c.opt.Name }

// Pid is the server process's id, for a diagnostic and for the tests that prove
// no old process survives a reload.
func (c *Client) Pid() int {
	if c.cmd.Process == nil {
		return 0
	}
	return c.cmd.Process.Pid
}

// Err reports why the connection ended, or nil while it is alive.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// read is the single reader of the server's stdout. It delivers responses to
// whoever waits for them, answers a server request it cannot serve, and ends the
// connection when the stream does.
func (c *Client) read() {
	defer close(c.done)
	for {
		f, err := readFrame(c.out)
		if err != nil {
			c.fail(err)
			return
		}
		switch {
		case f.Method == "" && f.ID != nil:
			c.deliver(f)
		case f.Method != "" && f.ID != nil:
			// A server request: v1 supports none, so it is told so rather
			// than left waiting.
			c.reply(f.ID, nil, &wireError{Code: -32601, Message: "method not supported: " + f.Method})
		default:
			c.note(f.Method)
		}
	}
}

// note handles a notification from the server.
func (c *Client) note(method string) {
	if method == "notifications/tools/list_changed" && c.opt.OnToolsChanged != nil {
		go c.opt.OnToolsChanged()
	}
}

// deliver hands a response to its waiter. A response for an id nobody waits for
// is dropped: it is late, and the call it belonged to has already returned.
func (c *Client) deliver(f *frame) {
	var id int64
	if err := json.Unmarshal(*f.ID, &id); err != nil {
		return
	}
	c.mu.Lock()
	ch, ok := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if ok {
		ch <- f
	}
}

// fail records why the reader stopped and releases every waiter.
func (c *Client) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		c.err = err
	}
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
}

// request sends one request and waits for its response. A response carrying an
// error becomes an *RPCError; a closed connection is ErrClosed.
func (c *Client) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	body, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("mcp: encode %s: %w", method, err)
	}
	c.mu.Lock()
	c.next++
	id := c.next
	ch := make(chan *frame, 1)
	c.pending[id] = ch
	closed := c.err
	c.mu.Unlock()
	if closed != nil {
		return nil, closed
	}
	defer func() {
		c.mu.Lock()
		if _, ok := c.pending[id]; ok {
			delete(c.pending, id)
			close(ch)
		}
		c.mu.Unlock()
	}()
	rawID := json.RawMessage(fmt.Sprintf("%d", id))
	if err := c.write(frame{JSONRPC: "2.0", ID: &rawID, Method: method, Params: body}); err != nil {
		return nil, err
	}
	select {
	case f, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("mcp: %s: %w", method, c.doneErr())
		}
		if f.Error != nil {
			return nil, &RPCError{Code: f.Error.Code, Message: f.Error.Message}
		}
		return f.Result, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("mcp: %s: %w", method, ctx.Err())
	}
}

// reply answers a server request with a result or an error.
func (c *Client) reply(id *json.RawMessage, result any, rpcErr *wireError) {
	f := frame{JSONRPC: "2.0", ID: id, Error: rpcErr}
	if rpcErr == nil {
		if body, err := json.Marshal(result); err == nil {
			f.Result = body
		}
	}
	_ = c.write(f)
}

// notify sends a notification, which has no id and gets no answer.
func (c *Client) notify(method string, params any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return c.write(frame{JSONRPC: "2.0", Method: method, Params: body})
}

// write serialises one message onto the server's stdin.
func (c *Client) write(f frame) error {
	data, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("mcp: encode: %w", err)
	}
	data = append(data, '\n')
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := c.stdin.Write(data); err != nil {
		return fmt.Errorf("mcp: %s: write: %w", c.opt.Name, errors.Join(ErrClosed, err))
	}
	return nil
}

// doneErr names why the connection closed.
func (c *Client) doneErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Call runs one tool and maps its result onto the tool contract.
func (c *Client) Call(ctx context.Context, name string, args json.RawMessage) (tool.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, c.opt.Timeout)
	defer cancel()
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	raw, err := c.request(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return tool.Result{}, err
	}
	return resultFrom(raw)
}

// Close ends the connection: stdin first, because that is the protocol's own
// shutdown, then SIGTERM, then SIGKILL. No process in the group survives it.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		_ = c.stdin.Close()
		select {
		case <-c.exited:
		case <-time.After(closeGrace):
			proc.Terminate(c.cmd.Process.Pid)
			select {
			case <-c.exited:
			case <-time.After(closeGrace):
				proc.Kill(c.cmd.Process.Pid)
				<-c.exited
			}
		}
		<-c.done
	})
	return c.closeErr
}

// envPairs renders the added environment as KEY=value pairs.
func envPairs(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for _, k := range slices.Sorted(maps.Keys(env)) {
		out = append(out, k+"="+env[k])
	}
	return out
}

// osEnv is the inherited environment of a server, named so a test can replace
// it with a fixed slice.
var osEnv = func() []string { return os.Environ() }
