package diag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixedClock is a Now seam so reports and traces carry a deterministic time.
func fixedClock() time.Time {
	return time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
}

func newTestRecorder(t *testing.T, debug bool) (*Recorder, string) {
	t.Helper()
	dir := t.TempDir()
	r := New(Options{
		Dir:     dir,
		Version: "v1.2.3",
		Mode:    "tui",
		Model:   "openai/gpt-5",
		Debug:   debug,
		Now:     fixedClock,
		Stack:   func() []byte { return []byte("goroutine 1 [running]:\nmain.main()") },
	})
	return r, dir
}

func TestReport_WritesStackAndContext(t *testing.T) {
	r, dir := newTestRecorder(t, false)
	path, err := r.Report("boom", "streaming response")
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if path != filepath.Join(dir, CrashFile) {
		t.Fatalf("path = %q, want %q", path, filepath.Join(dir, CrashFile))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		Header,
		"time: 2026-10-16T12:00:00Z",
		"version: v1.2.3",
		"mode: tui",
		"model: openai/gpt-5",
		"operation: streaming response",
		"panic: boom",
		"main.main()",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("report missing %q:\n%s", want, text)
		}
	}
}

func TestReport_AppendsMostRecent(t *testing.T) {
	r, _ := newTestRecorder(t, false)
	if _, err := r.Report("first", "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Report("second", "two"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(r.CrashPath())
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), Header); n != 2 {
		t.Fatalf("want 2 reports, found %d:\n%s", n, data)
	}
	last := LatestReport(data)
	if !strings.Contains(string(last), "panic: second") {
		t.Fatalf("LatestReport = %q, want the second report", last)
	}
	if strings.Contains(string(last), "panic: first") {
		t.Fatalf("LatestReport leaked the first report: %q", last)
	}
}

func TestReport_UsesLatestContext(t *testing.T) {
	r, _ := newTestRecorder(t, false)
	r.SetModel("anthropic/claude")
	r.SetOp("compaction")
	path, err := r.Report("boom", "")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	text := string(data)
	if !strings.Contains(text, "model: anthropic/claude") || !strings.Contains(text, "operation: compaction") {
		t.Fatalf("report did not use the latest context:\n%s", text)
	}
}

func TestCatch_RecordsPanic(t *testing.T) {
	r, _ := newTestRecorder(t, false)
	func() {
		defer r.Catch("agent run")
		panic("kaboom")
	}()
	data, err := os.ReadFile(r.CrashPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "panic: kaboom") {
		t.Fatalf("Catch did not record the panic:\n%s", data)
	}
}

func TestDebugf_OnlyWhenEnabled(t *testing.T) {
	r, dir := newTestRecorder(t, false)
	r.Debugf("should not appear")
	if _, err := os.Stat(filepath.Join(dir, DebugFile)); !os.IsNotExist(err) {
		t.Fatalf("debug log written while disabled: %v", err)
	}
	r.SetDebug(true)
	r.Debugf("hello %s", "world")
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, DebugFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "hello world") {
		t.Fatalf("debug log = %q", data)
	}
	if !strings.Contains(string(data), "2026-10-16T12:00:00Z") {
		t.Fatalf("debug line has no timestamp: %q", data)
	}
}

func TestReport_NoDirIsAnError(t *testing.T) {
	r := New(Options{})
	if _, err := r.Report("boom", "op"); err == nil {
		t.Fatal("Report with no directory should error")
	}
}

func TestLatestReport_NoMarkerReturnsInput(t *testing.T) {
	data := []byte("not a report\n")
	if got := LatestReport(data); string(got) != string(data) {
		t.Fatalf("LatestReport = %q, want input", got)
	}
}

func TestDefault_CatchWithoutRecorderIsSilent(t *testing.T) {
	SetDefault(nil)
	defer SetDefault(nil)
	// A panic with no default recorder must still be recovered, not re-raised.
	func() {
		defer Catch("op")
		panic("quiet")
	}()
}

func TestPackageReport_UsesDefault(t *testing.T) {
	r, dir := newTestRecorder(t, false)
	SetDefault(r)
	defer SetDefault(nil)
	path, err := Report("boom", "command")
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if path != filepath.Join(dir, CrashFile) {
		t.Fatalf("path = %q, want %q", path, filepath.Join(dir, CrashFile))
	}
	SetDefault(nil)
	if _, err := Report("boom", "command"); err == nil {
		t.Fatal("Report with no default recorder should error")
	}
}
