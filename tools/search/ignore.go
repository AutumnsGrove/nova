package search

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/AutumnsGrove/nova/permission"
)

// ignoreSet is the rules parsed from one .gitignore file, anchored at the
// directory that held it. The directory is absolute; a rule is matched against
// a path relative to it.
type ignoreSet struct {
	dir   string
	rules []ignoreRule
}

// ignoreRule is one parsed .gitignore line. A later rule overrides an earlier
// one, and a negated rule re-includes a path.
type ignoreRule struct {
	pattern  string
	anchored bool // a leading or middle slash anchors the pattern to the file's directory
	negate   bool
	dirOnly  bool
}

// parseIgnore parses a .gitignore body found in directory base. An unreadable
// line is skipped rather than fatal: the ignore file's own errors must never
// fail a walk. A BOM and CRLF line endings are accepted.
func parseIgnore(base string, data []byte) *ignoreSet {
	set := &ignoreSet{dir: filepath.Clean(base)}
	data = trimBOM(data)
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	for _, line := range strings.Split(text, "\n") {
		if rule, ok := parseIgnoreLine(line); ok {
			set.rules = append(set.rules, rule)
		}
	}
	return set
}

// trimBOM drops a leading UTF-8 byte-order mark.
func trimBOM(data []byte) []byte {
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return data[3:]
	}
	return data
}

// parseIgnoreLine parses one line of a .gitignore file. It reports false for a
// blank line, a comment, or a pattern the matcher cannot use.
func parseIgnoreLine(line string) (ignoreRule, bool) {
	line = trimTrailingSpaces(line)
	if line == "" {
		return ignoreRule{}, false
	}
	escaped := false
	if len(line) >= 2 && line[0] == '\\' && (line[1] == '#' || line[1] == '!') {
		line, escaped = line[1:], true
	}
	if !escaped && line[0] == '#' {
		return ignoreRule{}, false
	}
	rule := ignoreRule{}
	if !escaped && line[0] == '!' {
		rule.negate = true
		line = line[1:]
	}
	if line == "" {
		return ignoreRule{}, false
	}
	if strings.HasSuffix(line, "/") {
		rule.dirOnly = true
		line = strings.TrimSuffix(line, "/")
	}
	if line == "" {
		return ignoreRule{}, false
	}
	if strings.HasPrefix(line, "/") {
		rule.anchored = true
		line = strings.TrimPrefix(line, "/")
	} else if strings.Contains(line, "/") {
		rule.anchored = true
	}
	if line == "" {
		return ignoreRule{}, false
	}
	rule.pattern = line
	return rule, true
}

// trimTrailingSpaces strips unescaped trailing spaces; a backslash keeps one
// space, matching gitignore's `\ ` escape.
func trimTrailingSpaces(s string) string {
	for len(s) > 0 && s[len(s)-1] == ' ' {
		if len(s) >= 2 && s[len(s)-2] == '\\' {
			return s[:len(s)-2] + " "
		}
		s = s[:len(s)-1]
	}
	return s
}

// match reports whether the rule matches rel (relative to the set's directory)
// and, if so, whether it ignores the path. A directory-only rule never matches
// a file.
func (r ignoreRule) match(rel string, isDir bool) bool {
	if r.dirOnly && !isDir {
		return false
	}
	if r.anchored {
		return permission.MatchPath(r.pattern, rel)
	}
	// An unanchored pattern has no slash, so it matches the base name at any
	// depth.
	return permission.MatchPath(r.pattern, path.Base(rel))
}

// match applies the set's rules in order and reports the last decision.
func (s *ignoreSet) match(rel string, isDir bool) (ignored, decided bool) {
	for _, rule := range s.rules {
		if !rule.match(rel, isDir) {
			continue
		}
		decided, ignored = true, !rule.negate
	}
	return ignored, decided
}

// matchStack applies every set in a stack from the outermost directory inward.
// A later (deeper) set overrides an earlier one, which is how a nested
// .gitignore can re-include a path its parent ignored.
func matchStack(stack []*ignoreSet, full string, isDir bool) bool {
	ignored := false
	for _, set := range stack {
		rel, ok := relUnder(set.dir, full)
		if !ok {
			continue
		}
		if ig, decided := set.match(rel, isDir); decided {
			ignored = ig
		}
	}
	return ignored
}

// relUnder returns full relative to base with slash separators, or false when
// full is not under base.
func relUnder(base, full string) (string, bool) {
	rel, err := filepath.Rel(base, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}
