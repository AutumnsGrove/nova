package testkit

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestClock_TKC01_DoesNotReadRealTime(t *testing.T) {
	start := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	c := NewFakeClock(start)
	began := time.Now()
	c.Advance(time.Hour)
	if got := c.Now(); !got.Equal(start.Add(time.Hour)) {
		t.Errorf("Now() = %v, want %v", got, start.Add(time.Hour))
	}
	if elapsed := time.Since(began); elapsed > time.Second {
		t.Errorf("Advance took %v, should be instant", elapsed)
	}
}

func TestClock_TKC02_AdvanceFiresInDeadlineOrder(t *testing.T) {
	start := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	c := NewFakeClock(start)
	var fired []time.Time
	c.onFire = func(deadline time.Time) { fired = append(fired, deadline) }

	c.After(3 * time.Second)
	c.After(1 * time.Second)
	c.After(2 * time.Second)
	c.Advance(3 * time.Second)

	want := []time.Time{start.Add(time.Second), start.Add(2 * time.Second), start.Add(3 * time.Second)}
	if len(fired) != len(want) {
		t.Fatalf("fired %d timers, want %d", len(fired), len(want))
	}
	for i := range want {
		if !fired[i].Equal(want[i]) {
			t.Errorf("fired[%d] = %v, want %v", i, fired[i], want[i])
		}
	}
}

func TestClock_TKC03_SleepReturnsOnAdvance(t *testing.T) {
	c := NewFakeClock(time.Unix(0, 0))
	done := make(chan error, 1)
	go func() { done <- c.Sleep(context.Background(), 5*time.Second) }()
	c.BlockUntil(t, 1)
	c.Advance(5 * time.Second)
	if err := <-done; err != nil {
		t.Errorf("Sleep = %v, want nil", err)
	}
}

func TestClock_TKC04_SleepHonoursCancellation(t *testing.T) {
	c := NewFakeClock(time.Unix(0, 0))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("Sleep = %v, want context.Canceled", err)
	}
}

func TestClock_TKC05_BlockUntilWaitsForSleepers(t *testing.T) {
	c := NewFakeClock(time.Unix(0, 0))
	done := make(chan struct{})
	go func() {
		_ = c.Sleep(context.Background(), time.Second)
		close(done)
	}()
	c.BlockUntil(t, 1)
	c.Advance(time.Second)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sleeper did not wake after Advance")
	}
}

func TestClock_TKC06_TimerAtExactTimeFires(t *testing.T) {
	c := NewFakeClock(time.Unix(0, 0))
	ch := c.After(5 * time.Second)
	c.Advance(5 * time.Second)
	select {
	case <-ch:
	default:
		t.Error("timer at exactly the new time did not fire")
	}
}
