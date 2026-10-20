package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
)

// The version names a path on the release server. A value that is not a plain
// version must be refused before any request is made, whether it came from a
// flag or from the release API.
func TestRun_UPD39_VersionMustBeAPlainTag(t *testing.T) {
	for _, bad := range []string{"../../evil", "v1/x", "v1 2", "v1?x=1", "-v1", "v1\n2", ""} {
		rs, _, _ := standardRelease(t, "v0.2.0")
		target := writeTarget(t, t.TempDir(), "old")
		opt := releaseOptions(rs, target, &fakeExec{})
		if bad == "" {
			rs.tag = "../up" // the API names the tag instead
		} else {
			opt.Version = bad
		}
		if _, err := Run(context.Background(), opt); err == nil || !strings.Contains(err.Error(), "version") {
			t.Errorf("version %q: err = %v, want a version error", bad, err)
		}
		if rs.sawRequest("/releases/download/") {
			t.Errorf("version %q: a download was attempted", bad)
		}
	}
	// Ordinary tags still work.
	for _, ok := range []string{"v0.2.0", "0.2.0", "v1.2.3-rc.1", "v1.2.3+build5"} {
		if !validVersion(ok) {
			t.Errorf("version %q was refused", ok)
		}
	}
}

// Nothing a server sends may be read without a bound.
func TestRun_UPD40_OversizeDownloadsAreRefused(t *testing.T) {
	oldMeta, oldArchive := maxMetaBytes, maxArchiveBytes
	t.Cleanup(func() { maxMetaBytes, maxArchiveBytes = oldMeta, oldArchive })

	rs, _, _ := standardRelease(t, "v0.2.0")
	maxArchiveBytes = 16
	target := writeTarget(t, t.TempDir(), "old")
	if _, err := Run(context.Background(), releaseOptions(rs, target, &fakeExec{})); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("an oversize archive: err = %v, want a too-large error", err)
	}
	maxArchiveBytes = oldArchive
	maxMetaBytes = 8
	if _, err := Run(context.Background(), releaseOptions(rs, target, &fakeExec{})); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("an oversize checksums file: err = %v, want a too-large error", err)
	}
	if got := readTarget(t, target); got != "old" {
		t.Fatalf("target = %q, want the old binary", got)
	}
}

// The check that the new binary runs asks it for its version and needs nothing
// from the caller's environment, which holds the user's keys.
func TestRun_UPD41_SanityCheckRunsWithAnEmptyEnvironment(t *testing.T) {
	rs, _, _ := standardRelease(t, "v0.2.0")
	target := writeTarget(t, t.TempDir(), "old")
	ex := &fakeExec{}
	opt := releaseOptions(rs, target, ex)
	opt.Env = []string{"PATH=/usr/bin", "OPENROUTER_API_KEY=sk-secret"}
	if _, err := Run(context.Background(), opt); err != nil {
		t.Fatalf("Run: %v", err)
	}
	checked := false
	for _, c := range ex.calls {
		if len(c.args) == 1 && c.args[0] == "version" {
			checked = true
			if c.env == nil || len(c.env) != 0 {
				t.Errorf("the version check got env %v, want an empty, non-nil environment", len(c.env))
			}
		}
	}
	if !checked {
		t.Fatal("the version check never ran")
	}
}

// customArchive builds a gzip tar with one entry of the given header.
func customArchive(t *testing.T, hdr tar.Header, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func releaseWith(t *testing.T, archive []byte) *releaseServer {
	t.Helper()
	sum := sha256.Sum256(archive)
	return serveRelease(t, "v0.2.0", archive, fmt.Sprintf("%s  nova_v0.2.0_linux_amd64.tar.gz\n", hex.EncodeToString(sum[:])))
}

// Only a regular file is the binary: a link or a device with the right name is
// an attempt to make the update write somewhere else.
func TestRun_UPD42_BinaryMemberMustBeARegularFile(t *testing.T) {
	archive := customArchive(t, tar.Header{Name: "nova", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd", Mode: 0o755}, nil)
	target := writeTarget(t, t.TempDir(), "old")
	if _, err := Run(context.Background(), releaseOptions(releaseWith(t, archive), target, &fakeExec{})); err == nil || !strings.Contains(err.Error(), "regular") {
		t.Fatalf("a symlink member: err = %v, want a regular-file error", err)
	}
	if got := readTarget(t, target); got != "old" {
		t.Fatalf("target = %q, want the old binary", got)
	}
}

// A small archive can unpack to far more than it weighs.
func TestRun_UPD43_UnpackedBinaryIsCapped(t *testing.T) {
	old := maxBinaryBytes
	maxBinaryBytes = 1 << 10
	t.Cleanup(func() { maxBinaryBytes = old })
	body := bytes.Repeat([]byte("A"), 64<<10)
	archive := customArchive(t, tar.Header{Name: "nova", Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(body))}, body)
	dir := t.TempDir()
	target := writeTarget(t, dir, "old")
	if _, err := Run(context.Background(), releaseOptions(releaseWith(t, archive), target, &fakeExec{})); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("an oversize binary: err = %v, want a too-large error", err)
	}
	if got := readTarget(t, target); got != "old" {
		t.Fatalf("target = %q, want the old binary", got)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".nova-update-") {
			t.Errorf("staged file %s was left behind", e.Name())
		}
	}
}
