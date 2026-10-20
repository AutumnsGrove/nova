// Package permission decides what happens to a tool call: it runs, it asks the
// user, or it is denied. The decision is declarative, so a policy is data a
// config file can hold and a test can assert.
package permission

import (
	"path/filepath"
	"regexp"
	"strings"
)

// MatchPath reports whether a path matches a path glob.
//
// `**` matches zero or more path segments; `*` and `?` never match `/`; `[abc]`,
// `[a-z]`, and their negations work; and a trailing `/` matches the directory
// and everything under it. Matching is case-sensitive and uses `/` separators.
// An unbalanced `[` makes the pattern match nothing.
func MatchPath(pattern, path string) bool {
	path = filepath.ToSlash(path)
	if strings.HasSuffix(pattern, "/") {
		base := strings.TrimSuffix(pattern, "/")
		if path == base {
			return true
		}
		pattern = base + "/**"
	}
	re, ok := compileGlob(pattern, true)
	if !ok {
		return false
	}
	return re.MatchString(path)
}

// MatchString reports whether a whole string matches a string glob.
//
// `*` matches any run of characters including empty and including `/`; `?`
// matches exactly one character; `[abc]` and negations work. Everything else is
// literal. Matching is case-sensitive and anchored at both ends.
func MatchString(pattern, s string) bool {
	re, ok := compileGlob(pattern, false)
	if !ok {
		return false
	}
	return re.MatchString(s)
}

// ValidPath reports whether pattern is a glob that MatchPath and MatchString
// can use: every character class is closed.
func ValidPath(pattern string) bool {
	_, ok := compileGlob(pattern, true)
	return ok
}

// compileGlob translates a glob into an anchored regular expression. The
// translation runs on bytes so multibyte literals pass through unchanged.
// It returns false for an unbalanced character class.
func compileGlob(pattern string, pathMode bool) (*regexp.Regexp, bool) {
	var out strings.Builder
	// The s flag lets `.` match a newline. Without it a deny pattern such as
	// `*rm -rf*` would stop matching once a command spans two lines, which is
	// exactly how a command would slip past it.
	out.WriteString(`(?s)^`)
	for i := 0; i < len(pattern); {
		switch c := pattern[i]; c {
		case '*':
			j := i
			for j < len(pattern) && pattern[j] == '*' {
				j++
			}
			switch {
			case j-i >= 2 && j < len(pattern) && pattern[j] == '/':
				// `**/` matches zero or more whole segments.
				out.WriteString(`(?:.*/)?`)
				i = j + 1
			case j-i >= 2:
				out.WriteString(`.*`)
				i = j
			case pathMode:
				out.WriteString(`[^/]*`)
				i = j
			default:
				out.WriteString(`.*`)
				i = j
			}
		case '?':
			if pathMode {
				out.WriteString(`[^/]`)
			} else {
				out.WriteByte('.')
			}
			i++
		case '[':
			i++
			out.WriteByte('[')
			if i < len(pattern) && (pattern[i] == '!' || pattern[i] == '^') {
				out.WriteByte('^')
				if pathMode {
					// A path glob never spans a separator except through `**`.
					out.WriteByte('/')
				}
				i++
			}
			if i < len(pattern) && pattern[i] == ']' {
				out.WriteString(`\]`)
				i++
			}
			for i < len(pattern) && pattern[i] != ']' {
				if pattern[i] == '\\' {
					out.WriteString(`\\`)
				} else {
					out.WriteByte(pattern[i])
				}
				i++
			}
			if i >= len(pattern) {
				return nil, false
			}
			out.WriteByte(']')
			i++
		default:
			if isRegexMeta(c) {
				out.WriteByte('\\')
			}
			out.WriteByte(c)
			i++
		}
	}
	out.WriteByte('$')
	re, err := regexp.Compile(out.String())
	if err != nil {
		return nil, false
	}
	return re, true
}

// isRegexMeta reports whether a byte must be escaped outside a class.
func isRegexMeta(c byte) bool { return strings.ContainsRune(`.+()|^${}\`, rune(c)) }
