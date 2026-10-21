package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/update"
)

// runUpdateWith runs the update command with a fake running-binary path and
// returns the options the runner saw (zero when it never ran).
func runUpdateWith(t *testing.T, exe string, args ...string) (opt update.Options, ran bool, exit int, out, errOut string) {
	t.Helper()
	old := executable
	executable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { executable = old })
	capture := func(_ context.Context, o update.Options) (update.Result, error) {
		opt, ran = o, true
		return update.Result{Message: "done"}, nil
	}
	var so, se bytes.Buffer
	cmd := updateCommand(&so, &se, &exit, capture)
	cmd.SetArgs(args)
	cmd.SetOut(&so)
	cmd.SetErr(&se)
	if err := cmd.Execute(); err != nil {
		se.WriteString(err.Error())
		if exit == 0 {
			exit = 2
		}
	}
	return opt, ran, exit, so.String(), se.String()
}

func TestUpdate_UPD33_TargetFlag(t *testing.T) {
	t.Setenv("NOVA_INSTALL_DIR", "")
	opt, ran, exit, _, _ := runUpdateWith(t, "/usr/local/bin/nova", "--target", "/opt/tools/nova")
	if !ran || exit != 0 || opt.Target != "/opt/tools/nova" {
		t.Fatalf("ran=%v exit=%d target=%q, want /opt/tools/nova", ran, exit, opt.Target)
	}
}

func TestUpdate_UPD34_InstallDirEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NOVA_INSTALL_DIR", dir)
	opt, ran, _, _, _ := runUpdateWith(t, "/usr/local/bin/nova")
	want := filepath.Join(dir, "nova")
	if !ran || opt.Target != want {
		t.Fatalf("target = %q, want %q", opt.Target, want)
	}
}

func TestUpdate_UPD35_FlagBeatsEnvBeatsExecutable(t *testing.T) {
	t.Setenv("NOVA_INSTALL_DIR", "/env/dir")
	opt, _, _, _, _ := runUpdateWith(t, "/usr/local/bin/nova", "--target", "/flag/nova")
	if opt.Target != "/flag/nova" {
		t.Fatalf("flag + env: target = %q, want the flag", opt.Target)
	}
	opt, _, _, _, _ = runUpdateWith(t, "/usr/local/bin/nova")
	if opt.Target != filepath.Join("/env/dir", "nova") {
		t.Fatalf("env + exe: target = %q, want the env dir", opt.Target)
	}
	t.Setenv("NOVA_INSTALL_DIR", "")
	opt, _, _, _, _ = runUpdateWith(t, "/usr/local/bin/nova")
	if opt.Target != "/usr/local/bin/nova" {
		t.Fatalf("exe only: target = %q, want the running binary", opt.Target)
	}
}

func TestUpdate_UPD36_RefusesAGoRunBinary(t *testing.T) {
	t.Setenv("NOVA_INSTALL_DIR", "")
	temp := filepath.Join(os.TempDir(), "go-build123", "b001", "exe", "nova")
	_, ran, exit, _, errOut := runUpdateWith(t, temp)
	if ran {
		t.Fatal("update ran against a go run temp binary")
	}
	if exit != 1 {
		t.Fatalf("exit = %d, want 1", exit)
	}
	for _, want := range []string{"go run", "--target", "NOVA_INSTALL_DIR"} {
		if !strings.Contains(errOut, want) {
			t.Fatalf("stderr = %q, want it to mention %q", errOut, want)
		}
	}
}

func TestUpdate_UPD37_ExplicitTargetOverridesTheGoRunGuard(t *testing.T) {
	t.Setenv("NOVA_INSTALL_DIR", "")
	temp := filepath.Join(os.TempDir(), "go-build123", "b001", "exe", "nova")
	opt, ran, exit, _, _ := runUpdateWith(t, temp, "--target", "/opt/nova")
	if !ran || exit != 0 || opt.Target != "/opt/nova" {
		t.Fatalf("ran=%v exit=%d target=%q: an explicit target must be honoured", ran, exit, opt.Target)
	}
}

func TestUpdate_UPD38_OutputNamesTheTarget(t *testing.T) {
	t.Setenv("NOVA_INSTALL_DIR", "")
	_, _, _, out, _ := runUpdateWith(t, "/usr/local/bin/nova", "--check")
	if !strings.Contains(out, "target: /usr/local/bin/nova") {
		t.Fatalf("stdout = %q, want it to name the target", out)
	}
}

// A running binary reached through a symlink must not be replaced by a regular
// file that takes the link's place while the real binary stays stale.
func TestUpdate_UPD44_RunningBinaryBehindASymlinkIsResolved(t *testing.T) {
	t.Setenv("NOVA_INSTALL_DIR", "")
	dir := t.TempDir()
	real := filepath.Join(dir, "versions", "nova-1")
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "nova")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	want, err := filepath.EvalSymlinks(real) // the temp directory itself may be a link
	if err != nil {
		t.Fatal(err)
	}
	opt, _, _, _, _ := runUpdateWith(t, link)
	if opt.Target != want {
		t.Fatalf("target = %q, want the file behind the link, %q", opt.Target, want)
	}
}
