package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ctxFixture builds a home, a project directory a, and a nested b under it.
type ctxFixture struct {
	base, home, a, b string
}

func newCtxFixture(t *testing.T) ctxFixture {
	t.Helper()
	base := t.TempDir()
	f := ctxFixture{
		base: base,
		home: filepath.Join(base, "home"),
		a:    filepath.Join(base, "a"),
		b:    filepath.Join(base, "a", "b"),
	}
	if err := os.MkdirAll(f.b, 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

// global returns the global context path for a name.
func (f ctxFixture) global(name string) string {
	return filepath.Join(f.home, ".config", "nova", name)
}

// load reads context with the project trusted.
func (f ctxFixture) load(t *testing.T) ([]ContextFile, []Warning) {
	t.Helper()
	return LoadContext(f.b, f.home, ContextOptions{Trusted: alwaysTrusted})
}

// pathsOf lists the included file paths.
func pathsOf(files []ContextFile) []string {
	out := make([]string, len(files))
	for i, file := range files {
		out[i] = file.Path
	}
	return out
}

func TestContext_CTF01_GlobalThenOuterToInner(t *testing.T) {
	f := newCtxFixture(t)
	write(t, f.global("AGENTS.md"), "global")
	write(t, filepath.Join(f.a, "AGENTS.md"), "a")
	write(t, filepath.Join(f.b, "AGENTS.md"), "b")
	files, warnings := f.load(t)
	want := []string{f.global("AGENTS.md"), filepath.Join(f.a, "AGENTS.md"), filepath.Join(f.b, "AGENTS.md")}
	if got := pathsOf(files); !equalStrings(got, want) {
		t.Fatalf("paths = %v, want %v (warnings %s)", got, want, warnText(warnings))
	}
}

func TestContext_CTF02_BothNamesInOrder(t *testing.T) {
	f := newCtxFixture(t)
	write(t, filepath.Join(f.a, "AGENTS.md"), "agents")
	write(t, filepath.Join(f.a, "CLAUDE.md"), "claude")
	files, _ := f.load(t)
	want := []string{filepath.Join(f.a, "AGENTS.md"), filepath.Join(f.a, "CLAUDE.md")}
	if got := pathsOf(files); !equalStrings(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
}

func TestContext_CTF03_Truncated(t *testing.T) {
	f := newCtxFixture(t)
	write(t, filepath.Join(f.a, "AGENTS.md"), strings.Repeat("x", defaultMaxFileBytes+1024))
	files, _ := f.load(t)
	if len(files) != 1 || !files[0].Truncated {
		t.Fatalf("files = %+v, want one truncated file", files)
	}
	if !strings.Contains(files[0].Content, "truncated") {
		t.Fatalf("content should carry a marker: %q", files[0].Content[max(0, len(files[0].Content)-40):])
	}
}

func TestContext_CTF04_TotalCap(t *testing.T) {
	f := newCtxFixture(t)
	body := strings.Repeat("x", defaultMaxFileBytes)
	for _, dir := range []string{f.base, f.home, f.a, f.b} {
		write(t, filepath.Join(dir, "AGENTS.md"), body)
		write(t, filepath.Join(dir, "CLAUDE.md"), body)
	}
	files, warnings := f.load(t)
	total := 0
	for _, file := range files {
		total += len(file.Content)
	}
	if total > defaultMaxTotalBytes {
		t.Fatalf("total = %d, want at most %d", total, defaultMaxTotalBytes)
	}
	if len(warnings) == 0 {
		t.Fatal("dropping files should warn")
	}
	// The outermost project file (base) must be gone before the inner ones.
	for _, file := range files {
		if file.Path == filepath.Join(f.base, "AGENTS.md") {
			t.Fatal("the outermost file should have been dropped first")
		}
	}
}

func TestContext_CTF05_Binary(t *testing.T) {
	f := newCtxFixture(t)
	write(t, filepath.Join(f.a, "AGENTS.md"), "before\x00after")
	files, warnings := f.load(t)
	if len(files) != 0 {
		t.Fatalf("files = %+v, want none", files)
	}
	if !strings.Contains(warnText(warnings), "binary") {
		t.Fatalf("warning should mention binary:\n%s", warnText(warnings))
	}
}

func TestContext_CTF06_EmptyFile(t *testing.T) {
	f := newCtxFixture(t)
	write(t, filepath.Join(f.a, "AGENTS.md"), "")
	files, warnings := f.load(t)
	if len(files) != 0 || len(warnings) != 0 {
		t.Fatalf("files = %v warnings = %v, want none", files, warnings)
	}
}

func TestContext_CTF07_Unreadable(t *testing.T) {
	f := newCtxFixture(t)
	denied := filepath.Join(f.a, "AGENTS.md")
	files, warnings := LoadContext(f.b, f.home, ContextOptions{
		Trusted: alwaysTrusted,
		ReadFile: func(path string) ([]byte, error) {
			if path == denied {
				return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrPermission}
			}
			return os.ReadFile(path)
		},
	})
	if len(files) != 0 {
		t.Fatalf("files = %+v, want none", files)
	}
	if !strings.Contains(warnText(warnings), "cannot read") {
		t.Fatalf("warning should report the failure:\n%s", warnText(warnings))
	}
}

func TestContext_CTF08_DirectoryNamedLikeFile(t *testing.T) {
	f := newCtxFixture(t)
	if err := os.Mkdir(filepath.Join(f.a, "AGENTS.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	files, warnings := f.load(t)
	if len(files) != 0 || len(warnings) != 0 {
		t.Fatalf("files = %v warnings = %v, want none", files, warnings)
	}
}

func TestContext_CTF09_SymlinkDeduplicated(t *testing.T) {
	f := newCtxFixture(t)
	real := filepath.Join(f.a, "AGENTS.md")
	write(t, real, "same")
	if err := os.Symlink(real, filepath.Join(f.b, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	files, _ := f.load(t)
	if len(files) != 1 {
		t.Fatalf("files = %v, want one", pathsOf(files))
	}
}

func TestContext_CTF10_Untrusted(t *testing.T) {
	f := newCtxFixture(t)
	write(t, f.global("AGENTS.md"), "global")
	write(t, filepath.Join(f.a, "AGENTS.md"), "project")
	files, warnings := LoadContext(f.b, f.home, ContextOptions{})
	if got, want := pathsOf(files), []string{f.global("AGENTS.md")}; !equalStrings(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	if !strings.Contains(warnText(warnings), "trust") {
		t.Fatalf("warning should note the untrusted project:\n%s", warnText(warnings))
	}
}

func TestContext_CTF11_NoFiles(t *testing.T) {
	f := newCtxFixture(t)
	files, warnings := f.load(t)
	if len(files) != 0 || len(warnings) != 0 {
		t.Fatalf("files = %v warnings = %v, want none", files, warnings)
	}
}

func TestContext_CTF12_RootTerminates(t *testing.T) {
	f := newCtxFixture(t)
	_, warnings := LoadContext(string(filepath.Separator), f.home, ContextOptions{Trusted: alwaysTrusted})
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
}

func TestContext_CTF13_BOMAndCRLF(t *testing.T) {
	f := newCtxFixture(t)
	write(t, filepath.Join(f.a, "AGENTS.md"), "\xEF\xBB\xBFline1\r\nline2\r\n")
	files, _ := f.load(t)
	if len(files) != 1 {
		t.Fatalf("files = %v, want one", pathsOf(files))
	}
	if files[0].Content != "line1\r\nline2\r\n" {
		t.Fatalf("content = %q, want CRLF preserved and BOM removed", files[0].Content)
	}
}

// equalStrings compares two string slices.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
