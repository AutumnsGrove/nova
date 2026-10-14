package update

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// writeCheckout lays out the smallest tree Detect accepts: a go.mod with the
// Nova module line and a cmd/nova directory.
func writeCheckout(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "cmd", "nova"), 0o755); err != nil {
		t.Fatal(err)
	}
	gomod := "module github.com/AumnsGrove/nova\n\ngo 1.26\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeTarget writes a stand-in for the installed binary and returns its path.
func writeTarget(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "nova")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// readTarget returns the target's bytes, or "" when it is missing.
func readTarget(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatal(err)
	}
	return string(data)
}

// execCall records one command the fake runner saw.
type execCall struct {
	dir  string
	env  []string
	name string
	args []string
}

// fakeExec stands in for the go toolchain. A "go" call is a build and writes
// buildBody to the -o path; any other call is the staged binary's sanity check.
type fakeExec struct {
	mu         sync.Mutex
	calls      []execCall
	buildErr   error
	versionErr error
	buildBody  []byte
}

func (f *fakeExec) exec(_ context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, execCall{dir: dir, env: env, name: name, args: args})
	f.mu.Unlock()
	if name == "go" {
		if f.buildErr != nil {
			return nil, f.buildErr
		}
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "-o" {
				if err := os.WriteFile(args[i+1], f.buildBody, 0o755); err != nil {
					return nil, err
				}
			}
		}
		return nil, nil
	}
	if f.versionErr != nil {
		return nil, f.versionErr
	}
	return []byte("nova v0.0.0\n"), nil
}

// last returns the most recent call, or false when there was none.
func (f *fakeExec) last() (execCall, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return execCall{}, false
	}
	return f.calls[len(f.calls)-1], true
}

// goCall returns the single go build call, failing when there was not exactly
// one. The staged binary's version check is a second call and is not a build.
func (f *fakeExec) goCall(t *testing.T) execCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	var builds []execCall
	for _, c := range f.calls {
		if c.name == "go" {
			builds = append(builds, c)
		}
	}
	if len(builds) != 1 {
		t.Fatalf("go build calls = %v, want exactly one", builds)
	}
	return builds[0]
}

// calledGo reports whether a go build was invoked.
func (f *fakeExec) calledGo() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c.name == "go" {
			return true
		}
	}
	return false
}

// sourceOptions builds Options for a source run with the fake runner.
func sourceOptions(root, target string, ex *fakeExec) Options {
	return Options{
		Mode:     Source,
		Dir:      root,
		Target:   target,
		Flavour:  "full",
		Exec:     ex.exec,
		LookPath: func(string) (string, error) { return "/usr/local/go/bin/go", nil },
		Env:      []string{"PATH=/usr/bin:/bin"},
		Out:      io.Discard,
	}
}

func TestDetect_UPD01_Checkout(t *testing.T) {
	root := t.TempDir()
	writeCheckout(t, root)
	got, ok := Detect(root)
	if !ok || got != root {
		t.Fatalf("Detect(%q) = %q, %v; want %q, true", root, got, ok, root)
	}
}

func TestDetect_UPD02_PlainDirectory(t *testing.T) {
	if got, ok := Detect(t.TempDir()); ok {
		t.Fatalf("Detect = %q, true; want false in a plain directory", got)
	}
}

func TestDetect_UPD03_NestedDirectory(t *testing.T) {
	root := t.TempDir()
	writeCheckout(t, root)
	nested := filepath.Join(root, "update", "sub")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	got, ok := Detect(nested)
	if !ok || got != root {
		t.Fatalf("Detect(%q) = %q, %v; want %q, true", nested, got, ok, root)
	}
}

func TestDetect_UPD04_AnotherModule(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "cmd", "nova"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := Detect(root); ok {
		t.Fatalf("Detect = %q, true; want false for another module", got)
	}
}

func TestRun_UPD05_ForceSource(t *testing.T) {
	root := t.TempDir()
	writeCheckout(t, root)
	target := writeTarget(t, t.TempDir(), "old")
	ex := &fakeExec{buildBody: []byte("fresh")}
	res, err := Run(context.Background(), sourceOptions(root, target, ex))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Mode != Source || !res.Changed {
		t.Fatalf("Result = %+v, want a changed source run", res)
	}
	if got := ex.goCall(t); got.name != "go" || got.dir != root {
		t.Fatalf("build call = %+v, want go in %s", got, root)
	}
	if got := readTarget(t, target); got != "fresh" {
		t.Fatalf("target = %q, want the built body", got)
	}
}

