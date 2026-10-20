package auth

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// OpenRouter's OAuth PKCE endpoints and the login timeout. They were confirmed
// against https://openrouter.ai/docs/use-cases/oauth-pkce on 2026-10-09: the
// user approves at /auth and the single-use code is exchanged for a key at
// /api/v1/auth/keys. OpenRouter's authorization codes expire after ten minutes,
// so the default timeout is set below that.
const (
	// DefaultAuthURL is where the user authorizes the request.
	DefaultAuthURL = "https://openrouter.ai/auth"
	// DefaultExchangeURL trades the code and verifier for an API key.
	DefaultExchangeURL = "https://openrouter.ai/api/v1/auth/keys"
	// DefaultLoginTimeout bounds how long Login waits for the callback.
	DefaultLoginTimeout = 5 * time.Minute
	// DefaultListen is the loopback callback address.
	DefaultListen = "127.0.0.1:0"
	// maxExchangeBody caps how much of an exchange response we read or quote.
	maxExchangeBody = 1 << 20
	// callbackHeaderTimeout bounds how long the callback server waits for a
	// request's headers.
	callbackHeaderTimeout = 10 * time.Second
)

// LoginOptions configures Login. Zero values take the documented defaults.
type LoginOptions struct {
	// AuthURL is the authorization endpoint. Default DefaultAuthURL.
	AuthURL string
	// ExchangeURL trades the code for a key. Default DefaultExchangeURL.
	ExchangeURL string
	// HTTPClient performs the exchange. Default a new http.Client.
	HTTPClient *http.Client
	// OpenBrowser opens the auth URL. A nil function means print only, which
	// is the headless path; an error is reported but does not stop the flow.
	OpenBrowser func(url string) error
	// Out receives the printed URL and any browser warning. Default os.Stderr.
	Out io.Writer
	// Timeout bounds the wait for the callback. Default DefaultLoginTimeout.
	Timeout time.Duration
	// Listen is the callback address. Default DefaultListen.
	Listen string
}

// BeginOptions configures Begin. It is the same type as LoginOptions, so a
// caller can hand a LoginOptions straight to Begin.
type BeginOptions = LoginOptions

// Flow is one PKCE sign-in in progress. Begin binds the callback listener and
// builds the authorization URL; Wait blocks for the loopback callback or a
// value passed to Submit, then exchanges the code for a key.
type Flow struct {
	flow     *loginFlow
	opts     LoginOptions
	verifier string
	server   *http.Server
	listener net.Listener
	closed   atomic.Bool
}

// Begin starts a PKCE flow. It binds a single-use loopback listener, prints the
// authorization URL, opens the browser when one is configured, and returns the
// flow and the URL. The listener and its goroutine are released by Close, which
// Wait and Login call on every path.
func Begin(opts BeginOptions) (*Flow, string, error) {
	opts = opts.withDefaults()

	ln, err := net.Listen("tcp", opts.Listen)
	if err != nil {
		return nil, "", fmt.Errorf("auth: listen for callback: %w", err)
	}

	verifier, err := NewVerifier()
	if err != nil {
		_ = ln.Close()
		return nil, "", err
	}
	state, err := NewState()
	if err != nil {
		_ = ln.Close()
		return nil, "", err
	}
	challenge := Challenge(verifier)

	lflow := &loginFlow{state: state, result: make(chan callbackResult, 1)}
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", lflow.callback)
	// A client that opens the loopback port and never sends a header must not
	// hold a connection open for the whole login.
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: callbackHeaderTimeout}
	go func() { _ = srv.Serve(ln) }()

	callbackURL := "http://" + ln.Addr().String() + "/callback"
	authURL := buildAuthURL(opts.AuthURL, callbackURL, challenge, state)
	fmt.Fprintln(opts.Out, "Open this URL in a browser to authorize Nova:")
	fmt.Fprintln(opts.Out, authURL)
	if opts.OpenBrowser != nil {
		if err := opts.OpenBrowser(authURL); err != nil {
			fmt.Fprintf(opts.Out, "Could not open a browser: %v\n", err)
		}
	}
	return &Flow{flow: lflow, opts: opts, verifier: verifier, server: srv, listener: ln}, authURL, nil
}

