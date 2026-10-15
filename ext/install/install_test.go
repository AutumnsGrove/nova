package install

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// runGit runs one git command in dir and returns its stdout.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// gitRepo creates a committed repository from files (path -> content).
func gitRepo(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.email", "t@example.com")
	runGit(t, dir, "config", "user.name", "t")
	writeRepoFiles(t, dir, files)
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "init")
	return dir, strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))
}

// writeRepoFiles writes files below dir, creating parents.
func writeRepoFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// newTestInstaller builds an installer under a temp root that confirms.
func newTestInstaller(t *testing.T) (*Installer, string, string) {
	t.Helper()
	root := t.TempDir()
	lock := filepath.Join(root, "extensions.lock")
	return NewInstaller(InstallOptions{
		Root:     root,
		LockPath: lock,
		Confirm:  func(Plan) bool { return true },
	}), root, lock
}

const echoManifest = "name: hello\nversion: 1.0.0\ndescription: x\nrun: [echo]\n"

func TestInstall_INS01_ParseForms(t *testing.T) {
	forms := []string{
		"github.com/a/b@v1", "a/b", "https://host/a/b.git", "git@host:a/b.git",
	}
	for _, f := range forms {
		if _, err := ParseSource(f); err != nil {
			t.Errorf("ParseSource(%q) = %v", f, err)
		}
	}
	local := t.TempDir()
	if _, err := ParseSource(local); err != nil {
		t.Errorf("ParseSource(local) = %v", err)
	}
}

func TestInstall_INS02_RejectedSources(t *testing.T) {
	for _, f := range []string{
		"file:///x", "ext::sh -c x", "https://user:pass@host/a/b.git",
		"-owner/repo", "owner/repo@-bad",
	} {
		if _, err := ParseSource(f); err == nil {
			t.Errorf("ParseSource(%q) accepted", f)
		}
	}
}

func TestInstall_INS03_RefForms(t *testing.T) {
	short := strings.Repeat("a", 7)
	full := strings.Repeat("b", 40)
	for _, ref := range []string{"v1.0.0", "main", short, full} {
		src, err := ParseSource("owner/repo@" + ref)
		if err != nil || src.Ref != ref {
			t.Errorf("ref %q: %v %+v", ref, err, src)
		}
	}
	if _, err := ParseSource("owner/repo@bad ref"); err == nil {
		t.Error("ref with a space accepted")
	}
}

func TestInstall_INS04_LocalGit(t *testing.T) {
	repo, _ := gitRepo(t, map[string]string{"extension.yaml": echoManifest})
	inst, root, lock := newTestInstaller(t)
	if _, err := inst.Install(context.Background(), Source{URL: repo, Ref: "main"}); err != nil {
		t.Fatal(err)
	}
	if !pathExists(filepath.Join(root, "hello", "extension.yaml")) {
		t.Fatal("extension not installed")
	}
	entries, _ := readLock(lock)
	if entries["hello"].Commit == "" {
		t.Fatalf("no lock entry: %+v", entries)
	}
}

func TestInstall_INS05_SeveralExtensions(t *testing.T) {
	repo, _ := gitRepo(t, map[string]string{
		"a/extension.yaml": "name: aaa\nversion: 1.0.0\ndescription: x\nrun: [echo]\n",
		"b/extension.yaml": "name: bbb\nversion: 1.0.0\ndescription: x\nrun: [echo]\n",
	})
	inst, root, _ := newTestInstaller(t)
	if _, err := inst.Install(context.Background(), Source{URL: repo, Ref: "main"}); err != nil {
		t.Fatal(err)
	}
	if !pathExists(filepath.Join(root, "aaa")) || !pathExists(filepath.Join(root, "bbb")) {
		t.Fatal("not all extensions installed")
	}
}

func TestInstall_INS06_NoManifest(t *testing.T) {
	repo, _ := gitRepo(t, map[string]string{"readme.md": "hi"})
	inst, root, lock := newTestInstaller(t)
	if _, err := inst.Install(context.Background(), Source{URL: repo, Ref: "main"}); err == nil {
		t.Fatal("expected an error")
	}
	assertNothingInstalled(t, root, lock)
}

func TestInstall_INS07_InvalidManifest(t *testing.T) {
	repo, _ := gitRepo(t, map[string]string{"extension.yaml": "name: Bad\nversion: 1.0.0\ndescription: x\nrun: [echo]\n"})
	inst, root, lock := newTestInstaller(t)
	if _, err := inst.Install(context.Background(), Source{URL: repo, Ref: "main"}); err == nil {
		t.Fatal("expected an error")
	}
	assertNothingInstalled(t, root, lock)
}

