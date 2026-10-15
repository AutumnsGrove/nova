package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/mode"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/testkit"
)

// baseConfig is a minimal config for a serve test server.
func baseConfig() *config.Config {
	cfg := config.Defaults()
	cfg.Model = "test/model"
	cfg.Tools.Active = []string{"read"}
	off := false
	cfg.Compaction.Enabled = &off
	return &cfg
}

// newModeServer builds a mode.Server over a scripted provider.
func newModeServer(t *testing.T, cfg *config.Config, turns ...testkit.Turn) (*mode.Server, *testkit.Provider) {
	t.Helper()
	prov := testkit.NewProvider(testkit.RealClock(), turns...)
	s, err := mode.New(mode.Options{
		Config: cfg, CWD: t.TempDir(), Home: t.TempDir(),
		Provider: prov, NoSession: true, Version: "test",
	})
	if err != nil {
		t.Fatalf("mode.New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, prov
}

// newTestHTTP starts an httptest server over the transport and stops both when
// the test ends.
func newTestHTTP(t *testing.T, s *mode.Server, opt Options) (*httptest.Server, *server) {
	t.Helper()
	srv, err := newServer(s, opt)
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	srv.start()
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	t.Cleanup(srv.close)
	return ts, srv
}

// do posts a JSON body and returns the response.
func do(t *testing.T, method, url, contentType, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

// decodeJSON reads and decodes a response body.
func decodeJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode body: %v", err)
	}
}

func TestServe_ATH01_NonLoopbackWithoutToken(t *testing.T) {
	s, _ := newModeServer(t, baseConfig())
	err := ListenAndServe(context.Background(), s, Options{Listen: "0.0.0.0:0"})
	if err == nil {
		t.Fatal("binding 0.0.0.0 without a token should fail")
	}
}

func TestServe_ATH02_NonLoopbackWithToken(t *testing.T) {
	s, _ := newModeServer(t, baseConfig())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ListenAndServe(ctx, s, Options{Listen: "0.0.0.0:0", Token: "secret"}); err != nil {
		t.Fatalf("non-loopback with a token: %v", err)
	}
}

func TestServe_ATH03_LoopbackForms(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:8080", "[::1]:8080", "localhost:8080", "127.0.0.5:1"} {
		if !loopbackListen(addr) {
			t.Errorf("loopbackListen(%q) = false, want true", addr)
		}
	}
}

func TestServe_ATH04_WildcardAndEmptyHost(t *testing.T) {
	for _, addr := range []string{":8080", "::", "", "0.0.0.0:80", "[::]:80"} {
		if loopbackListen(addr) {
			t.Errorf("loopbackListen(%q) = true, want false", addr)
		}
	}
}

func TestServe_ATH05_ATH06_TokenRejected(t *testing.T) {
	s, _ := newModeServer(t, baseConfig())
	ts, _ := newTestHTTP(t, s, Options{Token: "secret"})
	var bodies []string
	for _, header := range []string{"", "Bearer wrong", "Basic secret", "Bearer "} {
		req, _ := http.NewRequest("GET", ts.URL+"/v1/state", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("header %q: status %d, want 401", header, resp.StatusCode)
		}
		if got := resp.Header.Get("WWW-Authenticate"); got != "Bearer" {
			t.Fatalf("header %q: WWW-Authenticate = %q", header, got)
		}
		bodies = append(bodies, string(body))
	}
	for i := 1; i < len(bodies); i++ {
		if bodies[i] != bodies[0] {
			t.Fatalf("error bodies differ: %q vs %q", bodies[0], bodies[i])
		}
	}
}

