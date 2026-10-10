package permission

import "strings"

// HasShellControlOperator reports whether s contains a shell control operator
// that could chain or substitute another command: ; && || | & ` $( <( > < and
// a newline or carriage return. A prefix-matching allow rule must not match a
// command containing one, or `git status*` would allow `git status; rm -rf /`.
func HasShellControlOperator(s string) bool {
	return strings.ContainsAny(s, ";&|`><\n\r") || strings.Contains(s, "$(")
}