func TestInstall_INS08_BuildRuns(t *testing.T) {
	repo, _ := gitRepo(t, map[string]string{"extension.yaml": "name: hello\nversion: 1.0.0\ndescription: x\nrun: [echo]\nbuild: [\"sh\", \"-c\", \"touch built\"]\n"})
	inst, root, _ := newTestInstaller(t)
	if _, err := inst.Install(context.Background(), Source{URL: repo, Ref: "main"}); err != nil {
		t.Fatal(err)
	}
	if !pathExists(filepath.Join(root, "hello", "built")) {
		t.Fatal("build artifact missing")
	}
}

func TestInstall_INS09_BuildFails(t *testing.T) {
	repo, _ := gitRepo(t, map[string]string{"extension.yaml": "name: hello\nversion: 1.0.0\ndescription: x\nrun: [echo]\nbuild: [\"sh\", \"-c\", \"exit 1\"]\n"})
	inst, root, lock := newTestInstaller(t)
	if _, err := inst.Install(context.Background(), Source{URL: repo, Ref: "main"}); err == nil {
		t.Fatal("expected a build failure")
	}
	assertNothingInstalled(t, root, lock)
}

func TestInstall_INS10_ConfirmRefused(t *testing.T) {
	repo, _ := gitRepo(t, map[string]string{"extension.yaml": echoManifest})
	inst, root, lock := newTestInstaller(t)
	inst.opt.Confirm = func(Plan) bool { return false }
	if _, err := inst.Install(context.Background(), Source{URL: repo, Ref: "main"}); err == nil {
		t.Fatal("expected refusal")
	}
	assertNothingInstalled(t, root, lock)
}

func TestInstall_INS11_ConfirmNil(t *testing.T) {
	repo, _ := gitRepo(t, map[string]string{"extension.yaml": echoManifest})
	inst, root, lock := newTestInstaller(t)
	inst.opt.Confirm = nil
	if _, err := inst.Install(context.Background(), Source{URL: repo, Ref: "main"}); err == nil {
		t.Fatal("expected refusal")
	}
	assertNothingInstalled(t, root, lock)
}

func TestInstall_INS12_PlanContents(t *testing.T) {
	repo, commit := gitRepo(t, map[string]string{"extension.yaml": "name: hello\nversion: 1.0.0\ndescription: x\nrun: [echo]\ncapabilities: [tools]\nbuild: [\"sh\", \"-c\", \"true\"]\n"})
	inst, _, _ := newTestInstaller(t)
	var got Plan
	inst.opt.Confirm = func(p Plan) bool { got = p; return true }
	if _, err := inst.Install(context.Background(), Source{URL: repo, Ref: "main"}); err != nil {
		t.Fatal(err)
	}
	if got.Name != "hello" || got.Commit != commit || len(got.Run) != 1 || len(got.Build) != 3 || len(got.Capabilities) != 1 {
		t.Fatalf("plan = %+v", got)
	}
}

func TestInstall_INS13_NameCollision(t *testing.T) {
	repo1, _ := gitRepo(t, map[string]string{"extension.yaml": echoManifest})
	repo2, _ := gitRepo(t, map[string]string{"extension.yaml": echoManifest})
	inst, _, _ := newTestInstaller(t)
	if _, err := inst.Install(context.Background(), Source{URL: repo1, Ref: "main"}); err != nil {
		t.Fatal(err)
	}
	if _, err := inst.Install(context.Background(), Source{URL: repo2, Ref: "main"}); err == nil {
		t.Fatal("expected a collision error")
	}
}

func TestInstall_INS14_BuiltinName(t *testing.T) {
	repo, _ := gitRepo(t, map[string]string{"extension.yaml": "name: read\nversion: 1.0.0\ndescription: x\nrun: [echo]\n"})
	inst, _, lock := newTestInstaller(t)
	if _, err := inst.Install(context.Background(), Source{URL: repo, Ref: "main"}); err == nil {
		t.Fatal("expected a built-in conflict")
	}
	assertNothingInstalled(t, filepath.Dir(lock), lock)
}

func TestInstall_INS15_CloneTimeout(t *testing.T) {
	inst, _, lock := newTestInstaller(t)
	inst.git = timeoutGit{}
	inst.opt.Git = timeoutGit{}
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	if _, err := inst.Install(ctx, Source{URL: "https://example.invalid/x"}); err == nil {
		t.Fatal("expected a timeout error")
	}
	assertNothingInstalled(t, filepath.Dir(lock), lock)
}

// timeoutGit is a GitRunner that always fails.
type timeoutGit struct{}

func (timeoutGit) Run(context.Context, string, ...string) (string, error) {
	return "", errors.New("timeout")
}

