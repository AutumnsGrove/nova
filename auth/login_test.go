package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/testkit"
)

// fakeExchange is a stand-in for OpenRouter's code-exchange endpoint. It
// records the parsed request bodies and answers with a fixed status and body.
type fakeExchange struct {
	URL string

	mu       sync.Mutex
	requests []exchangeRequest
	status   int
	body     string
}

func newFakeExchange(t *testing.T, status int, body string) *fakeExchange {
	t.Helper()
	fx := &fakeExchange{status: status, body: body}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req exchangeRequest
		_ = json.Unmarshal(raw, &req)
		fx.mu.Lock()
		fx.requests = append(fx.requests, req)
		fx.mu.Unlock()
		w.WriteHeader(fx.status)
		io.WriteString(w, fx.body)
	}))
	t.Cleanup(srv.Close)
	fx.URL = srv.URL
	return fx
}

// count returns how many exchange calls have been made.
func (fx *fakeExchange) count() int {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	return len(fx.requests)
}

// first returns the first recorded exchange request.
func (fx *fakeExchange) first(t *testing.T) exchangeRequest {
	t.Helper()
	fx.mu.Lock()
	defer fx.mu.Unlock()
	if len(fx.requests) == 0 {
		t.Fatal("exchange was not called")
	}
	return fx.requests[0]
}

// syncBuffer is a concurrency-safe bytes.Buffer: Login writes to it from its
// own goroutine while the test reads the printed URL.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// loginResult is one Login return.
type loginResult struct {
	key string
	err error
}

// loginHarness runs Login in the background and captures its authorization URL.
type loginHarness struct {
	authURL     *url.URL
	callbackURL string
	printed     *syncBuffer
	result      chan loginResult
	cancel      context.CancelFunc
}

