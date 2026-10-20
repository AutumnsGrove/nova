//go:build !notui

// Package git reads the branch and working-tree line counts the status region
// shows. It shells out to git with a short deadline and treats every failure as
// "no git here", so a missing git or a non-repo never breaks the UI. It is a
// seam: tests supply a fake reader, and only the test that guards the git
// arguments runs the real binary.
package git

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Info is one read of a working tree.
type Info struct {
	Branch  string
	Added   int
	Removed int
	OK      bool
}

// Reader reads the git state for a directory.
type Reader interface {
	Read(ctx context.Context, cwd string) Info
}

// Fake is a Reader for tests.
type Fake struct {
	Info  Info
	Calls int
}

// Read returns the configured info and counts the call.
func (f *Fake) Read(context.Context, string) Info {
	f.Calls++
	return f.Info
}

// Detect returns the real reader, with the deadline a status refresh can afford.
func Detect() Reader { return Exec{Timeout: time.Second} }

// runGit runs one git command and returns its stdout.
type runGit func(ctx context.Context, cwd string, args ...string) ([]byte, error)

// Exec reads with the git binary.
type Exec struct {
	Timeout time.Duration
	run     runGit
}

// Read asks git for the branch and the line counts against HEAD. A detached
// HEAD has no branch but still reports changes; a directory outside a work tree
// is not ok.
func (e Exec) Read(ctx context.Context, cwd string) Info {
	if cwd == "" {
		return Info{}
	}
	run := e.run
	if run == nil {
		run = execGit
	}
	if e.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.Timeout)
		defer cancel()
	}
	branchOut, branchErr := run(ctx, cwd, "symbolic-ref", "--quiet", "--short", "HEAD")
	numstat, diffErr := run(ctx, cwd, "diff", "HEAD", "--numstat")
	if diffErr != nil {
		// A repo with no commits has no HEAD to diff against.
		numstat, diffErr = run(ctx, cwd, "diff", "--numstat")
	}
	if branchErr != nil && diffErr != nil {
		return Info{}
	}
	info := Info{OK: true, Branch: strings.TrimSpace(string(branchOut))}
	info.Added, info.Removed = parseNumstat(numstat)
	return info
}

// execGit runs git with no optional locks, so a status refresh never races a
// concurrent git command in the user's own terminal. The folder may be one the
// user never trusted, and a repository's config can name programs that git runs
// while reading it, so the file-system monitor is switched off here.
func execGit(ctx context.Context, cwd string, args ...string) ([]byte, error) {
	full := append([]string{"-C", cwd, "--no-optional-locks", "-c", "core.fsmonitor=false"}, args...)
	return exec.CommandContext(ctx, "git", full...).Output()
}

// parseNumstat sums the added and removed columns. A binary file is marked with
// "-" and is skipped; a malformed line is ignored rather than failing the read.
func parseNumstat(out []byte) (added, removed int) {
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			continue
		}
		a, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		r, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		added += a
		removed += r
	}
	return added, removed
}

// Describe renders the changes segment, e.g. (+3,-1).
func (i Info) Describe() string {
	if !i.OK {
		return ""
	}
	return fmt.Sprintf("(+%d,-%d)", i.Added, i.Removed)
}