func TestInstall_INS16_GitHooksDisabled(t *testing.T) {
	repo, _ := gitRepo(t, map[string]string{"extension.yaml": echoManifest})
	hookDir := filepath.Join(repo, ".git", "hooks")
	marker := filepath.Join(repo, "HOOKED")
	_ = os.MkdirAll(hookDir, 0o755)
	_ = os.WriteFile(filepath.Join(hookDir, "post-checkout"), []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755)
	inst, _, _ := newTestInstaller(t)
	if _, err := inst.Install(context.Background(), Source{URL: repo, Ref: "main"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a post-checkout hook ran during install")
	}
}

func TestInstall_INS17_GitNeverPrompts(t *testing.T) {
	found := false
	for _, kv := range gitEnv() {
		if kv == "GIT_TERMINAL_PROMPT=0" {
			found = true
		}
	}
	if !found {
		t.Fatalf("gitEnv = %v", gitEnv())
	}
}

func TestInstall_INS18_CommitPinned(t *testing.T) {
	repo, commit := gitRepo(t, map[string]string{"extension.yaml": echoManifest})
	inst, _, lock := newTestInstaller(t)
	if _, err := inst.Install(context.Background(), Source{URL: repo, Ref: "main"}); err != nil {
		t.Fatal(err)
	}
	entries, _ := readLock(lock)
	if entries["hello"].Commit != commit {
		t.Fatalf("commit = %q, want %q", entries["hello"].Commit, commit)
	}
}

func TestInstall_INS19_LocalLink(t *testing.T) {
	src := t.TempDir()
	writeRepoFiles(t, src, map[string]string{"extension.yaml": echoManifest})
	inst, root, _ := newTestInstaller(t)
	if _, err := inst.Install(context.Background(), Source{URL: src, Path: src, Local: true}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filepath.Join(root, "hello"))
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("not a symlink: %v %v", info, err)
	}
	if err := inst.Remove("hello"); err != nil {
		t.Fatal(err)
	}
	if !pathExists(src) {
		t.Fatal("removing the link removed the source")
	}
}

func TestInstall_INS20_CancelMidInstall(t *testing.T) {
	repo, _ := gitRepo(t, map[string]string{"extension.yaml": echoManifest})
	inst, root, lock := newTestInstaller(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := inst.Install(ctx, Source{URL: repo, Ref: "main"}); err == nil {
		t.Fatal("expected a cancellation error")
	}
	assertNothingInstalled(t, root, lock)
}

func TestInstall_INS21_Update(t *testing.T) {
	repo, _ := gitRepo(t, map[string]string{"extension.yaml": echoManifest})
	inst, root, lock := newTestInstaller(t)
	if _, err := inst.Install(context.Background(), Source{URL: repo, Ref: "main"}); err != nil {
		t.Fatal(err)
	}
	writeRepoFiles(t, repo, map[string]string{"extension.yaml": "name: hello\nversion: 1.0.1\ndescription: y\nrun: [echo]\n"})
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-q", "-m", "bump")
	head := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	if _, err := inst.Update(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	entries, _ := readLock(lock)
	if entries["hello"].Commit != head {
		t.Fatalf("commit = %q, want %q", entries["hello"].Commit, head)
	}
	if !pathExists(filepath.Join(root, "hello", "extension.yaml")) {
		t.Fatal("new directory missing")
	}
}

func TestInstall_INS22_UpdateFailureRestores(t *testing.T) {
	repo, first := gitRepo(t, map[string]string{"extension.yaml": echoManifest})
	inst, root, lock := newTestInstaller(t)
	if _, err := inst.Install(context.Background(), Source{URL: repo, Ref: "main"}); err != nil {
		t.Fatal(err)
	}
	writeRepoFiles(t, repo, map[string]string{"extension.yaml": "name: hello\nversion: 1.0.1\ndescription: y\nrun: [echo]\nbuild: [\"sh\", \"-c\", \"exit 1\"]\n"})
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-q", "-m", "break")
	if _, err := inst.Update(context.Background(), "hello"); err == nil {
		t.Fatal("expected the update to fail")
	}
	entries, _ := readLock(lock)
	if entries["hello"].Commit != first {
		t.Fatalf("lock changed on failed update: %+v", entries["hello"])
	}
	if !pathExists(filepath.Join(root, "hello")) {
		t.Fatal("old directory missing after failed update")
	}
}

func TestInstall_INS23_UpdateNothingNew(t *testing.T) {
	repo, _ := gitRepo(t, map[string]string{"extension.yaml": echoManifest})
	inst, _, _ := newTestInstaller(t)
	if _, err := inst.Install(context.Background(), Source{URL: repo, Ref: "main"}); err != nil {
		t.Fatal(err)
	}
	if _, err := inst.Update(context.Background(), "hello"); err == nil {
		t.Fatal("expected up-to-date error")
	}
}

func TestInstall_INS24_Remove(t *testing.T) {
	repo, _ := gitRepo(t, map[string]string{"extension.yaml": echoManifest})
	inst, root, lock := newTestInstaller(t)
	if _, err := inst.Install(context.Background(), Source{URL: repo, Ref: "main"}); err != nil {
		t.Fatal(err)
	}
	if err := inst.Remove("hello"); err != nil {
		t.Fatal(err)
	}
	if pathExists(filepath.Join(root, "hello")) {
		t.Fatal("directory not removed")
	}
	if entries, _ := readLock(lock); len(entries) != 0 {
		t.Fatal("lock entry not removed")
	}
	if err := inst.Remove("hello"); err == nil {
		t.Fatal("expected not-installed error")
	}
}

func TestInstall_INS25_ListDrift(t *testing.T) {
	repo, _ := gitRepo(t, map[string]string{"extension.yaml": echoManifest})
	inst, root, _ := newTestInstaller(t)
	if _, err := inst.Install(context.Background(), Source{URL: repo, Ref: "main"}); err != nil {
		t.Fatal(err)
	}
	runGit(t, filepath.Join(root, "hello"), "config", "user.email", "t@example.com")
	runGit(t, filepath.Join(root, "hello"), "config", "user.name", "t")
	runGit(t, filepath.Join(root, "hello"), "commit", "-q", "--allow-empty", "-m", "drift")
	list, _ := inst.List()
	if len(list) != 1 || !list[0].Drifted {
		t.Fatalf("list = %+v", list)
	}
}

func TestInstall_INS26_LockAtomicFail(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "extensions.lock")
	if err := writeLock(lock, map[string]LockEntry{"a": {Name: "a", Commit: "1"}}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(lock)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)
	err := writeLock(lock, map[string]LockEntry{"b": {Name: "b", Commit: "2"}})
	if err == nil {
		t.Skip("directory permissions did not stop the write")
	}
	after, _ := os.ReadFile(lock)
	if string(before) != string(after) {
		t.Fatal("old lock was changed by a failed write")
	}
}

func TestInstall_INS27_CorruptLock(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "extensions.lock")
	_ = os.WriteFile(lock, []byte(": not yaml ["), 0o644)
	entries, warnings := readLock(lock)
	if len(entries) != 0 || len(warnings) == 0 {
		t.Fatalf("entries=%v warnings=%v", entries, warnings)
	}
	// An install rewrites the lock with valid content.
	repo, _ := gitRepo(t, map[string]string{"extension.yaml": echoManifest})
	inst := NewInstaller(InstallOptions{Root: dir, LockPath: lock, Confirm: func(Plan) bool { return true }})
	if _, err := inst.Install(context.Background(), Source{URL: repo, Ref: "main"}); err != nil {
		t.Fatal(err)
	}
	if entries, _ := readLock(lock); len(entries) != 1 {
		t.Fatal("lock was not rewritten")
	}
}

