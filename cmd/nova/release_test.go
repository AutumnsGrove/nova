package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"gopkg.in/yaml.v3"
)

// workflow is the subset of release.yml the REL tests read.
type workflow struct {
	Permissions map[string]string `yaml:"permissions"`
	On          map[string]struct {
		Tags []string `yaml:"tags"`
	} `yaml:"on"`
	Jobs map[string]struct {
		Permissions map[string]string `yaml:"permissions"`
		Steps       []struct {
			Run string            `yaml:"run"`
			Env map[string]string `yaml:"env"`
		} `yaml:"steps"`
		Strategy struct {
			Matrix struct {
				OS   []string `yaml:"os"`
				Arch []string `yaml:"arch"`
			} `yaml:"matrix"`
		} `yaml:"strategy"`
	} `yaml:"jobs"`
}

func loadReleaseWorkflow(t *testing.T) workflow {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("read release.yml: %v", err)
	}
	var w workflow
	if err := yaml.Unmarshal(data, &w); err != nil {
		t.Fatalf("parse release.yml: %v", err)
	}
	return w
}

func TestRelease_REL01_Structure(t *testing.T) {
	w := loadReleaseWorkflow(t)
	tags := w.On["push"].Tags
	if len(tags) != 1 || tags[0] != "v*" {
		t.Fatalf("trigger tags = %v, want [v*]", tags)
	}
	m := w.Jobs["build"].Strategy.Matrix
	if fmt.Sprint(m.OS) != "[linux darwin]" {
		t.Fatalf("matrix os = %v", m.OS)
	}
	if fmt.Sprint(m.Arch) != "[amd64 arm64]" {
		t.Fatalf("matrix arch = %v", m.Arch)
	}
	var runs []string
	for _, s := range w.Jobs["build"].Steps {
		runs = append(runs, s.Run)
	}
	joined := strings.Join(runs, "\n")
	if !strings.Contains(joined, "notui") || !strings.Contains(joined, "nova-min") {
		t.Fatalf("build does not make both flavours:\n%s", joined)
	}
}

func TestRelease_REL02_AcceptanceGate(t *testing.T) {
	w := loadReleaseWorkflow(t)
	accept, ok := w.Jobs["accept"]
	if !ok {
		t.Fatal("release has no accept job")
	}
	var runs []string
	for _, s := range accept.Steps {
		runs = append(runs, s.Run)
	}
	joined := strings.Join(runs, "\n")
	if !strings.Contains(joined, "scripts/accept") || !strings.Contains(joined, "docs/archive/chunks") {
		t.Fatalf("accept job does not run every spec:\n%s", joined)
	}
}

func TestRelease_REL03_BuildFlags(t *testing.T) {
	w := loadReleaseWorkflow(t)
	for _, s := range w.Jobs["build"].Steps {
		if s.Env["CGO_ENABLED"] != "0" {
			continue
		}
		for _, want := range []string{"-trimpath", "-s -w", "-X main.version", "-X main.commit", "-X main.date"} {
			if !strings.Contains(s.Run, want) {
				t.Fatalf("build flags missing %q:\n%s", want, s.Run)
			}
		}
		return
	}
	t.Fatal("no CGO_ENABLED=0 build step found")
}

// makeFakeBinary writes a shell script that logs its arguments.
func makeFakeBinary(t *testing.T, path, version string) {
	t.Helper()
	script := "#!/bin/sh\nif [ -n \"${NOVA_FAKE_LOG:-}\" ]; then echo \"$*\" >>\"$NOVA_FAKE_LOG\"; fi\necho \"nova " + version + "\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestRelease_REL04_Archives(t *testing.T) {
	dir := t.TempDir()
	full := filepath.Join(dir, "nova")
	min := filepath.Join(dir, "nova-min")
	makeFakeBinary(t, full, "v0.1.0")
	makeFakeBinary(t, min, "v0.1.0")
	out := filepath.Join(dir, "dist")
	cmd := exec.Command("sh", "./scripts/package.sh", "v0.1.0", "linux", "amd64", full, min, out)
	cmd.Dir = filepath.Join("..", "..")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("package.sh: %v\n%s", err, b)
	}
	for _, name := range []string{"nova_v0.1.0_linux_amd64.tar.gz", "nova-min_v0.1.0_linux_amd64.tar.gz"} {
		list := exec.Command("tar", "-tzf", filepath.Join(out, name))
		b, err := list.CombinedOutput()
		if err != nil {
			t.Fatalf("tar -tzf %s: %v\n%s", name, err, b)
		}
		for _, want := range []string{"nova", "LICENSE", "README.md"} {
			if !strings.Contains(string(b), want) {
				t.Fatalf("%s does not contain %s:\n%s", name, want, b)
			}
		}
	}
}

