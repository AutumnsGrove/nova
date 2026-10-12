//go:build !notui

package ui

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/mode"
	"github.com/AutumnsGrove/nova/provider"
)

// requirePTY skips the test unless the PTY smoke tests are enabled, and points
// the log file at a temporary home.
func requirePTY(t *testing.T) {
	t.Helper()
	if os.Getenv("NOVA_PTY") == "" {
		t.Skip("set NOVA_PTY=1 to run the PTY smoke tests")
	}
	t.Setenv("HOME", t.TempDir())
}

// scriptProvider replies with one fixed assistant message.
type scriptProvider struct{ reply string }

func (scriptProvider) Name() string { return "script" }

func (p scriptProvider) Stream(_ context.Context, _ provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error) {
	if onEvent != nil && p.reply != "" {
		onEvent(provider.StreamEvent{Type: provider.StreamTextDelta, Text: p.reply})
	}
	return &provider.Response{
		Message:      message.Assistant(p.reply),
		FinishReason: "stop",
		Usage:        provider.Usage{CompletionTokens: 4, TotalTokens: 4},
	}, nil
}

// modeClock is a wall clock that satisfies mode.Clock.
type modeClock struct{}

func (modeClock) Now() time.Time { return time.Now() }
func (modeClock) Sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// ptyServer builds a real mode.Server over the scripted provider.
func ptyServer(t *testing.T, reply string) *mode.Server {
	t.Helper()
	srv, err := mode.New(mode.Options{
		Config:    &config.Config{Model: "test/model"},
		CWD:       t.TempDir(),
		Home:      t.TempDir(),
		NoSession: true,
		NoTools:   true,
		Provider:  scriptProvider{reply: reply},
		Clock:     modeClock{},
		Version:   "test",
		Mode:      "tui",
	})
	if err != nil {
		t.Fatalf("mode.New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

// ptyReader reads the master continuously into a buffer, so no output is lost
// between assertions.
type ptyReader struct {
	mu  sync.Mutex
	buf strings.Builder
}

func newPTYReader(master *os.File) *ptyReader {
	r := &ptyReader{}
	go func() {
		b := make([]byte, 8192)
		for {
			n, err := master.Read(b)
			if n > 0 {
				r.mu.Lock()
				r.buf.Write(b[:n])
				r.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	return r
}

// waitFor blocks until want appears (ANSI stripped) or d elapses.
func (r *ptyReader) waitFor(want string, d time.Duration) (string, bool) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		s := r.buf.String()
		r.mu.Unlock()
		if strings.Contains(ansi.Strip(s), want) {
			return s, true
		}
		time.Sleep(20 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String(), false
}

// startTUI runs runWith over a pty and returns its reader and error channel.
func startTUI(t *testing.T, srv *mode.Server) (*ptyReader, *os.File, chan error) {
	t.Helper()
	master, slave := openPTY(t)
	t.Cleanup(func() { master.Close() })
	reader := newPTYReader(master)
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	t.Cleanup(cancel)
	go func() { done <- runWith(ctx, srv, RunOptions{Version: "test"}, slave, slave) }()
	return reader, master, done
}

// quit sends ctrl+c twice, which the model treats as quit.
func quit(master *os.File) {
	master.Write([]byte{3})
	time.Sleep(150 * time.Millisecond)
	master.Write([]byte{3})
}

// waitQuit fails the test if the UI does not exit.
func waitQuit(t *testing.T, done chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runWith: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the TUI did not quit")
	}
}

// TestPTY_PTY01_StartsAndQuits starts the real TUI on a pseudo-terminal and
// quits it with ctrl+c twice.
func TestPTY_PTY01_StartsAndQuits(t *testing.T) {
	requirePTY(t)
	reader, master, done := startTUI(t, ptyServer(t, ""))
	if _, ok := reader.waitFor("nova", 6*time.Second); !ok {
		t.Fatal("the TUI never rendered")
	}
	quit(master)
	waitQuit(t, done)
}

// TestPTY_PTY02_PromptRoundTrip types a prompt and expects the reply on screen.
func TestPTY_PTY02_PromptRoundTrip(t *testing.T) {
	requirePTY(t)
	reader, master, done := startTUI(t, ptyServer(t, "hello from nova"))
	if _, ok := reader.waitFor("nova", 6*time.Second); !ok {
		t.Fatal("the TUI never rendered")
	}
	master.Write([]byte("hi\r"))
	if out, ok := reader.waitFor("hello from nova", 15*time.Second); !ok {
		t.Fatalf("reply not shown; screen was:\n%s", ansi.Strip(out))
	}
	quit(master)
	waitQuit(t, done)
}
