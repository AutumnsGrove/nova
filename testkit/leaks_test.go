package testkit

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// leakRecorder is the part of testing.TB the leak check uses, so the check can
// be tested without failing the real test.
type leakRecorder struct {
	failed  bool
	message string
}

func (r *leakRecorder) Helper() {}

func (r *leakRecorder) Errorf(format string, args ...any) {
	r.failed = true
	r.message = fmt.Sprintf(format, args...)
}

// leakyGoroutine blocks forever so the leak check can find it by name.
func leakyGoroutine(block chan struct{}) {
	<-block
}

func TestLeaks_TKL01_PassesWhenClean(t *testing.T) {
	baseline := snapshotGoroutines()
	rec := &leakRecorder{}
	checkLeaks(rec, baseline)
	if rec.failed {
		t.Errorf("checkLeaks reported a leak on a clean test: %s", rec.message)
	}
}

func TestLeaks_TKL02_DetectsLeak(t *testing.T) {
	saved := leakTimeout
	leakTimeout = 50 * time.Millisecond
	t.Cleanup(func() { leakTimeout = saved })

	baseline := snapshotGoroutines()
	block := make(chan struct{})
	defer close(block)
	go leakyGoroutine(block)

	rec := &leakRecorder{}
	checkLeaks(rec, baseline)
	if !rec.failed {
		t.Fatal("checkLeaks did not report the leaked goroutine")
	}
	if !strings.Contains(rec.message, "leakyGoroutine") {
		t.Errorf("leak report should name the goroutine:\n%s", rec.message)
	}
}
