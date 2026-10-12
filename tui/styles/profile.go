//go:build !notui

package styles

import (
	"io"

	"github.com/charmbracelet/colorprofile"
)

// DetectProfile chooses a colour profile from the environment and the output
// stream. NO_COLOR set to any non-empty value, or an output that is not a
// terminal, means no colour. Otherwise it detects truecolor, 256, or 16.
func DetectProfile(env func(string) string, out io.Writer) colorprofile.Profile {
	if env("NO_COLOR") != "" {
		return colorprofile.ASCII
	}
	var environ []string
	for _, k := range []string{"TERM", "COLORTERM", "CLICOLOR", "CLICOLOR_FORCE"} {
		if v := env(k); v != "" {
			environ = append(environ, k+"="+v)
		}
	}
	switch p := colorprofile.Detect(out, environ); p {
	case colorprofile.TrueColor, colorprofile.ANSI256, colorprofile.ANSI:
		return p
	default:
		return colorprofile.ASCII
	}
}
