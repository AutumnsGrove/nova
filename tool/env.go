package tool

import (
	"slices"
	"strings"
)

// FilterEnv returns environ without the entries whose variable name drop
// reports true for. environ is in os.Environ form ("NAME=value"), and the result
// is a new slice.
func FilterEnv(environ []string, drop func(name string) bool) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if !drop(name) {
			out = append(out, kv)
		}
	}
	return out
}

// ScrubEnv returns environ without the variables called exactly one of names.
func ScrubEnv(environ []string, names []string) []string {
	return FilterEnv(environ, func(name string) bool { return slices.Contains(names, name) })
}
