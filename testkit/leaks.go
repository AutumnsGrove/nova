package testkit

import (
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// leakTimeout is how long new goroutines get to finish before they count as
// leaked. It is a var so the leak tests do not have to wait a full second.
var leakTimeout = time.Second

// leakTB is the slice of testing.TB the check needs, so tests can pass a fake
// and observe a failure without failing the real test.
type leakTB interface {
	Helper()
	Errorf(format string, args ...any)
}

// NoLeaks snapshots the running goroutines and, in a cleanup, fails the test if
// any new ones are still running after leakTimeout.
func NoLeaks(t testing.TB) {
	t.Helper()
	baseline := snapshotGoroutines()
	t.Cleanup(func() { checkLeaks(t, baseline) })
}

// checkLeaks waits for the goroutine set to return to baseline, then fails
// listing the stacks of whatever is left.
func checkLeaks(t leakTB, baseline map[uint64]string) {
	t.Helper()
	deadline := time.Now().Add(leakTimeout)
	for {
		leaked := leakedStacks(snapshotGoroutines(), baseline)
		if len(leaked) == 0 {
			return
		}
		if time.Now().After(deadline) {
			sort.Strings(leaked)
			t.Errorf("testkit: %d goroutine(s) leaked:\n\n%s", len(leaked), strings.Join(leaked, "\n\n"))
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// leakedStacks returns the stacks present now but not at baseline.
func leakedStacks(current, baseline map[uint64]string) []string {
	var leaked []string
	for id, stack := range current {
		if _, ok := baseline[id]; !ok {
			leaked = append(leaked, stack)
		}
	}
	return leaked
}

// snapshotGoroutines maps each running goroutine's id to its full stack.
func snapshotGoroutines() map[uint64]string {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	if n == len(buf) {
		buf = make([]byte, 2*len(buf))
		n = runtime.Stack(buf, true)
	}
	out := make(map[uint64]string)
	for _, block := range strings.Split(string(buf[:n]), "\n\n") {
		if id, ok := goroutineID(block); ok {
			out[id] = block
		}
	}
	return out
}

// goroutineID reads the id from a stack's "goroutine N [state]:" header.
func goroutineID(block string) (uint64, bool) {
	header := block
	if i := strings.IndexByte(block, '\n'); i >= 0 {
		header = block[:i]
	}
	header = strings.TrimPrefix(header, "goroutine ")
	if i := strings.IndexByte(header, ' '); i >= 0 {
		header = header[:i]
	}
	id, err := strconv.ParseUint(header, 10, 64)
	return id, err == nil
}
