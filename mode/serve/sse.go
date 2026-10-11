package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/AutumnsGrove/nova/proto"
)

// streamItem is one SSE payload with its sequence number. The payload is a
// marshalled event or ui_request; the transport does not interpret it.
type streamItem struct {
	seq     int64
	payload []byte
}

// publish assigns the next sequence number, appends the payload to the ring,
// and fans it out. A client whose buffer is full is dropped, never blocked.
func (s *server) publish(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	it := streamItem{seq: s.seq, payload: data}
	s.ring = append(s.ring, it)
	if len(s.ring) > s.opt.RingSize {
		s.ring = append([]streamItem(nil), s.ring[len(s.ring)-s.opt.RingSize:]...)
	}
	for ch := range s.clients {
		select {
		case ch <- it:
		default:
			delete(s.clients, ch)
			close(ch)
		}
	}
}

// subscribe registers a client and returns the events it must replay. With no
// Last-Event-ID (hasLast false) it starts live. If lastID is older than the
// ring, resync is true and the caller sends a state snapshot instead.
func (s *server) subscribe(lastID int64, hasLast bool) (replay []streamItem, ch chan streamItem, resync bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch = make(chan streamItem, 256)
	s.clients[ch] = struct{}{}
	if !hasLast {
		return nil, ch, false
	}
	if len(s.ring) > 0 && lastID+1 < s.ring[0].seq {
		return nil, ch, true
	}
	for _, it := range s.ring {
		if it.seq > lastID {
			replay = append(replay, it)
		}
	}
	return replay, ch, false
}

// unsubscribe removes a client. It is safe when the publisher dropped it first.
func (s *server) unsubscribe(ch chan streamItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.clients[ch]; ok {
		delete(s.clients, ch)
		close(ch)
	}
}

// snapshot builds the resync event: a full state_changed carrying get_state.
func (s *server) snapshot(ctx context.Context) (streamItem, bool) {
	resp := s.mode.Handle(ctx, proto.Command{Type: proto.CmdGetState})
	if !resp.OK {
		return streamItem{}, false
	}
	ev := proto.Event{Type: proto.EventStateChanged, State: resp.Data, Reason: "resync"}
	data, err := json.Marshal(ev)
	if err != nil {
		return streamItem{}, false
	}
	s.mu.Lock()
	s.seq++
	seq := s.seq
	s.mu.Unlock()
	return streamItem{seq: seq, payload: data}, true
}

// handleEvents streams the server's events as server-sent events.
func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, proto.CodeInternal, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	lastID, hasLast := parseLastEventID(r.Header.Get("Last-Event-ID"))
	replay, ch, resync := s.subscribe(lastID, hasLast)
	defer s.unsubscribe(ch)

	var skipThrough int64
	if resync {
		if snap, ok := s.snapshot(r.Context()); ok {
			writeSSE(w, snap)
			flusher.Flush()
			skipThrough = snap.seq
		}
	}
	for _, it := range replay {
		writeSSE(w, it)
	}
	if len(replay) > 0 {
		flusher.Flush()
	}

	heartbeat := s.opt.Clock.After(s.opt.Heartbeat)
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.done:
			_, _ = io.WriteString(w, ": closing\n\n")
			flusher.Flush()
			return
		case <-heartbeat:
			_, _ = io.WriteString(w, ": ping\n\n")
			flusher.Flush()
			heartbeat = s.opt.Clock.After(s.opt.Heartbeat)
		case it, ok := <-ch:
			if !ok {
				return
			}
			if it.seq <= skipThrough {
				continue
			}
			writeSSE(w, it)
			flusher.Flush()
		}
	}
}

// writeSSE writes one item as an id and data line.
func writeSSE(w io.Writer, it streamItem) {
	fmt.Fprintf(w, "id: %d\ndata: %s\n\n", it.seq, it.payload)
}

// parseLastEventID parses the Last-Event-ID header. An absent or invalid value
// reports hasLast false.
func parseLastEventID(header string) (int64, bool) {
	if header == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(header, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}
