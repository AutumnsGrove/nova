package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// buildCache holds the one built binary per build-tag set, so a package's tests
// build nova at most once each.
var (
	buildMu    sync.Mutex
	buildCache = map[string]string{}
)

// builtNova builds the nova binary with the given build tags and returns its
// path. It is the L2 entry point for scenarios that need the real binary.
func builtNova(t *testing.T, tags ...string) string {
	t.Helper()
	key := strings.Join(tags, ",")
	buildMu.Lock()
	defer buildMu.Unlock()
	if bin, ok := buildCache[key]; ok {
		return bin
	}
	dir, err := os.MkdirTemp("", "nova-bin")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	bin := filepath.Join(dir, "nova")
	args := []string{"build"}
	if key != "" {
		args = append(args, "-tags", key)
	}
	args = append(args, "-o", bin, ".")
	out, err := exec.Command("go", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("build nova: %v\n%s", err, out)
	}
	buildCache[key] = bin
	return bin
}

// runBinary runs the built nova with args and the given extra environment,
// returning stdout, stderr, and the exit code.
func runBinary(t *testing.T, bin string, env []string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), env...)
	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("run %s %v: %v", bin, args, err)
		}
	}
	return out.String(), errOut.String(), code
}
