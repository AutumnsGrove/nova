package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/auth"
	"github.com/AutumnsGrove/nova/config"
)

// safeBuffer is a bytes.Buffer safe for concurrent write and read.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestCLI_CLI06_Login drives `nova login` against a fake exchange server and a
// loopback callback, and checks the key lands in the store with mode 0600.
func TestCLI_CLI06_Login(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	exchange := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"key": "stored-key"})
	}))
	defer exchange.Close()
	t.Setenv("NOVA_AUTH_URL", "https://auth.example/authorize")
	t.Setenv("NOVA_EXCHANGE_URL", exchange.URL)

	var errBuf safeBuffer
	done := make(chan int, 1)
	go func() {
		done <- run([]string{"login", "--no-browser"}, strings.NewReader(""), &bytes.Buffer{}, &errBuf, true)
	}()

	authURL := waitForAuthURL(t, &errBuf)
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse auth url: %v", err)
	}
	query := parsed.Query()
	redirect := query.Get("callback_url")
	state := query.Get("state")
	if redirect == "" || state == "" {
		t.Fatalf("auth url missing callback_url or state: %s", authURL)
	}
	resp, err := http.Get(redirect + "?code=abc&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	_ = resp.Body.Close()

	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("login exit = %d, stderr: %s", code, errBuf.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("login did not finish")
	}

	storePath := filepath.Join(config.UserConfigDir(home), "auth.json")
	store := auth.NewFileStore(storePath)
	key, err := store.Load()
	if err != nil || key != "stored-key" {
		t.Fatalf("stored key = %q, %v; want stored-key", key, err)
	}
}

// waitForAuthURL polls the captured output for the printed authorization URL.
func waitForAuthURL(t *testing.T, out *safeBuffer) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		text := out.String()
		if i := strings.Index(text, "https://auth.example/authorize?"); i >= 0 {
			line := text[i:]
			if j := strings.IndexByte(line, '\n'); j >= 0 {
				line = line[:j]
			}
			return strings.TrimSpace(line)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the authorization URL was not printed:\n%s", out.String())
	return ""
}
