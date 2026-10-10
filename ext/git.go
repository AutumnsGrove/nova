package ext

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GitRunner runs git operations for the installer. Tests may inject a fake.
type GitRunner interface {
	Run(ctx context.Context, dir string, args ...string) (string, error)
}

// gitEnv is the environment that keeps git non-interactive and disables hooks.
func gitEnv() []string {
	return []string{"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=true"}
}

// execGit is the default GitRunner: it invokes the git binary.
type execGit struct{}

func (g execGit) Run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), gitEnv()...)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(out.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(out.String()), nil
}

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
	if ref != "" && looksLikeCommit(ref) {
		if _, err := git.Run(ctx, dir, "-c", "core.hooksPath=/dev/null", "checkout", "--", ref); err != nil {
			return err
		}
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
func looksLikeCommit(ref string) bool {
	if len(ref) < 7 || len(ref) > 40 {
		return false
	}
	for _, r := range ref {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

// pathExists reports whether path can be stat'd.
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