// startLogin begins a Login in a goroutine. A caller-supplied OpenBrowser is
// wrapped so the harness still learns the URL; when nil, the harness opens
// nothing, which also exercises the headless path.
func startLogin(t *testing.T, opts LoginOptions) *loginHarness {
	t.Helper()
	var printed syncBuffer
	authCh := make(chan string, 1)
	userOpen := opts.OpenBrowser
	opts.Out = &printed
	opts.OpenBrowser = func(u string) error {
		authCh <- u
		if userOpen != nil {
			return userOpen(u)
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h := &loginHarness{printed: &printed, result: make(chan loginResult, 1), cancel: cancel}
	go func() {
		key, err := Login(ctx, opts)
		h.result <- loginResult{key, err}
	}()
	select {
	case raw := <-authCh:
		parsed, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse auth URL: %v", err)
		}
		h.authURL = parsed
	case <-time.After(3 * time.Second):
		t.Fatal("Login did not reach the browser step in time")
	}
	h.callbackURL = h.authURL.Query().Get("callback_url")
	return h
}

// state returns the state parameter from the captured authorization URL.
func (h *loginHarness) state() string { return h.authURL.Query().Get("state") }

// wait blocks for Login to return.
func (h *loginHarness) wait(t *testing.T) loginResult {
	t.Helper()
	select {
	case r := <-h.result:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("Login did not return in time")
		return loginResult{}
	}
}

// callback GETs the loopback callback with a query string.
func (h *loginHarness) callback(t *testing.T, query string) *http.Response {
	t.Helper()
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(h.callbackURL + "?" + query)
	if err != nil {
		t.Fatalf("callback GET: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// TestLogin_LOG01_HappyPath checks a successful callback and exchange returns
// the key.
func TestLogin_LOG01_HappyPath(t *testing.T) {
	fx := newFakeExchange(t, http.StatusOK, `{"key":"the-key"}`)
	h := startLogin(t, LoginOptions{ExchangeURL: fx.URL})
	h.callback(t, "code=the-code&state="+url.QueryEscape(h.state()))

	r := h.wait(t)
	if r.err != nil || r.key != "the-key" {
		t.Fatalf("Login() = (%q, %v), want (the-key, nil)", r.key, r.err)
	}
}

// TestLogin_LOG02_AuthURLParameters checks the authorization URL carries the
// callback, the S256 challenge, and the state.
func TestLogin_LOG02_AuthURLParameters(t *testing.T) {
	h := startLogin(t, LoginOptions{})
	q := h.authURL.Query()
	if got := q.Get("callback_url"); got != h.callbackURL || got == "" {
		t.Errorf("callback_url = %q, want %q", got, h.callbackURL)
	}
	if got := q.Get("code_challenge"); got == "" {
		t.Error("code_challenge is empty")
	}
	if got := q.Get("code_challenge_method"); got != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", got)
	}
	if got := q.Get("state"); got == "" {
		t.Error("state is empty")
	}
	h.cancel()
	_ = h.wait(t)
}

// TestLogin_LOG03_ExchangeSendsVerifier checks the exchange body carries the
// code and the verifier that matches the challenge.
func TestLogin_LOG03_ExchangeSendsVerifier(t *testing.T) {
	fx := newFakeExchange(t, http.StatusOK, `{"key":"k"}`)
	h := startLogin(t, LoginOptions{ExchangeURL: fx.URL})
	h.callback(t, "code=code-123&state="+url.QueryEscape(h.state()))
	_ = h.wait(t)

	got := fx.first(t)
	if got.Code != "code-123" {
		t.Errorf("exchange code = %q, want code-123", got.Code)
	}
	if want := h.authURL.Query().Get("code_challenge"); Challenge(got.CodeVerifier) != want {
		t.Errorf("verifier does not match the challenge %q", want)
	}
	if got.CodeChallengeMethod != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", got.CodeChallengeMethod)
	}
}

// TestLogin_LOG04_StateMismatch checks a wrong state is refused, named on the
// page, and never exchanged.
func TestLogin_LOG04_StateMismatch(t *testing.T) {
	fx := newFakeExchange(t, http.StatusOK, `{"key":"k"}`)
	h := startLogin(t, LoginOptions{ExchangeURL: fx.URL})
	resp := h.callback(t, "code=c&state=wrong")
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "not valid") {
		t.Errorf("page does not mention the mismatch: %s", body)
	}
	if r := h.wait(t); r.err == nil {
		t.Fatal("Login() succeeded, want an error")
	}
	if fx.count() != 0 {
		t.Errorf("exchange was called %d times, want 0", fx.count())
	}
}

// TestLogin_LOG05_UserDenies checks an error callback is a clear error and
// never exchanged.
func TestLogin_LOG05_UserDenies(t *testing.T) {
	fx := newFakeExchange(t, http.StatusOK, `{"key":"k"}`)
	h := startLogin(t, LoginOptions{ExchangeURL: fx.URL})
	h.callback(t, "error=access_denied")

	r := h.wait(t)
	if r.err == nil || !strings.Contains(r.err.Error(), "denied") {
		t.Fatalf("Login() error = %v, want a denial", r.err)
	}
	if fx.count() != 0 {
		t.Errorf("exchange was called %d times, want 0", fx.count())
	}
}

// TestLogin_LOG06_Timeout checks a missing callback times out and the listener
// is closed.
func TestLogin_LOG06_Timeout(t *testing.T) {
	h := startLogin(t, LoginOptions{Timeout: 50 * time.Millisecond})
	r := h.wait(t)
	if r.err == nil || !strings.Contains(r.err.Error(), "timed out") {
		t.Fatalf("Login() error = %v, want a timeout", r.err)
	}
	assertListenerClosed(t, h.callbackURL)
}

// TestLogin_LOG07_ContextCancelled checks cancellation returns ctx.Err() and
// closes the listener.
func TestLogin_LOG07_ContextCancelled(t *testing.T) {
	h := startLogin(t, LoginOptions{})
	h.cancel()
	r := h.wait(t)
	if !errors.Is(r.err, context.Canceled) {
		t.Fatalf("Login() error = %v, want context.Canceled", r.err)
	}
	assertListenerClosed(t, h.callbackURL)
}

// TestLogin_LOG08_Exchange401 checks a non-2xx exchange error names the status.
func TestLogin_LOG08_Exchange401(t *testing.T) {
	fx := newFakeExchange(t, http.StatusUnauthorized, `{"error":"invalid"}`)
	h := startLogin(t, LoginOptions{ExchangeURL: fx.URL})
	h.callback(t, "code=c&state="+url.QueryEscape(h.state()))
	r := h.wait(t)
	if r.err == nil || !strings.Contains(r.err.Error(), "401") {
		t.Fatalf("Login() error = %v, want one naming 401", r.err)
	}
	if r.key != "" {
		t.Errorf("Login() returned a key on failure: %q", r.key)
	}
}

// TestLogin_LOG09_ExchangeNoKey checks a 200 body without a key is an error.
func TestLogin_LOG09_ExchangeNoKey(t *testing.T) {
	fx := newFakeExchange(t, http.StatusOK, `{"something":"else"}`)
	h := startLogin(t, LoginOptions{ExchangeURL: fx.URL})
	h.callback(t, "code=c&state="+url.QueryEscape(h.state()))
	if r := h.wait(t); r.err == nil || r.key != "" {
		t.Fatalf("Login() = (%q, %v), want an error and no key", r.key, r.err)
	}
}

// TestLogin_LOG10_ExchangeMalformed checks a malformed JSON body errors with a
// truncated copy of the body.
func TestLogin_LOG10_ExchangeMalformed(t *testing.T) {
	fx := newFakeExchange(t, http.StatusOK, "not-json-body-marker")
	h := startLogin(t, LoginOptions{ExchangeURL: fx.URL})
	h.callback(t, "code=c&state="+url.QueryEscape(h.state()))
	r := h.wait(t)
	if r.err == nil || !strings.Contains(r.err.Error(), "not-json-body-marker") {
		t.Fatalf("Login() error = %v, want a truncated body", r.err)
	}
}

// TestLogin_LOG11_NoCode checks a matching callback without a code is refused.
func TestLogin_LOG11_NoCode(t *testing.T) {
	fx := newFakeExchange(t, http.StatusOK, `{"key":"k"}`)
	h := startLogin(t, LoginOptions{ExchangeURL: fx.URL})
	h.callback(t, "state="+url.QueryEscape(h.state()))
	if r := h.wait(t); r.err == nil {
		t.Fatal("Login() succeeded without a code")
	}
	if fx.count() != 0 {
		t.Errorf("exchange was called %d times, want 0", fx.count())
	}
}

// TestLogin_LOG12_SecondCallbackRefused checks the listener accepts one
// callback: the second is refused or refused at the connection.
func TestLogin_LOG12_SecondCallbackRefused(t *testing.T) {
	fx := newFakeExchange(t, http.StatusOK, `{"key":"k"}`)
	h := startLogin(t, LoginOptions{ExchangeURL: fx.URL})
	h.callback(t, "code=c&state="+url.QueryEscape(h.state()))
	_ = h.wait(t)

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(h.callbackURL + "?code=again&state=x")
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode < 400 {
			t.Fatalf("second callback status = %d, want >= 400", resp.StatusCode)
		}
	}
}

