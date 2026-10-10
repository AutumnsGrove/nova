package ext

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/AutumnsGrove/nova/proto"
)

// maxFrameLine is the documented 16 MiB protocol line cap.
const maxFrameLine = 16 << 20

// Errors a request can fail with.
var (
	errDeadline  = errors.New("deadline")
	errCancelled = errors.New("cancelled")
	errExited    = errors.New("exited")
)

// Clock is the time seam. It is satisfied by testkit.Clock, so host tests can
// drive deadlines and backoff without sleeping on real time.
type Clock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) error
	After(d time.Duration) <-chan time.Time
}

// outFrame is a frame the host sends to an extension. Only the fields a given
// frame needs are set; the rest are omitted.
type outFrame struct {
	Type         string            `json:"type"`
	ID           string            `json:"id,omitempty"`
	Version      int               `json:"version,omitempty"`
	Extension    string            `json:"extension,omitempty"`
	Session      string            `json:"session,omitempty"`
	CWD          string            `json:"cwd,omitempty"`
	Mode         string            `json:"mode,omitempty"`
	Capabilities []string          `json:"capabilities,omitempty"`
	Flags        map[string]string `json:"flags,omitempty"`
	Hook         string            `json:"hook,omitempty"`
	Event        json.RawMessage   `json:"event,omitempty"`
	DeadlineMS   int               `json:"deadline_ms,omitempty"`
	Name         string            `json:"name,omitempty"`
	Arguments    json.RawMessage   `json:"arguments,omitempty"`
	CallID       string            `json:"call_id,omitempty"`
	Args         string            `json:"args,omitempty"`
	Kind         string            `json:"kind,omitempty"`
	Data         json.RawMessage   `json:"data,omitempty"`
	Keep         bool              `json:"keep,omitempty"`
	Method       string            `json:"method,omitempty"`
	Message      any               `json:"message,omitempty"`
	Usage        any               `json:"usage,omitempty"`
	Error        *proto.Error      `json:"error,omitempty"`
}

// reply is the subset of an extension's answer the host reads.
type reply struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Content string          `json:"content,omitempty"`
	IsError bool            `json:"is_error,omitempty"`
	Details json.RawMessage `json:"details,omitempty"`
	Error   *proto.Error    `json:"error,omitempty"`
}

// handler receives one unsolicited frame from an extension. typ "__log" is the
// connection's own diagnostics channel, not a protocol frame.
type handler func(typ string, line []byte, r reply)

// conn is one framed connection to an extension process. It correlates
// requests by id, reads on its own goroutine, and writes through a queue so a
// blocked pipe cannot stall the caller.
type conn struct {
	name    string
	r       *proto.Reader
	w       *proto.Writer
	closer  io.Closer
	handler handler
	clock   Clock

	writeCh  chan any
	closeCh  chan struct{}
	once     sync.Once
	closeMu  sync.Mutex
	closeErr error

	mu      sync.Mutex
	next    int64
	pending map[string]chan reply
}

// newConn builds a connection and starts its reader and writer goroutines.
func newConn(name string, r io.Reader, w io.Writer, closer io.Closer, clock Clock, h handler) *conn {
	c := &conn{
		name:    name,
		r:       proto.NewReader(r, maxFrameLine),
		w:       proto.NewWriter(w),
		closer:  closer,
		handler: h,
		clock:   clock,
		writeCh: make(chan any, 64),
		closeCh: make(chan struct{}),
		pending: map[string]chan reply{},
	}
	go c.writeLoop()
	go c.readLoop()
	return c
}

// writeLoop serialises writes so concurrent requests cannot interleave frames.
func (c *conn) writeLoop() {
	for {
		select {
		case <-c.closeCh:
			return
		case v := <-c.writeCh:
			if err := c.w.Write(v); err != nil {
				c.closeWith(err)
				return
			}
		}
	}
}

// readLoop reads frames until the connection ends. A line that is not valid
// JSON is logged and ignored; an over-long line ends the connection.
func (c *conn) readLoop() {
	for {
		line, err := c.r.Next()
		if err != nil {
			c.closeWith(err)
			return
		}
		var probe struct {
			Type string `json:"type"`
			ID   string `json:"id,omitempty"`
		}
		if json.Unmarshal(line, &probe) != nil {
			c.warn("extension sent a line that is not JSON")
			continue
		}
		if probe.ID != "" && c.deliver(probe.ID, line) {
			continue
		}
		if c.handler != nil {
			var r reply
			_ = json.Unmarshal(line, &r)
			c.handler(probe.Type, line, r)
		}
	}
}

// deliver routes a reply to the request waiting for it. It reports whether a
// waiter was found; a late or unknown id is left to the handler.
func (c *conn) deliver(id string, line []byte) bool {
	c.mu.Lock()
	ch := c.pending[id]
	c.mu.Unlock()
	if ch == nil {
		return false
	}
	var r reply
	if json.Unmarshal(line, &r) != nil {
		return false
	}
	select {
	case ch <- r:
	default:
	}
	return true
}

// request sends a frame and waits for its answer, a deadline, cancellation, or
// the connection ending.
func (c *conn) request(ctx context.Context, frame *outFrame, timeout time.Duration) (reply, error) {
	id := c.newID()
	frame.ID = id
	ch := make(chan reply, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := c.send(frame); err != nil {
		return reply{}, errExited
	}
	select {
	case r := <-ch:
		return r, nil
	case <-c.clock.After(timeout):
		return reply{}, errDeadline
	case <-ctx.Done():
		return reply{}, errCancelled
	case <-c.closeCh:
		return reply{}, errExited
	}
}

// notify sends a fire-and-forget frame.
func (c *conn) notify(v any) error { return c.send(v) }

// send queues one frame for the writer goroutine.
func (c *conn) send(v any) error {
	select {
	case c.writeCh <- v:
		return nil
	case <-c.closeCh:
		return errExited
	}
}

// newID returns the next request id.
func (c *conn) newID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.next++
	return c.name + "_" + itoa(c.next)
}

// closeWith records the first close reason and unblocks every waiter.
func (c *conn) closeWith(err error) {
	c.once.Do(func() {
		c.closeMu.Lock()
		c.closeErr = err
		c.closeMu.Unlock()
		if c.closer != nil {
			_ = c.closer.Close()
		}
		close(c.closeCh)
	})
}

// err returns the recorded close reason.
func (c *conn) err() error {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	return c.closeErr
}

// done returns a channel closed when the connection ends.
func (c *conn) done() <-chan struct{} { return c.closeCh }

// warn reports a per-connection diagnostic through the host's log.
func (c *conn) warn(msg string) {
	if c.handler != nil {
		c.handler("__log", []byte(msg), reply{Type: "warn", Content: msg})
	}
}

// itoa renders a positive int without importing strconv.
func itoa(n int64) string {
	if n <= 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