func TestInstall_INS28_ConcurrentDifferent(t *testing.T) {
	repo1, _ := gitRepo(t, map[string]string{"extension.yaml": echoManifest})
	repo2, _ := gitRepo(t, map[string]string{"extension.yaml": "name: world\nversion: 1.0.0\ndescription: x\nrun: [echo]\n"})
	inst, root, lock := newTestInstaller(t)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, repo := range []string{repo1, repo2} {
		wg.Add(1)
		go func(r string) {
			defer wg.Done()
			_, err := inst.Install(context.Background(), Source{URL: r, Ref: "main"})
			errs <- err
		}(repo)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if entries, _ := readLock(lock); len(entries) != 2 {
		t.Fatalf("lock = %+v", entries)
	}
	_ = root
}

func TestInstall_INS29_ConcurrentSame(t *testing.T) {
	repo, _ := gitRepo(t, map[string]string{"extension.yaml": echoManifest})
	inst, _, lock := newTestInstaller(t)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := inst.Install(context.Background(), Source{URL: repo, Ref: "main"})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	ok, failed := 0, 0
	for err := range errs {
		if err == nil {
			ok++
		} else {
			failed++
		}
	}
	if ok != 1 || failed != 1 {
		t.Fatalf("ok=%d failed=%d", ok, failed)
	}
	if entries, _ := readLock(lock); len(entries) != 1 {
		t.Fatalf("lock = %+v", entries)
	}
}

// assertNothingInstalled checks Root has no extension directories and the lock
// has no entries.
func assertNothingInstalled(t *testing.T, root, lock string) {
	t.Helper()
	entries, _ := readLock(lock)
	if len(entries) != 0 {
		t.Fatalf("lock entries left behind: %+v", entries)
	}
	files, _ := os.ReadDir(root)
	for _, f := range files {
		if f.IsDir() && !strings.HasPrefix(f.Name(), ".") && f.Name() != "extensions.lock" {
			t.Fatalf("directory %q left behind", f.Name())
		}
	}
}