// Addr is the loopback address the callback listener is bound to, such as
// "127.0.0.1:54321". A UI shows it so the user can tell the listener is up.
func (f *Flow) Addr() string {
	if f.listener == nil {
		return ""
	}
	return f.listener.Addr().String()
}

// Received reports whether a callback or a good pasted redirect has been
// accepted, so a UI can move from "waiting" to "finishing".
func (f *Flow) Received() bool { return f.flow.handled.Load() }

// Submit hands the flow the redirect URL the browser landed on. It fails on a
// value without a matching code and state, and leaves Wait blocked.
func (f *Flow) Submit(pasted string) error {
	pasted = strings.TrimSpace(pasted)
	if pasted == "" {
		return errors.New("auth: the pasted redirect is empty")
	}
	u, err := url.Parse(pasted)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return errors.New("auth: not a valid redirect URL")
	}
	res := f.flow.parse(u.Query())
	if res.err != nil {
		return res.err
	}
	if f.flow.handled.Swap(true) {
		return errors.New("auth: the login already finished")
	}
	f.flow.result <- callbackResult{code: res.code}
	return nil
}

// Wait blocks until the loopback callback arrives or Submit delivers a code,
// then exchanges the code for an API key.
func (f *Flow) Wait(ctx context.Context) (string, error) {
	waitCtx, cancel := context.WithTimeout(ctx, f.opts.Timeout)
	defer cancel()

	select {
	case <-waitCtx.Done():
		f.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", fmt.Errorf("auth: timed out after %s waiting for the callback", f.opts.Timeout)
	case res := <-f.flow.result:
		f.Close()
		if res.err != nil {
			return "", res.err
		}
		return exchange(waitCtx, f.opts.HTTPClient, f.opts.ExchangeURL, res.code, f.verifier, "S256")
	}
}

// Close releases the loopback listener. It is safe to call more than once.
func (f *Flow) Close() error {
	if f.closed.Swap(true) {
		return nil
	}
	var err error
	if f.server != nil {
		err = f.server.Close()
	}
	// Begin starts Serve in a goroutine, so a fast cancellation can run Close
	// before Serve has tracked the listener. Closing the listener here too
	// guarantees the port is released on every path.
	if f.listener != nil {
		_ = f.listener.Close()
	}
	return err
}

// Login runs OpenRouter's OAuth PKCE flow to completion and returns the API
// key. It is Begin followed by Wait: the one-shot behaviour the CLI uses.
func Login(ctx context.Context, opts LoginOptions) (string, error) {
	flow, _, err := Begin(opts)
	if err != nil {
		return "", err
	}
	defer flow.Close()
	return flow.Wait(ctx)
}

// withDefaults fills the zero values of o.
func (o LoginOptions) withDefaults() LoginOptions {
	if o.AuthURL == "" {
		o.AuthURL = DefaultAuthURL
	}
	if o.ExchangeURL == "" {
		o.ExchangeURL = DefaultExchangeURL
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{}
	}
	if o.Out == nil {
		o.Out = os.Stderr
	}
	if o.Timeout == 0 {
		o.Timeout = DefaultLoginTimeout
	}
	if o.Listen == "" {
		o.Listen = DefaultListen
	}
	return o
}

