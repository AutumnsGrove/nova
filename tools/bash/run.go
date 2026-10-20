package bash

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/AutumnsGrove/nova/proc"
	"github.com/AutumnsGrove/nova/tool"
)

const (
	termGraceDefault = 2 * time.Second
	maxProgressBytes = 4 * 1024
	progressInterval = 100 * time.Millisecond
)

// bashDetails is the structured data the bash tool attaches to Result.Details.
type bashDetails struct {
	ExitCode   *int    `json:"exit_code"`
	Signal     *string `json:"signal"`
	DurationMS int64   `json:"duration_ms"`
	TimedOut   bool    `json:"timed_out"`
	Cancelled  bool    `json:"cancelled"`
	Truncated  bool    `json:"truncated"`
}

// execution is the raw outcome of one command before it is shaped.
type execution struct {
	exitCode   int
	signal     int
	timedOut   bool
	cancelled  bool
	background bool
	duration   time.Duration
}

// execute runs one command and turns it into a model-facing result. Every
// failure of the command itself is a result, never a Go error.
func (o *options) execute(ctx context.Context, tctx *tool.Context, command string, seconds int) tool.Result {
	if !supported() {
		return tool.Errorf("bash is not supported on this platform")
	}
	work := ""
	if tctx != nil {
		work = tctx.WorkDir
	}
	if work != "" {
		if info, err := os.Stat(work); err != nil || !info.IsDir() {
			return tool.Errorf("working directory %s does not exist", work)
		}
	}
	timeout := o.callTimeout(seconds)
	if ctx.Err() != nil {
		return shapeResult(execution{cancelled: true}, tool.NewTailBuffer(o.maxOutputBytes), timeout)
	}

	flag := "-c"
	if o.login {
		flag = "-lc"
	}
	// A nil Stdin makes exec read from the null device, so the command can never
	// wait on input the model cannot give.
	cmd := exec.Command(o.shell, flag, command)
	cmd.Dir = work
	cmd.Env = o.buildEnv()
	proc.SetGroup(cmd)
	pr, pw, err := os.Pipe()
	if err != nil {
		return tool.Errorf("pipe: %v", err)
	}
	cmd.Stdout, cmd.Stderr = pw, pw

	began := o.clock.Now()
	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		_ = pr.Close()
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return tool.Errorf("shell not found: %s", o.shell)
		}
		return tool.Errorf("%v", err)
	}
	// Only the child holds the write end now, so the read end reaches EOF when
	// the last process in the group lets go of it.
	_ = pw.Close()

	sink := &capture{buf: tool.NewTailBuffer(o.maxOutputBytes), clock: o.clock, tctx: tctx}
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		defer pr.Close()
		// The copy ends at EOF or when the group is killed; either way what
		// was captured is the result, so its error carries nothing more.
		_, _ = io.Copy(sink, pr)
	}()

	out := o.supervise(ctx, cmd, readDone, timeout)
	out.duration = o.clock.Now().Sub(began)
	return shapeResult(out, sink.buf, timeout)
}

// callTimeout is the time one call may run: the requested seconds when positive,
// else the default, never above the maximum. The seconds are clamped before they
// become a Duration, because a huge request would overflow int64 nanoseconds and
// wrap to a negative timeout that expires at once.
func (o *options) callTimeout(seconds int) time.Duration {
	timeout := o.timeout
	if seconds > 0 {
		timeout = time.Duration(min(int64(seconds), math.MaxInt64/int64(time.Second))) * time.Second
	}
	if o.maxTimeout > 0 {
		timeout = min(timeout, o.maxTimeout)
	}
	return timeout
}

// supervise waits for the started command, its output pipe, the timeout, or
// cancellation, then stops the whole process group. It always force-kills the
// group at the end, even after a clean exit, so a background child that
// redirected its output cannot outlive the call (D51).
func (o *options) supervise(ctx context.Context, cmd *exec.Cmd, readDone <-chan struct{}, timeout time.Duration) (out execution) {
	pid := cmd.Process.Pid
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case err := <-waitDone:
		out.exitCode, out.signal = proc.ExitStatus(err)
		if !waitClosed(readDone, o.pipeGrace) {
			out.background = true
			proc.Terminate(pid)
			waitClosed(readDone, termGraceDefault)
		}
	case <-callCtx.Done():
		out.cancelled = ctx.Err() != nil
		out.timedOut = !out.cancelled
		proc.Terminate(pid)
		select {
		case <-waitDone:
		case <-time.After(termGraceDefault):
			proc.Kill(pid)
			<-waitDone
		}
		waitClosed(readDone, termGraceDefault)
	}
	proc.Kill(pid)
	waitClosed(readDone, termGraceDefault)
	return out
}

// capture writes the merged pipe into the capped buffer and emits throttled
// progress updates. It is the io.Writer io.Copy reads into.
type capture struct {
	buf   *tool.TailBuffer
	clock Clock
	tctx  *tool.Context
	last  time.Time
}

func (c *capture) Write(p []byte) (int, error) {
	_, _ = c.buf.Write(p)
	// Progress is nil-safe, so a call with no listener costs only the clock read.
	now := c.clock.Now()
	if c.last.IsZero() || now.Sub(c.last) >= progressInterval {
		c.last = now
		tail := p[max(len(p)-maxProgressBytes, 0):]
		c.tctx.Progress(strings.ToValidUTF8(string(tail), "\uFFFD"))
	}
	return len(p), nil
}

// waitClosed reports whether done closes within d.
func waitClosed(done <-chan struct{}, d time.Duration) bool {
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// shapeResult turns the raw outcome and captured output into a tool result.
func shapeResult(out execution, buf *tool.TailBuffer, timeout time.Duration) tool.Result {
	content := sanitizeOutput(buf.Bytes())
	add := func(s string) {
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += s
	}
	label := ""
	switch {
	case out.timedOut:
		label = fmt.Sprintf("error: command timed out after %s", timeout)
	case out.cancelled:
		label = "error: command cancelled"
	case out.signal != 0:
		label = fmt.Sprintf("(terminated by signal %s)", signalName(out.signal))
	case out.exitCode != 0:
		label = fmt.Sprintf("(exit code %d)", out.exitCode)
	}
	if label != "" {
		add(label)
	}
	if out.background {
		add("(background processes were still running and were stopped)")
	}
	if content == "" {
		content = "(no output)"
	}
	details := bashDetails{DurationMS: out.duration.Milliseconds(), TimedOut: out.timedOut, Cancelled: out.cancelled, Truncated: buf.Truncated()}
	if out.signal != 0 {
		name := signalName(out.signal)
		details.Signal = &name
	} else if !out.timedOut && !out.cancelled {
		details.ExitCode = &out.exitCode
	}
	raw, _ := json.Marshal(details)
	return tool.Result{Content: content, IsError: label != "", Truncated: buf.Truncated(), Details: raw}
}
