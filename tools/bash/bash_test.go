package bash

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
)

// requireBash skips a test when the shell is not installed.
func requireBash(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not on PATH")
	}
}

// bashTool returns the tool with the bash requirement checked.
func bashTool(t *testing.T, opts ...Option) tool.Tool {
	t.Helper()
	requireBash(t)
	return Bash(opts...)
}

// runBash runs the tool once and fails on a Go error.
func runBash(t *testing.T, tl tool.Tool, tctx *tool.Context, args map[string]any) tool.Result {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	res, err := tl.Run(context.Background(), raw, tctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

// command runs one command string in dir.
func command(t *testing.T, dir, cmd string, opts ...Option) tool.Result {
	t.Helper()
	return runBash(t, bashTool(t, opts...), &tool.Context{WorkDir: dir}, map[string]any{"command": cmd})
}

func TestBash_BSH01_Simple(t *testing.T) {
	res := command(t, t.TempDir(), "echo hi")
	if res.IsError || res.Content != "hi\n" {
		t.Fatalf("result = %+v, want hi", res)
	}
}

func TestBash_BSH02_NoOutput(t *testing.T) {
	res := command(t, t.TempDir(), "true")
	if res.IsError || res.Content != "(no output)" {
		t.Fatalf("result = %+v, want (no output)", res)
	}
}

func TestBash_BSH03_NonZeroExit(t *testing.T) {
	res := command(t, t.TempDir(), "exit 3")
	if !res.IsError || !strings.Contains(res.Content, "(exit code 3)") {
		t.Fatalf("result = %+v, want exit code 3", res)
	}
}

func TestBash_BSH04_Stderr(t *testing.T) {
	res := command(t, t.TempDir(), "echo e >&2")
	if !strings.Contains(res.Content, "e") {
		t.Fatalf("result = %+v, want stderr captured", res)
	}
}

func TestBash_BSH05_OrderPreserved(t *testing.T) {
	res := command(t, t.TempDir(), `printf '1\n'; printf '2\n' >&2; printf '3\n'`)
	if got := strings.TrimRight(res.Content, "\n"); got != "1\n2\n3" {
		t.Fatalf("content = %q, want the write order", got)
	}
}

func TestBash_BSH06_CommandNotFound(t *testing.T) {
	res := command(t, t.TempDir(), "nosuchcmd")
	if !res.IsError || !strings.Contains(res.Content, "(exit code 127)") {
		t.Fatalf("result = %+v, want exit 127", res)
	}
}

func TestBash_BSH07_WorkDir(t *testing.T) {
	dir := t.TempDir()
	res := command(t, dir, "pwd")
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if got := strings.TrimSpace(res.Content); got != want {
		t.Fatalf("pwd = %q, want %q", got, want)
	}
}

func TestBash_BSH08_MissingWorkDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	res := command(t, dir, "true")
	if !res.IsError || !strings.Contains(res.Content, "working directory") {
		t.Fatalf("result = %+v, want a missing-directory error", res)
	}
}

func TestBash_BSH09_WorkDirIsFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	res := command(t, file, "true")
	if !res.IsError || !strings.Contains(res.Content, "working directory") {
		t.Fatalf("result = %+v, want an error", res)
	}
}

func TestBash_BSH10_ShellNotFound(t *testing.T) {
	res := runBash(t, bashTool(t, WithShell(filepath.Join(t.TempDir(), "nope"))), &tool.Context{WorkDir: t.TempDir()}, map[string]any{"command": "true"})
	if !res.IsError || !strings.Contains(res.Content, "shell not found") {
		t.Fatalf("result = %+v, want shell not found", res)
	}
}

