package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestTemplates_TPL01_Loaded(t *testing.T) {
	root := t.TempDir()
	write(t, root+"/foo.md", "hello $1")
	templates, warnings := LoadTemplates([]string{root})
	if len(templates) != 1 || len(warnings) != 0 {
		t.Fatalf("templates = %+v warnings = %v", templates, warnings)
	}
	if templates[0].Name != "foo" || templates[0].Body != "hello $1" {
		t.Fatalf("template = %+v", templates[0])
	}
}

func TestTemplates_TPL02_FrontMatterDescription(t *testing.T) {
	root := t.TempDir()
	write(t, root+"/foo.md", "---\ndescription: does foo\n---\nbody")
	templates, _ := LoadTemplates([]string{root})
	if len(templates) != 1 || templates[0].Description != "does foo" {
		t.Fatalf("templates = %+v", templates)
	}
}

func TestTemplates_TPL03_BadName(t *testing.T) {
	root := t.TempDir()
	write(t, root+"/Foo.md", "body")
	templates, warnings := LoadTemplates([]string{root})
	if len(templates) != 0 || len(warnings) == 0 {
		t.Fatalf("templates = %v warnings = %v, want skipped with a warning", templates, warnings)
	}
}

func TestTemplates_TPL04_Duplicate(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	write(t, first+"/foo.md", "first")
	write(t, second+"/foo.md", "second")
	templates, warnings := LoadTemplates([]string{first, second})
	if len(templates) != 1 || templates[0].Body != "first" {
		t.Fatalf("templates = %+v, want the first", templates)
	}
	if len(warnings) == 0 {
		t.Fatal("a duplicate should warn")
	}
}

func TestTemplates_TPL05_Positional(t *testing.T) {
	tpl := Template{Body: "$1 and $2"}
	if got := tpl.Expand("a b"); got != "a and b" {
		t.Fatalf("got %q, want %q", got, "a and b")
	}
}

func TestTemplates_TPL06_MissingPositional(t *testing.T) {
	tpl := Template{Body: "[$3]"}
	if got := tpl.Expand("a b"); got != "[]" {
		t.Fatalf("got %q, want []", got)
	}
}

func TestTemplates_TPL07_AllArguments(t *testing.T) {
	tpl := Template{Body: "$@ / $ARGUMENTS"}
	if got := tpl.Expand("a b"); got != "a b / a b" {
		t.Fatalf("got %q, want %q", got, "a b / a b")
	}
}

func TestTemplates_TPL08_EscapedDollar(t *testing.T) {
	tpl := Template{Body: `cost \$1`}
	if got := tpl.Expand("x"); got != "cost $1" {
		t.Fatalf("got %q, want %q", got, "cost $1")
	}
}

func TestTemplates_TPL09_UnknownSequence(t *testing.T) {
	tpl := Template{Body: "$x $0 $$"}
	if got := tpl.Expand("a"); got != "$x $0 $$" {
		t.Fatalf("got %q, want %q", got, "$x $0 $$")
	}
}

func TestTemplates_TPL10_SinglePass(t *testing.T) {
	tpl := Template{Body: "$1"}
	if got := tpl.Expand("$1"); got != "$1" {
		t.Fatalf("got %q, want %q (no re-expansion)", got, "$1")
	}
}

func TestTemplates_TPL11_SplitArgsQuotes(t *testing.T) {
	got, err := SplitArgs(`a "b c" 'd e' f\ g`)
	if err != nil {
		t.Fatalf("SplitArgs: %v", err)
	}
	want := []string{"a", "b c", "d e", "f g"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestTemplates_TPL12_UnclosedQuote(t *testing.T) {
	if _, err := SplitArgs(`a "b`); err == nil {
		t.Fatal("an unclosed quote should be an error")
	}
	tpl := Template{Body: "<$1>"}
	if got := tpl.Expand(`a "b`); got != `<a "b>` {
		t.Fatalf("got %q, want the whole string as one argument", got)
	}
}

func TestTemplates_TPL13_EmptyArgs(t *testing.T) {
	tpl := Template{Body: "[$1][$@]"}
	if got := tpl.Expand(""); got != "[][]" {
		t.Fatalf("got %q, want [][]", got)
	}
}

func TestTemplates_TPL14_NewlinesAndUnicode(t *testing.T) {
	tpl := Template{Body: "$1 $2"}
	if got := tpl.Expand(`"a` + "\n" + `b" "héllo→"`); got != "a\nb héllo→" {
		t.Fatalf("got %q", got)
	}
}

func TestTemplates_TPL15_VeryLongBody(t *testing.T) {
	body := strings.Repeat("abc ", 256<<10)
	tpl := Template{Body: body}
	if got := tpl.Expand("x"); len(got) != len(body) {
		t.Fatalf("len = %d, want %d", len(got), len(body))
	}
}
