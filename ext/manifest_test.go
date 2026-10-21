package ext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/testkit"
)

// writeManifest writes extension.yaml into a fresh temp dir and returns it.
func writeManifest(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "extension.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func loadErr(t *testing.T, body string) error {
	t.Helper()
	_, err := LoadManifest(writeManifest(t, body))
	if err == nil {
		t.Fatalf("expected an error for manifest:\n%s", body)
	}
	return err
}

func TestManifest_MAN01_Valid(t *testing.T) {
	dir := writeManifest(t, "name: hello\nversion: 1.2.3\ndescription: A hello extension.\nrun: [echo]\n")
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "hello" || m.Version != "1.2.3" {
		t.Fatalf("parsed %+v", m)
	}
	if !filepath.IsAbs(m.Dir) {
		t.Fatalf("Dir = %q, want absolute", m.Dir)
	}
}

func TestManifest_MAN02_MissingOrBadName(t *testing.T) {
	for _, body := range []string{
		"version: 1.0.0\ndescription: x\n",
		"name: Bad_Name\nversion: 1.0.0\ndescription: x\n",
		"name: " + strings.Repeat("a", 65) + "\nversion: 1.0.0\ndescription: x\n",
	} {
		loadErr(t, body)
	}
}

func TestManifest_MAN03_BadVersion(t *testing.T) {
	loadErr(t, "name: hello\nversion: 1.x\ndescription: x\n")
}

func TestManifest_MAN04_ConstraintForms(t *testing.T) {
	cases := []struct {
		constraint, version string
		want                bool
	}{
		{"", "9.9.9", true},
		{">=0.1", "0.1.0", true},
		{">=0.1", "0.0.9", false},
		{"^0.2", "0.2.5", true},
		{"^0.2", "0.3.0", false},
		{"0.1.x", "0.1.9", true},
		{"0.1.x", "0.2.0", false},
	}
	for _, tc := range cases {
		got, err := matchConstraint(tc.constraint, tc.version)
		if err != nil {
			t.Fatalf("matchConstraint(%q): %v", tc.constraint, err)
		}
		if got != tc.want {
			t.Errorf("matchConstraint(%q, %q) = %v, want %v", tc.constraint, tc.version, got, tc.want)
		}
	}
	for _, c := range []string{">=0.1", "^0.2", "0.1.x", ""} {
		m := &Manifest{Name: "x", Version: "1.0.0", Description: "d", Nova: c, Run: []string{"echo"}}
		if err := m.Validate(); err != nil {
			t.Errorf("constraint %q rejected: %v", c, err)
		}
	}
}

func TestManifest_MAN05_Description(t *testing.T) {
	loadErr(t, "name: hello\nversion: 1.0.0\n")
	loadErr(t, "name: hello\nversion: 1.0.0\ndescription: "+strings.Repeat("a", 501)+"\n")
}

func TestManifest_MAN06_RunEscapes(t *testing.T) {
	for _, run := range []string{"../x", "a/../../x"} {
		loadErr(t, "name: hello\nversion: 1.0.0\ndescription: x\nrun: [\""+run+"\"]\n")
	}
}

func TestManifest_MAN07_RunForms(t *testing.T) {
	for _, run := range []string{"/usr/bin/env", "echo", "./bin/tool"} {
		dir := writeManifest(t, "name: hello\nversion: 1.0.0\ndescription: x\nrun: [\""+run+"\"]\n")
		if _, err := LoadManifest(dir); err != nil {
			t.Errorf("run %q rejected: %v", run, err)
		}
	}
}

func TestManifest_MAN08_DeclarativeOnly(t *testing.T) {
	body := "name: hello\nversion: 1.0.0\ndescription: x\ntools:\n  - name: word_count\n    description: count\n    parameters: {type: object}\n    exec: [\"wc\", \"-w\"]\n"
	if _, err := LoadManifest(writeManifest(t, body)); err != nil {
		t.Fatalf("declarative-only manifest rejected: %v", err)
	}
}

func TestManifest_MAN09_NoRunNoContent(t *testing.T) {
	loadErr(t, "name: hello\nversion: 1.0.0\ndescription: x\n")
}

func TestManifest_MAN10_UnknownCapability(t *testing.T) {
	err := loadErr(t, "name: hello\nversion: 1.0.0\ndescription: x\nrun: [echo]\ncapabilities: [teleport]\n")
	if !strings.Contains(err.Error(), "teleport") {
		t.Fatalf("error does not name the capability: %v", err)
	}
}