func TestRelease_REL05_Checksums(t *testing.T) {
	dir := t.TempDir()
	full := filepath.Join(dir, "nova")
	min := filepath.Join(dir, "nova-min")
	makeFakeBinary(t, full, "v0.1.0")
	makeFakeBinary(t, min, "v0.1.0")
	out := filepath.Join(dir, "dist")
	cmd := exec.Command("sh", "./scripts/package.sh", "v0.1.0", "darwin", "arm64", full, min, out)
	cmd.Dir = filepath.Join("..", "..")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("package.sh: %v\n%s", err, b)
	}
	data, err := os.ReadFile(filepath.Join(out, "checksums.txt"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("checksums.txt has %d lines, want 2:\n%s", len(lines), data)
	}
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			t.Fatalf("bad checksum line %q", line)
		}
		archive, err := os.ReadFile(filepath.Join(out, fields[1]))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(archive)
		if hex.EncodeToString(sum[:]) != fields[0] {
			t.Fatalf("checksum mismatch for %s", fields[1])
		}
	}
}

// fakeRelease serves a release over HTTP for the install tests.
type fakeRelease struct {
	*httptest.Server
	version   string
	os, arch  string
	archive   []byte
	checksums string
	fail      atomic.Bool

	mu       sync.Mutex
	requests []string
}

// setChecksums replaces the served checksums.txt safely.
func (fr *fakeRelease) setChecksums(s string) {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	fr.checksums = s
}

// paths returns a copy of the request paths, safe to read after a run.
func (fr *fakeRelease) paths() []string {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	return append([]string(nil), fr.requests...)
}