// TestLogin_LOG13_PageDoesNotReflect checks the page never echoes the code.
func TestLogin_LOG13_PageDoesNotReflect(t *testing.T) {
	fx := newFakeExchange(t, http.StatusOK, `{"key":"k"}`)
	h := startLogin(t, LoginOptions{ExchangeURL: fx.URL})
	resp := h.callback(t, "code=%3Cscript%3Ealert(1)%3C%2Fscript%3E&state="+url.QueryEscape(h.state()))
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "<script>") {
		t.Fatalf("page reflected the code: %s", body)
	}
	_ = h.wait(t)
}

// TestLogin_LOG14_LoopbackOnly checks the callback address is on loopback.
func TestLogin_LOG14_LoopbackOnly(t *testing.T) {
	h := startLogin(t, LoginOptions{})
	u, err := url.Parse(h.callbackURL)
	if err != nil {
		t.Fatalf("parse callback URL: %v", err)
	}
	host, _, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split host: %v", err)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		t.Errorf("callback host = %q, want loopback", host)
	}
	h.cancel()
	_ = h.wait(t)
}

// TestLogin_LOG15_OpenBrowserFails checks a browser failure is reported but the
// flow still completes.
func TestLogin_LOG15_OpenBrowserFails(t *testing.T) {
	fx := newFakeExchange(t, http.StatusOK, `{"key":"k"}`)
	h := startLogin(t, LoginOptions{
		ExchangeURL: fx.URL,
		OpenBrowser: func(string) error { return errors.New("no browser here") },
	})
	if !strings.Contains(h.printed.String(), h.authURL.String()) {
		t.Error("URL was not printed when the browser failed")
	}
	h.callback(t, "code=c&state="+url.QueryEscape(h.state()))
	if r := h.wait(t); r.err != nil || r.key != "k" {
		t.Fatalf("Login() = (%q, %v), want (k, nil)", r.key, r.err)
	}
}

