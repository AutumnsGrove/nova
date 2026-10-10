//go:build !windows

package bash

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/tool"
)

// waitGone fails unless pid disappears within a short grace.
func waitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid %d still alive", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// readPID reads a pid file written by a command.
func readPID(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse pid %q: %v", data, err)
	}
	return pid
}

// pidFile returns a fresh path and the environment that points a command at it.
func pidFile(t *testing.T) (string, map[string]string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pids")
	return path, map[string]string{"PIDS": path}
}

func TestBash_BSH18_TimeoutKillsGroup(t *testing.T) {
	pids, env := pidFile(t)
	cmd := `sleep 30 & echo $! > "$PIDS"; sleep 30`
	runBash(t, bashTool(t, WithEnv(env), WithTimeout(time.Second)), &tool.Context{WorkDir: t.TempDir()}, map[string]any{"command": cmd})
	waitGone(t, readPID(t, pids))
}

func TestBash_BSH19_TimeoutKillsGrandchildren(t *testing.T) {
	pids, env := pidFile(t)
	cmd := `sleep 30 & echo $! > "$PIDS"; wait`
	runBash(t, bashTool(t, WithEnv(env), WithTimeout(time.Second)), &tool.Context{WorkDir: t.TempDir()}, map[string]any{"command": cmd})
	waitGone(t, readPID(t, pids))
}

func TestBash_BSH20_SigkillAfterGrace(t *testing.T) {
	pids, env := pidFile(t)
	cmd := `trap '' TERM; echo $$ > "$PIDS"; sleep 30`
	began := time.Now()
	runBash(t, bashTool(t, WithEnv(env), WithTimeout(time.Second)), &tool.Context{WorkDir: t.TempDir()}, map[string]any{"command": cmd})
	if elapsed := time.Since(began); elapsed > 6*time.Second {
		t.Fatalf("took %v, want SIGKILL within the grace", elapsed)
	}
	waitGone(t, readPID(t, pids))
}

func TestBash_BSH23_BackgroundHoldingPipe(t *testing.T) {
	pids, env := pidFile(t)
	cmd := `sleep 30 & echo $! > "$PIDS"; echo done`
	began := time.Now()
	res := runBash(t, bashTool(t, WithEnv(env), WithTimeout(10*time.Second)), &tool.Context{WorkDir: t.TempDir()}, map[string]any{"command": cmd})
	if !strings.Contains(res.Content, "done") || !strings.Contains(res.Content, "background processes were still running") {
		t.Fatalf("result = %+v, want done and the background note", res)
	}
	if elapsed := time.Since(began); elapsed > 3*time.Second {
		t.Fatalf("took %v, want to return after the pipe grace", elapsed)
	}
	waitGone(t, readPID(t, pids))
}

func TestBash_BSH24_BackgroundRedirected(t *testing.T) {
	pids, env := pidFile(t)
	cmd := `sleep 30 >/dev/null 2>&1 & echo $! > "$PIDS"`
	began := time.Now()
	res := runBash(t, bashTool(t, WithEnv(env), WithTimeout(10*time.Second)), &tool.Context{WorkDir: t.TempDir()}, map[string]any{"command": cmd})
	if res.IsError || time.Since(began) > 3*time.Second {
		t.Fatalf("result = %+v after %v", res, time.Since(began))
	}
	waitGone(t, readPID(t, pids))
}

func TestBash_BSH25_NohupChild(t *testing.T) {
	pids, env := pidFile(t)
	cmd := `nohup sleep 30 >/dev/null 2>&1 & echo $! > "$PIDS"`
	runBash(t, bashTool(t, WithEnv(env), WithTimeout(10*time.Second)), &tool.Context{WorkDir: t.TempDir()}, map[string]any{"command": cmd})
	waitGone(t, readPID(t, pids))
}

// TestProperty_BSH_GroupKill spawns a random number of children, times the
// command out, and checks that every child it recorded is gone.
func TestProperty_BSH_GroupKill(t *testing.T) {
	const seed = 7
	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < 5; i++ {
		n := 1 + rng.Intn(5)
		pids, env := pidFile(t)
		cmd := fmt.Sprintf(`for i in $(seq 1 %d); do sleep 30 & echo $! >> "$PIDS"; done; sleep 30`, n)
		runBash(t, bashTool(t, WithEnv(env), WithTimeout(300*time.Millisecond)), &tool.Context{WorkDir: t.TempDir()}, map[string]any{"command": cmd})
		data, err := os.ReadFile(pids)
		if err != nil {
			t.Fatalf("seed %d iter %d: %v", seed, i, err)
		}
		for _, line := range strings.Fields(string(data)) {
			pid, err := strconv.Atoi(line)
			if err != nil {
				t.Fatalf("seed %d iter %d: bad pid %q", seed, i, line)
			}
			waitGone(t, pid)
		}
	}
}