func TestServe_ATH07_CorrectToken(t *testing.T) {
	s, _ := newModeServer(t, baseConfig())
	ts, _ := newTestHTTP(t, s, Options{Token: "secret"})
	req, _ := http.NewRequest("GET", ts.URL+"/v1/state", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
}

func TestServe_ATH08_TokenInQueryNotAccepted(t *testing.T) {
	s, _ := newModeServer(t, baseConfig())
	ts, _ := newTestHTTP(t, s, Options{Token: "secret"})
	resp := do(t, "GET", ts.URL+"/v1/state?token=secret", "", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("query token accepted: status %d", resp.StatusCode)
	}
}

func TestServe_ATH09_TokenNeverLogged(t *testing.T) {
	const token = "top-secret-token-value"
	s, _ := newModeServer(t, baseConfig())
	ts, _ := newTestHTTP(t, s, Options{Token: token})
	var captured bytes.Buffer
	log.SetOutput(&captured)
	defer log.SetOutput(os.Stderr)

	req, _ := http.NewRequest("GET", ts.URL+"/v1/state", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()
	if strings.Contains(captured.String(), token) {
		t.Fatalf("token appeared in logs: %q", captured.String())
	}
}

func TestServe_ATH10_ConstantTimeComparison(t *testing.T) {
	if !constantTimeEqual("abc", "abc") || constantTimeEqual("abc", "abd") {
		t.Fatal("constantTimeEqual misbehaves")
	}
	src, err := os.ReadFile("serve.go")
	if err != nil {
		t.Fatalf("read serve.go: %v", err)
	}
	if !strings.Contains(string(src), "subtle.ConstantTimeCompare") {
		t.Fatal("serve.go does not use subtle.ConstantTimeCompare")
	}
}

func TestServe_ATH11_HealthzNeedsNoToken(t *testing.T) {
	s, _ := newModeServer(t, baseConfig())
	ts, _ := newTestHTTP(t, s, Options{Token: "secret"})
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("healthz: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("healthz = %d %q, want 200 ok", resp.StatusCode, body)
	}
}

// getWithHost issues a GET with an explicit Host header.
func getWithHost(t *testing.T, url, host string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("GET", url+"/v1/state", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	return resp
}

func TestServe_ATH12_ATH13_ATH14_HostCheck(t *testing.T) {
	s, _ := newModeServer(t, baseConfig())
	ts, _ := newTestHTTP(t, s, Options{})
	evil := getWithHost(t, ts.URL, "evil.example")
	evil.Body.Close()
	if evil.StatusCode != http.StatusForbidden {
		t.Fatalf("evil Host: status %d, want 403", evil.StatusCode)
	}
	for _, host := range []string{"localhost:3000", "127.0.0.1:1", "[::1]:9"} {
		resp := getWithHost(t, ts.URL, host)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Host %q: status %d, want 200", host, resp.StatusCode)
		}
	}

	s2, _ := newModeServer(t, baseConfig())
	ts2, _ := newTestHTTP(t, s2, Options{AllowedHosts: []string{"proxy.local"}})
	resp := getWithHost(t, ts2.URL, "proxy.local")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("AllowedHosts: status %d, want 200", resp.StatusCode)
	}
}

func TestServe_ATH15_HostCheckSkippedWithToken(t *testing.T) {
	s, _ := newModeServer(t, baseConfig())
	ts, _ := newTestHTTP(t, s, Options{Token: "secret"})
	req, _ := http.NewRequest("GET", ts.URL+"/v1/state", nil)
	req.Host = "evil.example"
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("with a token the Host check should be skipped: %d", resp.StatusCode)
	}
}

func TestServe_ATH16_ATH17_ATH18_ATH19_CORS(t *testing.T) {
	s, _ := newModeServer(t, baseConfig())
	ts, _ := newTestHTTP(t, s, Options{})
	resp := do(t, "GET", ts.URL+"/v1/state", "", "")
	resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("CORS off: Allow-Origin = %q", got)
	}

	s2, _ := newModeServer(t, baseConfig())
	ts2, _ := newTestHTTP(t, s2, Options{CORSOrigin: "https://app.example"})
	req, _ := http.NewRequest("GET", ts2.URL+"/v1/state", nil)
	req.Header.Set("Origin", "https://app.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("cors request: %v", err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://app.example" {
		t.Fatalf("matching origin: Allow-Origin = %q", got)
	}
	if !strings.Contains(resp.Header.Get("Vary"), "Origin") {
		t.Fatalf("Vary = %q, want Origin", resp.Header.Get("Vary"))
	}

	req2, _ := http.NewRequest("GET", ts2.URL+"/v1/state", nil)
	req2.Header.Set("Origin", "https://other.example")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("cors request: %v", err)
	}
	resp2.Body.Close()
	if got := resp2.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("non-matching origin: Allow-Origin = %q", got)
	}

	if _, err := Handler(s, Options{CORSOrigin: "*"}); err == nil {
		t.Fatal("CORSOrigin * should be a configuration error")
	}

	req3, _ := http.NewRequest("OPTIONS", ts2.URL+"/v1/commands", nil)
	req3.Header.Set("Origin", "https://app.example")
	req3.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
	resp3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusNoContent {
		t.Fatalf("preflight status %d, want 204", resp3.StatusCode)
	}
	if got := resp3.Header.Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Authorization") || !strings.Contains(got, "Content-Type") {
		t.Fatalf("Allow-Headers = %q", got)
	}
}

