package main

import (
	"bytes"
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
