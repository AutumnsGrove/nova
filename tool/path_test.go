package tool

import (
	"path/filepath"
	"testing"
)

// TestProps_Shape checks the schema helpers produce the typed property maps the
// validator and the providers expect.
func TestProps_Shape(t *testing.T) {
	cases := []struct {
		name string
		got  map[string]any
		typ  string
	}{
		{"string", StringProp("d"), "string"},
		{"int", IntProp("d"), "integer"},
		{"bool", BoolProp("d"), "boolean"},
	}
	for _, c := range cases {
		if c.got["type"] != c.typ || c.got["description"] != "d" || len(c.got) != 2 {
			t.Errorf("%s prop = %v, want type %q and description %q only", c.name, c.got, c.typ, "d")
		}
	}
}

// TestResolvePath_RelativeRootAnchorsToWorkDir checks a relative root resolves
// against WorkDir, not the process directory.
func TestResolvePath_RelativeRootAnchorsToWorkDir(t *testing.T) {
	work := t.TempDir()
	ctx := &Context{WorkDir: work, Roots: []string{"."}}
	want := filepath.Join(work, "a.txt")
	if got, err := ResolvePath(ctx, "a.txt"); err != nil || got != want {
		t.Fatalf("ResolvePath = %q, %v; want %q", got, err, want)
	}
	if _, err := ResolvePath(ctx, filepath.Join(filepath.Dir(work), "elsewhere")); err == nil {
		t.Fatal("a path beside WorkDir must be refused when the root is WorkDir")
	}
}

// TestResolvePath_EmptyRootIgnored checks an empty root entry grants nothing.
func TestResolvePath_EmptyRootIgnored(t *testing.T) {
	ctx := &Context{WorkDir: t.TempDir(), Roots: []string{""}}
	if _, err := ResolvePath(ctx, "a.txt"); err == nil {
		t.Fatal("a roots list holding only an empty entry must refuse everything")
	}
}
