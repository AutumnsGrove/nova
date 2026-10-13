package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/auth"
	"github.com/AutumnsGrove/nova/config"
)

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// healthyHTTP answers 200 to any request.
func healthyHTTP() *http.Client {
	return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Status: "200 OK", Body: io.NopCloser(strings.NewReader(`{"data":[]}`))}, nil
	})}
}

// stallingHTTP blocks until the request context is cancelled.
func stallingHTTP() *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
}

// envFrom builds an Env lookup over a base map plus overrides.
func envFrom(base, overrides map[string]string) func(string) string {
	m := map[string]string{
		"PATH": "/usr/bin:/bin", "TERM": "xterm-256color", "COLORTERM": "truecolor", "LANG": "en_US.UTF-8",
	}
	for k, v := range base {
		m[k] = v
	}
	for k, v := range overrides {
		m[k] = v
	}
	return func(name string) string { return m[name] }
}

// testOptions returns a healthy baseline that tests override.
func testOptions(t *testing.T) Options {
	t.Helper()
	return Options{
		CWD:            t.TempDir(),
		Home:           t.TempDir(),
		Env:            envFrom(nil, map[string]string{"OPENROUTER_API_KEY": "sk-test"}),
		Offline:        true,
		HTTP:           healthyHTTP(),
		Size:           func() (int, int, bool) { return 80, 24, true },
		ReadFile:       os.ReadFile,
		NetworkTimeout: 200 * time.Millisecond,
		Timeout:        3 * time.Second,
		Version:        "v0.1.0",
		Flavour:        "full",
	}
}

// find returns the named check, failing when it is missing.
func find(t *testing.T, checks []Check, name string) Check {
	t.Helper()
	for _, c := range checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %q check in %v", name, names(checks))
	return Check{}
}

func names(checks []Check) []string {
	out := make([]string, len(checks))
	for i, c := range checks {
		out[i] = c.Name
	}
	return out
}

func TestDoctor_DRX01_AllOK(t *testing.T) {
	opt := testOptions(t)
	opt.Offline = false
	checks := Run(context.Background(), opt)
	for _, c := range checks {
		if c.Status != OK {
			t.Fatalf("check %s = %s (%s), want ok", c.Name, c.Status, c.Detail)
		}
	}
}

func TestDoctor_DRX02_MissingKey(t *testing.T) {
	opt := testOptions(t)
	opt.Env = envFrom(nil, nil) // no OPENROUTER_API_KEY
	checks := Run(context.Background(), opt)
	c := find(t, checks, "api key")
	if c.Status != Fail {
		t.Fatalf("api key = %s, want fail", c.Status)
	}
	if c.Fix == "" || !strings.Contains(c.Fix, "login") {
		t.Fatalf("api key fix = %q, want login guidance", c.Fix)
	}
}

func TestDoctor_DRX03_KeySource(t *testing.T) {
	cases := []struct {
		name string
		set  func(t *testing.T, opt *Options)
		want string
	}{
		{"config", func(_ *testing.T, opt *Options) { opt.ConfigKey = "cfg-secret" }, "config"},
		{"env", func(_ *testing.T, opt *Options) {
			opt.Env = envFrom(nil, map[string]string{"OPENROUTER_API_KEY": "env-secret"})
		}, "env"},
		{"store", func(t *testing.T, opt *Options) {
			opt.Env = envFrom(nil, nil)
			store := auth.NewFileStore(filepath.Join(config.UserConfigDir(opt.Home), "auth.json"))
			if err := store.Save("store-secret"); err != nil {
				t.Fatal(err)
			}
		}, "store"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opt := testOptions(t)
			tc.set(t, &opt)
			checks := Run(context.Background(), opt)
			c := find(t, checks, "api key")
			if c.Status != OK || !strings.Contains(c.Detail, tc.want) {
				t.Fatalf("api key = %s %q, want source %q", c.Status, c.Detail, tc.want)
			}
		})
	}
}

func TestDoctor_DRX04_NetworkTimeout(t *testing.T) {
	opt := testOptions(t)
	opt.Offline = false
	opt.HTTP = stallingHTTP()
	opt.NetworkTimeout = 50 * time.Millisecond
	checks := Run(context.Background(), opt)
	c := find(t, checks, "network")
	if c.Status != Warn {
		t.Fatalf("network = %s (%s), want warn", c.Status, c.Detail)
	}
}

