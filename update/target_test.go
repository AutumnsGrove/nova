package update

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun_UPD31_FreshInstallIntoMissingTarget(t *testing.T) {
	rs, body, _ := standardRelease(t, "v0.2.0")
	target := filepath.Join(t.TempDir(), "nova")
	res, err := Run(context.Background(), releaseOptions(rs, target, &fakeExec{}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Changed {
		t.Fatalf("Result = %+v, want Changed", res)
	}
	if got := readTarget(t, target); got != string(body) {
		t.Fatalf("target = %q, want the release body", got)
	}
}

func TestRun_UPD32_UnwritableDirectoryNamesThePath(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	rs, _, _ := standardRelease(t, "v0.2.0")
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	target := filepath.Join(dir, "nova")
	_, err := Run(context.Background(), releaseOptions(rs, target, &fakeExec{}))
	if err == nil || !strings.Contains(err.Error(), target) {
		t.Fatalf("err = %v, want an error naming %s", err, target)
	}
}
