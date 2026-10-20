// Package webfetch is the bundled web_fetch extension: a keyless HTTP GET that
// converts HTML to markdown. The destination is chosen by the model and every
// byte of the response is untrusted, so the dialer refuses non-public
// addresses and every size and time is capped.
package webfetch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AutumnsGrove/nova/ext"
	"github.com/AutumnsGrove/nova/tool"
)

// Limits and fixed request settings.
const (
	defaultChars   = 50000
	maxCharsCap    = 200000
	maxBody        = 5 << 20
	maxRedirects   = 5
	connectTimeout = 5 * time.Second
	totalTimeout   = 20 * time.Second
	userAgent      = "nova-webfetch"
	// errorBodyChars bounds how much of a non-2xx body is shown.
	errorBodyChars = 2000
)

// Extension is the webfetch bundled extension.
type Extension struct {
	allowPrivate bool
	allowHosts   []string

	// lookup resolves a host; tests replace it to avoid real DNS.
	lookup func(ctx context.Context, host string) ([]net.IPAddr, error)
}

// New returns a fresh webfetch extension.
func New() *Extension {
	return &Extension{
		lookup: func(ctx context.Context, host string) ([]net.IPAddr, error) {
			return net.DefaultResolver.LookupIPAddr(ctx, host)
		},
	}
}

// Name is the extension name.
func (e *Extension) Name() string { return "webfetch" }

// Init reads the config and registers the web_fetch tool.
func (e *Extension) Init(env *ext.Env) error {
	if env.Config != nil {
		e.allowPrivate = env.Config.Webfetch.AllowPrivate
		e.allowHosts = env.Config.Webfetch.AllowHosts
	}
	return env.Tool(tool.New(tool.Info{
		Name:        "web_fetch",
		Description: "Fetch a URL and return its text. HTML is converted to markdown.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url":       map[string]any{"type": "string"},
				"max_chars": map[string]any{"type": "integer"},
				"raw":       map[string]any{"type": "boolean"},
			},
			"required": []string{"url"},
		},
		ReadOnly:  true,
		OpenWorld: true,
		Parallel:  true,
	}, e.run))
}

// request is a validated web_fetch call.
type request struct {
	url      *url.URL
	maxChars int
	raw      bool
}

