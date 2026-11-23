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

// ScrubEnvPatterns returns environ without the variables a pattern names. A
// pattern matches a variable called exactly that, or — when it starts with an
// underscore — any name that ends with it, so `_API_KEY` hides every provider
// key an extension or a server has no business seeing. A name in pass is kept
// even when a pattern matches: an explicit pass wins over a broad pattern, which
// is the rule the extension host and the MCP client share.
func ScrubEnvPatterns(environ []string, patterns []string, pass []string) []string {
	return FilterEnv(environ, func(name string) bool {
		if slices.Contains(pass, name) {
			return false
		}
		return slices.ContainsFunc(patterns, func(p string) bool {
			return name == p || (strings.HasPrefix(p, "_") && strings.HasSuffix(name, p))
		})
	})
}
