//go:build !notui

package git

import (
	"context"
	"errors"
	"testing"
)

// fakeRunner answers git commands from a map keyed by the subcommand.
type fakeRunner struct {
	answers map[string][]byte
	errs    map[string]error
	calls   []string
}

func (f *fakeRunner) run(_ context.Context, _ string, args ...string) ([]byte, error) {
	key := args[0]
	if len(args) > 1 {
		key = args[0] + " " + args[1]
	}
	f.calls = append(f.calls, key)
	return f.answers[key], f.errs[key]
}

func TestGit_GIT01_BranchAndChanges(t *testing.T) {
	f := &fakeRunner{
		answers: map[string][]byte{
			"symbolic-ref --quiet": []byte("main\n"),
			"diff HEAD":            []byte("12\t3\ta.go\n2\t0\tb.go\n-\t-\timage.png\n"),
		},
	}
	info := (Exec{run: f.run}).Read(context.Background(), "/repo")
	if !info.OK || info.Branch != "main" || info.Added != 14 || info.Removed != 3 {
		t.Fatalf("info = %+v", info)
	}
	if got := info.Describe(); got != "(+14,-3)" {
		t.Fatalf("describe = %q", got)
	}
}

func TestGit_GIT02_DetachedHeadStillReportsChanges(t *testing.T) {
	f := &fakeRunner{
		answers: map[string][]byte{"diff HEAD": []byte("1\t1\ta.go\n")},
		errs:    map[string]error{"symbolic-ref --quiet": errors.New("detached")},
	}
	info := (Exec{run: f.run}).Read(context.Background(), "/repo")
	if !info.OK || info.Branch != "" || info.Added != 1 || info.Removed != 1 {
		t.Fatalf("info = %+v", info)
	}
}

func TestGit_GIT03_NotARepo(t *testing.T) {
	f := &fakeRunner{errs: map[string]error{
		"symbolic-ref --quiet": errors.New("not a repo"),
		"diff HEAD":            errors.New("not a repo"),
		"diff --numstat":       errors.New("not a repo"),
	}}
	info := (Exec{run: f.run}).Read(context.Background(), "/tmp")
	if info.OK || info.Branch != "" || info.Added != 0 {
		t.Fatalf("info = %+v, want a zero Info", info)
	}
	if got := info.Describe(); got != "" {
		t.Fatalf("describe = %q, want empty", got)
	}
}

func TestGit_GIT04_NoCommitsFallsBackToWorkingTreeDiff(t *testing.T) {
	f := &fakeRunner{
		answers: map[string][]byte{
			"symbolic-ref --quiet": []byte("main\n"),
			"diff --numstat":       []byte("4\t2\ta.go\n"),
		},
		errs: map[string]error{"diff HEAD": errors.New("no commits")},
	}
	info := (Exec{run: f.run}).Read(context.Background(), "/repo")
	if !info.OK || info.Added != 4 || info.Removed != 2 {
		t.Fatalf("info = %+v", info)
	}
}

func TestGit_GIT05_NoCwdIsNotOk(t *testing.T) {
	info := (Exec{run: func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("ran git with no cwd")
		return nil, nil
	}}).Read(context.Background(), "")
	if info.OK {
		t.Fatalf("info = %+v, want not ok", info)
	}
}

func TestGit_GIT06_Fake(t *testing.T) {
	f := &Fake{Info: Info{Branch: "x", Added: 1, OK: true}}
	if got := f.Read(context.Background(), "/r"); !got.OK || got.Branch != "x" || f.Calls != 1 {
		t.Fatalf("fake = %+v calls=%d", got, f.Calls)
	}
}
