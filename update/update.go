// Package update replaces the running nova binary from a source checkout or
// from a GitHub release. It reads no environment and finds no executable path
// of its own: the caller passes both, so every path is testable with a temp
// directory, an httptest server, and a fake command runner.
package update

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Mode says where a new binary comes from.
type Mode int

const (
	Auto Mode = iota // a checkout at Dir if there is one, else a release
	Source
	Release
)

// ExecFunc runs a command and returns its combined output. It is a field on
// Options so tests never need the real go toolchain.
type ExecFunc func(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error)

// Options configures one run. Zero values are filled with sensible defaults.
type Options struct {
	Mode    Mode
	Dir     string // source checkout; empty means the current directory
	Version string // release tag; empty means the latest
	Flavour string // "full" or "min"
	Target  string // the binary to replace
	Check   bool   // report only; never write
	Force   bool   // reinstall even when the version is unchanged

	BaseURL  string // release base
	APIURL   string // release API; default <BaseURL>/releases/latest
	GOOS     string // default runtime.GOOS
	GOARCH   string // default runtime.GOARCH
	HTTP     *http.Client
	Exec     ExecFunc
	LookPath func(string) (string, error)
	Env      []string
	Out      io.Writer
	Current  string // the running version, for --check
}

// Result describes what happened.
type Result struct {
	Mode    Mode
	From    string
	To      string
	Changed bool
	Message string
}

const defaultBaseURL = "https://github.com/AumnsGrove/nova"

// Run performs the update described by opt.
func Run(ctx context.Context, opt Options) (Result, error) {
	opt = withDefaults(opt)
	if opt.Target == "" {
		return Result{}, fmt.Errorf("no target binary to replace")
	}
	if opt.Flavour != "full" && opt.Flavour != "min" {
		return Result{}, fmt.Errorf("unknown flavour %q: want full or min", opt.Flavour)
	}
	mode, dir := opt.Mode, opt.Dir
	if mode == Auto {
		if root, ok := Detect(dir); ok {
			mode, dir = Source, root
		} else {
			mode = Release
		}
	}
	switch mode {
	case Source:
		return runSource(ctx, opt, dir)
	case Release:
		return runRelease(ctx, opt)
	default:
		return Result{}, fmt.Errorf("unknown update mode")
	}
}

// Detect finds a Nova source checkout at or above dir. It returns the module
// root and true, or "" and false. A directory is a checkout when it holds a
// go.mod whose module line is the Nova module and a cmd/nova directory.
func Detect(dir string) (string, bool) {
	if dir == "" {
		dir = "."
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}
	for {
		if isCheckout(abs) {
			return abs, true
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", false
		}
		abs = parent
	}
}

// isCheckout reports whether dir is the root of the Nova module.
func isCheckout(dir string) bool {
	if info, err := os.Stat(filepath.Join(dir, "cmd", "nova")); err != nil || !info.IsDir() {
		return false
	}
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")) == "github.com/AumnsGrove/nova"
		}
	}
	return false
}

// withDefaults fills the zero fields and resolves the API URL.
func withDefaults(opt Options) Options {
	if opt.Flavour == "" {
		opt.Flavour = "full"
	}
	if opt.GOOS == "" {
		opt.GOOS = runtime.GOOS
	}
	if opt.GOARCH == "" {
		opt.GOARCH = runtime.GOARCH
	}
	if opt.BaseURL == "" {
		opt.BaseURL = defaultBaseURL
	}
	opt.BaseURL = strings.TrimSuffix(opt.BaseURL, "/")
	if opt.APIURL == "" {
		opt.APIURL = opt.BaseURL + "/releases/latest"
	}
	if opt.HTTP == nil {
		opt.HTTP = &http.Client{Timeout: 2 * time.Minute}
	}
	if opt.Exec == nil {
		opt.Exec = defaultExec
	}
	if opt.LookPath == nil {
		opt.LookPath = exec.LookPath
	}
	if opt.Env == nil {
		opt.Env = os.Environ()
	}
	if opt.Out == nil {
		opt.Out = io.Discard
	}
	return opt
}

// defaultExec runs a command and returns its combined output.
func defaultExec(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	return cmd.CombinedOutput()
}

// displayVersion renders an unknown current version honestly.
func displayVersion(v string) string {
	if v == "" {
		return "(unknown)"
	}
	return v
}
