package providerauth

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/testkit"
)

// fakeOpener puts an xdg-open on PATH that writes its first argument to a named
// pipe, so a test waits for the call by reading, not by polling.
func fakeOpener(t *testing.T) (fifo string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the opener is xdg-open only on linux")
	}
	dir := t.TempDir()
	fifo = filepath.Join(dir, "args")
	testkit.Mkfifo(t, fifo)
	script := "#!/bin/sh\nprintf '%s' \"$1\" > '" + fifo + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "xdg-open"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return fifo
}

// The URL can come from a server's reply. Anything but an http(s) URL would be
// read by the opener as an option or a local path, so none of it is passed on.
func TestOpenBrowser_RefusesWhatIsNotAWebURL(t *testing.T) {
	fakeOpener(t)
	for _, bad := range []string{"", "-h", "--version", "file:///etc/passwd", "javascript:alert(1)", "ftp://x/y", "https://", "not a url", "//host/path"} {
		if err := OpenBrowser(bad); err == nil {
			t.Errorf("OpenBrowser(%q) = nil, want a refusal", bad)
		}
	}
}

func TestOpenBrowser_OpensAWebURL(t *testing.T) {
	fifo := fakeOpener(t)
	const url = "https://openrouter.ai/auth?callback_url=http%3A%2F%2Flocalhost%3A3000&code_challenge=abc"
	got := make(chan string, 1)
	go func() {
		data, _ := os.ReadFile(fifo) // blocks until the opener writes
		got <- string(data)
	}()
	if err := OpenBrowser(url); err != nil {
		t.Fatalf("OpenBrowser: %v", err)
	}
	select {
	case arg := <-got:
		if arg != url {
			t.Fatalf("the opener was given %q, want %q", arg, url)
		}
	case <-time.After(10 * time.Second): // a failure watchdog, not a wait
		t.Fatal("the opener never ran")
	}
}
