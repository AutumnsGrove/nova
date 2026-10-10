package testkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGolden_TKF04_MatchAndMismatch(t *testing.T) {
	t.Chdir(t.TempDir())
	writeGolden(t, "tkf04", "same\n")

	if err := compareGolden("tkf04", "same\n", false); err != nil {
		t.Errorf("matching golden returned %v, want nil", err)
	}
	err := compareGolden("tkf04", "different\n", false)
	if err == nil {
		t.Fatal("mismatching golden returned nil, want an error")
	}
	if !strings.Contains(err.Error(), "- same") || !strings.Contains(err.Error(), "+ different") {
		t.Errorf("error should show a diff:\n%v", err)
	}
}

func TestGolden_TKF05_UpdateWritesFile(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := compareGolden("tkf05", "brand new", true); err != nil {
		t.Fatalf("update returned %v, want nil", err)
	}
	got, err := os.ReadFile(filepath.Join("testdata", "tkf05.golden"))
	if err != nil {
		t.Fatalf("read updated golden: %v", err)
	}
	if string(got) != "brand new" {
		t.Errorf("updated golden = %q, want brand new", got)
	}
}

func TestGolden_TKF06_MissingFileNamesFlag(t *testing.T) {
	t.Chdir(t.TempDir())
	err := compareGolden("tkf06", "x", false)
	if err == nil {
		t.Fatal("missing golden returned nil, want an error")
	}
	if !strings.Contains(err.Error(), "-update") {
		t.Errorf("error should name the -update flag: %v", err)
	}
}

// writeGolden writes a golden file under the current directory's testdata.
func writeGolden(t *testing.T, name, content string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir testdata: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write golden: %v", err)
	}
}
