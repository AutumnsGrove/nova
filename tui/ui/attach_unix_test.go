//go:build !notui && unix

package ui

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// fifoGuard bounds how long a test waits for code that should never have
// opened a named pipe; a reader that did open it blocks until a writer comes.
const fifoGuard = 5 * time.Second

func TestUI_PST05_PathAttachSkipsNonRegularFiles(t *testing.T) {
	m, f, _ := newModel(t)
	fifo := filepath.Join(m.cwd, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("no named pipes here: %v", err)
	}
	m.setEditorText("describe " + fifo)
	done := make(chan struct{})
	go func() {
		defer close(done)
		execAll(update(m, enterKey))
	}()
	select {
	case <-done:
	case <-time.After(fifoGuard):
		// Release the blocked reader so the goroutine does not outlive the test.
		w, err := os.OpenFile(fifo, os.O_WRONLY, 0)
		if err == nil {
			_ = w.Close()
		}
		<-done
		t.Fatal("submitting a prompt that names a pipe blocked reading it")
	}
	if len(f.sends) != 1 || len(f.sends[0].Attachments) != 0 {
		t.Fatalf("sends = %+v, want one prompt with no attachment", f.sends)
	}
}
