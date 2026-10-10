package testkit

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// updateFlag is the standard golden escape hatch: `go test -update ./...`
// rewrites every golden file instead of comparing.
var updateFlag = flag.Bool("update", false, "update golden files")

// Golden compares got to testdata/<name>.golden, failing the test on a
// mismatch. With -update it rewrites the file and passes.
func Golden(t testing.TB, name, got string) {
	t.Helper()
	if err := compareGolden(name, got, *updateFlag); err != nil {
		t.Errorf("testkit: %v", err)
	}
}

// compareGolden is Golden's testable core: it returns an error instead of
// failing a test, so the golden behaviour can itself be tested.
func compareGolden(name, got string, update bool) error {
	path := filepath.Join("testdata", name+".golden")
	if update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("golden %s: %w", path, err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			return fmt.Errorf("golden %s: %w", path, err)
		}
		return nil
	}
	want, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("golden %s not found; run with -update to create it", path)
	}
	if string(want) != got {
		return fmt.Errorf("golden %s mismatch:\n%s", path, diffGolden(string(want), got))
	}
	return nil
}

// diffGolden is a line-by-line diff, good enough to see what changed.
func diffGolden(want, got string) string {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")
	var b strings.Builder
	for i := 0; i < len(wantLines) || i < len(gotLines); i++ {
		var w, g string
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if w != g {
			fmt.Fprintf(&b, "- %s\n+ %s\n", w, g)
		}
	}
	return b.String()
}
