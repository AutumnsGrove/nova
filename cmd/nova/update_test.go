package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/update"
)

// runUpdateCommand runs the update command in-process with a fake runner.
func runUpdateCommand(t *testing.T, run updateFunc, args ...string) (int, string, string) {
	t.Helper()
	// The test binary lives in a go-build directory, which update refuses as a
	// target, so these tests run as if installed in a normal place.
	old := executable
	executable = func() (string, error) { return "/usr/local/bin/nova", nil }
	t.Cleanup(func() { executable = old })
	var out, errOut bytes.Buffer
	exit := 0
	cmd := updateCommand(&out, &errOut, &exit, run)
	cmd.SetArgs(args)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	if err := cmd.Execute(); err != nil {
		errOut.WriteString(err.Error())
		if exit == 0 {
			exit = 2
		}
	}
	return exit, out.String(), errOut.String()
}

func TestUpdate_UPD27_Help(t *testing.T) {
	exit, out, _ := runUpdateCommand(t, update.Run, "--help")
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	for _, flag := range []string{"--check", "--source", "--release", "--force", "--flavour"} {
		if !strings.Contains(out, flag) {
			t.Fatalf("update --help is missing %q:\n%s", flag, out)
		}
	}
}

func TestUpdate_UPD28_ExitCodes(t *testing.T) {
	fail := func(context.Context, update.Options) (update.Result, error) {
		return update.Result{}, context.DeadlineExceeded
	}
	exit, _, errOut := runUpdateCommand(t, fail)
	if exit != 1 {
		t.Fatalf("exit = %d, want 1", exit)
	}
	if !strings.Contains(errOut, "nova update:") {
		t.Fatalf("stderr = %q, want the nova update prefix", errOut)
	}
	ok := func(context.Context, update.Options) (update.Result, error) {
		return update.Result{Message: "nothing to do"}, nil
	}
	exit, out, _ := runUpdateCommand(t, ok)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if !strings.Contains(out, "nothing to do") {
		t.Fatalf("stdout = %q, want the result message", out)
	}
}

func TestUpdate_UPD29_FlagsAndEnvironment(t *testing.T) {
	t.Run("flags", func(t *testing.T) {
		t.Setenv("NOVA_RELEASE_BASE", "https://example.test/base")
		t.Setenv("NOVA_API_URL", "https://example.test/api")
		t.Setenv("NOVA_VERSION", "v9.9.9")
		t.Setenv("NOVA_FLAVOUR", "full")
		var got update.Options
		capture := func(_ context.Context, opt update.Options) (update.Result, error) {
			got = opt
			return update.Result{}, nil
		}
		exit, _, _ := runUpdateCommand(t, capture, "--check", "--source", "/tmp/checkout", "--flavour", "min")
		if exit != 0 {
			t.Fatalf("exit = %d", exit)
		}
		if got.Mode != update.Source || got.Dir != "/tmp/checkout" {
			t.Fatalf("Options = %+v, want a source run at /tmp/checkout", got)
		}
		if !got.Check || got.Flavour != "min" {
			t.Fatalf("Options = %+v, want --check and flavour min", got)
		}
		if got.BaseURL != "https://example.test/base" || got.APIURL != "https://example.test/api" || got.Version != "v9.9.9" {
			t.Fatalf("Options = %+v, want the NOVA_* environment", got)
		}
		if got.Target == "" {
			t.Fatal("Options.Target is empty")
		}
	})
	t.Run("flavour environment", func(t *testing.T) {
		t.Setenv("NOVA_FLAVOUR", "min")
		var got update.Options
		capture := func(_ context.Context, opt update.Options) (update.Result, error) {
			got = opt
			return update.Result{}, nil
		}
		if exit, _, _ := runUpdateCommand(t, capture, "--release"); exit != 0 {
			t.Fatalf("exit = %d", exit)
		}
		if got.Mode != update.Release || got.Flavour != "min" {
			t.Fatalf("Options = %+v, want a min release", got)
		}
	})
}

func TestUpdate_UPD30_ConflictingModes(t *testing.T) {
	called := false
	run := func(context.Context, update.Options) (update.Result, error) {
		called = true
		return update.Result{}, nil
	}
	exit, _, errOut := runUpdateCommand(t, run, "--source", "/tmp/checkout", "--release")
	if exit == 0 {
		t.Fatalf("exit = 0, want a usage error")
	}
	if called {
		t.Fatal("the conflicting flags still ran an update")
	}
	if !strings.Contains(errOut, "source") || !strings.Contains(errOut, "release") {
		t.Fatalf("stderr = %q, want both flags named", errOut)
	}
}
