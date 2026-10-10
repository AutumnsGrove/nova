// Package spec parses Nova chunk specifications: the YAML front matter and
// the matrix tables that define the acceptance criteria for a chunk. The
// guard programs share this parser so they all agree on what a spec says.
package spec

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// idRe is the documented row-ID shape: two to four capital letters followed
// by two or three digits. It is also how matrixcheck recognises an ID inside
// a test name.
var idRe = regexp.MustCompile(`^[A-Z]{2,4}[0-9]{2,3}$`)

// Spec is one parsed chunk specification.
type Spec struct {
	Chunk    string
	Title    string
	Packages []string
	Budgets  map[string]int
	Depends  []string
	Rows     []Row // every row of every table under "## Matrix"
	Path     string
}

// Row is one matrix row: a unique ID and the case it describes.
type Row struct {
	ID   string // matches ^[A-Z]{2,4}[0-9]{2,3}$
	Line int    // 1-indexed line in the spec
	Case string // the "Case" cell, used in failure messages
}

// frontMatter mirrors the YAML block between the first two "---" lines.
type frontMatter struct {
	Chunk    string         `yaml:"chunk"`
	Title    string         `yaml:"title"`
	Packages []string       `yaml:"packages"`
	Budgets  map[string]int `yaml:"budgets"`
	Depends  []string       `yaml:"depends"`
}

// Load reads and parses the chunk spec at path.
func Load(path string) (*Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("spec: read %s: %w", path, err)
	}
	return parse(path, data)
}

// parse is Load without the file read, so a fuzz target can feed it bytes.
func parse(path string, data []byte) (*Spec, error) {
	lines := splitLines(data)
	if len(lines) == 0 || lines[0] != "---" {
		return nil, fmt.Errorf("spec: %s: missing front matter", path)
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, fmt.Errorf("spec: %s: missing closing front-matter delimiter", path)
	}

	var fm frontMatter
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &fm); err != nil {
		return nil, fmt.Errorf("spec: %s: invalid front matter: %w", path, err)
	}

	s := &Spec{
		Chunk:    fm.Chunk,
		Title:    fm.Title,
		Packages: fm.Packages,
		Budgets:  fm.Budgets,
		Depends:  fm.Depends,
		Path:     path,
	}
	s.Rows = matrixRows(lines, end+1)
	if err := checkDuplicateIDs(path, s.Rows); err != nil {
		return nil, err
	}
	return s, nil
}

// splitLines normalises CRLF to LF and strips any stray trailing CR so a spec
// saved on Windows parses exactly like one saved on Unix.
func splitLines(data []byte) []string {
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines
}

// matrixRows returns every ID-shaped table row inside the Matrix section,
// starting the scan at line index start. Headings only count at the start of a
// line, so prose that mentions "## Matrix" does not open a section.
func matrixRows(lines []string, start int) []Row {
	var rows []Row
	inMatrix := false
	for i := start; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(line, "## ") {
			inMatrix = strings.HasPrefix(line, "## Matrix")
			continue
		}
		if !inMatrix {
			continue
		}
		id, c, ok := parseRow(line)
		if !ok {
			continue
		}
		rows = append(rows, Row{ID: id, Line: i + 1, Case: c})
	}
	return rows
}

// parseRow reads "| ID | Case | ... |" and returns the ID and Case cells when
// the first cell is ID-shaped. Header and separator rows fail the shape test.
func parseRow(line string) (id, c string, ok bool) {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "|") {
		return "", "", false
	}
	cells := strings.Split(t, "|")
	if len(cells) < 3 {
		return "", "", false
	}
	id = strings.TrimSpace(cells[1])
	if !idRe.MatchString(id) {
		return "", "", false
	}
	return id, strings.TrimSpace(cells[2]), true
}

// checkDuplicateIDs rejects a spec that reuses a row ID, naming both lines so
// the author can find them.
func checkDuplicateIDs(path string, rows []Row) error {
	seen := make(map[string]int, len(rows))
	for _, r := range rows {
		if prev, ok := seen[r.ID]; ok {
			return fmt.Errorf("spec: %s: duplicate matrix ID %q at lines %d and %d", path, r.ID, prev, r.Line)
		}
		seen[r.ID] = r.Line
	}
	return nil
}

// Find returns the path of the one chunk spec for chunk under docs/chunks. It
// reports distinct errors for "none" and "several" so callers can tell them
// apart.
func Find(chunk string) (string, error) {
	pattern := filepath.Join("docs", "chunks", chunk+"-*.md")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", fmt.Errorf("spec: find %s: %w", chunk, err)
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("spec: no chunk spec matches %q in docs/chunks", chunk)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("spec: multiple chunk specs match %q: %s", chunk, strings.Join(matches, ", "))
	}
}
