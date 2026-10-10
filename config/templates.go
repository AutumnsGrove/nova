package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// templateNameRE is the allowed shape of a prompt template name.
var templateNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-_]*$`)

// Template is one `<name>.md` prompt template.
type Template struct {
	Name        string
	Description string
	Body        string
	Path        string
}

// LoadTemplates reads .md files from each directory in order. A duplicate name
// keeps the first and warns.
func LoadTemplates(dirs []string) ([]Template, []Warning) {
	var templates []Template
	var warnings []Warning
	seen := map[string]bool{}
	for _, root := range dirs {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			path := filepath.Join(root, entry.Name())
			name := strings.TrimSuffix(entry.Name(), ".md")
			if !templateNameRE.MatchString(name) {
				warnings = append(warnings, Warning{Source: path, Message: "invalid template name " + name})
				continue
			}
			data, err := os.ReadFile(path)
			if err != nil {
				warnings = append(warnings, Warning{Source: path, Message: "cannot read template: " + err.Error()})
				continue
			}
			doc, body, _, err := parseFrontMatter(string(data))
			if err != nil {
				warnings = append(warnings, Warning{Source: path, Message: "malformed front matter: " + err.Error()})
				continue
			}
			if seen[name] {
				warnings = append(warnings, Warning{Source: path, Message: "duplicate template name " + name + "; keeping the first"})
				continue
			}
			seen[name] = true
			templates = append(templates, Template{Name: name, Description: doc.Description, Body: body, Path: path})
		}
	}
	return templates, warnings
}

// Expand substitutes the template's placeholders with args. Splitting follows
// shell-like quoting; an unclosed quote makes the whole string one argument.
func (t Template) Expand(args string) string {
	parts, err := SplitArgs(args)
	if err != nil {
		parts = []string{args}
	}
	return expandBody(t.Body, parts)
}

// expandBody performs the single-pass substitution.
func expandBody(body string, args []string) string {
	var b strings.Builder
	b.Grow(len(body))
	for i := 0; i < len(body); {
		c := body[i]
		if c == '\\' && i+1 < len(body) && body[i+1] == '$' {
			b.WriteByte('$')
			i += 2
			continue
		}
		if c == '$' && i+1 < len(body) {
			rest := body[i+1:]
			switch {
			case rest[0] >= '1' && rest[0] <= '9':
				index := int(rest[0] - '1')
				if index < len(args) {
					b.WriteString(args[index])
				}
				i += 2
				continue
			case rest[0] == '@':
				b.WriteString(strings.Join(args, " "))
				i += 2
				continue
			case strings.HasPrefix(rest, "ARGUMENTS"):
				b.WriteString(strings.Join(args, " "))
				i += 1 + len("ARGUMENTS")
				continue
			}
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

// SplitArgs splits a string the way a shell splits words, without running one:
// whitespace separates, single and double quotes group, and backslash escapes
// the next byte outside quotes and inside double quotes. An unclosed quote is
// an error.
func SplitArgs(s string) ([]string, error) {
	var args []string
	var current strings.Builder
	started := false
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch quote {
		case '\'':
			if c == '\'' {
				quote = 0
			} else {
				current.WriteByte(c)
			}
			continue
		case '"':
			switch {
			case c == '"':
				quote = 0
			case c == '\\' && i+1 < len(s):
				i++
				current.WriteByte(s[i])
			default:
				current.WriteByte(c)
			}
			continue
		}
		switch c {
		case ' ', '\t', '\n', '\r':
			if started {
				args = append(args, current.String())
				current.Reset()
				started = false
			}
		case '\'':
			quote = '\''
			started = true
		case '"':
			quote = '"'
			started = true
		case '\\':
			if i+1 < len(s) {
				i++
				current.WriteByte(s[i])
			} else {
				current.WriteByte(c)
			}
			started = true
		default:
			current.WriteByte(c)
			started = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("config: unclosed quote in arguments")
	}
	if started {
		args = append(args, current.String())
	}
	return args, nil
}
