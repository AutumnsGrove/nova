//go:build !windows

package proc_test

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
	"testing"

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
	if err := syscall.Kill(-cmd.Process.Pid, 0); err == nil {
		proc.Kill(cmd.Process.Pid)
		t.Fatal("process group still alive after Terminate")
	}
}