func TestServe_HTP01_HTP02_HTP05_CommandRoundTrip(t *testing.T) {
	s, _ := newModeServer(t, baseConfig())
	ts, _ := newTestHTTP(t, s, Options{})

	resp := do(t, "POST", ts.URL+"/v1/commands", "application/json", `{"type":"get_state"}`)
	var frame proto.Response
	decodeJSON(t, resp, &frame)
	if resp.StatusCode != http.StatusOK || !frame.OK || frame.Command != "get_state" {
		t.Fatalf("get_state = %d %+v", resp.StatusCode, frame)
	}

	resp = do(t, "POST", ts.URL+"/v1/commands", "application/json", `{"type":"no_such_command"}`)
	frame = proto.Response{}
	decodeJSON(t, resp, &frame)
	if resp.StatusCode != http.StatusOK || frame.OK || frame.Error == nil {
		t.Fatalf("unknown command = %d %+v", resp.StatusCode, frame)
	}

	resp = do(t, "POST", ts.URL+"/v1/commands", "application/json", `{not json`)
	frame = proto.Response{}
	decodeJSON(t, resp, &frame)
	if resp.StatusCode != http.StatusBadRequest || frame.Error == nil || frame.Error.Code != proto.CodeBadRequest {
		t.Fatalf("invalid JSON = %d %+v", resp.StatusCode, frame)
	}
}