func newFakeRelease(t *testing.T, version string) *fakeRelease {
	t.Helper()
	fr := &fakeRelease{version: version, os: runtime.GOOS, arch: runtime.GOARCH}
	fr.archive = makeArchive(t, version)
	sum := hex.EncodeToString(sha256Sum(fr.archive))
	fr.checksums = fmt.Sprintf("%s  nova_%s_%s_%s.tar.gz\n%s  nova-min_%s_%s_%s.tar.gz\n",
		sum, version, fr.os, fr.arch, sum, version, fr.os, fr.arch)
	fr.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fr.mu.Lock()
		fr.requests = append(fr.requests, r.URL.Path)
		fr.mu.Unlock()
		switch {
		case r.URL.Path == "/releases/latest":
			fmt.Fprintf(w, `{"tag_name":%q}`, fr.version)
		case strings.HasSuffix(r.URL.Path, "checksums.txt"):
			fr.mu.Lock()
			checksums := fr.checksums
			fr.mu.Unlock()
			fmt.Fprint(w, checksums)
		case strings.HasSuffix(r.URL.Path, ".tar.gz"):
			if fr.fail.Load() {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			w.Write(fr.archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fr.Close)
	return fr
}

func sha256Sum(b []byte) []byte {
	s := sha256.Sum256(b)
	return s[:]
}

// makeArchive builds a tar.gz holding a fake nova plus LICENSE and README.
func makeArchive(t *testing.T, version string) []byte {
	t.Helper()
	dir := t.TempDir()
	makeFakeBinary(t, filepath.Join(dir, "nova"), version)
	for _, name := range []string{"LICENSE", "README.md"} {
		data, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	archive := filepath.Join(dir, "out.tar.gz")
	cmd := exec.Command("tar", "-czf", archive, "-C", dir, "nova", "LICENSE", "README.md")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tar: %v\n%s", err, b)
	}
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// installEnv builds the environment for one install.sh run.
func installEnv(t *testing.T, fr *fakeRelease, installDir, tmpDir string, extra ...string) []string {
	t.Helper()
	env := []string{
		"NOVA_RELEASE_BASE=" + fr.URL,
		"NOVA_INSTALL_DIR=" + installDir,
		"TMPDIR=" + tmpDir,
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
	}
	return append(env, extra...)
}

func runInstall(t *testing.T, fr *fakeRelease, installDir, tmpDir string, extra ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("sh", "install.sh")
	cmd.Dir = filepath.Join("..", "..")
	cmd.Env = installEnv(t, fr, installDir, tmpDir, extra...)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("run install.sh: %v", err)
		}
	}
	return string(out), code
}

func TestInstall_ISH01_HappyPath(t *testing.T) {
	fr := newFakeRelease(t, "v0.1.0")
	installDir := t.TempDir()
	out, code := runInstall(t, fr, installDir, t.TempDir())
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	info, err := os.Stat(filepath.Join(installDir, "nova"))
	if err != nil {
		t.Fatalf("binary not installed: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %o, want 755", info.Mode().Perm())
	}
}

func TestInstall_ISH02_ChecksumMismatch(t *testing.T) {
	fr := newFakeRelease(t, "v0.1.0")
	fr.setChecksums(strings.Replace(fr.checksums, "0", "1", 1))
	if !strings.Contains(fr.checksums, "1") {
		t.Skip("checksum did not contain a zero to corrupt")
	}
	installDir := t.TempDir()
	_, code := runInstall(t, fr, installDir, t.TempDir())
	if code == 0 {
		t.Fatal("install succeeded despite a bad checksum")
	}
	if _, err := os.Stat(filepath.Join(installDir, "nova")); err == nil {
		t.Fatal("install wrote a binary despite a bad checksum")
	}
}

func TestInstall_ISH03_UnsupportedPlatform(t *testing.T) {
	fr := newFakeRelease(t, "v0.1.0")
	bin := t.TempDir()
	script := "#!/bin/sh\ncase \"$1\" in -s) echo Plan9;; -m) echo mips;; *) echo Plan9;; esac\n"
	if err := os.WriteFile(filepath.Join(bin, "uname"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	out, code := runInstall(t, fr, t.TempDir(), t.TempDir(), "PATH="+bin+":"+os.Getenv("PATH"))
	if code == 0 || !strings.Contains(out, "Plan9") {
		t.Fatalf("exit %d out %q, want an unsupported-platform error", code, out)
	}
}

func TestInstall_ISH04_VersionOverride(t *testing.T) {
	fr := newFakeRelease(t, "v0.1.0")
	out, code := runInstall(t, fr, t.TempDir(), t.TempDir(), "NOVA_VERSION=v9.9.9")
	if code == 0 {
		t.Fatalf("expected a failure for a missing version:\n%s", out)
	}
	if !containsPath(fr.paths(), "/releases/download/v9.9.9/") {
		t.Fatalf("did not request v9.9.9: %v", fr.paths())
	}
}

func TestInstall_ISH05_MinFlavour(t *testing.T) {
	fr := newFakeRelease(t, "v0.1.0")
	_, code := runInstall(t, fr, t.TempDir(), t.TempDir(), "NOVA_FLAVOUR=min")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !containsPath(fr.paths(), "nova-min_v0.1.0_"+fr.os+"_"+fr.arch+".tar.gz") {
		t.Fatalf("did not request the min archive: %v", fr.paths())
	}
}

func TestInstall_ISH06_InstallDir(t *testing.T) {
	fr := newFakeRelease(t, "v0.1.0")
	installDir := filepath.Join(t.TempDir(), "nested", "bin")
	if _, code := runInstall(t, fr, installDir, t.TempDir()); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(installDir, "nova")); err != nil {
		t.Fatalf("install dir not created: %v", err)
	}
}

func TestInstall_ISH07_PATHHint(t *testing.T) {
	fr := newFakeRelease(t, "v0.1.0")
	out, code := runInstall(t, fr, t.TempDir(), t.TempDir())
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "add") || !strings.Contains(out, "PATH") {
		t.Fatalf("no PATH hint:\n%s", out)
	}
}

func TestInstall_ISH08_IdempotentUpgrade(t *testing.T) {
	installDir := t.TempDir()
	fr1 := newFakeRelease(t, "v0.1.0")
	if _, code := runInstall(t, fr1, installDir, t.TempDir()); code != 0 {
		t.Fatalf("first install exit %d", code)
	}
	fr2 := newFakeRelease(t, "v0.2.0")
	if _, code := runInstall(t, fr2, installDir, t.TempDir()); code != 0 {
		t.Fatalf("second install exit %d", code)
	}
	data, err := os.ReadFile(filepath.Join(installDir, "nova"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "v0.2.0") {
		t.Fatalf("binary was not upgraded:\n%s", data)
	}
}

func TestInstall_ISH09_NoSudo(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "sudo") {
			t.Fatal("install.sh runs sudo")
		}
	}
}

