// Package install installs, updates, removes and lists extensions from git
// repositories or local directories, and pins what it installed in a lockfile.
package install

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// gitWaitDelay is how long a cancelled git command may take to let go of its
// output pipes before they are closed under it. git clone starts helper
// processes that inherit them and are not killed with git itself.
const gitWaitDelay = 5 * time.Second

// maxGitOutputBytes bounds the git output kept for an error message.
const maxGitOutputBytes = 8 << 10

// commitRe matches a 7 to 40 character hex commit id.
var commitRe = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

// GitRunner runs git operations for the installer. Tests may inject a fake.
type GitRunner interface {
	Run(ctx context.Context, dir string, args ...string) (string, error)
}

// gitEnv is the environment that keeps git from ever waiting on a person.
func gitEnv() []string {
	return []string{"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=true"}
}

// execGit is the default GitRunner: it invokes the git binary.
type execGit struct{}

func (g execGit) Run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), gitEnv()...)
	cmd.WaitDelay = gitWaitDelay
	var out cappedBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(out.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(out.String()), nil
}

// cappedBuffer keeps the first maxGitOutputBytes written to it and discards the
// rest, so a chatty command cannot grow an error message without bound.
type cappedBuffer struct{ buf bytes.Buffer }

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := maxGitOutputBytes - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (b *cappedBuffer) String() string { return b.buf.String() }

// gitClone clones url into dir at ref. ref may be empty (the default branch),
// a branch, a tag, or a commit.
func gitClone(ctx context.Context, git GitRunner, url, ref, dir string) error {
	args := []string{"-c", "core.hooksPath=/dev/null", "clone"}
	if ref != "" && !looksLikeCommit(ref) {
		args = append(args, "--branch", ref, "--depth", "1")
	}
	args = append(args, "--", url, dir)
	if _, err := git.Run(ctx, "", args...); err != nil {
		return err
	}
	if looksLikeCommit(ref) {
		// The trailing "--" tells git ref is a revision, not a path to restore.
		_, err := git.Run(ctx, dir, "-c", "core.hooksPath=/dev/null", "checkout", "--detach", ref, "--")
		return err
	}
	return nil
}

// gitHead resolves the current commit SHA of a clone.
func gitHead(ctx context.Context, git GitRunner, dir string) (string, error) {
	return git.Run(ctx, dir, "rev-parse", "HEAD")
}

// findExtensions returns the extension directories in a clone: the root when
// it has a manifest, otherwise every immediate subdirectory that does.
func findExtensions(root string) []string {
	if pathExists(filepath.Join(root, "extension.yaml")) {
		return []string{root}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(root, e.Name())
		if pathExists(filepath.Join(dir, "extension.yaml")) {
			out = append(out, dir)
		}
	}
	return out
}

// looksLikeCommit reports whether ref is a 7-40 character hex commit.
func looksLikeCommit(ref string) bool { return commitRe.MatchString(ref) }

// pathExists reports whether path can be stat'd.
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
