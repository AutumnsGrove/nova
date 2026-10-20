package mode

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/config"
)

// writeDeclExt writes a declarative extension with one tool.
func writeDeclExt(t *testing.T, tool string) string {
	t.Helper()
	dir := t.TempDir()
	body := "name: decl\nversion: 1.0.0\ndescription: x\ntools:\n  - name: " + tool + "\n    description: x\n    parameters: {type: object}\n    exec: [echo]\n"
	if err := os.WriteFile(filepath.Join(dir, "extension.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// newExtServer builds a server whose config loads dirs as extensions.
func newExtServer(t *testing.T, dirs []string) *Server {
	t.Helper()
	cfg := config.Defaults()
	cfg.Extensions.Dirs = dirs
	cfg.Extensions.Disable = bundledNames()
	s, err := New(Options{Config: &cfg, CWD: t.TempDir(), Home: t.TempDir(), NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestModeExt_LoadsDeclarativeTool(t *testing.T) {
	s := newExtServer(t, []string{writeDeclExt(t, "hello")})
	waitExtStarted(t, s)
	if _, ok := s.tools.Get("hello"); !ok {
		t.Fatalf("declarative extension tool never registered: %v", s.tools.Names())
	}
}

// waitExtStarted blocks until the server's background extension start is done.
func waitExtStarted(t *testing.T, s *Server) {
	t.Helper()
	select {
	case <-s.extStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("extensions never finished starting")
	}
}

func TestModeExt_Disabled(t *testing.T) {
	dir := writeDeclExt(t, "hello")
	cfg := config.Defaults()
	cfg.Extensions.Dirs = []string{dir}
	cfg.Extensions.Disable = []string{"decl"}
	s, err := New(Options{Config: &cfg, CWD: t.TempDir(), Home: t.TempDir(), NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	waitExtStarted(t, s)
	if _, ok := s.tools.Get("hello"); ok {
		t.Fatal("disabled extension still loaded")
	}
}
