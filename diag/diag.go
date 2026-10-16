// Package diag records crash reports and optional routine debug traces. It is
// deliberately stdlib-only so any layer may import it, and it records a short
// operation label rather than raw arguments, so a report never contains a
// secret.
package diag

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// Header marks the start of a crash report so a viewer can find the most recent
// one in an appended file.
const Header = "=== nova crash report ==="

// The file names under the configuration directory.
const (
	CrashFile = "crash.log"
	DebugFile = "debug.log"
)

// Options configures a Recorder. Dir, when set, is the directory that holds
// CrashFile and DebugFile; when empty, reports cannot be written.
type Options struct {
	Dir     string
	Version string
	Mode    string
	Model   string
	Debug   bool

	// Now and Stack are seams for tests.
	Now   func() time.Time
	Stack func() []byte
}

// Recorder writes crash reports and, when debug is on, routine trace lines. It
// is safe for concurrent use, because the agent loop, the provider, and the
// extension host all run on their own goroutines.
type Recorder struct {
	opt Options

	mu        sync.Mutex
	op        string
	model     string
	mode      string
	debug     bool
	crashPath string
	debugPath string
	debugFile *os.File
}

// New builds a Recorder. The zero Options is valid: it records nothing and is
// useful only to keep callers from branching on nil.
func New(opt Options) *Recorder {
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.Stack == nil {
		opt.Stack = func() []byte { return debug.Stack() }
	}
	r := &Recorder{
		opt:   opt,
		op:    "",
		model: opt.Model,
		mode:  opt.Mode,
		debug: opt.Debug,
	}
	if opt.Dir != "" {
		r.crashPath = filepath.Join(opt.Dir, CrashFile)
		r.debugPath = filepath.Join(opt.Dir, DebugFile)
	}
	return r
}

// CrashPath is the crash file path, or empty when no directory was configured.
func (r *Recorder) CrashPath() string { return r.crashPath }

// DebugPath is the debug file path, or empty when no directory was configured.
func (r *Recorder) DebugPath() string { return r.debugPath }

// SetOp records the last operation for the next crash report.
func (r *Recorder) SetOp(op string) {
	r.mu.Lock()
	r.op = op
	r.mu.Unlock()
}

// SetModel records the active model for the next crash report.
func (r *Recorder) SetModel(model string) {
	r.mu.Lock()
	r.model = model
	r.mu.Unlock()
}

// SetMode records the active mode for the next crash report.
func (r *Recorder) SetMode(mode string) {
	r.mu.Lock()
	r.mode = mode
	r.mu.Unlock()
}

// SetDebug turns routine tracing on or off.
func (r *Recorder) SetDebug(on bool) {
	r.mu.Lock()
	r.debug = on
	r.mu.Unlock()
}

// Report formats and appends one crash report and returns the file path, so the
// caller can tell the user where to look. op overrides any operation set with
// SetOp; when empty, the last operation is used.
func (r *Recorder) Report(v any, op string) (string, error) {
	if r == nil || r.crashPath == "" {
		return "", errors.New("diag: no crash log directory")
	}
	r.mu.Lock()
	if op != "" {
		r.op = op
	}
	text := r.format(v)
	r.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(r.crashPath), 0o750); err != nil {
		return r.crashPath, err
	}
	f, err := os.OpenFile(r.crashPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return r.crashPath, err
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		return r.crashPath, err
	}
	return r.crashPath, nil
}

// format renders one report. It is called with the lock held.
func (r *Recorder) format(v any) string {
	var b strings.Builder
	mode, model := r.mode, r.model
	fmt.Fprintln(&b, Header)
	fmt.Fprintf(&b, "time: %s\n", r.opt.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "version: %s\n", orNone(r.opt.Version))
	fmt.Fprintf(&b, "mode: %s\n", orNone(mode))
	fmt.Fprintf(&b, "model: %s\n", orNone(model))
	fmt.Fprintf(&b, "operation: %s\n", orNone(r.op))
	fmt.Fprintf(&b, "panic: %v\n", v)
	if stack := bytes.TrimRight(r.opt.Stack(), "\n"); len(stack) > 0 {
		fmt.Fprintf(&b, "stack:\n%s\n", stack)
	}
	return b.String()
}

