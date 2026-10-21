// Package bash provides the shell tool: one command, one process group, capped
// output, and a result the model can read.
package bash

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"time"

	"github.com/AutumnsGrove/nova/tool"
)

const (
	defaultTimeout        = 120 * time.Second
	defaultMaxTimeout     = 600 * time.Second
	defaultMaxOutputBytes = 51200
	defaultPipeGrace      = 200 * time.Millisecond
)

// Clock is the time seam for progress throttling. It is only used to decide
// when an update is due; timeouts use the request context.
type Clock interface{ Now() time.Time }

// realClock is the system clock.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// options is the resolved configuration of one bash tool.
type options struct {
	timeout        time.Duration
	maxTimeout     time.Duration
	maxOutputBytes int
	shell          string
	login          bool
	clock          Clock
	env            map[string]string
	scrub          []string
	pipeGrace      time.Duration
}

// Option customizes the bash tool.
type Option func(*options)

// WithTimeout sets the default per-command timeout.
func WithTimeout(d time.Duration) Option { return func(o *options) { o.timeout = d } }

// WithMaxTimeout caps a timeout requested in the arguments.
func WithMaxTimeout(d time.Duration) Option { return func(o *options) { o.maxTimeout = d } }

// WithMaxOutputBytes caps captured output.
func WithMaxOutputBytes(n int) Option { return func(o *options) { o.maxOutputBytes = n } }

// WithShell overrides the shell binary (default "bash", run as `bash -c`).
func WithShell(path string) Option { return func(o *options) { o.shell = path } }

// WithLoginShell runs the shell as a login shell (`-lc`).
func WithLoginShell(on bool) Option { return func(o *options) { o.login = on } }

// WithClock injects the clock used for progress throttling.
func WithClock(c Clock) Option { return func(o *options) { o.clock = c } }

// WithEnv adds or overrides environment variables for the command.
func WithEnv(extra map[string]string) Option { return func(o *options) { o.env = extra } }

// WithScrubEnv removes the named variables from the environment the command
// inherits, so a model-run command cannot read Nova's own credentials. An entry
// given to WithEnv is not removed: it was asked for.
func WithScrubEnv(names []string) Option { return func(o *options) { o.scrub = names } }

// WithPipeGrace sets how long to wait for the output pipe to close after the
// shell exits before treating a holder as a stray background process.
func WithPipeGrace(d time.Duration) Option { return func(o *options) { o.pipeGrace = d } }

// Bash returns the bash tool.
func Bash(opts ...Option) tool.Tool {
	o := &options{
		timeout:        defaultTimeout,
		maxTimeout:     defaultMaxTimeout,
		maxOutputBytes: defaultMaxOutputBytes,
		shell:          "bash",
		clock:          realClock{},
		pipeGrace:      defaultPipeGrace,
	}
	for _, opt := range opts {
		opt(o)
	}
	return tool.New(tool.Info{
		Name:        "bash",
		Description: "Run a bash command in the working directory. Returns combined stdout/stderr, the exit code, and a timeout in seconds (default 120).",
		Destructive: true,
		OpenWorld:   true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "description": "Bash command to execute"},
				"timeout": map[string]any{"type": "integer", "description": "Timeout in seconds (default 120, capped)"},
			},
			"required": []string{"command"},
		},
	}, func(ctx context.Context, args json.RawMessage, tctx *tool.Context) (tool.Result, error) {
		var in struct {
			Command string `json:"command"`
			Timeout int    `json:"timeout"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return tool.Result{}, fmt.Errorf("invalid arguments: %w", err)
		}
		if in.Command == "" {
			return tool.Errorf("command is required"), nil
		}
		return o.execute(ctx, tctx, in.Command, in.Timeout), nil
	})
}

// buildEnv is the parent environment minus the scrubbed variables, plus the
// fixed variables, plus the configured extras, in key order so the environment
// is the same on every call. exec uses the last value for a duplicate key, so a
// later entry overrides an earlier one (D52).
func (o *options) buildEnv() []string {
	env := append(tool.ScrubEnv(os.Environ(), o.scrub),
		"NOVA=1", "TERM=dumb", "NO_COLOR=1",
		"PAGER=cat", "GIT_PAGER=cat", "GIT_TERMINAL_PROMPT=0",
	)
	for _, k := range slices.Sorted(maps.Keys(o.env)) {
		env = append(env, k+"="+o.env[k])
	}
	return env
}
