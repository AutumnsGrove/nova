package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// releaseServer serves the three endpoints install.sh uses: the latest tag,
// checksums.txt, and the archive.
type releaseServer struct {
	*httptest.Server
	tag        string
	archive    []byte
	checksums  string
	failStatus int

	mu    sync.Mutex
	paths []string
}

func serveRelease(t *testing.T, tag string, archive []byte, checksums string) *releaseServer {
	t.Helper()
	rs := &releaseServer{tag: tag, archive: archive, checksums: checksums}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs.mu.Lock()
		rs.paths = append(rs.paths, r.URL.Path)
		rs.mu.Unlock()
		switch {
		case r.URL.Path == "/releases/latest":
			fmt.Fprintf(w, `{"tag_name":%q}`, rs.tag)
		case strings.HasSuffix(r.URL.Path, "/checksums.txt"):
			fmt.Fprint(w, rs.checksums)
		case strings.HasSuffix(r.URL.Path, ".tar.gz"):
			if rs.failStatus != 0 {
				http.Error(w, "boom", rs.failStatus)
				return
			}
			w.Write(rs.archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(rs.Close)
	return rs
}

// requestPaths returns a copy of the requested paths.
func (rs *releaseServer) requestPaths() []string {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return append([]string(nil), rs.paths...)
}

// sawRequest reports whether any requested path contains want.
func (rs *releaseServer) sawRequest(want string) bool {
	for _, p := range rs.requestPaths() {
		if strings.Contains(p, want) {
			return true
		}
	}
	return false
}

// makeArchive builds a gzip tar with the named binary member and any extras.
func makeArchive(t *testing.T, member string, body []byte, extra map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	write := func(name string, data []byte, mode int64) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	for name, data := range extra {
		write(name, data, 0o644)
	}
	write(member, body, 0o755)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// standardRelease serves a correct full-flavour release for tag.
func standardRelease(t *testing.T, tag string) (*releaseServer, []byte, string) {
	t.Helper()
	body := []byte("#!/bin/sh\necho nova " + tag + "\n")
	name := "nova_" + tag + "_linux_amd64.tar.gz"
	archive := makeArchive(t, "nova", body, map[string][]byte{"LICENSE": []byte("MIT")})
	sum := sha256.Sum256(archive)
	checksums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), name)
	return serveRelease(t, tag, archive, checksums), body, name
}

// releaseOptions builds Options for a release run against rs.
func releaseOptions(rs *releaseServer, target string, ex *fakeExec) Options {
	return Options{
		Mode:     Release,
		Target:   target,
		Flavour:  "full",
		BaseURL:  rs.URL,
		APIURL:   rs.URL + "/releases/latest",
		GOOS:     "linux",
		GOARCH:   "amd64",
		HTTP:     rs.Client(),
		Exec:     ex.exec,
		LookPath: func(string) (string, error) { return "", fmt.Errorf("no go") },
		Env:      []string{"PATH=/usr/bin:/bin"},
		Out:      io.Discard,
	}
}

func TestRun_UPD06_ReleaseInsideCheckout(t *testing.T) {
	root := t.TempDir()
	writeCheckout(t, root)
	target := writeTarget(t, t.TempDir(), "old")
	rs, body, _ := standardRelease(t, "v0.2.0")
	ex := &fakeExec{}
	opt := releaseOptions(rs, target, ex)
	opt.Dir = root
	if _, err := Run(context.Background(), opt); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !rs.sawRequest(".tar.gz") {
		t.Fatalf("no archive requested: %v", rs.requestPaths())
	}
	if got := readTarget(t, target); got != string(body) {
		t.Fatalf("target = %q, want the release body", got)
	}
	if ex.calledGo() {
		t.Fatal("a release run invoked the go toolchain")
	}
}

func TestRun_UPD07_ResolveLatest(t *testing.T) {
	rs, _, _ := standardRelease(t, "v0.2.0")
	target := writeTarget(t, t.TempDir(), "old")
	res, err := Run(context.Background(), releaseOptions(rs, target, &fakeExec{}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !rs.sawRequest("/releases/latest") {
		t.Fatalf("latest not resolved: %v", rs.requestPaths())
	}
	if res.To != "v0.2.0" {
		t.Fatalf("To = %q, want v0.2.0", res.To)
	}
}

func TestRun_UPD08_PinnedVersion(t *testing.T) {
	rs, _, name := standardRelease(t, "v1.2.3")
	target := writeTarget(t, t.TempDir(), "old")
	opt := releaseOptions(rs, target, &fakeExec{})
	opt.Version = "v1.2.3"
	if _, err := Run(context.Background(), opt); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rs.sawRequest("/releases/latest") {
		t.Fatalf("pinned version asked the API: %v", rs.requestPaths())
	}
	if !rs.sawRequest(name) {
		t.Fatalf("did not request %s: %v", name, rs.requestPaths())
	}
}

func TestRun_UPD09_ArchiveName(t *testing.T) {
	t.Run("full", func(t *testing.T) {
		rs, _, name := standardRelease(t, "v0.2.0")
		target := writeTarget(t, t.TempDir(), "old")
		if _, err := Run(context.Background(), releaseOptions(rs, target, &fakeExec{})); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if name != "nova_v0.2.0_linux_amd64.tar.gz" || !rs.sawRequest(name) {
			t.Fatalf("archive name = %q, requests %v", name, rs.requestPaths())
		}
	})
	t.Run("min", func(t *testing.T) {
		tag := "v0.2.0"
		body := []byte("min")
		name := "nova-min_" + tag + "_linux_amd64.tar.gz"
		archive := makeArchive(t, "nova-min", body, nil)
		sum := sha256.Sum256(archive)
		checksums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), name)
		rs := serveRelease(t, tag, archive, checksums)
		target := writeTarget(t, t.TempDir(), "old")
		opt := releaseOptions(rs, target, &fakeExec{})
		opt.Flavour = "min"
		if _, err := Run(context.Background(), opt); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if !rs.sawRequest(name) {
			t.Fatalf("did not request %s: %v", name, rs.requestPaths())
		}
	})
}

func TestRun_UPD10_ChecksumMismatch(t *testing.T) {
	tag := "v0.2.0"
	body := []byte("real")
	name := "nova_" + tag + "_linux_amd64.tar.gz"
	archive := makeArchive(t, "nova", body, nil)
	checksums := fmt.Sprintf("%s  %s\n", strings.Repeat("0", 64), name)
	rs := serveRelease(t, tag, archive, checksums)
	target := writeTarget(t, t.TempDir(), "old")
	if _, err := Run(context.Background(), releaseOptions(rs, target, &fakeExec{})); err == nil {
		t.Fatal("Run succeeded despite a bad checksum")
	}
	if got := readTarget(t, target); got != "old" {
		t.Fatalf("target = %q, want the old binary", got)
	}
}

func TestRun_UPD11_MissingChecksumEntry(t *testing.T) {
	tag := "v0.2.0"
	body := []byte("real")
	archive := makeArchive(t, "nova", body, nil)
	rs := serveRelease(t, tag, archive, "deadbeef  other.tar.gz\n")
	target := writeTarget(t, t.TempDir(), "old")
	if _, err := Run(context.Background(), releaseOptions(rs, target, &fakeExec{})); err == nil {
		t.Fatal("Run succeeded without a checksum entry")
	}
	if got := readTarget(t, target); got != "old" {
		t.Fatalf("target = %q, want the old binary", got)
	}
}

func TestRun_UPD12_DownloadFailure(t *testing.T) {
	rs, _, _ := standardRelease(t, "v0.2.0")
	rs.failStatus = http.StatusInternalServerError
	target := writeTarget(t, t.TempDir(), "old")
	if _, err := Run(context.Background(), releaseOptions(rs, target, &fakeExec{})); err == nil {
		t.Fatal("Run succeeded despite a failed download")
	}
	if got := readTarget(t, target); got != "old" {
		t.Fatalf("target = %q, want the old binary", got)
	}
}

func TestRun_UPD13_ExtraTarMembers(t *testing.T) {
	rs, body, _ := standardRelease(t, "v0.2.0")
	dir := t.TempDir()
	target := writeTarget(t, dir, "old")
	if _, err := Run(context.Background(), releaseOptions(rs, target, &fakeExec{})); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := readTarget(t, target); got != string(body) {
		t.Fatalf("target = %q, want the binary member", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "LICENSE")); err == nil {
		t.Fatal("an unrelated archive member was written next to the target")
	}
}

func TestRun_UPD14_PathTraversalMember(t *testing.T) {
	tag := "v0.2.0"
	name := "nova_" + tag + "_linux_amd64.tar.gz"
	archive := makeArchive(t, "../nova", []byte("evil"), nil)
	sum := sha256.Sum256(archive)
	checksums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), name)
	rs := serveRelease(t, tag, archive, checksums)
	parent := t.TempDir()
	dir := filepath.Join(parent, "nested")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := writeTarget(t, dir, "old")
	if _, err := Run(context.Background(), releaseOptions(rs, target, &fakeExec{})); err == nil {
		t.Fatal("Run accepted an archive with only a traversal member")
	}
	if _, err := os.Stat(filepath.Join(parent, "nova")); err == nil {
		t.Fatal("a member escaped the target directory")
	}
	if got := readTarget(t, target); got != "old" {
		t.Fatalf("target = %q, want the old binary", got)
	}
}

