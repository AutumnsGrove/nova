package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/auth"
)

// fakeConsole serves the device flow and provisioning endpoints.
type fakeConsole struct {
	*httptest.Server
	mu          sync.Mutex
	tokenCalls  int
	pendingFor  int
	deny        bool
	expired     bool
	keyName     string
	revoked     []string
	serviceAcct bool
}

func newFakeConsole(t *testing.T) *fakeConsole {
	t.Helper()
	fc := &fakeConsole{}
	fc.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fc.mu.Lock()
		defer fc.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/auth/device/code":
			json.NewEncoder(w).Encode(map[string]any{
				"device_code": "dc", "user_code": "ABCD",
				"verification_uri_complete": "/console/device?user_code=ABCD",
				"expires_in":                600, "interval": 0,
			})
		case r.URL.Path == "/auth/device/token":
			fc.tokenCalls++
			switch {
			case fc.deny:
				json.NewEncoder(w).Encode(map[string]any{"error": "access_denied"})
			case fc.expired:
				json.NewEncoder(w).Encode(map[string]any{"error": "expired_token"})
			case fc.tokenCalls <= fc.pendingFor:
				json.NewEncoder(w).Encode(map[string]any{"error": "authorization_pending"})
			default:
				json.NewEncoder(w).Encode(map[string]any{"access_token": "sess", "refresh_token": "r"})
			}
		case r.URL.Path == "/api/orgs":
			json.NewEncoder(w).Encode([]map[string]string{{"id": "org1"}})
		case r.URL.Path == "/api/service-accounts" && r.Method == http.MethodGet:
			if fc.serviceAcct {
				json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"account": map[string]string{"id": "sa1", "name": "nova"}}}})
			} else {
				json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
			}
		case r.URL.Path == "/api/service-accounts" && r.Method == http.MethodPost:
			fc.serviceAcct = true
			json.NewEncoder(w).Encode(map[string]any{"account": map[string]string{"id": "sa1", "name": "nova"}})
		case r.URL.Path == "/api/service-accounts/sa1":
			json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"id": "k0", "name": fc.keyName, "status": "active"}}})
		case strings.HasPrefix(r.URL.Path, "/api/service-accounts/keys/") && strings.HasSuffix(r.URL.Path, "/revoke"):
			fc.revoked = append(fc.revoked, r.URL.Path)
			json.NewEncoder(w).Encode(map[string]any{})
		case r.URL.Path == "/api/service-accounts/sa1/keys":
			json.NewEncoder(w).Encode(map[string]string{"token": "oc_sk_123"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fc.Close)
	return fc
}

func testConfig(t *testing.T, server string) Config {
	t.Helper()
	return Config{
		Server: server,
		Out:    io.Discard,
		Sleep:  func(context.Context, time.Duration) error { return nil },
	}
}

func TestOpencode_LoginFullFlow(t *testing.T) {
	fc := newFakeConsole(t)
	fc.pendingFor = 1
	fc.keyName = "nova-test"
	cfg := testConfig(t, fc.URL)
	cfg.KeyName = "nova-test"
	key, err := Login(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if key != "oc_sk_123" {
		t.Fatalf("key = %q", key)
	}
	if len(fc.revoked) != 1 {
		t.Fatalf("revoked = %v, want the old key revoked", fc.revoked)
	}
}

func TestOpencode_LoginDenied(t *testing.T) {
	fc := newFakeConsole(t)
	fc.deny = true
	_, err := Login(context.Background(), testConfig(t, fc.URL))
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("err = %v, want denied", err)
	}
}

func TestOpencode_LoginExpired(t *testing.T) {
	fc := newFakeConsole(t)
	fc.expired = true
	_, err := Login(context.Background(), testConfig(t, fc.URL))
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("err = %v, want expired", err)
	}
}

func TestOpencode_VerificationURLMapping(t *testing.T) {
	got, err := resolveVerificationURL("https://console.opencode.ai/console", "/console/device?user_code=X")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "https://opencode.ai/") || strings.Contains(got, "console.opencode.ai") {
		t.Fatalf("url = %q", got)
	}
}

func TestOpencode_DefaultKeyName(t *testing.T) {
	name := DefaultKeyName()
	if !strings.HasPrefix(name, "nova-") || len(name) > 60 {
		t.Fatalf("name = %q", name)
	}
}

func TestOpencode_StoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode-go.json")
	if err := SaveKey(path, "k"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
	got, err := LoadKey(path)
	if err != nil || got != "k" {
		t.Fatalf("LoadKey = %q, %v", got, err)
	}
	if _, err := LoadKey(filepath.Join(t.TempDir(), "missing.json")); !errors.Is(err, auth.ErrNoKey) {
		t.Fatalf("missing = %v, want ErrNoKey", err)
	}
}

func TestOpencode_RequestDeviceCode(t *testing.T) {
	fc := newFakeConsole(t)
	device, err := RequestDeviceCode(context.Background(), testConfig(t, fc.URL))
	if err != nil {
		t.Fatal(err)
	}
	if device.DeviceCode != "dc" || device.UserCode != "ABCD" || device.Interval != 5*time.Second {
		t.Fatalf("device = %+v", device)
	}
}
