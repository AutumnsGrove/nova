package webfetch

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/tool"
)

// local returns a tool result for a local test server. Loopback is allowed, so
// the SSRF checks are exercised separately.
func local(t *testing.T, e *Extension, urlStr string, raw bool, maxChars int) tool.Result {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"url": urlStr, "raw": raw, "max_chars": maxChars})
	res, err := e.run(context.Background(), body, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return res
}

func newLocal() *Extension {
	e := New()
	e.allowPrivate = true
	return e
}

func TestWebfetch_WFT01_FetchHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><body><h1>Hi</h1><p>Hello there</p></body></html>`)
	}))
	defer srv.Close()
	res := local(t, newLocal(), srv.URL+"/page", false, 0)
	if !strings.HasPrefix(res.Content, srv.URL+"/page (200, text/html,") {
		t.Fatalf("header = %q", res.Content)
	}
	if !strings.Contains(res.Content, "# Hi") || !strings.Contains(res.Content, "Hello there") {
		t.Fatalf("content = %q", res.Content)
	}
}

func TestWebfetch_WFT02_TextMarkdownJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/text":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "plain body")
		case "/md":
			w.Header().Set("Content-Type", "text/markdown")
			fmt.Fprint(w, "# Title")
		case "/json":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"a":1,"b":[2,3]}`)
		}
	}))
	defer srv.Close()
	e := newLocal()
	if got := local(t, e, srv.URL+"/text", false, 0).Content; !strings.Contains(got, "plain body") {
		t.Fatalf("text = %q", got)
	}
	if got := local(t, e, srv.URL+"/md", false, 0).Content; !strings.Contains(got, "# Title") {
		t.Fatalf("md = %q", got)
	}
	if got := local(t, e, srv.URL+"/json", false, 0).Content; !strings.Contains(got, "\n  \"a\": 1") {
		t.Fatalf("json = %q", got)
	}
}

func TestWebfetch_WFT03_UnsupportedContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte{0x89, 'P', 'N', 'G'})
	}))
	defer srv.Close()
	res := local(t, newLocal(), srv.URL, false, 0)
	if !res.IsError || !strings.Contains(res.Content, "unsupported content type: image/png") {
		t.Fatalf("result = %q", res.Content)
	}
}

