package auth

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// beginFlow starts a flow with the fake exchange and returns it with the
// parsed authorization URL.
func beginFlow(t *testing.T, fx *fakeExchange, open func(string) error) (*Flow, *url.URL) {
	t.Helper()
	flow, raw, err := Begin(LoginOptions{AuthURL: "https://example.test/auth", ExchangeURL: fx.URL, OpenBrowser: open})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	t.Cleanup(func() { _ = flow.Close() })
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse auth URL: %v", err)
	}
	return flow, u
}

func TestAuth_SGN23_OAuthBegins(t *testing.T) {
	fx := newFakeExchange(t, http.StatusOK, `{"key":"k"}`)
	_, u := beginFlow(t, fx, nil)
	if u.Query().Get("code_challenge") == "" || u.Query().Get("callback_url") == "" {
		t.Fatalf("auth URL missing PKCE parameters: %s", u)
	}
}

func TestAuth_SGN24_BrowserOpens(t *testing.T) {
	fx := newFakeExchange(t, http.StatusOK, `{"key":"k"}`)
	var got string
	_, u := beginFlow(t, fx, func(raw string) error { got = raw; return nil })
	if got == "" || got != u.String() {
		t.Fatalf("browser opened %q, want %q", got, u.String())
	}
}

func TestAuth_SGN25_PastedRedirectCompletes(t *testing.T) {
	fx := newFakeExchange(t, http.StatusOK, `{"key":"pasted-key"}`)
	flow, u := beginFlow(t, fx, nil)
	redirect := u.Query().Get("callback_url") + "?code=the-code&state=" + url.QueryEscape(u.Query().Get("state"))
	if err := flow.Submit(redirect); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	key, err := flow.Wait(context.Background())
	if err != nil || key != "pasted-key" {
		t.Fatalf("Wait() = (%q, %v)", key, err)
	}
}

func TestAuth_SGN26_MalformedRedirect(t *testing.T) {
	fx := newFakeExchange(t, http.StatusOK, `{"key":"k"}`)
	flow, u := beginFlow(t, fx, nil)
	if err := flow.Submit("garbage"); err == nil {
		t.Fatal("Submit accepted garbage")
	}
	wrong := u.Query().Get("callback_url") + "?code=c&state=wrong"
	if err := flow.Submit(wrong); err == nil {
		t.Fatal("Submit accepted a mismatched state")
	}
	if fx.count() != 0 {
		t.Fatal("a malformed submit reached the exchange")
	}
}

func TestAuth_SGN27_LocalCallbackCompletes(t *testing.T) {
	fx := newFakeExchange(t, http.StatusOK, `{"key":"callback-key"}`)
	flow, u := beginFlow(t, fx, nil)
	callback := u.Query().Get("callback_url")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(callback + "?code=the-code&state=" + url.QueryEscape(u.Query().Get("state")))
	if err != nil {
		t.Fatalf("callback GET: %v", err)
	}
	resp.Body.Close()
	key, err := flow.Wait(context.Background())
	if err != nil || key != "callback-key" {
		t.Fatalf("Wait() = (%q, %v)", key, err)
	}
}

func TestAuth_SGN30_CloseReleasesListener(t *testing.T) {
	fx := newFakeExchange(t, http.StatusOK, `{"key":"k"}`)
	flow, u := beginFlow(t, fx, nil)
	if err := flow.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := flow.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	client := &http.Client{Timeout: time.Second}
	if _, err := client.Get(u.Query().Get("callback_url")); err == nil {
		t.Fatal("callback still accepts after Close")
	}
}

func FuzzRedirect(f *testing.F) {
	f.Add("http://127.0.0.1:1/callback?code=x&state=y")
	f.Add("garbage")
	f.Add("")
	f.Add("http://x/callback?error=denied&state=s")
	f.Fuzz(func(t *testing.T, pasted string) {
		flow := &Flow{flow: &loginFlow{state: "s", result: make(chan callbackResult, 1)}}
		err := flow.Submit(pasted)
		if err != nil {
			return
		}
		u, perr := url.Parse(strings.TrimSpace(pasted))
		if perr != nil || u.Query().Get("code") == "" || u.Query().Get("state") != "s" {
			t.Fatalf("Submit accepted %q without a matching code and state", pasted)
		}
	})
}

func TestAuth_SGN56_AddrAndReceivedAfterCallback(t *testing.T) {
	fx := newFakeExchange(t, http.StatusOK, `{"key":"k"}`)
	flow, u := beginFlow(t, fx, nil)
	callback := u.Query().Get("callback_url")
	if !strings.HasPrefix(callback, "http://"+flow.Addr()+"/") {
		t.Fatalf("Addr() = %q, but the callback URL is %q", flow.Addr(), callback)
	}
	if flow.Received() {
		t.Fatal("Received() is true before any callback")
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(callback + "?code=the-code&state=" + url.QueryEscape(u.Query().Get("state")))
	if err != nil {
		t.Fatalf("callback GET: %v", err)
	}
	resp.Body.Close()
	if !flow.Received() {
		t.Fatal("Received() is false after the callback arrived")
	}
}

func TestAuth_SGN57_ReceivedAfterAPastedRedirect(t *testing.T) {
	fx := newFakeExchange(t, http.StatusOK, `{"key":"k"}`)
	flow, u := beginFlow(t, fx, nil)
	// A rejected paste is not a callback.
	if err := flow.Submit("not a url"); err == nil {
		t.Fatal("a malformed redirect was accepted")
	}
	if flow.Received() {
		t.Fatal("Received() is true after a rejected paste")
	}
	redirect := u.Query().Get("callback_url") + "?code=c&state=" + url.QueryEscape(u.Query().Get("state"))
	if err := flow.Submit(redirect); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !flow.Received() {
		t.Fatal("Received() is false after a good paste")
	}
}
