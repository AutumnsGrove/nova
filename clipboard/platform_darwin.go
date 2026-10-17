//go:build darwin

package clipboard

import "os/exec"

// detect uses osascript: asking the pasteboard for `«class PNGf»` makes macOS
// convert a TIFF screenshot to PNG on request.
func detect() Reader {
	if _, err := exec.LookPath("osascript"); err != nil {
		return unavailable{}
	}
	return commandReader{
		name:  "osascript",
		args:  []string{"-e", "the clipboard as «class PNGf»"},
		parse: parseAppleScriptData,
	}
}
