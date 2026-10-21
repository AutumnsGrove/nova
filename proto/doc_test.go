package proto

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// docText reads docs/PROTOCOL.md, which this package's types must match.
func docText(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../docs/PROTOCOL.md")
	if err != nil {
		t.Fatalf("read PROTOCOL.md: %v", err)
	}
	return string(b)
}

// constValues returns the string values of the constants in codes.go whose
// name starts with prefix. Reading the source keeps this test from carrying
// its own copy of the list, which would drift with the code it checks.
func constValues(t *testing.T, prefix string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "codes.go", nil, 0)
	if err != nil {
		t.Fatalf("parse codes.go: %v", err)
	}
	var out []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, name := range vs.Names {
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if strings.HasPrefix(name.Name, prefix) && ok && lit.Kind == token.STRING {
					v, _ := strconv.Unquote(lit.Value)
					out = append(out, v)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// section returns the text of doc from the first line starting with start up
// to the next line starting with end.
func section(t *testing.T, doc, start, end string) string {
	t.Helper()
	i := strings.Index(doc, start)
	if i < 0 {
		t.Fatalf("PROTOCOL.md has no %q", start)
	}
	rest := doc[i:]
	if j := strings.Index(rest[len(start):], end); j >= 0 {
		return rest[:len(start)+j]
	}
	return rest
}

// jsonTags returns the set of JSON field names of a struct type.
func jsonTags(v any) map[string]bool {
	tags := map[string]bool{}
	typ := reflect.TypeOf(v)
	for i := range typ.NumField() {
		if name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ","); name != "" && name != "-" {
			tags[name] = true
		}
	}
	return tags
}

// topLevelNames lists the field names in a "{ a, b?, c: {d, e} }" shape,
// skipping the contents of nested braces and any trailing // comment.
func topLevelNames(shape string) []string {
	shape, _, _ = strings.Cut(shape, "//")
	var names []string
	depth, start := 0, -1
	flush := func(end int) {
		if start >= 0 {
			if name := strings.Trim(strings.TrimSpace(shape[start:end]), "?"); name != "" {
				names = append(names, name)
			}
			start = -1
		}
	}
	for i, r := range shape {
		switch {
		case r == '{':
			depth++
			if depth == 1 {
				start = i + 1
			}
		case r == '}':
			if depth == 1 {
				flush(i)
			}
			depth--
		case depth == 1 && (r == ',' || r == ':'):
			flush(i)
			if r == ':' {
				start = -1 // the value after a colon is not a name
			}
		case depth == 1 && start < 0 && r != ' ':
			start = i
		}
	}
	return names
}

func TestProto_DOC01_CommandsMatchTable(t *testing.T) {
	table := section(t, docText(t), "| Command | Fields | Notes |", "\n\n")
	cell := regexp.MustCompile("`([a-z_.]+)`")
	var documented []string
	tags := jsonTags(Command{})
	for _, row := range strings.Split(table, "\n")[2:] {
		cols := strings.Split(row, "|")
		if len(cols) < 4 {
			continue
		}
		for _, m := range cell.FindAllStringSubmatch(cols[1], -1) {
			documented = append(documented, m[1])
		}
		for _, f := range cell.FindAllStringSubmatch(cols[2], -1) {
			if !tags[f[1]] {
				t.Errorf("command row %q documents field %q that proto.Command does not have", cols[1], f[1])
			}
		}
	}
	sort.Strings(documented)
	if got, want := strings.Join(documented, " "), strings.Join(constValues(t, "Cmd"), " "); got != want {
		t.Errorf("documented commands differ from proto.Cmd*:\n doc: %s\ncode: %s", got, want)
	}
}

func TestProto_DOC02_EventsMatchBlock(t *testing.T) {
	block := section(t, docText(t), "## Events", "\n## ")
	_, rest, _ := strings.Cut(block, "```\n")
	body, _, _ := strings.Cut(rest, "```")
	tags := jsonTags(Event{})
	var documented []string
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		name, shape, _ := strings.Cut(line, " ")
		documented = append(documented, name)
		for _, field := range topLevelNames(shape) {
			if !tags[field] {
				t.Errorf("event %s documents field %q that proto.Event does not have", name, field)
			}
		}
	}
	sort.Strings(documented)
	if got, want := strings.Join(documented, " "), strings.Join(constValues(t, "Event"), " "); got != want {
		t.Errorf("documented events differ from proto.Event*:\n doc: %s\ncode: %s", got, want)
	}
}

func TestProto_DOC03_ErrorCodesMatch(t *testing.T) {
	para := section(t, docText(t), "Error codes are stable strings:", "\n\n")
	var documented []string
	for _, m := range regexp.MustCompile("`([a-z_]+)`").FindAllStringSubmatch(para, -1) {
		documented = append(documented, m[1])
	}
	sort.Strings(documented)
	if got, want := strings.Join(documented, " "), strings.Join(constValues(t, "Code"), " "); got != want {
		t.Errorf("documented error codes differ from proto.Code*:\n doc: %s\ncode: %s", got, want)
	}
}

func TestProto_DOC04_TopLevelNames(t *testing.T) {
	got := topLevelNames("{ run?, call: {id, name}, error: {code}, tokens }  // note, with: punctuation")
	if want := "run call error tokens"; strings.Join(got, " ") != want {
		t.Fatalf("names = %v, want %s", got, want)
	}
}