// parseRequest decodes the arguments and applies the URL and size rules. Its
// errors are written for the model.
func parseRequest(args json.RawMessage) (request, error) {
	var in struct {
		URL      string `json:"url"`
		MaxChars int    `json:"max_chars"`
		Raw      bool   `json:"raw"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return request{}, fmt.Errorf("invalid arguments: %w", err)
	}
	if strings.TrimSpace(in.URL) == "" {
		return request{}, errors.New("url is required")
	}
	u, err := url.Parse(in.URL)
	if err != nil {
		return request{}, fmt.Errorf("invalid url %q", in.URL)
	}
	if err := checkURL(u); err != nil {
		return request{}, err
	}
	maxChars := in.MaxChars
	if maxChars <= 0 {
		maxChars = defaultChars
	}
	return request{url: u, maxChars: min(maxChars, maxCharsCap), raw: in.Raw}, nil
}

// checkURL applies the rules for a request URL, whether the model gave it or a
// redirect did. The error never echoes the URL, which may hold credentials.
func checkURL(u *url.URL) error {
	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return fmt.Errorf("unsupported scheme %q; only http and https are allowed", u.Scheme)
	case u.Hostname() == "":
		return errors.New("the url has no host")
	case u.User != nil:
		return errors.New("a URL with embedded credentials is not allowed")
	}
	return nil
}

// run fetches the URL and shapes the body into a result.
func (e *Extension) run(ctx context.Context, args json.RawMessage, _ *tool.Context) (tool.Result, error) {
	req, err := parseRequest(args)
	if err != nil {
		return tool.Errorf("%v", err), nil
	}
	ctx, cancel := context.WithTimeout(ctx, totalTimeout)
	defer cancel()
	resp, finalURL, err := e.get(ctx, e.httpClient(), req.url)
	if err != nil {
		return failure(ctx, "fetch failed", err), nil
	}
	defer func() { _ = resp.Body.Close() }()

	body, capped, err := readCapped(resp.Body, maxBody)
	if err != nil {
		return failure(ctx, "read failed", err), nil
	}
	if capped && req.raw {
		return tool.Errorf("response too large"), nil
	}
	capNote := ""
	if capped {
		capNote = fmt.Sprintf("\n... (truncated at %d bytes)", maxBody)
	}
	contentType := mediaType(resp.Header.Get("Content-Type"))
	text, err := render(req.raw, contentType, resp.Header.Get("Content-Type"), body, finalURL)
	if err != nil {
		return tool.Errorf("%v", err), nil
	}
	header := fmt.Sprintf("%s (%d, %s, %d bytes)", finalURL, resp.StatusCode, contentType, len(body))
	details := cardDetails(finalURL, resp.StatusCode, contentType, len(body))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		out := header + "\n\n" + cutRunes(text+capNote, errorBodyChars)
		return tool.Result{Content: out, IsError: resp.StatusCode >= 400, Details: details}, nil
	}
	if cut, omitted := cutLines(text, req.maxChars); omitted > 0 {
		text = cut + fmt.Sprintf("\n... (truncated; %d characters omitted)", omitted)
	}
	return tool.Result{Content: header + "\n\n" + text + capNote, Details: details}, nil
}

// failure turns a transport error into a result. A refused address keeps its
// own message, and a context that ended says why instead of a transport error.
func failure(ctx context.Context, what string, err error) tool.Result {
	switch {
	case errors.Is(err, errBlocked):
		return tool.Errorf("%v", err)
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return tool.Errorf("the request timed out")
	case ctx.Err() != nil:
		return tool.Errorf("cancelled")
	}
	return tool.Errorf("%s: %v", what, err)
}

// cardDetails builds the data the TUI card shows.
func cardDetails(u *url.URL, status int, contentType string, n int) json.RawMessage {
	data, _ := json.Marshal(map[string]any{"url": u.String(), "status": status, "content_type": contentType, "bytes": n})
	return data
}

// render turns a body into text per its content type.
func render(raw bool, contentType, header string, body []byte, base *url.URL) (string, error) {
	switch {
	case contentType == "text/html" || contentType == "application/xhtml+xml":
		text := decodeCharset(body, charsetOf(body, header))
		if raw {
			return text, nil
		}
		return htmlToMarkdown([]byte(text), base), nil
	case strings.HasPrefix(contentType, "text/"), contentType == "application/json":
		text := decodeCharset(body, charsetOf(body, header))
		if contentType == "application/json" {
			if pretty, err := prettyJSON(text); err == nil {
				return pretty, nil
			}
		}
		return text, nil
	default:
		return "", fmt.Errorf("unsupported content type: %s", contentType)
	}
}

// httpClient builds a client whose dialer refuses non-public addresses and
// whose environment proxy is ignored.
func (e *Extension) httpClient() *http.Client {
	tr := &http.Transport{
		Proxy:               nil,
		DialContext:         e.dialChecked(&net.Dialer{Timeout: connectTimeout}),
		TLSHandshakeTimeout: connectTimeout,
		// Each fetch builds its own transport, so a kept-alive connection would
		// never be reused or closed, and would hold two goroutines forever.
		DisableKeepAlives: true,
	}
	// Redirects are followed in get, so that every hop is checked.
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &http.Client{Transport: tr, CheckRedirect: noRedirect, Timeout: totalTimeout}
}

// dialChecked returns a DialContext that resolves the host itself, refuses a
// non-public address, and connects to the address it checked. Connecting to the
// checked IP, not to the name, is what stops DNS rebinding between the check
// and the connect.
func (e *Extension) dialChecked(dialer *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if hostAllowed(host, e.allowHosts) {
			return dialer.DialContext(ctx, network, addr)
		}
		addrs, err := e.lookup(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(addrs) == 0 {
			return nil, fmt.Errorf("no addresses for %s", host)
		}
		var lastErr error
		for _, a := range addrs {
			if err := checkAddr(a.IP, e.allowPrivate); err != nil {
				return nil, err
			}
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(a.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

// get performs the GET, following up to maxRedirects and revalidating each hop.
// It returns the response and the URL it came from.
func (e *Extension) get(ctx context.Context, client *http.Client, u *url.URL) (*http.Response, *url.URL, error) {
	for hops := 0; ; hops++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("User-Agent", userAgent)
		resp, err := client.Do(req)
		if err != nil {
			return nil, nil, err
		}
		loc := resp.Header.Get("Location")
		if resp.StatusCode < 300 || resp.StatusCode >= 400 || loc == "" {
			return resp, u, nil
		}
		_ = resp.Body.Close()
		if hops == maxRedirects {
			return nil, nil, errors.New("too many redirects")
		}
		next, err := u.Parse(loc)
		if err != nil {
			return nil, nil, err
		}
		if err := checkURL(next); err != nil {
			return nil, nil, fmt.Errorf("refusing redirect: %w", err)
		}
		u = next
	}
}

// readCapped reads at most limit bytes and reports whether more remained.
func readCapped(r io.Reader, limit int64) ([]byte, bool, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(data)) > limit {
		return data[:limit], true, nil
	}
	return data, false, nil
}

// mediaType returns the lowercased media type without parameters.
func mediaType(header string) string {
	mt, _, err := mime.ParseMediaType(header)
	if err != nil {
		header, _, _ = strings.Cut(header, ";")
		return strings.ToLower(strings.TrimSpace(header))
	}
	return strings.ToLower(mt)
}

// prettyJSON indents a JSON document, returning an error if it is not one.
func prettyJSON(s string) (string, error) {
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(s), "", "  "); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// cutRunes cuts s to at most n characters, never inside one.
func cutRunes(s string, n int) string {
	for i := range s {
		if n == 0 {
			return s[:i]
		}
		n--
	}
	return s
}

// cutLines cuts s to at most n characters, at a line boundary where it can, and
// returns the number of characters omitted.
func cutLines(s string, n int) (string, int) {
	cut := cutRunes(s, n)
	if len(cut) == len(s) {
		return s, 0
	}
	if idx := strings.LastIndexByte(cut, '\n'); idx > 0 {
		cut = cut[:idx]
	}
	return cut, utf8.RuneCountInString(s[len(cut):])
}