func TestManifest_MAN11_KnownCapabilities(t *testing.T) {
	caps := []string{
		"tools", "commands", "hooks:turn_end", "ui:notify", "ui:confirm",
		"ui:select", "ui:input", "ui:form", "ui:status", "ui:widget",
		"session:append_entry", "session:read", "model:complete", "providers",
		"mcp", "network", "fs", "exec",
	}
	for _, c := range caps {
		body := "name: hello\nversion: 1.0.0\ndescription: x\nrun: [echo]\ncapabilities: [\"" + c + "\"]\n"
		if _, err := LoadManifest(writeManifest(t, body)); err != nil {
			t.Errorf("capability %q rejected: %v", c, err)
		}
	}
}

func TestManifest_MAN12_ToolNameVsBuiltin(t *testing.T) {
	loadErr(t, "name: hello\nversion: 1.0.0\ndescription: x\ntools:\n  - name: read\n    description: x\n    parameters: {type: object}\n    exec: [echo]\n")
}

func TestManifest_MAN13_DuplicateTools(t *testing.T) {
	body := "name: hello\nversion: 1.0.0\ndescription: x\ntools:\n  - name: a\n    description: x\n    parameters: {type: object}\n    exec: [echo]\n  - name: a\n    description: x\n    parameters: {type: object}\n    exec: [echo]\n"
	loadErr(t, body)
}

func TestManifest_MAN14_ParametersNotObject(t *testing.T) {
	loadErr(t, "name: hello\nversion: 1.0.0\ndescription: x\ntools:\n  - name: a\n    description: x\n    parameters: {type: array}\n    exec: [echo]\n")
}

func TestManifest_MAN15_Timeout(t *testing.T) {
	body := "name: hello\nversion: 1.0.0\ndescription: x\ntools:\n  - name: a\n    description: x\n    parameters: {type: object}\n    exec: [echo]\n  - name: b\n    description: x\n    parameters: {type: object}\n    exec: [echo]\n    timeout_ms: 999999\n"
	m, err := LoadManifest(writeManifest(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Tools[0].TimeoutMS; got != 30_000 {
		t.Errorf("default timeout = %d, want 30000", got)
	}
	if got := m.Tools[1].TimeoutMS; got != 600_000 {
		t.Errorf("capped timeout = %d, want 600000", got)
	}
}

func TestManifest_MAN16_UnknownKeyWarns(t *testing.T) {
	m, err := LoadManifest(writeManifest(t, "name: hello\nversion: 1.0.0\ndescription: x\nrun: [echo]\nextra: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Warnings) == 0 || !strings.Contains(strings.Join(m.Warnings, "\n"), "extra") {
		t.Fatalf("Warnings = %v, want one naming extra", m.Warnings)
	}
}

func TestManifest_MAN17_TooBig(t *testing.T) {
	body := "name: hello\nversion: 1.0.0\ndescription: x\nrun: [echo]\n# " + strings.Repeat("a", maxManifestBytes)
	loadErr(t, body)
}

func TestManifest_MAN18_MalformedYAML(t *testing.T) {
	err := loadErr(t, "name: [\n")
	if !strings.Contains(err.Error(), "line") {
		t.Fatalf("error does not name a line: %v", err)
	}
}

// A cloned repository chooses what extension.yaml is. A named pipe must be
// refused, not waited on, and a device must not be read to the end.
func TestManifest_MAN17b_NotARegularFile(t *testing.T) {
	dir := t.TempDir()
	testkit.Mkfifo(t, filepath.Join(dir, "extension.yaml"))
	done := make(chan error, 1)
	go func() {
		_, err := LoadManifest(dir)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("err = %v, want a refusal naming regular files", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("LoadManifest blocked on a named pipe")
	}

	dev := t.TempDir()
	if err := os.Symlink("/dev/zero", filepath.Join(dev, "extension.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifest(dev); err == nil {
		t.Fatal("a manifest symlinked to a device was accepted")
	}
}

// The command of an exec tool is checked once, at load, so it cannot be filled
// in from a model-supplied argument later.
func TestManifest_MAN19_ExecCommandIsNotATemplate(t *testing.T) {
	err := loadErr(t, "name: hello\nversion: 1.0.0\ndescription: x\ntools:\n  - name: t\n    description: x\n    parameters: {type: object}\n    exec: [\"{{.dir}}/run.sh\"]\n")
	if !strings.Contains(err.Error(), "placeholder") {
		t.Fatalf("err = %v, want a refusal naming placeholders", err)
	}
}

func FuzzManifest(f *testing.F) {
	f.Add([]byte("name: hello\nversion: 1.0.0\ndescription: x\nrun: [echo]\n"))
	f.Add([]byte("name: [\n"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := parseManifest(t.TempDir(), data)
		if err == nil && m == nil {
			t.Fatal("nil manifest with nil error")
		}
	})
}