// Catch recovers a panic in the calling goroutine and records it with this
// Recorder. Use it as `defer r.Catch("operation")` at the top of a goroutine.
func (r *Recorder) Catch(op string) {
	if v := recover(); v != nil {
		_, _ = r.Report(v, op)
	}
}

// Debugf appends a timestamped trace line when debug is on. It never creates
// the debug file until the first line is written.
func (r *Recorder) Debugf(format string, args ...any) {
	if r == nil || r.debugPath == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.debug {
		return
	}
	if r.debugFile == nil {
		if err := os.MkdirAll(filepath.Dir(r.debugPath), 0o750); err != nil {
			return
		}
		f, err := os.OpenFile(r.debugPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return
		}
		r.debugFile = f
	}
	line := fmt.Sprintf("%s %s\n", r.opt.Now().UTC().Format(time.RFC3339), fmt.Sprintf(format, args...))
	_, _ = r.debugFile.WriteString(line)
}

// Close releases the debug file, if one is open.
func (r *Recorder) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.debugFile == nil {
		return nil
	}
	err := r.debugFile.Close()
	r.debugFile = nil
	return err
}

// LatestReport returns the last crash report in data, or data unchanged when it
// holds no report marker.
func LatestReport(data []byte) []byte {
	i := bytes.LastIndex(data, []byte(Header))
	if i < 0 {
		return data
	}
	return data[i:]
}

// orNone renders an empty field as "(unset)" so a report never has a blank,
// ambiguous line.
func orNone(s string) string {
	if s == "" {
		return "(unset)"
	}
	return s
}

// def is the process-wide recorder. It is set once during startup and read by
// goroutines that have no natural owner to pass a Recorder to.
var (
	defMu sync.RWMutex
	def   *Recorder
)

// SetDefault installs the process-wide recorder. Passing nil clears it.
func SetDefault(r *Recorder) {
	defMu.Lock()
	def = r
	defMu.Unlock()
}

// Default returns the process-wide recorder, or nil before startup sets one.
func Default() *Recorder {
	defMu.RLock()
	r := def
	defMu.RUnlock()
	return r
}

// SetOp records the last operation on the default recorder, if any. The
// package-level setters keep callers from branching on a nil recorder.
func SetOp(op string) {
	if r := Default(); r != nil {
		r.SetOp(op)
	}
}

// SetModel records the active model on the default recorder, if any.
func SetModel(model string) {
	if r := Default(); r != nil {
		r.SetModel(model)
	}
}

// SetMode records the active mode on the default recorder, if any.
func SetMode(mode string) {
	if r := Default(); r != nil {
		r.SetMode(mode)
	}
}

// SetDebug turns routine tracing on the default recorder on or off, if any.
func SetDebug(on bool) {
	if r := Default(); r != nil {
		r.SetDebug(on)
	}
}

// Catch recovers a panic in the calling goroutine and records it with the
// default recorder. With no recorder it prints a one-line error to stderr, so a
// panic is never silent. Use it as `defer diag.Catch("operation")`.
func Catch(op string) {
	v := recover()
	if v == nil {
		return
	}
	if r := Default(); r != nil {
		_, _ = r.Report(v, op)
		return
	}
	fmt.Fprintf(os.Stderr, "nova: panic in %s: %v\n", op, v)
}

// Report records a panic through the default recorder and returns the crash
// file path. It is the package-level form of Recorder.Report for code that has
// no recorder to hand.
func Report(v any, op string) (string, error) {
	r := Default()
	if r == nil {
		return "", errors.New("diag: no default recorder")
	}
	return r.Report(v, op)
}

// Tracef writes a routine trace line through the default recorder when it has
// debug on. It is a no-op otherwise.
func Tracef(format string, args ...any) {
	if r := Default(); r != nil {
		r.Debugf(format, args...)
	}
}
