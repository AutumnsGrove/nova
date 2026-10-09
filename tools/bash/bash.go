// Package bash provides the bash execution tool.
package bash

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/AutumnsGrove/nova/tool"
)

const (
	defaultTimeout = 120 * time.Second
	maxOutputBytes = 100 * 1024
)

// Option customizes the bash tool.
type Option func(*options)

type options struct {
	timeout        time.Duration
	maxOutputBytes int
	shell          string
}

// WithTimeout sets the default per-command timeout.
func WithTimeout(d time.Duration) Option {
	return func(o *options) { o.timeout = d }
}

// WithMaxOutputBytes caps captured output.
func WithMaxOutputBytes(n int) Option {
	return func(o *options) { o.maxOutputBytes = n }
}

// WithShell overrides the shell binary (default "bash", invoked as `bash -lc`).
func WithShell(path string) Option {
	return func(o *options) { o.shell = path }
}

// Bash returns the bash tool. It runs commands with `bash -lc` in the tool
// context's working directory and returns combined stdout/stderr.
func Bash(opts ...Option) tool.Tool {
	o := options{timeout: defaultTimeout, maxOutputBytes: maxOutputBytes, shell: "bash"}
	for _, opt := range opts {
		opt(&o)
	}

	return tool.New(tool.Info{
		Name:        "bash",
		Description: "Execute a bash command in the working directory. Returns combined stdout/stderr and the exit code. Use a timeout (seconds) to bound long commands.",
		Destructive: true,
		OpenWorld:   true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "description": "Bash command to execute"},
				"timeout": map[string]any{"type": "integer", "description": "Timeout in seconds (default 120)"},
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

		timeout := o.timeout
		if in.Timeout > 0 {
			timeout = time.Duration(in.Timeout) * time.Second
		}
		cctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		cmd := exec.CommandContext(cctx, o.shell, "-lc", in.Command)
		if tctx != nil && tctx.WorkDir != "" {
			cmd.Dir = tctx.WorkDir
		}

		out, err := cmd.CombinedOutput()
		text := truncate(string(out), o.maxOutputBytes)

		if cctx.Err() == context.DeadlineExceeded {
			return tool.Result{
				Content: fmt.Sprintf("%s\n(no output)\nerror: command timed out after %s", text, timeout),
				IsError: true,
			}, nil
		}
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				if text == "" {
					text = "(no output)"
				}
				return tool.Result{
					Content: fmt.Sprintf("%s\n(exit code %d)", text, exitErr.ExitCode()),
					IsError: true,
				}, nil
			}
			return tool.Result{}, fmt.Errorf("exec: %w", err)
		}
		if text == "" {
			text = "(no output)"
		}
		return tool.Result{Content: text}, nil
	})
}

func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("\n... (output truncated at %d bytes)", max)
}
