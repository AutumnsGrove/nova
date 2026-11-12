//go:build !windows

package proc_test

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/proc"
)

func TestExitStatus_PRC01_Zero(t *testing.T) {
	if code, sig := proc.ExitStatus(nil); code != 0 || sig != 0 {
		t.Fatalf("ExitStatus(nil) = %d, %d; want 0, 0", code, sig)
	}
}

func TestExitStatus_PRC02_ExitCode(t *testing.T) {
	err := exec.Command("sh", "-c", "exit 7").Run()
	if code, sig := proc.ExitStatus(err); code != 7 || sig != 0 {
		t.Fatalf("ExitStatus = %d, %d; want 7, 0", code, sig)
	}
}

func TestExitStatus_PRC03_Signal(t *testing.T) {
	cmd := exec.Command("sh", "-c", "kill -TERM $$")
	err := cmd.Run()
	if code, sig := proc.ExitStatus(err); code != -1 || sig != int(syscall.SIGTERM) {
		t.Fatalf("ExitStatus = %d, %d; want -1, %d", code, sig, syscall.SIGTERM)
	}
}

func TestExitStatus_PRC04_NotAnExit(t *testing.T) {
	if code, sig := proc.ExitStatus(errors.New("start failed")); code != -1 || sig != 0 {
		t.Fatalf("ExitStatus = %d, %d; want -1, 0", code, sig)
	}
}

// A backgrounded child holds the output pipe open; killing the group is what
// lets Wait return instead of blocking until the child exits on its own.
func TestGroup_PRC05_KillReachesChildren(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "sh", "-c", "sleep 60 & wait")
	proc.SetGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	proc.Terminate(cmd.Process.Pid)
	err := cmd.Wait()
	if _, sig := proc.ExitStatus(err); sig != int(syscall.SIGTERM) {
		t.Fatalf("signal = %d, want SIGTERM (err %v)", sig, err)
	}
	if err := waitGone(func() error { return syscall.Kill(-cmd.Process.Pid, 0) }, 2*time.Second); err != nil {
		proc.Kill(cmd.Process.Pid)
		t.Fatalf("process group still alive after Terminate: %v", err)
	}
}

// waitGone polls until the group has no members left. A signalled child is
// still in its group until it is reaped, so one check straight after Terminate
// races the reaper: an idle machine reaps in microseconds and a loaded runner
// does not, which failed the accept gate (#94).
func waitGone(probe func() error, within time.Duration) error {
	deadline := time.Now().Add(within)
	for {
		err := probe()
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if !time.Now().Before(deadline) {
			if err == nil {
				return errors.New("the group still has a member")
			}
			return err
		}
		// Bounded poll: the deadline fails the test, so this is only the retry interval.
		time.Sleep(5 * time.Millisecond)
	}
}

func TestGroup_PRC06_WaitGoneToleratesAStraggler(t *testing.T) {
	calls := 0
	straggler := func() error {
		calls++
		if calls < 3 {
			return nil // a member is still there
		}
		return syscall.ESRCH
	}
	if err := waitGone(straggler, time.Second); err != nil {
		t.Fatalf("a straggler that reaps in time is not a failure: %v", err)
	}
	if calls < 3 {
		t.Fatalf("probed %d times, want the straggler to be polled until gone", calls)
	}
	err := waitGone(func() error { return nil }, 20*time.Millisecond)
	if err == nil {
		t.Fatal("a group that never empties has to fail, not hang")
	}
}