// TestLogin_LOG16_Headless checks a nil OpenBrowser still prints the URL and
// works with a manual callback.
func TestLogin_LOG16_Headless(t *testing.T) {
	fx := newFakeExchange(t, http.StatusOK, `{"key":"k"}`)
	var printed syncBuffer
	result := make(chan loginResult, 1)
	go func() {
		key, err := Login(context.Background(), LoginOptions{
			ExchangeURL: fx.URL,
			Out:         &printed,
			OpenBrowser: nil,
		})
		result <- loginResult{key, err}
	}()

	authURL := waitForPrintedURL(t, &printed)
	callback := authURL.Query().Get("callback_url")
	state := authURL.Query().Get("state")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(callback + "?code=c&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatalf("callback GET: %v", err)
	}
	resp.Body.Close()

	select {
	case r := <-result:
		if r.err != nil || r.key != "k" {
			t.Fatalf("Login() = (%q, %v), want (k, nil)", r.key, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Login did not return")
	}
}

// TestLogin_LOG17_NoLeaks checks no goroutine or port survives a failure.
func TestLogin_LOG17_NoLeaks(t *testing.T) {
	testkit.NoLeaks(t)
	h := startLogin(t, LoginOptions{Timeout: 50 * time.Millisecond})
	if r := h.wait(t); r.err == nil {
		t.Fatal("Login() succeeded, want a timeout")
	}
	h2 := startLogin(t, LoginOptions{})
	h2.cancel()
	_ = h2.wait(t)
}

// waitForPrintedURL polls a buffer for the authorization URL line.
func waitForPrintedURL(t *testing.T, printed *syncBuffer) *url.URL {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, line := range strings.Split(printed.String(), "\n") {
			line = strings.TrimSpace(line)
			if u, err := url.Parse(line); err == nil && u.Host != "" && u.Query().Get("code_challenge") != "" {
				return u
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no authorization URL was printed; output was %q", printed.String())
	return nil
}

// assertListenerClosed checks nothing accepts on the callback address.
func assertListenerClosed(t *testing.T, callbackURL string) {
	t.Helper()
	u, err := url.Parse(callbackURL)
	if err != nil {
		t.Fatalf("parse callback URL: %v", err)
	}
	conn, err := net.DialTimeout("tcp", u.Host, 300*time.Millisecond)
	if err == nil {
		conn.Close()
		t.Errorf("listener at %s is still accepting", u.Host)
	}
}

// FuzzCallbackQuery sends arbitrary query strings to the callback handler and
// checks it never panics, always answers with one fixed page, and never
// produces a key.
func FuzzCallbackQuery(f *testing.F) {
	f.Add("code=abc&state=s")
	f.Add("error=access_denied")
	f.Add("code=%3Cscript%3E&state=s")
	f.Add("")
	f.Fuzz(func(t *testing.T, raw string) {
		flow := &loginFlow{state: "s", result: make(chan callbackResult, 1)}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/callback", nil)
		req.URL.RawQuery = raw
		flow.callback(rec, req)

		select {
		case res := <-flow.result:
			if res.err == nil && res.code == "" {
				t.Fatal("callback returned neither a code nor an error")
			}
			// Every page is one of the fixed sentences, so no query text can
			// reach the browser.
			if !isFixedPage(rec.Body.String()) {
				t.Fatalf("callback page is not one of the fixed pages: %s", rec.Body.String())
			}
		default:
			t.Fatal("callback sent no result")
		}
	})
}

// fixedPages are the only bodies writePage may produce. A reflected parameter
// would make the body one that is not in this set.
var fixedPages = []string{
	pageWith("You can close this tab and return to Nova."),
	pageWith("Authorization was denied. You can close this tab."),
	pageWith("This link is not valid for this login. You can close this tab."),
	pageWith("Authorization failed: no code was returned. You can close this tab."),
}

// isFixedPage reports whether body is exactly one of fixedPages.
func isFixedPage(body string) bool {
	for _, p := range fixedPages {
		if body == p {
			return true
		}
	}
	return false
}

// pageWith renders the callback page around text; it mirrors writePage's HTML.
func pageWith(text string) string {
	return `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Nova</title></head><body><p>` + text + `</p></body></html>`
}
