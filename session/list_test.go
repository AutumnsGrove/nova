package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// createSession makes a session file with the given entries.
func createSession(t *testing.T, path string, entries ...Entry) {
	t.Helper()
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open %s: %v", path, err)
	}
	for _, e := range entries {
		if _, err := s.Append(e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestSession_SLS01_ListSorted(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	paths := []string{}
	for i, name := range []string{"a.jsonl", "b.jsonl", "c.jsonl"} {
		p := filepath.Join(dir, name)
		createSession(t, p, userEntry("x"))
		when := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatalf("Chtimes: %v", err)
		}
		paths = append(paths, p)
	}
	infos, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 3 || infos[0].Path != paths[2] || infos[2].Path != paths[0] {
		t.Fatalf("infos = %+v, want newest first", infos)
	}
}

func TestSession_SLS02_UnparseableSkipped(t *testing.T) {
	dir := t.TempDir()
	createSession(t, filepath.Join(dir, "good.jsonl"), userEntry("x"))
	if err := os.WriteFile(filepath.Join(dir, "bad.jsonl"), []byte("not a session\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ignore.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	infos, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 1 || !strings.HasSuffix(infos[0].Path, "good.jsonl") {
		t.Fatalf("infos = %+v, want only the good session", infos)
	}
}

func TestSession_SLS03_NameFromLastSessionInfo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	createSession(t, path,
		Entry{Type: TypeSessionInfo, Name: "first"},
		userEntry("x"),
		Entry{Type: TypeSessionInfo, Name: "second"},
	)
	infos, err := List(filepath.Dir(path))
	if err != nil || len(infos) != 1 {
		t.Fatalf("List: %v %+v", err, infos)
	}
	if infos[0].Name != "second" {
		t.Fatalf("name = %q, want the last session_info", infos[0].Name)
	}
	if infos[0].Entries != 3 {
		t.Fatalf("entries = %d, want 3", infos[0].Entries)
	}
}

func TestSession_SLS04_DefaultDir(t *testing.T) {
	a := DefaultDir("/home/me/project")
	b := DefaultDir("/home/me/project")
	c := DefaultDir("/home/me/other")
	if a != b {
		t.Fatalf("DefaultDir is not stable: %q vs %q", a, b)
	}
	if a == c {
		t.Fatalf("DefaultDir should differ per cwd: %q", a)
	}
	if strings.Contains(a, "me/project") || strings.Contains(a, "project") {
		t.Fatalf("DefaultDir %q contains the raw path", a)
	}
}

func TestSession_SLS05_DirectoryMode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "sessions")
	createSession(t, filepath.Join(dir, "s.jsonl"), userEntry("x"))
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o700 {
		t.Fatalf("dir mode = %o, want 0700", perm)
	}
}

func TestSession_FRK01_CopiesBranch(t *testing.T) {
	src, _ := openStore(t, Options{})
	a, _ := src.Append(userEntry("a"))
	b, _ := src.Append(assistantEntry("b"))
	dst := filepath.Join(t.TempDir(), "fork.jsonl")
	f, err := Fork(src, b.ID, dst, Options{})
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	defer f.Close()
	branch, _ := f.Branch("")
	if len(branch) != 2 || branch[0].ID != a.ID || branch[0].Parent != "" || branch[1].ID != b.ID || branch[1].Parent != a.ID {
		t.Fatalf("forked branch = %+v, want ids and parents preserved", branch)
	}
}

func TestSession_FRK02_ParentHeader(t *testing.T) {
	src, _ := openStore(t, Options{})
	a, _ := src.Append(userEntry("a"))
	dst := filepath.Join(t.TempDir(), "fork.jsonl")
	f, err := Fork(src, a.ID, dst, Options{})
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	defer f.Close()
	if f.Header().ParentSession != src.Header().ID {
		t.Fatalf("parent = %q, want %q", f.Header().ParentSession, src.Header().ID)
	}
}

func TestSession_FRK03_Leaf(t *testing.T) {
	src, _ := openStore(t, Options{})
	a, _ := src.Append(userEntry("a"))
	b, _ := src.Append(assistantEntry("b"))
	c, _ := src.Append(userEntry("c"))
	dst := filepath.Join(t.TempDir(), "fork.jsonl")
	f, err := Fork(src, b.ID, dst, Options{})
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	defer f.Close()
	if f.Leaf() != b.ID {
		t.Fatalf("leaf = %q, want %q", f.Leaf(), b.ID)
	}
	_ = a
	_ = c
}

func TestSession_FRK04_UnknownEntry(t *testing.T) {
	src, _ := openStore(t, Options{})
	dst := filepath.Join(t.TempDir(), "fork.jsonl")
	if _, err := Fork(src, "nope", dst, Options{}); err == nil {
		t.Fatal("Fork of an unknown entry should error")
	}
	if _, err := os.Stat(dst); err == nil {
		t.Fatal("Fork must not create the destination on failure")
	}
}

func TestSession_FRK05_DestinationExists(t *testing.T) {
	src, _ := openStore(t, Options{})
	a, _ := src.Append(userEntry("a"))
	dst := filepath.Join(t.TempDir(), "fork.jsonl")
	if err := os.WriteFile(dst, []byte("keep me\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Fork(src, a.ID, dst, Options{}); err == nil {
		t.Fatal("Fork onto an existing file should error")
	}
	data, _ := os.ReadFile(dst)
	if string(data) != "keep me\n" {
		t.Fatalf("destination changed to %q", data)
	}
}

func TestSession_FRK06_ExcludesOtherBranches(t *testing.T) {
	src, _ := openStore(t, Options{})
	a, _ := src.Append(userEntry("a"))
	b, _ := src.Append(assistantEntry("b"))
	c, _ := src.AppendOn(a.ID, userEntry("c"))
	dst := filepath.Join(t.TempDir(), "fork.jsonl")
	f, err := Fork(src, b.ID, dst, Options{})
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	defer f.Close()
	if _, ok := f.Get(c.ID); ok {
		t.Fatal("fork should not include the other branch")
	}
}
