package bash

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/AutumnsGrove/nova/tool"
)

const (
	termGraceDefault = 2 * time.Second
	maxProgressBytes = 4 * 1024
	progressInterval = 100 * time.Millisecond
)

// bashDetails is the structured data the bash tool attaches to Result.Details.
type bashDetails struct {
	ExitCode   any   `json:"exit_code"`
	Signal     any   `json:"signal"`
	DurationMS int64 `json:"duration_ms"`
	TimedOut   bool  `json:"timed_out"`
	Cancelled  bool  `json:"cancelled"`
	Truncated  bool  `json:"truncated"`
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
	timeout := o.timeout
	if seconds > 0 {
		timeout = time.Duration(seconds) * time.Second
	}
	if o.maxTimeout > 0 && timeout > o.maxTimeout {
		timeout = o.maxTimeout
	}
	if ctx != nil && ctx.Err() != nil {
		return shapeResult(execution{cancelled: true}, newTailBuffer(o.maxOutputBytes), timeout)
	}

	flag := "-c"
	if o.login {
		flag = "-lc"
	}
	cmd := exec.Command(o.shell, flag, command)
	cmd.Dir = work
	cmd.Env = buildEnv(o.env)
	setProcessGroup(cmd)
	if devnull, err := os.Open(os.DevNull); err == nil {
		cmd.Stdin = devnull
		defer devnull.Close()
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return tool.Errorf("pipe: %v", err)
	}
	cmd.Stdout, cmd.Stderr = pw, pw

	began := o.clock.Now()
	if err := cmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return tool.Errorf("shell not found: %s", o.shell)
		}
		return tool.Errorf("%v", err)
	}
	pw.Close()
	pid := cmd.Process.Pid

	sink := &capture{buf: newTailBuffer(o.maxOutputBytes), clock: o.clock, tctx: tctx}
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		defer pr.Close()
		io.Copy(sink, pr)
	}()
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()

	var out execution
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case err := <-waitDone:
		out.exitCode, out.signal = exitStatus(err)
		if !waitClosed(readDone, o.pipeGrace) {
			out.background = true
			terminateGroup(pid)
			waitClosed(readDone, termGraceDefault)
		}
	case <-callCtx.Done():
		out.cancelled = ctx.Err() != nil
		out.timedOut = !out.cancelled
		terminateGroup(pid)
		select {
		case <-waitDone:
		case <-time.After(termGraceDefault):
			forceKillGroup(pid)
			<-waitDone
		}
		waitClosed(readDone, termGraceDefault)
	}
	forceKillGroup(pid)
	waitClosed(readDone, termGraceDefault)
	out.duration = o.clock.Now().Sub(began)
	return shapeResult(out, sink.buf, timeout)
}

// capture writes the merged pipe into the capped buffer and emits throttled
// progress updates. It is the io.Writer io.Copy reads into.
type capture struct {
	buf   *tailBuffer
	clock Clock
	tctx  *tool.Context
	last  time.Time
}

func (c *capture) Write(p []byte) (int, error) {
	c.buf.write(p)
	if c.tctx == nil {
		return len(p), nil
	}
	now := c.clock.Now()
	if !c.last.IsZero() && now.Sub(c.last) < progressInterval {
		return len(p), nil
	}
	c.last = now
	text := p
	if len(text) > maxProgressBytes {
		text = text[len(text)-maxProgressBytes:]
	}
	c.tctx.Progress(string(text))
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
func shapeResult(out execution, buf *tailBuffer, timeout time.Duration) tool.Result {
	content := sanitizeOutput(buf.bytes())
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
	details := bashDetails{DurationMS: out.duration.Milliseconds(), TimedOut: out.timedOut, Cancelled: out.cancelled, Truncated: buf.truncated()}
	if out.signal != 0 {
		details.Signal = signalName(out.signal)
	} else if !out.timedOut && !out.cancelled {
		details.ExitCode = out.exitCode
	}
	raw, _ := json.Marshal(details)
	return tool.Result{Content: content, IsError: label != "", Truncated: buf.truncated(), Details: raw}
}
