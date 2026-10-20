package serve

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/permission"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/testkit"
)

// sseBlock is one parsed server-sent event.
type sseBlock struct {
	id      string
	data    string
	comment string
}

// nextSSE reads one event block. A comment-only block sets comment.
func nextSSE(t *testing.T, br *bufio.Reader) sseBlock {
	t.Helper()
	var b sseBlock
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			return b
		}
		switch {
		case strings.HasPrefix(line, "id: "):
			b.id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "data: "):
			b.data = strings.TrimPrefix(line, "data: ")
		case strings.HasPrefix(line, ":"):
			b.comment = strings.TrimPrefix(line, ": ")
		}
	}
}

// openSSE starts a streaming GET and returns the response and reader.
func openSSE(t *testing.T, url string, lastEventID string) (*http.Response, *bufio.Reader) {
	t.Helper()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("events status %d", resp.StatusCode)
	}
	return resp, bufio.NewReader(resp.Body)
}

// eventType returns the type field of an SSE data payload.
func eventType(t *testing.T, data string) string {
	t.Helper()
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(data), &probe); err != nil {
		t.Fatalf("decode event %q: %v", data, err)
	}
	return probe.Type
}

// prompt posts a prompt and drains the response.
func prompt(t *testing.T, url, text string) {
	t.Helper()
	resp := do(t, "POST", url+"/v1/commands", "application/json", `{"type":"prompt","text":"`+text+`"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("prompt status %d", resp.StatusCode)
	}
}

func TestServe_SSE01_OrderedEventsDecode(t *testing.T) {
	s, _ := newModeServer(t, baseConfig(), testkit.Turn{Text: "hello"})
	ts, _ := newTestHTTP(t, s, Options{})
	resp, br := openSSE(t, ts.URL+"/v1/events", "")
	defer resp.Body.Close()
	prompt(t, ts.URL, "hi")

	var last int64
	seen := 0
	for {
		b := nextSSE(t, br)
		if b.data == "" {
			continue
		}
		var ev proto.Event
		if err := json.Unmarshal([]byte(b.data), &ev); err != nil {
			t.Fatalf("decode event: %v", err)
		}
		if ev.Seq <= last {
			t.Fatalf("seq %d after %d", ev.Seq, last)
		}
		last = ev.Seq
		seen++
		if ev.Type == proto.EventRunEnd {
			break
		}
	}
	if seen == 0 {
		t.Fatal("no events")
	}
}

func TestServe_SSE02_Heartbeat(t *testing.T) {
	s, _ := newModeServer(t, baseConfig())
	ts, _ := newTestHTTP(t, s, Options{Heartbeat: 5 * time.Millisecond})
	resp, br := openSSE(t, ts.URL+"/v1/events", "")
	defer resp.Body.Close()

	b := nextSSE(t, br)
	if b.comment != "ping" {
		t.Fatalf("first block = %+v, want a ping comment", b)
	}
}

func TestServe_SSE03_Headers(t *testing.T) {
	s, _ := newModeServer(t, baseConfig())
	ts, _ := newTestHTTP(t, s, Options{})
	resp, _ := openSSE(t, ts.URL+"/v1/events", "")
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q", cc)
	}
	if xa := resp.Header.Get("X-Accel-Buffering"); xa != "no" {
		t.Fatalf("X-Accel-Buffering = %q", xa)
	}
}

func TestServe_SSE04_FlushPerEvent(t *testing.T) {
	s, _ := newModeServer(t, baseConfig(), testkit.Turn{Text: "hello"})
	ts, _ := newTestHTTP(t, s, Options{})
	resp, br := openSSE(t, ts.URL+"/v1/events", "")
	defer resp.Body.Close()
	prompt(t, ts.URL, "hi")
	// The first event must arrive while the stream stays open.
	b := nextSSE(t, br)
	if b.data == "" {
		t.Fatalf("first block has no data: %+v", b)
	}
}

func TestServe_SSE05_ResumeWithinRing(t *testing.T) {
	s, _ := newModeServer(t, baseConfig(), testkit.Turn{Text: "one"}, testkit.Turn{Text: "two"})
	ts, _ := newTestHTTP(t, s, Options{})
	resp, br := openSSE(t, ts.URL+"/v1/events", "")
	prompt(t, ts.URL, "hi")
	var ids []string
	for {
		b := nextSSE(t, br)
		if b.data != "" && eventType(t, b.data) == proto.EventRunEnd {
			ids = append(ids, b.id)
			break
		}
		if b.data != "" {
			ids = append(ids, b.id)
		}
	}
	resp.Body.Close()

	// Reconnect from the first event and expect the rest, in order.
	resp2, br2 := openSSE(t, ts.URL+"/v1/events", ids[0])
	defer resp2.Body.Close()
	b := nextSSE(t, br2)
	if b.id != ids[1] {
		t.Fatalf("resume id = %q, want %q", b.id, ids[1])
	}
}

func TestServe_SSE06_ResumeOlderThanRing(t *testing.T) {
	s, _ := newModeServer(t, baseConfig(), testkit.Turn{Text: "one"}, testkit.Turn{Text: "two"})
	ts, _ := newTestHTTP(t, s, Options{RingSize: 2})
	resp, br := openSSE(t, ts.URL+"/v1/events", "")
	prompt(t, ts.URL, "hi")
	for {
		if b := nextSSE(t, br); b.data != "" && eventType(t, b.data) == proto.EventRunEnd {
			break
		}
	}
	resp.Body.Close()

	resp2, br2 := openSSE(t, ts.URL+"/v1/events", "1")
	defer resp2.Body.Close()
	b := nextSSE(t, br2)
	var ev proto.Event
	if err := json.Unmarshal([]byte(b.data), &ev); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ev.Type != proto.EventStateChanged || ev.Reason != "resync" || len(ev.State) == 0 {
		t.Fatalf("resync event = %+v", ev)
	}
}

func TestServe_SSE07_NoLastEventIDStartsLive(t *testing.T) {
	s, _ := newModeServer(t, baseConfig(), testkit.Turn{Text: "one"}, testkit.Turn{Text: "two"})
	ts, _ := newTestHTTP(t, s, Options{})
	resp, br := openSSE(t, ts.URL+"/v1/events", "")
	prompt(t, ts.URL, "hi")
	var firstRunLast int64
	for {
		b := nextSSE(t, br)
		if b.data == "" {
			continue
		}
		var ev proto.Event
		_ = json.Unmarshal([]byte(b.data), &ev)
		if ev.Type == proto.EventRunEnd {
			firstRunLast = ev.Seq
			break
		}
	}
	resp.Body.Close()

	resp2, br2 := openSSE(t, ts.URL+"/v1/events", "")
	defer resp2.Body.Close()
	prompt(t, ts.URL, "again")
	b := nextSSE(t, br2)
	var ev proto.Event
	if err := json.Unmarshal([]byte(b.data), &ev); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ev.Seq <= firstRunLast {
		t.Fatalf("replayed old event seq %d (after %d)", ev.Seq, firstRunLast)
	}
}

func TestServe_SSE08_ManyClients(t *testing.T) {
	s, _ := newModeServer(t, baseConfig(), testkit.Turn{Text: "hello"})
	ts, _ := newTestHTTP(t, s, Options{})
	const clients = 20
	type stream struct {
		resp *http.Response
		br   *bufio.Reader
	}
	streams := make([]stream, clients)
	for i := range streams {
		resp, br := openSSE(t, ts.URL+"/v1/events", "")
		streams[i] = stream{resp, br}
		defer resp.Body.Close()
	}
	prompt(t, ts.URL, "hi")

	read := func(br *bufio.Reader) []string {
		var ids []string
		for {
			b := nextSSE(t, br)
			if b.data == "" {
				continue
			}
			ids = append(ids, b.id)
			if eventType(t, b.data) == proto.EventRunEnd {
				return ids
			}
		}
	}
	want := read(streams[0].br)
	for i := 1; i < clients; i++ {
		got := read(streams[i].br)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("client %d saw %v, want %v", i, got, want)
		}
	}
}

func TestServe_SSE09_SlowClientDropped(t *testing.T) {
	s, _ := newModeServer(t, baseConfig())
	srv, err := newServer(s, Options{})
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	slow := make(chan streamItem, 256)
	fast := make(chan streamItem, 4096)
	srv.clients[slow] = struct{}{}
	srv.clients[fast] = struct{}{}
	for i := 0; i < 300; i++ {
		srv.publish(proto.Event{Type: proto.EventError})
	}
	if _, ok := srv.clients[slow]; ok {
		t.Fatal("a full client was not dropped")
	}
	if _, ok := srv.clients[fast]; !ok {
		t.Fatal("a client that keeps up was dropped")
	}
	for i := 0; i < 256; i++ {
		<-slow
	}
	if _, open := <-slow; open {
		t.Fatal("the dropped client's channel is still open")
	}
}

func TestServe_SSE10_DisconnectReleases(t *testing.T) {
	s, _ := newModeServer(t, baseConfig())
	srv, err := newServer(s, Options{})
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	ts := httptest.NewServer(srv)
	testkit.NoLeaks(t)
	t.Cleanup(ts.Close)
	t.Cleanup(srv.close)

	resp, _ := openSSE(t, ts.URL+"/v1/events", "")
	resp.Body.Close()
}

func TestServe_SSE11_RunSurvivesLastClient(t *testing.T) {
	s, _ := newModeServer(t, baseConfig(), testkit.Turn{Text: "done"})
	ts, _ := newTestHTTP(t, s, Options{})
	events, cancel := s.Subscribe(64)
	defer cancel()
	resp, _ := openSSE(t, ts.URL+"/v1/events", "")
	prompt(t, ts.URL, "hi")
	resp.Body.Close()

	select {
	case ev := <-events:
		for ev.Type != proto.EventRunEnd {
			ev = <-events
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not finish after the last client left")
	}
}

func TestServe_SSE12_RingSizeHonoured(t *testing.T) {
	s, _ := newModeServer(t, baseConfig())
	srv, err := newServer(s, Options{RingSize: 5})
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	for i := 0; i < 20; i++ {
		srv.publish(proto.Event{Type: proto.EventError})
	}
	replay, ch, resync := srv.subscribe(15, true)
	if resync {
		t.Fatal("lastID 15 is inside the ring; want no resync")
	}
	if len(replay) != 5 {
		t.Fatalf("replay = %d events, want 5", len(replay))
	}
	if replay[0].seq != 16 {
		t.Fatalf("oldest replayable seq = %d, want 16", replay[0].seq)
	}
	srv.unsubscribe(ch)
}

func TestServe_HTP11_UIResponseRouted(t *testing.T) {
	cfg := baseConfig()
	cfg.Tools.Active = []string{"bash"}
	cfg.Permissions = permission.Policy{Default: permission.Ask, Headless: permission.Deny}
	s, _ := newModeServer(t, cfg,
		testkit.Turn{ToolCalls: []testkit.Call{{ID: "c1", Name: "bash", Args: `{"command":"echo hi"}`}}},
		testkit.Turn{Text: "done"},
	)
	ts, _ := newTestHTTP(t, s, Options{})
	resp, br := openSSE(t, ts.URL+"/v1/events", "")
	defer resp.Body.Close()
	prompt(t, ts.URL, "run it")

	var ui proto.UIRequest
	for {
		b := nextSSE(t, br)
		if b.data != "" && eventType(t, b.data) == proto.FrameUIRequest {
			if err := json.Unmarshal([]byte(b.data), &ui); err != nil {
				t.Fatalf("decode ui_request: %v", err)
			}
			break
		}
	}
	body, _ := json.Marshal(proto.UIResponse{Type: proto.FrameUIResponse, ID: ui.ID, OK: true})
	answer := do(t, "POST", ts.URL+"/v1/ui_response", "application/json", string(body))
	answer.Body.Close()
	if answer.StatusCode != http.StatusOK {
		t.Fatalf("ui_response status %d", answer.StatusCode)
	}

	for {
		b := nextSSE(t, br)
		if b.data != "" && eventType(t, b.data) == proto.EventRunEnd {
			return
		}
	}
}

// flushProbe is a ResponseWriter whose first Flush records how many clients
// were subscribed at that moment.
type flushProbe struct {
	*httptest.ResponseRecorder
	srv     *server
	flushed bool
	clients int
}

func (p *flushProbe) Flush() {
	if !p.flushed {
		p.flushed = true
		p.srv.mu.Lock()
		p.clients = len(p.srv.clients)
		p.srv.mu.Unlock()
	}
	p.ResponseRecorder.Flush()
}

// A client that has seen the response headers must already be subscribed:
// otherwise an event published right after it connects is delivered to nobody.
func TestServe_SSE13_SubscribedBeforeHeaders(t *testing.T) {
	s, _ := newModeServer(t, baseConfig())
	_, srv := newTestHTTP(t, s, Options{})
	// A cancelled request context makes the handler return as soon as it is streaming.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	probe := &flushProbe{ResponseRecorder: httptest.NewRecorder(), srv: srv}
	srv.handleEvents(probe, httptest.NewRequest("GET", "/v1/events", nil).WithContext(ctx))
	if !probe.flushed {
		t.Fatal("handler never flushed")
	}
	if probe.clients != 1 {
		t.Fatalf("clients subscribed at first flush = %d, want 1", probe.clients)
	}
}
