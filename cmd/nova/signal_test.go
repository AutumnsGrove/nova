//go:build unix

package main

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRoot_SecondSignalEndsABlockedCommand pins the escape hatch: the first
// SIGINT asks a command to stop, and a command that cannot watch its context
// needs a second one to be killed. The `continue? [y/N]` prompt in `ext install`
// is such a command — it reads stdin and nothing else — so before the fix the
// signal handler stayed registered and every later signal was dropped, leaving
// the prompt escapable only by answering it (#126).
func TestRoot_SecondSignalEndsABlockedCommand(t *testing.T) {
	bin := builtNova(t)
	src, err := filepath.Abs("../../examples/extensions/hello-go")
	if err != nil {
		t.Fatalf("examples path: %v", err)
	}
	cmd := exec.Command(bin, "ext", "install", src)
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "OPENROUTER_API_KEY=", "NO_COLOR=1")
	// stdin stays open and unanswered, so the prompt blocks. stdout is read to
	// know the prompt is on screen: that is also proof the signal handler has
	// been installed, since the whole command tree runs behind it.
	stdin, keepOpen, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer keepOpen.Close()
	cmd.Stdin = stdin
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start ext install: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	prompt := make(chan string, 1)
	go func() {
		reader := bufio.NewReader(out)
		var seen strings.Builder
		for {
			line, err := reader.ReadString(' ')
			seen.WriteString(line)
			if strings.Contains(seen.String(), "continue?") {
				prompt <- seen.String()
				return
			}
			if err != nil {
				prompt <- seen.String()
				return
			}
		}
	}()
	select {
	case seen := <-prompt:
		if !strings.Contains(seen, "continue?") {
			t.Fatalf("ext install never reached its prompt: %q", seen)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("ext install printed no prompt")
	}

	// The first signal is swallowed on purpose: it cancels the context, and the
	// prompt is not watching it. Every later signal has to end the process, so
	// this polls for the exit rather than guessing how long a signal takes.
	for attempt := 1; attempt <= 5; attempt++ {
		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			t.Fatalf("signal %d: %v", attempt, err)
		}
		select {
		case err := <-exited:
			if err == nil {
				t.Fatalf("exit %d: ext install exited 0 at its prompt", attempt)
			}
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
	t.Fatal("the prompt ignored five SIGINTs; the second one has to kill it")
}