func TestInstall_ISH10_TempCleaned(t *testing.T) {
	fr := newFakeRelease(t, "v0.1.0")
	tmp := t.TempDir()
	if _, code := runInstall(t, fr, t.TempDir(), tmp); code != 0 {
		t.Fatalf("exit %d", code)
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temp dir not cleaned: %v", entries)
	}
}

func TestInstall_ISH11_DownloadFailure(t *testing.T) {
	fr := newFakeRelease(t, "v0.1.0")
	fr.fail.Store(true)
	installDir := t.TempDir()
	_, code := runInstall(t, fr, installDir, t.TempDir())
	if code == 0 {
		t.Fatal("install succeeded despite a failed download")
	}
	if _, err := os.Stat(filepath.Join(installDir, "nova")); err == nil {
		t.Fatal("install wrote a binary after a failed download")
	}
}

func TestInstall_ISH12_OnlySanityCheckRuns(t *testing.T) {
	fr := newFakeRelease(t, "v0.1.0")
	log := filepath.Join(t.TempDir(), "log")
	if _, code := runInstall(t, fr, t.TempDir(), t.TempDir(), "NOVA_FAKE_LOG="+log); code != 0 {
		t.Fatalf("exit %d", code)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("the sanity check did not run: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "version" {
		t.Fatalf("downloaded binary ran with %q, want version", got)
	}
}

func TestInstall_ISH13_Dash(t *testing.T) {
	dash, err := exec.LookPath("dash")
	if err != nil {
		t.Skip("dash is not installed")
	}
	fr := newFakeRelease(t, "v0.1.0")
	cmd := exec.Command(dash, "install.sh")
	cmd.Dir = filepath.Join("..", "..")
	cmd.Env = installEnv(t, fr, t.TempDir(), t.TempDir())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("dash install.sh: %v\n%s", err, out)
	}
}

func containsPath(paths []string, want string) bool {
	for _, p := range paths {
		if strings.Contains(p, want) {
			return true
		}
	}
	return false
}

// The version names a path on the release server, so a value that is not a plain
// tag is refused before anything is downloaded, whether it came from the
// environment or from the release API.
func TestInstall_ISH14_VersionMustBeAPlainTag(t *testing.T) {
	for _, bad := range []string{"../../evil", "v1/x", "v1 2", "-v1", "v1?x=1", "v1;id"} {
		fr := newFakeRelease(t, "v0.1.0")
		installDir := t.TempDir()
		out, code := runInstall(t, fr, installDir, t.TempDir(), "NOVA_VERSION="+bad)
		if code == 0 || !strings.Contains(out, "version") {
			t.Errorf("NOVA_VERSION=%q: exit %d out %q, want a version error", bad, code, out)
		}
		if containsPath(fr.paths(), "/releases/download/") {
			t.Errorf("NOVA_VERSION=%q: a download was attempted: %v", bad, fr.paths())
		}
	}
	fr := newFakeRelease(t, "../up")
	if out, code := runInstall(t, fr, t.TempDir(), t.TempDir()); code == 0 || !strings.Contains(out, "version") {
		t.Errorf("a bad tag from the API: exit %d out %q, want a version error", code, out)
	}
}