func TestServe_HTP03_HTP04_RequestBody(t *testing.T) {
	s, _ := newModeServer(t, baseConfig())
	ts, _ := newTestHTTP(t, s, Options{MaxBody: 64})
	resp := do(t, "POST", ts.URL+"/v1/commands", "text/plain", `{"type":"get_state"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("wrong content type: %d, want 415", resp.StatusCode)
	}

	resp = do(t, "POST", ts.URL+"/v1/commands", "application/json", strings.Repeat("a", 200))
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: %d, want 413", resp.StatusCode)
	}
}

func TestServe_HTP06_HTP07_Routes(t *testing.T) {
	s, _ := newModeServer(t, baseConfig())
	ts, _ := newTestHTTP(t, s, Options{})
	resp := do(t, "GET", ts.URL+"/v1/commands", "", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET /v1/commands: %d, want 405", resp.StatusCode)
	}
	resp = do(t, "GET", ts.URL+"/nope", "", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown route: %d, want 404", resp.StatusCode)
	}
}

func TestServe_HTP08_HandlerPanic(t *testing.T) {
	s, _ := newModeServer(t, baseConfig())
	ts, srv := newTestHTTP(t, s, Options{})
	srv.mux.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) { panic("kaboom secret") })
	resp := do(t, "GET", ts.URL+"/boom", "", "")
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("panic status %d, want 500", resp.StatusCode)
	}
	for _, leak := range []string{"kaboom", "panic", "goroutine"} {
		if strings.Contains(string(body), leak) {
			t.Fatalf("panic body leaks %q: %s", leak, body)
		}
	}
}

func TestServe_HTP09_RunSurvivesClientDisconnect(t *testing.T) {
	s, _ := newModeServer(t, baseConfig(), testkit.Turn{Text: "done"})
	ts, _ := newTestHTTP(t, s, Options{})
	events, cancel := s.Subscribe(64)
	defer cancel()

	ctx, cancelReq := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "POST", ts.URL+"/v1/commands", strings.NewReader(`{"type":"prompt","text":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	resp.Body.Close()
	cancelReq()

	select {
	case ev := <-events:
		for ev.Type != proto.EventRunEnd {
			ev = <-events
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not finish after the client disconnected")
	}
}

func TestServe_HTP10_UnknownUIResponse(t *testing.T) {
	s, _ := newModeServer(t, baseConfig())
	ts, _ := newTestHTTP(t, s, Options{})
	resp := do(t, "POST", ts.URL+"/v1/ui_response", "application/json", `{"type":"ui_response","id":"nope","ok":true}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown ui_response: %d, want 404", resp.StatusCode)
	}
}

func TestServe_HTP12_StateRoute(t *testing.T) {
	s, _ := newModeServer(t, baseConfig())
	ts, _ := newTestHTTP(t, s, Options{})
	resp := do(t, "GET", ts.URL+"/v1/state", "", "")
	var frame proto.Response
	decodeJSON(t, resp, &frame)
	if !frame.OK || frame.Command != "get_state" {
		t.Fatalf("state = %+v", frame)
	}
}

func TestServe_HTP13_SlowLorisHeader(t *testing.T) {
	s, _ := newModeServer(t, baseConfig())
	srv, err := newServer(s, Options{headerTimeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	t.Cleanup(srv.close)
	ts := httptest.NewUnstartedServer(srv)
	ts.Config.ReadHeaderTimeout = 50 * time.Millisecond
	ts.Start()
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "GET /healthz HTTP/1.1\r\nHost: 127.0.0.1\r\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadAll(conn); err != nil {
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			t.Fatal("connection was not closed after the header timeout")
		}
	}
}

func TestServe_HTP14_GracefulShutdown(t *testing.T) {
	s, _ := newModeServer(t, baseConfig())
	ts, srv := newTestHTTP(t, s, Options{})

	resp, err := http.Get(ts.URL + "/v1/events")
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	defer resp.Body.Close()
	// Give the handler a moment to read the initial headers, then stop.
	srv.close()
	_ = resp.Body.(io.Reader)
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), ": closing") {
		t.Fatalf("stream did not end with a final comment: %q", body)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if err := ListenAndServe(ctx, s, Options{Listen: "127.0.0.1:0"}); err != nil {
		t.Fatalf("ListenAndServe: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("shutdown took %v", elapsed)
	}
}

// deadlineRecorder records the write deadline a handler asks for, the way
// net/http's real response writer accepts one through http.ResponseController.
type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
	set      bool
}

func (d *deadlineRecorder) SetWriteDeadline(t time.Time) error {
	d.deadline, d.set = t, true
	return nil
}

func (d *deadlineRecorder) Flush() { d.ResponseRecorder.Flush() }

func TestServe_HTP15_WriteDeadlineOnRequestRoutes(t *testing.T) {
	s, _ := newModeServer(t, baseConfig())
	srv, err := newServer(s, Options{})
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	t.Cleanup(srv.close)

	for _, route := range []struct{ method, path string }{
		{"GET", "/v1/state"},
		{"POST", "/v1/commands"},
		{"POST", "/v1/ui_response"},
		{"GET", "/healthz"},
	} {
		rec := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
		req := httptest.NewRequest(route.method, route.path, strings.NewReader("{}"))
		before := time.Now()
		srv.ServeHTTP(rec, req)
		if !rec.set {
			t.Errorf("%s %s: no write deadline set", route.method, route.path)
			continue
		}
		if got := rec.deadline.Sub(before); got < 25*time.Second || got > 35*time.Second {
			t.Errorf("%s %s: deadline %v from now, want about 30s", route.method, route.path, got)
		}
	}
}

func TestServe_HTP16_NoWriteDeadlineOnEvents(t *testing.T) {
	s, _ := newModeServer(t, baseConfig())
	srv, err := newServer(s, Options{})
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	t.Cleanup(srv.close)

	ctx, cancel := context.WithCancel(context.Background())
	rec := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	req := httptest.NewRequest("GET", "/v1/events", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() { srv.ServeHTTP(rec, req); close(done) }()
	cancel() // end the stream; the handler has set up (or skipped) its deadline by now
	<-done
	if rec.set {
		t.Fatalf("event stream got a write deadline %v; it must stay open", rec.deadline)
	}
}
