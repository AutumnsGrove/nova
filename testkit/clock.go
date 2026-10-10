// Package testkit holds the fakes and helpers every Nova test uses: a fake
// clock, a scripted provider, a fault-injecting SSE server, filesystem and
// golden helpers, and a goroutine leak check. It is tested as strictly as
// product code, because everything else depends on it.
package testkit

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"
)

// Clock is the time seam. Production code takes a Clock so tests can drive
// time with a FakeClock instead of sleeping on real time.
type Clock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) error
	After(d time.Duration) <-chan time.Time
}

// realClock is the system clock.
type realClock struct{}

// RealClock returns a Clock backed by the wall clock.
func RealClock() Clock { return realClock{} }

func (realClock) Now() time.Time { return time.Now() }

func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// fakeTimer is one pending deadline.
type fakeTimer struct {
	deadline time.Time
	ch       chan time.Time
}

// FakeClock is a clock driven entirely by Advance. It never reads real time.
type FakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
	notify chan struct{}

	// onFire, when set, is called for each timer as it fires, in deadline
	// order. It lets a test observe fire order without racing goroutine
	// scheduling.
	onFire func(time.Time)
}

// NewFakeClock returns a clock stopped at start.
func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{now: start, notify: make(chan struct{})}
}

// Now returns the fake time, never the real one.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// After registers a timer due d after the current fake time.
func (c *FakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{deadline: c.now.Add(d), ch: make(chan time.Time, 1)}
	c.timers = append(c.timers, t)
	c.notifyLocked()
	return t.ch
}

// Sleep waits for d of fake time or for ctx to be cancelled.
func (c *FakeClock) Sleep(ctx context.Context, d time.Duration) error {
	ch := c.After(d)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-ch:
		return nil
	}
}

// Advance moves time forward and fires every timer due at or before the new
// time, in deadline order, then returns.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	sort.SliceStable(c.timers, func(i, j int) bool {
		return c.timers[i].deadline.Before(c.timers[j].deadline)
	})
	var due []*fakeTimer
	remaining := c.timers[:0]
	for _, t := range c.timers {
		if t.deadline.After(now) {
			remaining = append(remaining, t)
		} else {
			due = append(due, t)
		}
	}
	c.timers = remaining
	onFire := c.onFire
	c.notifyLocked()
	c.mu.Unlock()

	for _, t := range due {
		if onFire != nil {
			onFire(t.deadline)
		}
		t.ch <- now
	}
}

// BlockUntil waits until n timers are pending, so a test can avoid racing a
// goroutine that is about to call Sleep.
func (c *FakeClock) BlockUntil(t testing.TB, n int) {
	t.Helper()
	for {
		c.mu.Lock()
		pending := len(c.timers)
		notify := c.notify
		c.mu.Unlock()
		if pending >= n {
			return
		}
		<-notify
	}
}

// notifyLocked wakes every BlockUntil waiter. The caller holds c.mu.
func (c *FakeClock) notifyLocked() {
	close(c.notify)
	c.notify = make(chan struct{})
}