func TestRun_UPD15_ReleaseReplaces(t *testing.T) {
	rs, body, _ := standardRelease(t, "v0.2.0")
	target := writeTarget(t, t.TempDir(), "old")
	res, err := Run(context.Background(), releaseOptions(rs, target, &fakeExec{}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Changed {
		t.Fatalf("Result = %+v, want Changed", res)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %o, want 755", info.Mode().Perm())
	}
	if got := readTarget(t, target); got != string(body) {
		t.Fatalf("target = %q, want the release body", got)
	}
}

func TestRun_UPD16_AlreadyCurrent(t *testing.T) {
	rs, _, _ := standardRelease(t, "v0.2.0")
	target := writeTarget(t, t.TempDir(), "old")
	opt := releaseOptions(rs, target, &fakeExec{})
	opt.Current = "v0.2.0"
	res, err := Run(context.Background(), opt)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Changed {
		t.Fatalf("Result = %+v, want no change", res)
	}
	if rs.sawRequest(".tar.gz") {
		t.Fatalf("downloaded an archive that was already current: %v", rs.requestPaths())
	}
	if got := readTarget(t, target); got != "old" {
		t.Fatalf("target = %q, want the old binary", got)
	}
}

func TestRun_UPD17_ForceReinstall(t *testing.T) {
	rs, body, _ := standardRelease(t, "v0.2.0")
	target := writeTarget(t, t.TempDir(), "old")
	opt := releaseOptions(rs, target, &fakeExec{})
	opt.Current = "v0.2.0"
	opt.Force = true
	res, err := Run(context.Background(), opt)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Changed {
		t.Fatalf("Result = %+v, want a forced change", res)
	}
	if got := readTarget(t, target); got != string(body) {
		t.Fatalf("target = %q, want the release body", got)
	}
}

func TestRun_UPD18_CheckWritesNothing(t *testing.T) {
	rs, _, _ := standardRelease(t, "v0.2.0")
	target := writeTarget(t, t.TempDir(), "old")
	opt := releaseOptions(rs, target, &fakeExec{})
	opt.Check = true
	res, err := Run(context.Background(), opt)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Changed {
		t.Fatalf("Result = %+v, want Changed false", res)
	}
	if rs.sawRequest(".tar.gz") {
		t.Fatalf("check downloaded an archive: %v", rs.requestPaths())
	}
	if got := readTarget(t, target); got != "old" {
		t.Fatalf("target = %q, want the old binary", got)
	}
}

func TestRun_UPD19_UnsupportedPlatform(t *testing.T) {
	rs, _, _ := standardRelease(t, "v0.2.0")
	target := writeTarget(t, t.TempDir(), "old")
	opt := releaseOptions(rs, target, &fakeExec{})
	opt.GOOS = "windows"
	_, err := Run(context.Background(), opt)
	if err == nil || !strings.Contains(err.Error(), "windows") {
		t.Fatalf("Run error = %v, want one naming windows", err)
	}
	if len(rs.requestPaths()) != 0 {
		t.Fatalf("requested a release for an unsupported platform: %v", rs.requestPaths())
	}
	if got := readTarget(t, target); got != "old" {
		t.Fatalf("target = %q, want the old binary", got)
	}
}

func TestRun_UPD20_SanityCheckFails(t *testing.T) {
	rs, _, _ := standardRelease(t, "v0.2.0")
	target := writeTarget(t, t.TempDir(), "old")
	ex := &fakeExec{versionErr: fmt.Errorf("not a nova binary")}
	if _, err := Run(context.Background(), releaseOptions(rs, target, ex)); err == nil {
		t.Fatal("Run installed a binary that failed its sanity check")
	}
	if got := readTarget(t, target); got != "old" {
		t.Fatalf("target = %q, want the old binary", got)
	}
}