func TestWebfetch_WFT04_Raw(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<h1>Title</h1>`)
	}))
	defer srv.Close()
	res := local(t, newLocal(), srv.URL, true, 0)
	if !strings.Contains(res.Content, "<h1>Title</h1>") {
		t.Fatalf("raw = %q", res.Content)
	}
}

func TestWebfetch_WFT05_NonHTTPScheme(t *testing.T) {
	e := newLocal()
	for _, u := range []string{"file:///etc/passwd", "ftp://example.com/x"} {
		res := local(t, e, u, false, 0)
		if !res.IsError || !strings.Contains(res.Content, "unsupported scheme") {
			t.Fatalf("%s result = %q", u, res.Content)
		}
	}
}

func TestWebfetch_WFT06_Credentials(t *testing.T) {
	res := local(t, newLocal(), "http://user:pass@example.com/", false, 0)
	if !res.IsError || !strings.Contains(res.Content, "credentials") {
		t.Fatalf("result = %q", res.Content)
	}
}

func TestWebfetch_WFT07_LoopbackBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "secret")
	}))
	defer srv.Close()
	e := New() // allowPrivate is false
	res := local(t, e, srv.URL, false, 0)
	if !res.IsError || !strings.Contains(res.Content, "blocked address") {
		t.Fatalf("result = %q", res.Content)
	}
}

func TestWebfetch_WFT08_PrivateRangesBlocked(t *testing.T) {
	for _, s := range []string{"10.0.0.1", "192.168.1.1", "172.16.0.1", "fc00::1"} {
		if reason := blockedReason(net.ParseIP(s), false); reason == "" {
			t.Fatalf("%s should be blocked", s)
		}
	}
}

func TestWebfetch_WFT09_MetadataBlocked(t *testing.T) {
	if reason := blockedReason(net.ParseIP("169.254.169.254"), false); reason == "" {
		t.Fatal("169.254.169.254 should be blocked")
	}
}

func TestWebfetch_WFT10_OtherReserved(t *testing.T) {
	for _, s := range []string{"100.64.0.1", "0.0.0.0", "224.0.0.1", "fe80::1"} {
		if reason := blockedReason(net.ParseIP(s), false); reason == "" {
			t.Fatalf("%s should be blocked", s)
		}
	}
}

func TestWebfetch_WFT11_IPv4MappedLoopback(t *testing.T) {
	if reason := blockedReason(net.ParseIP("::ffff:127.0.0.1"), false); reason == "" {
		t.Fatal("::ffff:127.0.0.1 should be blocked")
	}
}

func TestWebfetch_WFT12_HostnameResolvesPrivate(t *testing.T) {
	e := New()
	e.lookup = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("10.0.0.5")}}, nil
	}
	res := local(t, e, "http://internal.example/", false, 0)
	if !res.IsError || !strings.Contains(res.Content, "blocked address") {
		t.Fatalf("result = %q", res.Content)
	}
}

func TestWebfetch_WFT13_DNSRebinding(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "first address")
	}))
	defer srv.Close()
	var mu sync.Mutex
	lookups := 0
	e := New()
	e.allowPrivate = true
	e.lookup = func(context.Context, string) ([]net.IPAddr, error) {
		mu.Lock()
		defer mu.Unlock()
		lookups++
		if lookups == 1 {
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("10.0.0.9")}}, nil
	}
	res := local(t, e, srv.URL, false, 0)
	mu.Lock()
	n := lookups
	mu.Unlock()
	if res.IsError || !strings.Contains(res.Content, "first address") {
		t.Fatalf("result = %q", res.Content)
	}
	if n != 1 {
		t.Fatalf("lookups = %d, want 1", n)
	}
}

func TestWebfetch_WFT14_RedirectToBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://10.0.0.1/")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	e := New()
	e.allowHosts = []string{strings.Split(host, ":")[0]}
	res := local(t, e, srv.URL, false, 0)
	if !res.IsError || !strings.Contains(res.Content, "blocked address") {
		t.Fatalf("result = %q", res.Content)
	}
}

func TestWebfetch_WFT15_TooManyRedirects(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", srv.URL+"/next")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()
	e := New()
	e.allowHosts = []string{"127.0.0.1"}
	res := local(t, e, srv.URL, false, 0)
	if !res.IsError || !strings.Contains(res.Content, "too many redirects") {
		t.Fatalf("result = %q", res.Content)
	}
}

func TestWebfetch_WFT16_RedirectScheme(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "ftp://example.com/x")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()
	e := New()
	e.allowHosts = []string{"127.0.0.1"}
	res := local(t, e, srv.URL, false, 0)
	if !res.IsError || !strings.Contains(res.Content, "scheme") {
		t.Fatalf("result = %q", res.Content)
	}
}

func TestWebfetch_WFT17_AllowLists(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "allowed")
	}))
	defer srv.Close()
	byPrivate := newLocal()
	if res := local(t, byPrivate, srv.URL, false, 0); res.IsError {
		t.Fatalf("allow_private result = %q", res.Content)
	}
	byHost := New()
	byHost.allowHosts = []string{"127.0.0.1"}
	if res := local(t, byHost, srv.URL, false, 0); res.IsError {
		t.Fatalf("allow_hosts result = %q", res.Content)
	}
}

func TestWebfetch_WFT18_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	e := newLocal()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	body, _ := json.Marshal(map[string]any{"url": srv.URL})
	done := make(chan tool.Result, 1)
	go func() {
		res, _ := e.run(ctx, body, nil)
		done <- res
	}()
	select {
	case res := <-done:
		if !res.IsError {
			t.Fatalf("result = %q", res.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fetch did not time out")
	}
}

func TestWebfetch_WFT19_OverBodyCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		chunk := strings.Repeat("a", 1<<20)
		for i := 0; i < 6; i++ {
			fmt.Fprint(w, chunk)
		}
	}))
	defer srv.Close()
	e := newLocal()
	if res := local(t, e, srv.URL, true, 0); !res.IsError || !strings.Contains(res.Content, "too large") {
		t.Fatalf("raw result = %q", res.Content)
	}
	if res := local(t, e, srv.URL, false, 0); !strings.Contains(res.Content, "truncated at") {
		t.Fatalf("non-raw result missing note: %q", res.Content[len(res.Content)-80:])
	}
}

func TestWebfetch_WFT20_GzipBomb(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		chunk := strings.Repeat("a", 1<<20)
		for i := 0; i < 10; i++ {
			gz.Write([]byte(chunk))
		}
		gz.Close()
	}))
	defer srv.Close()
	res := local(t, newLocal(), srv.URL, false, 0)
	if !strings.Contains(res.Content, "truncated at") {
		t.Fatalf("result missing cap note")
	}
}

func TestWebfetch_WFT21_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/404" {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, strings.Repeat("x", 5000))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "boom")
	}))
	defer srv.Close()
	e := newLocal()
	res := local(t, e, srv.URL+"/404", false, 0)
	if !res.IsError || !strings.Contains(res.Content, "(404,") || len(res.Content) > headerBodyCap+120 {
		t.Fatalf("404 result len=%d %q", len(res.Content), res.Content[:80])
	}
	res = local(t, e, srv.URL+"/500", false, 0)
	if !res.IsError || !strings.Contains(res.Content, "(500,") {
		t.Fatalf("500 result = %q", res.Content)
	}
}

func TestWebfetch_WFT22_MaxChars(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		for i := 0; i < 500; i++ {
			fmt.Fprintf(w, "line %d\n", i)
		}
	}))
	defer srv.Close()
	res := local(t, newLocal(), srv.URL, false, 1000)
	if !strings.Contains(res.Content, "characters omitted") {
		t.Fatalf("result = %q", res.Content)
	}
}

func TestWebfetch_WFT23_MaxCharsCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, strings.Repeat("z", 300000))
	}))
	defer srv.Close()
	res := local(t, newLocal(), srv.URL, false, 10_000_000)
	if !strings.Contains(res.Content, "characters omitted") {
		t.Fatal("max_chars should be capped at 200000 and truncate the 300k body")
	}
}

func TestWebfetch_WFT24_Charset(t *testing.T) {
	latin1 := []byte{'c', 'a', 'f', 0xE9}
	if got := decodeCharset(latin1, "iso-8859-1"); got != "café" {
		t.Fatalf("latin1 = %q", got)
	}
	win := []byte{0x93, 'q', 0x94}
	if got := decodeCharset(win, "windows-1252"); got != "“q”" {
		t.Fatalf("win1252 = %q", got)
	}
	if got := decodeCharset([]byte{0xff, 0xfe}, "utf-8"); !strings.Contains(got, "\uFFFD") {
		t.Fatalf("invalid = %q", got)
	}
	if got := decodeCharset([]byte("plain"), "utf-8"); got != "plain" {
		t.Fatalf("utf8 = %q", got)
	}
}

func TestWebfetch_WFT25_ScriptsStylesDropped(t *testing.T) {
	html := `<html><head><style>p{color:red}</style></head><body><script>alert(1)</script><p>keep</p></body></html>`
	got := htmlToMarkdown([]byte(html), nil)
	if strings.Contains(got, "alert") || strings.Contains(got, "color:red") || !strings.Contains(got, "keep") {
		t.Fatalf("markdown = %q", got)
	}
}

func TestWebfetch_WFT26_BoilerplateDropped(t *testing.T) {
	html := `<html><body><nav>menu</nav><main><p>article</p></main><footer>foot</footer></body></html>`
	got := htmlToMarkdown([]byte(html), nil)
	if strings.Contains(got, "menu") || strings.Contains(got, "foot") || !strings.Contains(got, "article") {
		t.Fatalf("markdown = %q", got)
	}
}

func TestWebfetch_WFT27_Structure(t *testing.T) {
	html := `<html><body>
<h2>Heading</h2>
<ul><li>one</li><li>two</li></ul>
<p>See <a href="/rel">the docs</a> and <code>x := 1</code></p>
<table><tr><th>Name</th><th>Value</th></tr><tr><td>a</td><td>1</td></tr></table>
</body></html>`
	base, _ := parseURL("https://example.com/dir/page")
	got := htmlToMarkdown([]byte(html), base)
	for _, want := range []string{"## Heading", "- one", "- two", "[the docs](https://example.com/rel)", "`x := 1`", "| Name | Value |"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
}

func TestWebfetch_WFT28_DeeplyNested(t *testing.T) {
	body := strings.Repeat("<div>", 10000) + "x" + strings.Repeat("</div>", 10000)
	got := htmlToMarkdown([]byte(body), nil)
	if !strings.Contains(got, "x") {
		t.Fatalf("deeply nested dropped content")
	}
}

func TestWebfetch_WFT29_Malformed(t *testing.T) {
	got := htmlToMarkdown([]byte("<p>unclosed <b>bold <table><tr><td>"), nil)
	if !strings.Contains(got, "unclosed") {
		t.Fatalf("malformed = %q", got)
	}
}

func TestWebfetch_WFT30_HugePageFast(t *testing.T) {
	body := "<html><body>" + strings.Repeat("<p>paragraph text</p>", 250000) + "</body></html>"
	start := time.Now()
	_ = htmlToMarkdown([]byte(body), nil)
	if time.Since(start) > 2*time.Second {
		t.Fatalf("conversion took %s", time.Since(start))
	}
}

func TestWebfetch_WFT31_NoCookiesNoAuth(t *testing.T) {
	var mu sync.Mutex
	var sawCookie, sawAuth bool
	var target *httptest.Server
	target = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if r.Header.Get("Cookie") != "" {
			sawCookie = true
		}
		if r.Header.Get("Authorization") != "" {
			sawAuth = true
		}
		mu.Unlock()
		fmt.Fprint(w, "done")
	}))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "a", Value: "b"})
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()
	e := New()
	e.allowHosts = []string{"127.0.0.1"}
	if res := local(t, e, srv.URL, false, 0); res.IsError {
		t.Fatalf("result = %q", res.Content)
	}
	mu.Lock()
	defer mu.Unlock()
	if sawCookie || sawAuth {
		t.Fatalf("cookie=%v auth=%v", sawCookie, sawAuth)
	}
}

func TestWebfetch_WFT32_ProxyIgnored(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "direct")
	}))
	defer srv.Close()
	res := local(t, newLocal(), srv.URL, false, 0)
	if res.IsError || !strings.Contains(res.Content, "direct") {
		t.Fatalf("result = %q", res.Content)
	}
}

func TestWebfetch_WFT33_UserAgent(t *testing.T) {
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
	}))
	defer srv.Close()
	local(t, newLocal(), srv.URL, false, 0)
	if ua != userAgent {
		t.Fatalf("user agent = %q", ua)
	}
}

func TestWebfetch_WFT34_Annotations(t *testing.T) {
	var registered tool.Tool
	env := testEnv(func(t tool.Tool) { registered = t })
	e := New()
	_ = e.Init(env)
	info := registered.Info()
	if !info.ReadOnly || !info.OpenWorld || !info.Parallel {
		t.Fatalf("info = %+v", info)
	}
}

func TestWebfetch_WFT35_CancelMidRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	e := newLocal()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		body, _ := json.Marshal(map[string]any{"url": srv.URL})
		_, _ = e.run(ctx, body, nil)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("fetch did not return after cancel")
	}
}

func TestWebfetch_WFT36_ConcurrentFetches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.URL.Path)
	}))
	defer srv.Close()
	e := newLocal()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			res := local(t, e, fmt.Sprintf("%s/%d", srv.URL, n), false, 0)
			if !strings.Contains(res.Content, fmt.Sprintf("/%d", n)) {
				t.Errorf("fetch %d = %q", n, res.Content)
			}
		}(i)
	}
	wg.Wait()
}

func TestWebfetch_WFT37_OddURLs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()
	e := newLocal()
	for _, suffix := range []string{"/a b", "/caf%C3%A9", "/x?q=1#frag"} {
		if res := local(t, e, srv.URL+suffix, false, 0); res.IsError {
			t.Fatalf("%s result = %q", suffix, res.Content)
		}
	}
}

func TestWebfetch_WFT38_BadURLs(t *testing.T) {
	e := newLocal()
	for _, u := range []string{"", "/relative", "http://" + strings.Repeat("a", 100000) + ".com/"} {
		res := local(t, e, u, false, 0)
		if !res.IsError {
			t.Fatalf("%q result = %q", u, res.Content)
		}
	}
}
