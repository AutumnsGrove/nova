package ext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tool"
)

// defaultBuildTimeout bounds one build step when the caller sets no timeout.
const defaultBuildTimeout = 5 * time.Minute

// maxBuildOutputBytes bounds the build output kept for an error message.
const maxBuildOutputBytes = 64 << 10

// CommandRunner runs a build step.
type CommandRunner interface {
	Run(ctx context.Context, dir string, argv []string) error
}

// ExecRunner runs a build step directly. Env is the environment it runs in;
// nil means the parent's.
type ExecRunner struct{ Env []string }

// NewExecRunner returns an ExecRunner whose environment is the process's minus
// the variables called exactly one of scrub.
func NewExecRunner(scrub []string) ExecRunner {
	return ExecRunner{Env: tool.ScrubEnv(os.Environ(), scrub)}
}

// Run runs argv in dir. A failure carries the end of the command's output.
func (r ExecRunner) Run(ctx context.Context, dir string, argv []string) error {
	if len(argv) == 0 {
		return nil
	}
	cmd := groupCommand(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = r.Env
	out := tool.NewTailBuffer(maxBuildOutputBytes)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ext: build failed: %w: %s", err, strings.TrimSpace(string(out.Bytes())))
	}
	return nil
}

// runBuild runs one build step under a timeout, with run or, when it is nil,
// the direct runner.
func runBuild(ctx context.Context, run CommandRunner, dir string, argv []string, timeout time.Duration) error {
	if run == nil {
		run = ExecRunner{}
	}
	if timeout <= 0 {
		timeout = defaultBuildTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return run.Run(ctx, dir, argv)
}

// CheckOptions configures Check.
type CheckOptions struct {
	Run     CommandRunner
	Timeout time.Duration
	Clock   Clock
}

// Check validates and briefly starts one extension directory, returning every
// problem it finds. It never installs anything.
func Check(ctx context.Context, dir string, opt CheckOptions) []error {
	m, err := LoadManifest(dir)
	if err != nil {
		return []error{err}
	}
	var (
		mu       sync.Mutex
		problems []error
	)
	add := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		problems = append(problems, err)
	}
	if len(m.Build) > 0 {
		if err := runBuild(ctx, opt.Run, dir, m.Build, opt.Timeout); err != nil {
			add(err)
		}
	}
	if len(m.Run) == 0 {
		return problems
	}
	// The host refuses a malformed tool or a name already taken, and logs the
	// reason as a warning, so every warning is a problem. The built-in names are
	// registered as stand-ins so a clash with one is found.
	reg := tool.NewRegistry()
	for name := range builtinTools {
		_ = reg.Register(tool.New(tool.Info{Name: name}, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
			return tool.Result{}, nil
		}))
	}
	h := NewHost(HostOptions{
		Dirs:     []string{dir},
		Registry: reg,
		Clock:    opt.Clock,
		Env:      os.Environ,
		CWD:      dir,
		Log: func(level, msg string) {
			if level == "warn" {
				add(errors.New(msg))
			}
		},
		Emit: func(ev proto.Event) {
			if ev.Type == proto.EventExtensionError && ev.Error != nil {
				add(errors.New(ev.Error.Message))
			}
		},
	})
	for _, err := range h.Start(ctx) {
		add(err)
	}
	_ = h.Shutdown(ctx)
	mu.Lock()
	defer mu.Unlock()
	return problems
}
