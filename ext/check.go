package ext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tool"
)

// CommandRunner runs a build step.
type CommandRunner interface {
	Run(ctx context.Context, dir string, argv []string) error
}

// ExecRunner runs a build step directly, with the parent's environment.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, dir string, argv []string) error {
	if len(argv) == 0 {
		return nil
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ext: build failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
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
	var problems []error
	m, err := LoadManifest(dir)
	if err != nil {
		return []error{err}
	}
	if len(m.Build) > 0 {
		run := opt.Run
		if run == nil {
			run = ExecRunner{}
		}
		if opt.Timeout <= 0 {
			opt.Timeout = 5 * time.Minute
		}
		bctx, cancel := context.WithTimeout(ctx, opt.Timeout)
		if err := run.Run(bctx, dir, m.Build); err != nil {
			problems = append(problems, err)
		}
		cancel()
	}
	if len(m.Run) == 0 {
		return problems
	}
	reg := tool.NewRegistry()
	for name := range builtinTools {
		_ = reg.Register(tool.New(tool.Info{Name: name}, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
			return tool.Result{}, nil
		}))
	}
	clock := opt.Clock
	if clock == nil {
		clock = realClock{}
	}
	h := NewHost(HostOptions{
		Dirs:     []string{dir},
		Registry: reg,
		Clock:    clock,
		Env:      os.Environ,
		CWD:      dir,
		Log: func(level, msg string) {
			if level == "warn" {
				problems = append(problems, errors.New(msg))
			}
		},
		Emit: func(ev proto.Event) {
			if ev.Type == proto.EventExtensionError && ev.Error != nil {
				problems = append(problems, errors.New(ev.Error.Message))
			}
		},
	})
	problems = append(problems, h.Start(ctx)...)
	for _, name := range reg.Names() {
		if builtinTools[name] {
			continue
		}
		info, ok := reg.Info(name)
		if ok && info.Parameters != nil && info.Parameters["type"] != "object" {
			problems = append(problems, fmt.Errorf("ext: tool %q has an invalid schema", name))
		}
	}
	_ = h.Shutdown(ctx)
	return problems
}
