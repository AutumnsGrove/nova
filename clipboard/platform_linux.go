//go:build linux

package clipboard

import "os/exec"

// detect prefers Wayland's wl-paste, then X11's xclip.
func detect() Reader {
	if p, err := exec.LookPath("wl-paste"); err == nil {
		return commandReader{name: p, args: []string{"--type", "image/png", "--no-newline"}, parse: parseRawPNG}
	}
	if p, err := exec.LookPath("xclip"); err == nil {
		return commandReader{name: p, args: []string{"-selection", "clipboard", "-t", "image/png", "-o"}, parse: parseRawPNG}
	}
	return unavailable{}
}
