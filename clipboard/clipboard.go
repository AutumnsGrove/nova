// Package clipboard reads an image from the OS clipboard. It is a seam: the
// platform readers sit behind a Reader interface, so a test never touches a
// real clipboard and a host with none (SSH, a container) fails with a message
// instead of hanging.
package clipboard

import (
	"context"
	"errors"
)

// ErrNoImage means the clipboard holds no image.
var ErrNoImage = errors.New("clipboard: no image on the clipboard")

// ErrTooLarge means the clipboard image is bigger than the reader will hold.
var ErrTooLarge = errors.New("clipboard: image is too large")

// ErrUnavailable means this host has no clipboard reader at all.
var ErrUnavailable = errors.New("clipboard: no clipboard available")

// Image is one clipboard image: PNG bytes with a display name. The bytes are
// as the OS tool produced them; media.Normalize is what makes them safe to send.
type Image struct {
	Data     []byte
	MimeType string
	Name     string
}

// Reader reads the current clipboard image.
type Reader interface {
	Read(ctx context.Context) (Image, error)
}

// Detect returns the reader for this host, or one that reports ErrUnavailable.
func Detect() Reader {
	return detect()
}

// Fake is a Reader for tests.
type Fake struct {
	Image Image
	Err   error
	Calls int
}

// Read returns the configured result and counts the call.
func (f *Fake) Read(context.Context) (Image, error) {
	f.Calls++
	if f.Err != nil {
		return Image{}, f.Err
	}
	return f.Image, nil
}

// unavailable is the reader on a host with no clipboard.
type unavailable struct{}

// Read always reports ErrUnavailable.
func (unavailable) Read(context.Context) (Image, error) { return Image{}, ErrUnavailable }