// buildAuthURL renders the authorization URL with the callback, the S256
// challenge, and the state. The query is encoded, so a callback URL with its
// own query cannot break the outer URL.
func buildAuthURL(base, callbackURL, challenge, state string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	q.Set("callback_url", callbackURL)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	// Without a label OpenRouter names the key after the loopback host and port.
	q.Set("key_label", "Nova")
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// callbackResult is what the handler passes back to Login. The callback never
// exchanges the code, so it cannot produce a key.
type callbackResult struct {
	code string
	err  error
	// page is the fixed sentence shown to the browser. It never contains any
	// request parameter.
	page string
	// status is the HTTP status for the page.
	status int
}

// loginFlow is the callback state for one Login call. It is a separate type so
// the handler can be fuzzed without a network.
type loginFlow struct {
	state   string
	result  chan callbackResult
	handled atomic.Bool
}

// callback accepts exactly one request, answers it with a small page, and
// hands the parsed result to Login.
func (f *loginFlow) callback(w http.ResponseWriter, r *http.Request) {
	if f.handled.Swap(true) {
		http.NotFound(w, r)
		return
	}
	res := f.parse(r.URL.Query())
	writePage(w, res.status, res.page)
	f.result <- res
}

// parse reads the callback query. A state that does not match is rejected
// before the code is trusted. The raw error parameter is deliberately not
// quoted back, so no untrusted text reaches the page.
func (f *loginFlow) parse(q url.Values) callbackResult {
	if q.Get("error") != "" {
		return callbackResult{
			err:    errors.New("auth: authorization was denied"),
			page:   "Authorization was denied. You can close this tab.",
			status: http.StatusForbidden,
		}
	}
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(f.state)) != 1 {
		return callbackResult{
			err:    errors.New("auth: callback state did not match the login request"),
			page:   "This link is not valid for this login. You can close this tab.",
			status: http.StatusBadRequest,
		}
	}
	code := q.Get("code")
	if code == "" {
		return callbackResult{
			err:    errors.New("auth: callback had no authorization code"),
			page:   "Authorization failed: no code was returned. You can close this tab.",
			status: http.StatusBadRequest,
		}
	}
	return callbackResult{
		code:   code,
		page:   "You can close this tab and return to Nova.",
		status: http.StatusOK,
	}
}

// writePage writes a tiny HTML page whose text is escaped. It flushes so the
// page reaches the browser before Login closes the listener.
func writePage(w http.ResponseWriter, status int, text string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, "<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><title>Nova</title></head><body><p>%s</p></body></html>", html.EscapeString(text))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// exchangeRequest is the POST body for the code exchange.
type exchangeRequest struct {
	Code                string `json:"code"`
	CodeVerifier        string `json:"code_verifier"`
	CodeChallengeMethod string `json:"code_challenge_method,omitempty"`
}

// exchangeResponse is the success body; the API key is in "key".
type exchangeResponse struct {
	Key string `json:"key"`
}

// exchange trades the single-use code and the verifier for an API key. A
// non-2xx status, a body without a key, or a body that is not JSON is an error
// that names the status and quotes a truncated body so a failure is
// diagnosable without logging the verifier.
func exchange(ctx context.Context, client *http.Client, urlStr, code, verifier, method string) (string, error) {
	body, err := json.Marshal(exchangeRequest{Code: code, CodeVerifier: verifier, CodeChallengeMethod: method})
	if err != nil {
		return "", fmt.Errorf("auth: encode exchange request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, urlStr, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("auth: build exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", fmt.Errorf("auth: exchange request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxExchangeBody))
	if err != nil {
		return "", fmt.Errorf("auth: read exchange response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("auth: exchange failed with status %d: %s", resp.StatusCode, truncateBody(data))
	}
	var out exchangeResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("auth: exchange response was not JSON: %s", truncateBody(data))
	}
	if out.Key == "" {
		return "", fmt.Errorf("auth: exchange response had no key (status %d)", resp.StatusCode)
	}
	return out.Key, nil
}

// truncateBody renders a response body for an error message, collapsed and
// capped so a large or binary body does not swamp the message.
func truncateBody(data []byte) string {
	s := strings.Join(strings.Fields(string(data)), " ")
	const limit = 200
	if len(s) <= limit {
		return s
	}
	// Back up to a character boundary so the message stays valid UTF-8.
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}
