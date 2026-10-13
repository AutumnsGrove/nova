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

	"github.com/AutumnsGrove/nova/ext"
	"github.com/AutumnsGrove/nova/tool"
)

// Limits and fixed request settings.
const (
	defaultChars  = 50000
	maxCharsCap   = 200000
	maxBody       = 5 << 20
	maxRedirects  = 5
	connectTimout = 5 * time.Second
	totalTimeout  = 20 * time.Second
	userAgent     = "nova-webfetch"
	headerBodyCap = 2000
)

// Extension is the webfetch bundled extension.
type Extension struct {
	env          *ext.Env
	allowPrivate bool
	allowHosts   []string

	lookup    func(ctx context.Context, host string) ([]net.IPAddr, error)
	transport http.RoundTripper
	client    *http.Client
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
	e.env = env
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

func (e *Extension) run(ctx context.Context, args json.RawMessage, _ *tool.Context) (tool.Result, error) {
	var in struct {
		URL      string `json:"url"`
		MaxChars int    `json:"max_chars"`
		Raw      bool   `json:"raw"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return tool.Errorf("invalid arguments: %v", err), nil
	}
	if strings.TrimSpace(in.URL) == "" {
		return tool.Errorf("url is required"), nil
	}
	u, err := url.Parse(in.URL)
	if err != nil {
		return tool.Errorf("invalid url %q", in.URL), nil
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return tool.Errorf("unsupported scheme %q; only http and https are allowed", u.Scheme), nil
	}
	if u.Host == "" {
		return tool.Errorf("invalid url %q", in.URL), nil
	}
	if u.User != nil {
		return tool.Errorf("a URL with embedded credentials is not allowed"), nil
	}
	maxChars := in.MaxChars
	if maxChars <= 0 {
		maxChars = defaultChars
	}
	if maxChars > maxCharsCap {
		maxChars = maxCharsCap
	}

	ctx, cancel := context.WithTimeout(ctx, totalTimeout)
	defer cancel()
	resp, finalURL, err := e.get(ctx, e.httpClient(), u)
	if err != nil {
		if errors.Is(err, errBlocked) {
			return tool.Errorf("%v", err), nil
		}
		if ctx.Err() != nil {
			return tool.Errorf("the request timed out"), nil
		}
		return tool.Errorf("fetch failed: %v", err), nil
	}
	defer resp.Body.Close()

	raw, truncatedBody, err := readCapped(resp.Body, maxBody)
	if err != nil {
		return tool.Errorf("read failed: %v", err), nil
	}
	if truncatedBody && in.Raw {
		return tool.Errorf("response too large"), nil
	}
	body := raw
	capNote := ""
	if truncatedBody {
		capNote = fmt.Sprintf("\n... (truncated at %d bytes)", maxBody)
	}

	contentType := mediaType(resp.Header.Get("Content-Type"))
	text, err := e.render(in.Raw, contentType, resp.Header.Get("Content-Type"), body, finalURL)
	if err != nil {
		return tool.Errorf("%v", err), nil
	}
	header := fmt.Sprintf("%s (%d, %s, %d bytes)", finalURL, resp.StatusCode, contentType, len(body))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		out := header + "\n\n" + truncate(text+capNote, headerBodyCap)
		if resp.StatusCode >= 400 {
			return tool.Result{Content: out, IsError: true, Details: e.details(finalURL, resp, len(body))}, nil
		}
		return tool.Result{Content: out, Details: e.details(finalURL, resp, len(body))}, nil
	}
	if len(text) > maxChars {
		cut, omitted := cutLines(text, maxChars)
		text = cut + fmt.Sprintf("\n... (truncated; %d characters omitted)", omitted)
	}
	text += capNote
	return tool.Result{Content: header + "\n\n" + text, Details: e.details(finalURL, resp, len(body))}, nil
}

// details builds the TUI card data.
func (e *Extension) details(u *url.URL, resp *http.Response, n int) json.RawMessage {
	data, _ := json.Marshal(map[string]any{"url": u.String(), "status": resp.StatusCode, "content_type": mediaType(resp.Header.Get("Content-Type")), "bytes": n})
	return data
}

// render turns a body into text per its content type.
func (e *Extension) render(raw bool, contentType, header string, body []byte, base *url.URL) (string, error) {
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

// httpClient builds a client with an SSRF-checked dialer and no proxy.
func (e *Extension) httpClient() *http.Client {
	if e.transport != nil {
		return &http.Client{Transport: e.transport, CheckRedirect: noRedirect, Timeout: totalTimeout}
	}
	dialer := &net.Dialer{Timeout: connectTimout}
	tr := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
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
			if lastErr == nil {
				lastErr = fmt.Errorf("no addresses for %s", host)
			}
			return nil, lastErr
		},
	}
	return &http.Client{Transport: tr, CheckRedirect: noRedirect, Timeout: totalTimeout}
}

// noRedirect keeps redirect handling in this package, so every hop is checked.
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// get performs the GET, following up to maxRedirects, revalidating each hop.
func (e *Extension) get(ctx context.Context, client *http.Client, u *url.URL) (*http.Response, *url.URL, error) {
	for i := 0; i <= maxRedirects; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("User-Agent", userAgent)
		resp, err := client.Do(req)
		if err != nil {
			return nil, nil, err
		}
		if resp.StatusCode < 300 || resp.StatusCode >= 400 {
			return resp, resp.Request.URL, nil
		}
		loc := resp.Header.Get("Location")
		_ = resp.Body.Close()
		if loc == "" {
			return resp, resp.Request.URL, nil
		}
		if i == maxRedirects {
			return nil, nil, fmt.Errorf("too many redirects")
		}
		next, err := u.Parse(loc)
		if err != nil {
			return nil, nil, err
		}
		if next.Scheme != "http" && next.Scheme != "https" {
			return nil, nil, fmt.Errorf("refusing redirect to scheme %q", next.Scheme)
		}
		u = next
	}
	return nil, nil, fmt.Errorf("too many redirects")
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
		if idx := strings.IndexByte(header, ';'); idx >= 0 {
			header = header[:idx]
		}
		return strings.ToLower(strings.TrimSpace(header))
	}
	return strings.ToLower(mt)
}

// prettyJSON indents a JSON document, returning the original on failure.
func prettyJSON(s string) (string, error) {
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(s), "", "  "); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// truncate cuts s to n bytes.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// cutLines cuts s to at most n bytes at a line boundary and returns the number
// of bytes omitted.
func cutLines(s string, n int) (string, int) {
	if len(s) <= n {
		return s, 0
	}
	cut := s[:n]
	if idx := strings.LastIndexByte(cut, '\n'); idx > 0 {
		cut = cut[:idx]
	}
	return cut, len(s) - len(cut)
}
