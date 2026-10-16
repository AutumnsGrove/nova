package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/diag"
)

// withRecorder installs a temp recorder as the process default for a test.
func withRecorder(t *testing.T, debug bool) *diag.Recorder {
	t.Helper()
	rec := diag.New(diag.Options{Dir: t.TempDir(), Debug: debug})
	diag.SetDefault(rec)
	t.Cleanup(func() { diag.SetDefault(nil) })
	return rec
}

func TestLogCommand_PrintsLatestReport(t *testing.T) {
	rec := withRecorder(t, false)
	if _, err := rec.Report("first", "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := rec.Report("second", "two"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := logCommand(&out).Execute(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "panic: second") || strings.Contains(got, "panic: first") {
		t.Fatalf("log = %q", got)
	}
}

func TestLogCommand_NoReports(t *testing.T) {
	withRecorder(t, false)
	var out bytes.Buffer
	if err := logCommand(&out).Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no crash reports") {
		t.Fatalf("log = %q", out.String())
	}
}

func TestLogCommand_Path(t *testing.T) {
	rec := withRecorder(t, false)
	var out bytes.Buffer
	cmd := logCommand(&out)
	cmd.SetArgs([]string{"--path"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), rec.CrashPath()) {
		t.Fatalf("path output = %q, want %q", out.String(), rec.CrashPath())
	}
}

func TestDebugRequested(t *testing.T) {
	cases := map[string]bool{
		"": false, "0": false, "false": false, "FALSE": false,
		"no": false, "off": false, "1": true, "true": true, "yes": true,
	}
	for v, want := range cases {
		if got := debugRequested(v); got != want {
			t.Errorf("debugRequested(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestModeLabel(t *testing.T) {
	cases := []struct {
		mode string
		tui  bool
		want string
	}{
		{"", true, "tui"},
		{"tui", true, "tui"},
		{"json", false, "json"},
		{"", false, "print"},
	}
	for _, c := range cases {
		if got := modeLabel(c.mode, c.tui); got != c.want {
			t.Errorf("modeLabel(%q, %v) = %q, want %q", c.mode, c.tui, got, c.want)
		}
	}
}

func TestNewRecorder_UsesHomeAndEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NOVA_DEBUG", "1")
	rec := newRecorder()
	want := filepath.Join(home, ".config", "nova", "crash.log")
	if rec.CrashPath() != want {
		t.Fatalf("CrashPath = %q, want %q", rec.CrashPath(), want)
	}
	rec.Debugf("hello")
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(rec.DebugPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "hello") {
		t.Fatalf("debug log = %q", data)
	}
}