// sha256sum -b writes "<hash> *<name>"; the Go updater accepts it, so must this.
func TestInstall_ISH15_BinaryModeChecksumLine(t *testing.T) {
	fr := newFakeRelease(t, "v0.1.0")
	for _, form := range []string{" *nova_", "  ./nova_"} {
		fr.setChecksums(strings.ReplaceAll(fr.checksums, "  nova_", form))
		if out, code := runInstall(t, fr, t.TempDir(), t.TempDir()); code != 0 {
			t.Fatalf("checksum line form %q: exit %d:\n%s", form, code, out)
		}
		fr.setChecksums(strings.ReplaceAll(fr.checksums, form, "  nova_"))
	}
}

// A link named nova in the archive is not the binary.
func TestInstall_ISH16_SymlinkMemberIsRefused(t *testing.T) {
	fr := newFakeRelease(t, "v0.1.0")
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{Name: "nova", Typeflag: tar.TypeSymlink, Linkname: "/bin/sh", Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	fr.archive = buf.Bytes()
	sum := hex.EncodeToString(sha256Sum(fr.archive))
	fr.setChecksums(fmt.Sprintf("%s  nova_v0.1.0_%s_%s.tar.gz\n", sum, fr.os, fr.arch))
	installDir := t.TempDir()
	if out, code := runInstall(t, fr, installDir, t.TempDir()); code == 0 {
		t.Fatalf("a symlink member was installed:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(installDir, "nova")); err == nil {
		t.Fatal("a binary was installed from a symlink member")
	}
}

// The checksums.txt that is published is the one the release job writes, not the
// ones package.sh writes per platform. install.sh and `nova update` look an
// archive up by its bare name, so the workflow's own step must write bare names.
func TestRelease_REL06_PublishedChecksumsInstall(t *testing.T) {
	var script string
	for _, s := range loadReleaseWorkflow(t).Jobs["release"].Steps {
		if strings.Contains(s.Run, "checksums.txt") {
			script = s.Run
		}
	}
	if script == "" {
		t.Fatal("the release job has no step that writes checksums.txt")
	}
	fr := newFakeRelease(t, "v0.1.0")
	work := t.TempDir()
	dist := filepath.Join(work, "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		fmt.Sprintf("nova_v0.1.0_%s_%s.tar.gz", fr.os, fr.arch),
		fmt.Sprintf("nova-min_v0.1.0_%s_%s.tar.gz", fr.os, fr.arch),
	} {
		if err := os.WriteFile(filepath.Join(dist, name), fr.archive, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	step := exec.Command("sh", "-c", script)
	step.Dir = work
	if out, err := step.CombinedOutput(); err != nil {
		t.Fatalf("the checksum step failed: %v\n%s", err, out)
	}
	published, err := os.ReadFile(filepath.Join(dist, "checksums.txt"))
	if err != nil {
		t.Fatal(err)
	}
	fr.setChecksums(string(published))
	if out, code := runInstall(t, fr, t.TempDir(), t.TempDir()); code != 0 {
		t.Fatalf("install.sh could not use the published checksums.txt (exit %d):\n%s\n%s", code, out, published)
	}
}

// Only the job that publishes the release writes to the repository. A build or
// acceptance step that is compromised should not hold a token that can.
func TestRelease_REL07_WriteAccessOnlyWhereItIsUsed(t *testing.T) {
	w := loadReleaseWorkflow(t)
	if w.Permissions["contents"] != "read" {
		t.Errorf("workflow permissions = %v, want contents: read", w.Permissions)
	}
	for name, job := range w.Jobs {
		wantWrite := name == "release"
		if got := job.Permissions["contents"] == "write"; got != wantWrite {
			t.Errorf("job %s: contents write = %v, want %v", name, got, wantWrite)
		}
	}
}
