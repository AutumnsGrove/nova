package clipboard

import (
	"context"
	"encoding/hex"
	"errors"
	"os/exec"
	"testing"
	"time"
)

func TestClipboard_CLB01_ParseAppleScriptData(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	out := []byte("«data PNGf" + hex.EncodeToString(png) + "»\n")
	got, err := parseAppleScriptData(out)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if string(got) != string(png) {
		t.Fatalf("got %x, want %x", got, png)
	}
	if _, err := parseAppleScriptData([]byte("not a pasteboard")); !errors.Is(err, ErrNoImage) {
		t.Fatalf("junk err = %v, want ErrNoImage", err)
	}
}

func TestClipboard_CLB02_CommandReaderErrors(t *testing.T) {
	// A missing binary is unavailable.
	if _, err := (commandReader{name: "nova-no-such-clipboard-tool", parse: parseRawPNG}).Read(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing binary err = %v, want ErrUnavailable", err)
	}
	// A command that prints nothing is no image.
	if _, err := (commandReader{name: "true", parse: parseRawPNG}).Read(context.Background()); !errors.Is(err, ErrNoImage) {
		t.Fatalf("empty output err = %v, want ErrNoImage", err)
	}
}

func TestClipboard_CLB03_Fake(t *testing.T) {
	f := &Fake{Image: Image{Data: []byte("png"), MimeType: "image/png"}}
	got, err := f.Read(context.Background())
	if err != nil || string(got.Data) != "png" {
		t.Fatalf("Read = %+v, %v", got, err)
	}
	if f.Calls != 1 {
		t.Fatalf("calls = %d, want 1", f.Calls)
	}
	if _, err := (&Fake{Err: ErrNoImage}).Read(context.Background()); !errors.Is(err, ErrNoImage) {
		t.Fatalf("fake err = %v, want ErrNoImage", err)
	}
}

func TestClipboard_CLB04_ReadTimesOut(t *testing.T) {
	old := readTimeout
	readTimeout = 50 * time.Millisecond
	defer func() { readTimeout = old }()
	// sleep runs longer than the timeout and would hang the TUI without it.
	if _, err := (commandReader{name: "sleep", args: []string{"5"}, parse: parseRawPNG}).Read(context.Background()); !errors.Is(err, ErrNoImage) {
		t.Fatalf("timeout err = %v, want ErrNoImage", err)
	}
}

func skipWithout(t *testing.T, tool string) {
	t.Helper()
	if _, err := exec.LookPath(tool); err != nil {
		t.Skipf("%s is not installed", tool)
	}
}

// A clipboard can hold an image of any size. The reader stops at a cap and says
// so, instead of buffering whatever a tool prints.
func TestClipboard_CLB05_OutputCapped(t *testing.T) {
	skipWithout(t, "head")
	old := maxOutput
	maxOutput = 100
	defer func() { maxOutput = old }()
	r := commandReader{name: "head", args: []string{"-c", "1000", "/dev/zero"}, parse: parseRawPNG}
	if _, err := r.Read(context.Background()); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	r.args = []string{"-c", "100", "/dev/zero"}
	if img, err := r.Read(context.Background()); err != nil || len(img.Data) != 100 {
		t.Fatalf("at the cap: %d bytes, err %v, want 100 bytes", len(img.Data), err)
	}
}

// A caller that gave up gets its own error, not "no image".
func TestClipboard_CLB06_CallerCancel(t *testing.T) {
	skipWithout(t, "sleep")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (commandReader{name: "sleep", args: []string{"5"}, parse: parseRawPNG}).Read(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// A tool that forks a helper keeps the output pipe open after it is killed.
// The read must still return at the timeout.
func TestClipboard_CLB07_HelperHoldingPipe(t *testing.T) {
	skipWithout(t, "sh")
	old := readTimeout
	readTimeout = 50 * time.Millisecond
	defer func() { readTimeout = old }()
	start := time.Now()
	r := commandReader{name: "sh", args: []string{"-c", "sleep 3 & sleep 3"}, parse: parseRawPNG}
	if _, err := r.Read(context.Background()); !errors.Is(err, ErrNoImage) {
		t.Fatalf("err = %v, want ErrNoImage", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Read took %v, want it bounded by the timeout", elapsed)
	}
}
