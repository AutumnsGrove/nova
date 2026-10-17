package clipboard

import (
	"context"
	"encoding/hex"
	"errors"
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