func TestBash_BSH11_StdinClosed(t *testing.T) {
	done := make(chan tool.Result, 1)
	go func() { done <- command(t, t.TempDir(), "cat") }()
	select {
	case res := <-done:
		if res.Content != "(no output)" {
			t.Fatalf("result = %+v, want no output", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cat hung, want stdin closed")
	}
}

func TestBash_BSH12_ReadDoesNotHang(t *testing.T) {
	done := make(chan tool.Result, 1)
	go func() { done <- command(t, t.TempDir(), "read x; echo got") }()
	select {
	case res := <-done:
		if strings.TrimSpace(res.Content) != "got" {
			t.Fatalf("result = %+v, want got", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("read hung, want EOF")
	}
}

func TestBash_BSH13_Environment(t *testing.T) {
	res := command(t, t.TempDir(), `echo $NOVA $TERM $NO_COLOR`)
	if got := strings.TrimSpace(res.Content); got != "1 dumb 1" {
		t.Fatalf("env = %q, want 1 dumb 1", got)
	}
}

func TestBash_BSH14_WithEnv(t *testing.T) {
	res := command(t, t.TempDir(), `echo $FOO`, WithEnv(map[string]string{"FOO": "bar"}))
	if strings.TrimSpace(res.Content) != "bar" {
		t.Fatalf("content = %q, want bar", res.Content)
	}
}

// Nova's own credentials are in the process environment, and a model-run
// command must not inherit them. Other variables and explicit extras survive.
func TestBash_BSH52_ScrubbedEnvironment(t *testing.T) {
	t.Setenv("NOVA_TEST_SECRET", "hunter2")
	t.Setenv("NOVA_TEST_KEPT", "ok")
	res := command(t, t.TempDir(), `echo "[$NOVA_TEST_SECRET][$NOVA_TEST_KEPT][$NOVA_TEST_EXPLICIT]"`,
		WithScrubEnv([]string{"NOVA_TEST_SECRET", "NOVA_TEST_EXPLICIT"}),
		WithEnv(map[string]string{"NOVA_TEST_EXPLICIT": "asked-for"}))
	if got := strings.TrimSpace(res.Content); got != "[][ok][asked-for]" {
		t.Fatalf("env = %q, want the secret gone and the others kept", got)
	}
}

func TestBash_BSH15_PagerDisabled(t *testing.T) {
	res := command(t, t.TempDir(), `echo $PAGER`)
	if strings.TrimSpace(res.Content) != "cat" {
		t.Fatalf("content = %q, want cat", res.Content)
	}
}

func TestBash_BSH16_KilledBySignal(t *testing.T) {
	res := command(t, t.TempDir(), "kill -9 $$")
	if !res.IsError || !strings.Contains(res.Content, "(terminated by signal SIGKILL)") {
		t.Fatalf("result = %+v, want SIGKILL", res)
	}
}

func TestBash_BSH17_Timeout(t *testing.T) {
	began := time.Now()
	res := command(t, t.TempDir(), "sleep 10", WithTimeout(time.Second))
	if !res.IsError || !strings.Contains(res.Content, "timed out") {
		t.Fatalf("result = %+v, want a timeout", res)
	}
	if elapsed := time.Since(began); elapsed > 5*time.Second {
		t.Fatalf("timeout took %v, want about 1s", elapsed)
	}
}

func TestBash_BSH21_CancelMidCommand(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	raw, _ := json.Marshal(map[string]any{"command": "sleep 10"})
	res, err := Bash(WithTimeout(30*time.Second)).Run(ctx, raw, &tool.Context{WorkDir: dir})
	if err != nil {
		t.Fatalf("Run returned a Go error: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "command cancelled") {
		t.Fatalf("result = %+v, want cancellation", res)
	}
}

func TestBash_BSH22_CancelBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	raw, _ := json.Marshal(map[string]any{"command": "sleep 10"})
	res, err := Bash().Run(ctx, raw, &tool.Context{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Run returned a Go error: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "command cancelled") {
		t.Fatalf("result = %+v, want cancellation", res)
	}
}

func TestBash_BSH26_UnderCap(t *testing.T) {
	res := command(t, t.TempDir(), `printf 'abc'`)
	if res.Content != "abc" || res.Truncated {
		t.Fatalf("result = %+v, want abc verbatim", res)
	}
}

func TestBash_BSH27_OverCap(t *testing.T) {
	res := command(t, t.TempDir(), "head -c 204800 /dev/zero | tr '\\0' a", WithMaxOutputBytes(1000))
	if !res.Truncated || !strings.Contains(res.Content, "bytes omitted") {
		t.Fatalf("result truncated=%v content=%q, want a marker", res.Truncated, res.Content[:min(80, len(res.Content))])
	}
	if len(res.Content) > 1000+64 {
		t.Fatalf("content length %d exceeds the cap plus marker", len(res.Content))
	}
}

func TestBash_BSH28_TailKept(t *testing.T) {
	res := command(t, t.TempDir(), "head -c 102400 /dev/zero | tr '\\0' x; echo ERROR_MARKER", WithMaxOutputBytes(1000))
	if !strings.Contains(res.Content, "ERROR_MARKER") {
		t.Fatalf("content = %q, want the tail kept", res.Content)
	}
}

func TestBash_BSH29_BoundedMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("prints 500 MiB")
	}
	res := command(t, t.TempDir(), "head -c 524288000 /dev/zero | tr '\\0' a", WithMaxOutputBytes(4096))
	if !res.Truncated {
		t.Fatalf("result = %+v, want truncation", res)
	}
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	if m.HeapAlloc > 20<<20 {
		t.Fatalf("heap use %d bytes, want under 20 MiB", m.HeapAlloc)
	}
}

func TestBash_BSH30_LongLine(t *testing.T) {
	res := command(t, t.TempDir(), "head -c 5242880 /dev/zero | tr '\\0' a", WithMaxOutputBytes(1000))
	if !res.Truncated || len(res.Content) > 1000+64 {
		t.Fatalf("result = %+v len=%d, want a bounded cut", res.Truncated, len(res.Content))
	}
}

func TestBash_BSH31_InvalidUTF8(t *testing.T) {
	res := command(t, t.TempDir(), `printf '\xff'`)
	if !strings.Contains(res.Content, "\uFFFD") || !utf8.ValidString(res.Content) {
		t.Fatalf("content = %q, want U+FFFD", res.Content)
	}
}

func TestBash_BSH32_NUL(t *testing.T) {
	res := command(t, t.TempDir(), "printf 'a\\0b'")
	if strings.ContainsRune(res.Content, 0) || !strings.Contains(res.Content, "\uFFFD") {
		t.Fatalf("content = %q, want NUL replaced", res.Content)
	}
}

func TestBash_BSH33_CarriageReturn(t *testing.T) {
	res := command(t, t.TempDir(), `printf 'a\rb\rc\n'`)
	if got := strings.TrimRight(res.Content, "\n"); got != "c" {
		t.Fatalf("content = %q, want c", got)
	}
}

func TestBash_BSH34_CRLF(t *testing.T) {
	res := command(t, t.TempDir(), `printf 'a\r\nb\r\n'`)
	if res.Content != "a\nb\n" {
		t.Fatalf("content = %q, want a\\nb\\n", res.Content)
	}
}

func TestBash_BSH35_Binary(t *testing.T) {
	res := command(t, t.TempDir(), "head -c 100 /dev/urandom")
	if res.IsError || !utf8.ValidString(res.Content) {
		t.Fatalf("result = %+v, want valid UTF-8", res)
	}
}

func TestBash_BSH36_EmptyCommand(t *testing.T) {
	res := command(t, t.TempDir(), "")
	if !res.IsError || !strings.Contains(res.Content, "command is required") {
		t.Fatalf("result = %+v, want a required-command error", res)
	}
}

func TestBash_BSH37_WhitespaceCommand(t *testing.T) {
	res := command(t, t.TempDir(), "   ")
	if res.IsError {
		t.Fatalf("result = %+v, want the shell to accept it", res)
	}
}

func TestBash_BSH38_TimeoutDefaults(t *testing.T) {
	for _, seconds := range []int{0, -1} {
		res := runBash(t, bashTool(t, WithTimeout(300*time.Millisecond)), &tool.Context{WorkDir: t.TempDir()}, map[string]any{"command": "sleep 5", "timeout": seconds})
		if !strings.Contains(res.Content, "timed out") {
			t.Fatalf("timeout %d: result = %+v, want the default used", seconds, res)
		}
	}
}

func TestBash_BSH39_TimeoutCapped(t *testing.T) {
	began := time.Now()
	res := runBash(t, bashTool(t, WithMaxTimeout(300*time.Millisecond)), &tool.Context{WorkDir: t.TempDir()}, map[string]any{"command": "sleep 5", "timeout": 99999})
	if !strings.Contains(res.Content, "timed out") || time.Since(began) > 3*time.Second {
		t.Fatalf("result = %+v, want the max timeout applied", res)
	}
}

// TestBash_BSH51_TimeoutOverflow checks a timeout so large that seconds times
// time.Second overflows int64 is capped, not wrapped into a negative duration
// that expires at once.
func TestBash_BSH51_TimeoutOverflow(t *testing.T) {
	for _, opts := range [][]Option{nil, {WithMaxTimeout(0)}} {
		res := runBash(t, bashTool(t, opts...), &tool.Context{WorkDir: t.TempDir()}, map[string]any{"command": "echo hi", "timeout": 9_300_000_000})
		if res.IsError || res.Content != "hi\n" {
			t.Fatalf("result = %+v, want the command to run", res)
		}
	}
}

func TestBash_BSH40_TimeoutStringRejected(t *testing.T) {
	reg := tool.NewRegistry()
	reg.MustRegister(bashTool(t))
	res := reg.Execute(context.Background(), "bash", json.RawMessage(`{"command":"true","timeout":"5"}`), &tool.Context{WorkDir: t.TempDir()})
	if !res.IsError {
		t.Fatalf("result = %+v, want schema validation to reject the string", res)
	}
}

// stepClock returns a time that advances 150ms on every read, so progress
// emissions are always due.
type stepClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(150 * time.Millisecond)
	return c.t
}

func TestBash_BSH41_Progress(t *testing.T) {
	var mu sync.Mutex
	var updates []string
	clk := &stepClock{}
	dir := t.TempDir()
	tctx := &tool.Context{WorkDir: dir, Emit: func(u tool.Update) {
		mu.Lock()
		updates = append(updates, u.Text)
		mu.Unlock()
	}}
	cmd := `for i in 1 2 3 4 5; do printf 'line%d\n' $i; sleep 0.12; done`
	runBash(t, bashTool(t, WithClock(clk), WithTimeout(5*time.Second)), tctx, map[string]any{"command": cmd})
	mu.Lock()
	defer mu.Unlock()
	if len(updates) < 1 {
		t.Fatal("want at least one progress update")
	}
	for _, u := range updates {
		if len(u) > maxProgressBytes {
			t.Fatalf("update of %d bytes exceeds %d", len(u), maxProgressBytes)
		}
	}
}

func TestBash_BSH42_ProgressNilEmit(t *testing.T) {
	res := runBash(t, bashTool(t), &tool.Context{WorkDir: t.TempDir()}, map[string]any{"command": "echo hi"})
	if res.IsError {
		t.Fatalf("result = %+v, want no panic", res)
	}
}

func TestBash_BSH43_Details(t *testing.T) {
	res := command(t, t.TempDir(), "exit 3")
	var d map[string]any
	if err := json.Unmarshal(res.Details, &d); err != nil {
		t.Fatalf("details: %v", err)
	}
	if d["exit_code"].(float64) != 3 || d["timed_out"].(bool) || d["cancelled"].(bool) || d["truncated"].(bool) {
		t.Fatalf("details = %v", d)
	}
	if _, ok := d["signal"]; !ok {
		t.Fatalf("details = %v, want a signal field", d)
	}
	res = runBash(t, bashTool(t, WithTimeout(time.Second)), &tool.Context{WorkDir: t.TempDir()}, map[string]any{"command": "sleep 5"})
	json.Unmarshal(res.Details, &d)
	if !d["timed_out"].(bool) || d["exit_code"] != nil {
		t.Fatalf("timeout details = %v, want timed_out and a null exit code", d)
	}
}

func TestBash_BSH44_Annotations(t *testing.T) {
	info := Bash().Info()
	if !info.Destructive || !info.OpenWorld || info.Parallel || info.ReadOnly {
		t.Fatalf("info = %+v, want destructive, open-world, not parallel", info)
	}
}

// recordShell writes a script that records its arguments into $RECORD.
func recordShell(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "record.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$RECORD\"\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

func TestBash_BSH45_LoginShell(t *testing.T) {
	for _, tc := range []struct {
		login bool
		flag  string
	}{{false, "-c"}, {true, "-lc"}} {
		record := filepath.Join(t.TempDir(), "args")
		res := runBash(t, bashTool(t, WithShell(recordShell(t)), WithLoginShell(tc.login), WithEnv(map[string]string{"RECORD": record})), &tool.Context{WorkDir: t.TempDir()}, map[string]any{"command": "echo x"})
		if res.IsError {
			t.Fatalf("result = %+v", res)
		}
		data, err := os.ReadFile(record)
		if err != nil {
			t.Fatalf("read record: %v", err)
		}
		if lines := strings.Split(string(data), "\n"); lines[0] != tc.flag {
			t.Fatalf("login=%v args = %q, want flag %s", tc.login, lines, tc.flag)
		}
	}
}

func TestBash_BSH46_NoLeaks(t *testing.T) {
	testkit.NoLeaks(t)
	for _, cmd := range []string{"echo hi", "exit 1", "sleep 0.05", "true"} {
		command(t, t.TempDir(), cmd, WithTimeout(5*time.Second))
	}
}

// rawExecute runs a command string without the JSON layer, for cases that need
// bytes JSON cannot carry.
func rawExecute(t *testing.T, dir, cmd string, opts ...Option) tool.Result {
	t.Helper()
	o := &options{
		timeout:        defaultTimeout,
		maxTimeout:     defaultMaxTimeout,
		maxOutputBytes: defaultMaxOutputBytes,
		shell:          "bash",
		clock:          realClock{},
		pipeGrace:      defaultPipeGrace,
	}
	for _, opt := range opts {
		opt(o)
	}
	return o.execute(context.Background(), &tool.Context{WorkDir: dir}, cmd, 0)
}

func TestBash_BSH47_NonUTF8Command(t *testing.T) {
	record := filepath.Join(t.TempDir(), "args")
	cmd := "echo " + string([]byte{0xff})
	res := rawExecute(t, t.TempDir(), cmd, WithShell(recordShell(t)), WithEnv(map[string]string{"RECORD": record}))
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	if !strings.Contains(string(data), cmd) {
		t.Fatalf("record = %q, want the command bytes unchanged", data)
	}
}

func TestBash_BSH48_VeryLongCommand(t *testing.T) {
	res := command(t, t.TempDir(), "echo "+strings.Repeat("a", 1<<20))
	if strings.Contains(res.Content, "panic") {
		t.Fatalf("result = %+v, want no panic", res)
	}
}

func TestBash_BSH49_Concurrent(t *testing.T) {
	tl := bashTool(t)
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := "echo " + string(rune('a'+i))
			raw, _ := json.Marshal(map[string]any{"command": cmd})
			res, err := tl.Run(context.Background(), raw, &tool.Context{WorkDir: t.TempDir()})
			if err != nil {
				errs <- err
				return
			}
			if strings.TrimSpace(res.Content) != string(rune('a'+i)) {
				errs <- &resultError{res.Content}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

type resultError struct{ got string }

func (e *resultError) Error() string { return "unexpected result: " + e.got }

func TestBash_BSH50_ExitAbove255(t *testing.T) {
	res := command(t, t.TempDir(), "exit 256")
	if res.IsError {
		t.Fatalf("result = %+v, want exit 256 to wrap to 0", res)
	}
}