func TestDoctor_DRX05_Offline(t *testing.T) {
	opt := testOptions(t)
	opt.Offline = true
	opt.HTTP = stallingHTTP()
	checks := Run(context.Background(), opt)
	c := find(t, checks, "network")
	if c.Status != OK || !strings.Contains(c.Detail, "offline") {
		t.Fatalf("network = %s %q, want a skipped ok", c.Status, c.Detail)
	}
}

func TestDoctor_DRX06_GitAndBashMissing(t *testing.T) {
	opt := testOptions(t)
	opt.Env = envFrom(map[string]string{"PATH": ""}, nil)
	checks := Run(context.Background(), opt)
	if c := find(t, checks, "git"); c.Status != Warn {
		t.Fatalf("git = %s, want warn", c.Status)
	}
	if c := find(t, checks, "bash"); c.Status != Fail {
		t.Fatalf("bash = %s, want fail", c.Status)
	}
}

func TestDoctor_DRX07_TerminalWarnings(t *testing.T) {
	opt := testOptions(t)
	opt.Env = envFrom(map[string]string{"TERM": "dumb", "COLORTERM": "", "LANG": "C", "LC_ALL": "", "LC_CTYPE": ""}, nil)
	checks := Run(context.Background(), opt)
	c := find(t, checks, "terminal")
	if c.Status != Warn {
		t.Fatalf("terminal = %s (%s), want warn", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "dumb") || !strings.Contains(c.Detail, "UTF-8") {
		t.Fatalf("terminal detail = %q, want dumb and UTF-8 warnings", c.Detail)
	}
}

func TestDoctor_DRX08_ConfigWarnings(t *testing.T) {
	opt := testOptions(t)
	path := filepath.Join(config.UserConfigDir(opt.Home), "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not_a_real_key: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	checks := Run(context.Background(), opt)
	c := find(t, checks, "config")
	if c.Status != Warn || !strings.Contains(c.Detail, "not_a_real_key") {
		t.Fatalf("config = %s %q, want a warning naming the key", c.Status, c.Detail)
	}
}

func TestDoctor_DRX09_UnwritableDirectories(t *testing.T) {
	opt := testOptions(t)
	home := t.TempDir()
	if err := os.Chmod(home, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0o700) })
	opt.Home = home
	checks := Run(context.Background(), opt)
	c := find(t, checks, "directories")
	if c.Status != Fail || !strings.Contains(c.Detail, home) {
		t.Fatalf("directories = %s %q, want fail naming %s", c.Status, c.Detail, home)
	}
}

func TestDoctor_DRX10_TrustStatus(t *testing.T) {
	opt := testOptions(t)
	checks := Run(context.Background(), opt)
	c := find(t, checks, "trust")
	if c.Status != OK || !strings.Contains(c.Detail, "trusted") {
		t.Fatalf("trust = %s %q", c.Status, c.Detail)
	}
}

func TestDoctor_DRX11_Extensions(t *testing.T) {
	opt := testOptions(t)
	opt.Host = func() []ExtensionStatus {
		return []ExtensionStatus{
			{Name: "good", OK: true},
			{Name: "bad", OK: false, Detail: "manifest is invalid"},
		}
	}
	checks := Run(context.Background(), opt)
	c := find(t, checks, "extensions")
	if c.Status != Fail || !strings.Contains(c.Detail, "bad") {
		t.Fatalf("extensions = %s %q, want a fail naming bad", c.Status, c.Detail)
	}
}

func TestDoctor_DRX12_ProjectContext(t *testing.T) {
	opt := testOptions(t)
	global := filepath.Join(config.UserConfigDir(opt.Home), "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(global), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(global, []byte("# rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(config.SkillsDir(config.UserConfigDir(opt.Home)), "demo")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skill := "---\nname: demo\ndescription: a demo skill\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skill), 0o644); err != nil {
		t.Fatal(err)
	}
	checks := Run(context.Background(), opt)
	c := find(t, checks, "project")
	if c.Status != OK || !strings.Contains(c.Detail, "1 context") || !strings.Contains(c.Detail, "1 skill") {
		t.Fatalf("project = %s %q, want one context file and one skill", c.Status, c.Detail)
	}
}

