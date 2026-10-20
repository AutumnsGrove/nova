package ext

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
)

// maxFrameLine is the documented 16 MiB protocol line cap.
const maxFrameLine = 16 << 20

// writeQueueSize is how many frames may wait for the writer before a sender
// has to wait for the peer to read.
const writeQueueSize = 64

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
	Type    string                `json:"type"`
	ID      string                `json:"id,omitempty"`
	Result  json.RawMessage       `json:"result,omitempty"`
	Content string                `json:"content,omitempty"`
	IsError bool                  `json:"is_error,omitempty"`
	Details json.RawMessage       `json:"details,omitempty"`
	Parts   []message.ContentPart `json:"parts,omitempty"`
	Error   *proto.Error          `json:"error,omitempty"`
}

// handler receives one unsolicited frame from an extension, with the
// connection it arrived on so a reply never reaches a later process. typ
// "__log" is the connection's own diagnostics channel, not a protocol frame.
type handler func(c *conn, typ string, line []byte, r reply)

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

	writeCh chan any
	// ctx ends when the connection does, so work started on its behalf (a UI
	// prompt, a nested model call) stops with it.
	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once

	mu      sync.Mutex
	next    int64
	pending map[string]chan reply
}

// newConn builds a connection and starts its reader and writer goroutines.
func newConn(name string, r io.Reader, w io.Writer, closer io.Closer, clock Clock, h handler) *conn {
	ctx, cancel := context.WithCancel(context.Background())
	c := &conn{
		name:    name,
		r:       proto.NewReader(r, maxFrameLine),
		w:       proto.NewWriter(w),
		closer:  closer,
		handler: h,
		clock:   clock,
		writeCh: make(chan any, writeQueueSize),
		ctx:     ctx,
		cancel:  cancel,
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
		case <-c.ctx.Done():
			return
		case v := <-c.writeCh:
			if err := c.w.Write(v); err != nil {
				c.close()
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
			if errors.Is(err, proto.ErrLineTooLong) {
				c.warn("extension sent a frame over the size limit; closing the connection")
			}
			c.close()
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
			c.handler(c, probe.Type, line, r)
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
// the connection ending. The deadline also bounds queueing the frame, so a peer
// that has stopped reading cannot hold the caller past it.
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

	deadline := c.clock.After(timeout)
	select {
	case c.writeCh <- frame:
	case <-deadline:
		return reply{}, errDeadline
	case <-ctx.Done():
		return reply{}, errCancelled
	case <-c.ctx.Done():
		return reply{}, errExited
	}
	select {
	case r := <-ch:
		return r, nil
	case <-deadline:
		return reply{}, errDeadline
	case <-ctx.Done():
		return reply{}, errCancelled
	case <-c.ctx.Done():
		// An extension that answers and then exits closes the connection right
		// behind its answer, so prefer the answer if it already arrived.
		select {
		case r := <-ch:
			return r, nil
		default:
			return reply{}, errExited
		}
	}
}

// notify queues a fire-and-forget frame, waiting for room if the peer is slow.
// It gives up only when the connection ends.
func (c *conn) notify(v any) error {
	select {
	case c.writeCh <- v:
		return nil
	case <-c.ctx.Done():
		return errExited
	}
}

// tryNotify queues a frame only if there is room, for a frame (such as
// shutdown) that must never wait on a peer that has stopped reading.
func (c *conn) tryNotify(v any) {
	select {
	case c.writeCh <- v:
	default:
	}
}

// newID returns the next request id.
func (c *conn) newID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.next++
	return c.name + "_" + strconv.FormatInt(c.next, 10)
}

// close ends the connection once: it closes the pipes and wakes every waiter.
func (c *conn) close() {
	c.once.Do(func() {
		if c.closer != nil {
			_ = c.closer.Close()
		}
		c.cancel()
	})
}

// warn reports a per-connection diagnostic through the host's log.
func (c *conn) warn(msg string) {
	if c.handler != nil {
		c.handler(c, "__log", []byte(msg), reply{Type: "warn", Content: msg})
	}
}
