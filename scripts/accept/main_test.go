package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubBin writes executable `go` and `gofmt` stubs, puts them first on PATH,
// and points $RECORD at a log file. It returns the log path.
func stubBin(t *testing.T, goBody, gofmtBody string) string {
	t.Helper()
	dir := t.TempDir()
	record := filepath.Join(dir, "record")
	for name, body := range map[string]string{"go": goBody, "gofmt": gofmtBody} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatalf("write stub %s: %v", name, err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("RECORD", record)
	return record
}

const (
	okGo = `#!/bin/sh
printf 'go %s\n' "$*" >> "$RECORD"
exit 0
`
	okGofmt = `#!/bin/sh
printf 'gofmt %s\n' "$*" >> "$RECORD"
exit 0
`
)

// fixture writes a chunk spec and package files under a temp root.
func fixture(t *testing.T, spec string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	files["docs/chunks/C00-fixture.md"] = spec
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return root
}

const goodSpec = `---
chunk: C00
packages: [./p]
budgets: { ./p: 100 }
---

# fixture

## Matrix

| ID | Case |
|---|---|
| AA01 | a case |
`

var stepNames = []string{"gofmt", "vet", "build", "build-notui", "test-short", "test", "matrixcheck", "linecount", "importcheck"}

func TestAccept_ACC01_AllStepsPass(t *testing.T) {
	dir := fixture(t, goodSpec, map[string]string{"p/p.go": "package p\n"})
	stubBin(t, okGo, okGofmt)
	t.Chdir(dir)
	var out, errOut bytes.Buffer
	code := run([]string{"C00"}, &out, &errOut, nil)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
	}
	for _, name := range stepNames {
		if !strings.Contains(out.String(), name) {
			t.Errorf("summary does not list step %q:\n%s", name, out.String())
		}
	}
	if got := strings.Count(out.String(), "ok"); got < len(stepNames) {
		t.Errorf("summary has %d ok markers, want at least %d:\n%s", got, len(stepNames), out.String())
	}
}

func TestAccept_ACC02_GofmtDirty(t *testing.T) {
	dir := fixture(t, goodSpec, map[string]string{"p/p.go": "package p\n"})
	gofmt := "#!/bin/sh\nprintf 'p/bad.go\\n'\nexit 0\n"
	record := stubBin(t, okGo, gofmt)
	t.Chdir(dir)
	var out, errOut bytes.Buffer
	code := run([]string{"C00"}, &out, &errOut, nil)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String()+errOut.String(), "bad.go") {
		t.Errorf("output should print the unformatted file:\n%s%s", out.String(), errOut.String())
	}
	if got, _ := os.ReadFile(record); strings.Contains(string(got), "go ") {
		t.Errorf("a later step ran after gofmt failed:\n%s", got)
	}
}

func TestAccept_ACC03_TestFails(t *testing.T) {
	dir := fixture(t, goodSpec, map[string]string{"p/p.go": "package p\n"})
	body := `#!/bin/sh
printf 'go %s\n' "$*" >> "$RECORD"
case "$*" in
	*test*) echo "--- FAIL: TestBroken"; exit 1 ;;
esac
exit 0
`
	stubBin(t, body, okGofmt)
	t.Chdir(dir)
	var out, errOut bytes.Buffer
	code := run([]string{"C00"}, &out, &errOut, nil)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String()+errOut.String(), "TestBroken") {
		t.Errorf("output should include the failing test output:\n%s%s", out.String(), errOut.String())
	}
}

func TestAccept_ACC04_UnknownChunk(t *testing.T) {
	dir := fixture(t, goodSpec, map[string]string{"p/p.go": "package p\n"})
	stubBin(t, okGo, okGofmt)
	t.Chdir(dir)
	var out, errOut bytes.Buffer
	code := run([]string{"C99"}, &out, &errOut, nil)
	if code != 2 {
		t.Fatalf("exit = %d, want 2\n%s%s", code, out.String(), errOut.String())
	}
}

func TestAccept_ACC05_FromStep(t *testing.T) {
	dir := fixture(t, goodSpec, map[string]string{"p/p.go": "package p\n"})
	record := stubBin(t, okGo, okGofmt)
	t.Chdir(dir)
	var out, errOut bytes.Buffer
	code := run([]string{"C00", "-from", "matrixcheck"}, &out, &errOut, nil)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s%s", code, out.String(), errOut.String())
	}
	got, _ := os.ReadFile(record)
	log := strings.TrimSpace(string(got))
	if !strings.HasPrefix(log, "go run ./scripts/matrixcheck") {
		t.Errorf("earlier steps were not skipped:\n%s", log)
	}
	for _, skipped := range []string{"gofmt", "vet", "build", "test"} {
		if strings.Contains(log, skipped) {
			t.Errorf("skipped step %q still ran:\n%s", skipped, log)
		}
	}
}

func TestAccept_ACC06_StepsRunInOrder(t *testing.T) {
	dir := fixture(t, goodSpec, map[string]string{"p/p.go": "package p\n"})
	record := stubBin(t, okGo, okGofmt)
	t.Chdir(dir)
	var out, errOut bytes.Buffer
	if code := run([]string{"C00"}, &out, &errOut, nil); code != 0 {
		t.Fatalf("exit = %d, want 0\n%s%s", code, out.String(), errOut.String())
	}
	want := strings.Join([]string{
		"gofmt -l .",
		"go vet ./...",
		"go build ./...",
		"go build -tags notui ./...",
		"go test -short -race -p 2 ./p",
		"go test -race -p 2 ./p",
		"go run ./scripts/matrixcheck docs/chunks/C00-fixture.md ./p",
		"go run ./scripts/linecount docs/chunks/C00-fixture.md",
		"go run ./scripts/importcheck",
	}, "\n")
	got, _ := os.ReadFile(record)
	if strings.TrimSpace(string(got)) != want {
		t.Errorf("command log mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestAccept_ACC07_MissingPackage(t *testing.T) {
	spec := `---
chunk: C00
packages: [./missing]
budgets: { ./missing: 100 }
---

## Matrix

| ID | Case |
|---|---|
| AA01 | a case |
`
	dir := fixture(t, spec, map[string]string{"p/p.go": "package p\n"})
	stubBin(t, okGo, okGofmt)
	t.Chdir(dir)
	var out, errOut bytes.Buffer
	code := run([]string{"C00"}, &out, &errOut, nil)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String()+errOut.String(), "./missing") {
		t.Errorf("output should name the missing package:\n%s%s", out.String(), errOut.String())
	}
}