func TestDoctor_DRX13_ExitCodes(t *testing.T) {
	if ExitCode([]Check{{Status: Warn}}) != 0 || ExitCode([]Check{{Status: OK}}) != 0 {
		t.Fatal("ok and warn must exit 0")
	}
	if ExitCode([]Check{{Status: Warn}, {Status: Fail}}) != 1 {
		t.Fatal("a fail must exit 1")
	}
}

func TestDoctor_DRX14_JSON(t *testing.T) {
	opt := testOptions(t)
	opt.Offline = true
	checks := Run(context.Background(), opt)
	var buf bytes.Buffer
	if err := WriteJSON(&buf, checks); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	var got []Check
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, buf.String())
	}
	if len(got) != len(checks) {
		t.Fatalf("JSON has %d checks, want %d", len(got), len(checks))
	}
	for _, c := range got {
		if c.Name == "" || c.Detail == "" {
			t.Fatalf("check missing fields: %+v", c)
		}
	}
}

// snapshot records every path, mode, and content under roots.
func snapshot(t *testing.T, roots ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, root := range roots {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			rel := path
			data, _ := os.ReadFile(path)
			out[rel] = fmt.Sprintf("%s %s", info.Mode(), data)
			return nil
		})
	}
	return out
}

func TestDoctor_DRX15_NeverModifies(t *testing.T) {
	opt := testOptions(t)
	path := filepath.Join(config.UserConfigDir(opt.Home), "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("model: a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, opt.Home, opt.CWD)
	_ = Run(context.Background(), opt)
	after := snapshot(t, opt.Home, opt.CWD)
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatalf("doctor changed the filesystem:\nbefore=%v\nafter=%v", before, after)
	}
}

func TestDoctor_DRX16_NeverPrintsSecret(t *testing.T) {
	opt := testOptions(t)
	secret := "sk-or-v1-super-secret-value"
	opt.Env = envFrom(nil, map[string]string{"OPENROUTER_API_KEY": secret})
	checks := Run(context.Background(), opt)
	var buf bytes.Buffer
	Write(&buf, checks)
	_ = WriteJSON(&buf, checks)
	if strings.Contains(buf.String(), secret) || strings.Contains(fmt.Sprint(checks), secret) {
		t.Fatalf("doctor printed the secret:\n%s", buf.String())
	}
}

func TestDoctor_DRX17_BoundedRuntime(t *testing.T) {
	opt := testOptions(t)
	opt.Offline = false
	opt.HTTP = stallingHTTP()
	opt.Timeout = 100 * time.Millisecond
	opt.NetworkTimeout = time.Hour // the run timeout must dominate
	began := time.Now()
	_ = Run(context.Background(), opt)
	if elapsed := time.Since(began); elapsed > 2*time.Second {
		t.Fatalf("doctor took %s, want under 2s", elapsed)
	}
}

// TestProperty_DRX_NoSecrets asserts that no output ever contains a
// credential-looking environment value.
func TestProperty_DRX_NoSecrets(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 50; i++ {
		secret := fmt.Sprintf("sk-%d-%s", rng.Intn(1000), strings.Repeat("x", 1+rng.Intn(8)))
		opt := testOptions(t)
		opt.Offline = true
		opt.Env = envFrom(nil, map[string]string{"OPENROUTER_API_KEY": secret})
		checks := Run(context.Background(), opt)
		var buf bytes.Buffer
		Write(&buf, checks)
		if strings.Contains(buf.String(), secret) {
			t.Fatalf("output leaked the secret %q", secret)
		}
	}
}

// TestDoctor_DRX_UnreadableConfigFails proves the one config failure path.
func TestDoctor_DRX_UnreadableConfigFails(t *testing.T) {
	opt := testOptions(t)
	opt.ReadFile = func(string) ([]byte, error) { return nil, errors.New("permission denied") }
	checks := Run(context.Background(), opt)
	c := find(t, checks, "config")
	if c.Status != Fail {
		t.Fatalf("config = %s %q, want fail", c.Status, c.Detail)
	}
}