func TestRun_UPD21_SourceBuildCommand(t *testing.T) {
	root := t.TempDir()
	writeCheckout(t, root)
	target := writeTarget(t, t.TempDir(), "old")
	ex := &fakeExec{buildBody: []byte("fresh")}
	if _, err := Run(context.Background(), sourceOptions(root, target, ex)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	build := ex.goCall(t)
	if build.name != "go" || len(build.args) == 0 || build.args[0] != "build" {
		t.Fatalf("build call = %+v, want go build", build)
	}
	if !containsArg(build.args, "./cmd/nova") {
		t.Fatalf("build args = %v, want ./cmd/nova", build.args)
	}
	if !containsEnv(build.env, "CGO_ENABLED=0") {
		t.Fatalf("build env = %v, want CGO_ENABLED=0", build.env)
	}
}

func TestRun_UPD22_MinFlavourSourceBuild(t *testing.T) {
	root := t.TempDir()
	writeCheckout(t, root)
	target := writeTarget(t, t.TempDir(), "old")
	ex := &fakeExec{buildBody: []byte("fresh")}
	opt := sourceOptions(root, target, ex)
	opt.Flavour = "min"
	if _, err := Run(context.Background(), opt); err != nil {
		t.Fatalf("Run: %v", err)
	}
	build := ex.goCall(t)
	if !containsArg(build.args, "-tags") || !containsArg(build.args, "notui") {
		t.Fatalf("build args = %v, want -tags notui", build.args)
	}
}

func TestRun_UPD23_GoMissing(t *testing.T) {
	root := t.TempDir()
	writeCheckout(t, root)
	target := writeTarget(t, t.TempDir(), "old")
	ex := &fakeExec{}
	opt := sourceOptions(root, target, ex)
	opt.LookPath = func(string) (string, error) { return "", fmt.Errorf("not found") }
	if _, err := Run(context.Background(), opt); err == nil || !strings.Contains(err.Error(), "go") {
		t.Fatalf("Run error = %v, want one naming go", err)
	}
	if got := readTarget(t, target); got != "old" {
		t.Fatalf("target = %q, want the old binary", got)
	}
}

func TestRun_UPD24_BuildFailure(t *testing.T) {
	root := t.TempDir()
	writeCheckout(t, root)
	target := writeTarget(t, t.TempDir(), "old")
	ex := &fakeExec{buildErr: fmt.Errorf("compile failed")}
	if _, err := Run(context.Background(), sourceOptions(root, target, ex)); err == nil {
		t.Fatal("Run succeeded despite a failed build")
	}
	if got := readTarget(t, target); got != "old" {
		t.Fatalf("target = %q, want the old binary", got)
	}
}

func TestRun_UPD25_SourceReplaces(t *testing.T) {
	root := t.TempDir()
	writeCheckout(t, root)
	target := writeTarget(t, t.TempDir(), "old")
	ex := &fakeExec{buildBody: []byte("built")}
	if _, err := Run(context.Background(), sourceOptions(root, target, ex)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %o, want 755", info.Mode().Perm())
	}
	if got := readTarget(t, target); got != "built" {
		t.Fatalf("target = %q, want the built body", got)
	}
}

func TestRun_UPD26_SourceWithoutCheckout(t *testing.T) {
	target := writeTarget(t, t.TempDir(), "old")
	ex := &fakeExec{}
	opt := sourceOptions(t.TempDir(), target, ex)
	if _, err := Run(context.Background(), opt); err == nil {
		t.Fatal("Run succeeded outside a checkout")
	}
	if got := readTarget(t, target); got != "old" {
		t.Fatalf("target = %q, want the old binary", got)
	}
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func containsEnv(env []string, want string) bool {
	for _, e := range env {
		if e == want {
			return true
		}
	}
	return false
}

func FuzzDetect(f *testing.F) {
	f.Add(".")
	f.Add("/tmp")
	f.Fuzz(func(t *testing.T, dir string) {
		root, ok := Detect(dir)
		if !ok {
			return
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			t.Fatalf("Abs(%q): %v", dir, err)
		}
		rel, err := filepath.Rel(root, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Fatalf("Detect(%q) = %q, not an ancestor", dir, root)
		}
	})
}

func FuzzChecksums(f *testing.F) {
	f.Add("abc123  nova_v1_linux_amd64.tar.gz\n")
	f.Fuzz(func(t *testing.T, data string) {
		_, _ = checksumFor([]byte(data), "nova_linux_amd64.tar.gz")
	})
}
