package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/message"
)

// openStore opens a session in a temp dir and closes it at cleanup.
func openStore(t *testing.T, opt Options) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s.jsonl")
	s, err := Open(path, opt)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

// userEntry builds a user message entry.
func userEntry(content string) Entry {
	return Entry{Type: TypeMessage, Message: &message.Message{Role: message.RoleUser, Content: content}}
}

// assistantEntry builds an assistant message entry.
func assistantEntry(content string) Entry {
	return Entry{Type: TypeMessage, Message: &message.Message{Role: message.RoleAssistant, Content: content}}
}

// fileLines returns the non-empty lines of a file.
func fileLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func TestSession_SES01_Create(t *testing.T) {
	s, path := openStore(t, Options{})
	h := s.Header()
	if h.Type != headerType || h.Version != Version || h.ID == "" || h.CWD == "" || h.Created.IsZero() {
		t.Fatalf("header = %+v, want a valid header", h)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Stat: %v", err)
	}
}

func TestSession_SES02_OpenExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	a, _ := s.Append(userEntry("one"))
	b, _ := s.Append(assistantEntry("two"))
	s.Close()

	re, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer re.Close()
	if got := len(re.Entries()); got != 2 {
		t.Fatalf("entries = %d, want 2", got)
	}
	if re.Leaf() != b.ID {
		t.Fatalf("leaf = %q, want %q", re.Leaf(), b.ID)
	}
	_ = a
}

func TestSession_SES03_AppendAssignsIDAndTS(t *testing.T) {
	when := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	s, _ := openStore(t, Options{Now: func() time.Time { return when }})
	e, err := s.Append(userEntry("x"))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if e.ID == "" || !e.TS.Equal(when) {
		t.Fatalf("entry = %+v, want id and ts", e)
	}
}

func TestSession_SES04_ParentDefaultsToLeaf(t *testing.T) {
	s, _ := openStore(t, Options{})
	a, _ := s.Append(userEntry("a"))
	b, _ := s.Append(assistantEntry("b"))
	if b.Parent != a.ID {
		t.Fatalf("b.Parent = %q, want %q", b.Parent, a.ID)
	}
}

func TestSession_SES05_AppendOnBranch(t *testing.T) {
	s, _ := openStore(t, Options{})
	a, _ := s.Append(userEntry("a"))
	_, _ = s.Append(assistantEntry("b"))
	c, _ := s.AppendOn(a.ID, userEntry("c"))
	if c.Parent != a.ID {
		t.Fatalf("c.Parent = %q, want %q", c.Parent, a.ID)
	}
	kids := s.Children(a.ID)
	if len(kids) != 2 {
		t.Fatalf("children = %v, want two branches", kids)
	}
}

func TestSession_SES06_UniqueIDs(t *testing.T) {
	s, _ := openStore(t, Options{})
	seen := map[string]bool{}
	for i := 0; i < 10000; i++ {
		e, err := s.Append(userEntry("x"))
		if err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
		if seen[e.ID] {
			t.Fatalf("duplicate id %q", e.ID)
		}
		seen[e.ID] = true
	}
}

func TestSession_SES07_IDCollisionRetry(t *testing.T) {
	seq := []string{"e_h", "e_fixed", "e_second"}
	i := 0
	s, _ := openStore(t, Options{ID: func() string {
		id := seq[i]
		if i < len(seq)-1 {
			i++
		}
		return id
	}})
	if _, err := s.Append(Entry{Type: TypeMessage, ID: "e_fixed", Message: &message.Message{Role: message.RoleUser}}); err != nil {
		t.Fatalf("Append fixed: %v", err)
	}
	e, err := s.Append(userEntry("x"))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if e.ID != "e_second" {
		t.Fatalf("id = %q, want e_second after a collision", e.ID)
	}
}

func TestSession_SES08_NoHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"message","id":"a"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Open(path, Options{}); err == nil {
		t.Fatal("Open should fail without a header")
	}
}

func TestSession_SES09_UnsupportedVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"session","v":99,"id":"s"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Open(path, Options{}); err == nil {
		t.Fatal("Open should fail on an unsupported version")
	}
}

func TestSession_SES10_CorruptMiddleLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	body := `{"type":"session","v":1,"id":"s"}` + "\n" +
		`{"type":"message","id":"a","message":{"role":"user","content":"a"}}` + "\n" +
		`{not json` + "\n" +
		`{"type":"message","id":"b","message":{"role":"user","content":"b"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if got := len(s.Entries()); got != 2 {
		t.Fatalf("entries = %d, want 2", got)
	}
	warnings := s.Warnings()
	if len(warnings) != 1 || warnings[0].Line != 3 {
		t.Fatalf("warnings = %+v, want one at line 3", warnings)
	}
}

func TestSession_SES11_TruncatedFinalLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	body := `{"type":"session","v":1,"id":"s"}` + "\n" +
		`{"type":"message","id":"a","message":{"role":"user","content":"a"}}` + "\n" +
		`{"type":"message","id":"b"`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if got := len(s.Entries()); got != 1 {
		t.Fatalf("entries = %d, want 1", got)
	}
	if len(s.Warnings()) == 0 {
		t.Fatal("want a warning for the truncated line")
	}
	if _, err := s.Append(userEntry("c")); err != nil {
		t.Fatalf("Append after truncation: %v", err)
	}
	lines := fileLines(t, path)
	if !json.Valid([]byte(lines[len(lines)-1])) {
		t.Fatalf("last line = %q, want a fresh entry", lines[len(lines)-1])
	}
}

func TestSession_SES12_MissingFinalNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	body := `{"type":"session","v":1,"id":"s"}` + "\n" +
		`{"type":"message","id":"a","message":{"role":"user","content":"a"}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if _, err := s.Append(userEntry("b")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	lines := fileLines(t, path)
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want 3", len(lines))
	}
	for i, line := range lines {
		if !json.Valid([]byte(line)) {
			t.Fatalf("line %d invalid: %q", i+1, line)
		}
	}
}

func TestSession_SES13_WrongShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	body := `{"type":"session","v":1,"id":"s"}` + "\n" +
		`{"type":"message","message":5}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if len(s.Warnings()) == 0 {
		t.Fatal("want a warning for the wrong-shaped entry")
	}
}

func TestSession_SES14_UnknownType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	raw := `{"type":"future","id":"x","payload":{"a":1}}`
	body := `{"type":"session","v":1,"id":"s"}` + "\n" + raw + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	e, ok := s.Get("x")
	if !ok || e.Type != "future" || !bytes.Equal(e.Raw, []byte(raw)) {
		t.Fatalf("entry = %+v ok=%v, want the raw line kept", e, ok)
	}
	if _, err := s.Append(userEntry("a")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if !strings.Contains(strings.Join(fileLines(t, path), "\n"), raw) {
		t.Fatal("the unknown entry should still be in the file")
	}
	if msgs, _ := s.Messages(""); len(msgs) != 1 {
		t.Fatalf("context = %d messages, want the unknown entry excluded", len(msgs))
	}
}

func TestSession_SES15_DuplicateID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	body := `{"type":"session","v":1,"id":"s"}` + "\n" +
		`{"type":"message","id":"dup","message":{"role":"user","content":"first"}}` + "\n" +
		`{"type":"message","id":"dup","message":{"role":"user","content":"second"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if len(s.Entries()) != 1 || len(s.Warnings()) != 1 {
		t.Fatalf("entries = %d warnings = %d, want first kept and one warning", len(s.Entries()), len(s.Warnings()))
	}
	e, _ := s.Get("dup")
	if e.Message == nil || e.Message.Content != "first" {
		t.Fatalf("entry = %+v, want the first kept", e)
	}
}

func TestSession_SES16_MissingParent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	body := `{"type":"session","v":1,"id":"s"}` + "\n" +
		`{"type":"message","id":"a","parent":"nope","message":{"role":"user","content":"a"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	e, _ := s.Get("a")
	if e.Parent != "" || len(s.Warnings()) == 0 {
		t.Fatalf("entry = %+v warnings = %v, want a root and a warning", e, s.Warnings())
	}
}

func TestSession_SES17_ParentCycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	body := `{"type":"session","v":1,"id":"s"}` + "\n" +
		`{"type":"message","id":"a","parent":"b","message":{"role":"user","content":"a"}}` + "\n" +
		`{"type":"message","id":"b","parent":"a","message":{"role":"user","content":"b"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		s, err := Open(path, Options{})
		if err != nil {
			t.Errorf("Open: %v", err)
			return
		}
		defer s.Close()
		if len(s.Warnings()) == 0 {
			t.Error("want a warning for the cycle")
		}
		if _, err := s.Branch(""); err != nil {
			t.Errorf("Branch: %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cycle handling hung")
	}
}

func TestSession_SES18_ConcurrentAppends(t *testing.T) {
	s, path := openStore(t, Options{})
	var wg sync.WaitGroup
	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				if _, err := s.Append(userEntry(fmt.Sprintf("%d-%d", g, i))); err != nil {
					t.Errorf("Append: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	if got := len(s.Entries()); got != 200 {
		t.Fatalf("entries = %d, want 200", got)
	}
	for i, line := range fileLines(t, path) {
		if !json.Valid([]byte(line)) {
			t.Fatalf("line %d invalid: %q", i+1, line)
		}
	}
}

func TestSession_SES19_TwoStores(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	a, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open a: %v", err)
	}
	defer a.Close()
	b, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open b: %v", err)
	}
	defer b.Close()

	ea, _ := a.Append(userEntry("from a"))
	eb, _ := b.Append(userEntry("from b"))
	_, _ = a.Append(userEntry("from a again"))

	if got := len(b.Entries()); got != 3 {
		t.Fatalf("b sees %d entries, want 3", got)
	}
	if eb.Parent != ea.ID {
		t.Fatalf("b attached to %q, want a's leaf %q", eb.Parent, ea.ID)
	}
	lines := fileLines(t, path)
	if len(lines) != 4 {
		t.Fatalf("lines = %d, want header plus 3 entries", len(lines))
	}
	for i, line := range lines {
		if !json.Valid([]byte(line)) {
			t.Fatalf("line %d invalid: %q", i+1, line)
		}
	}
}

func TestSession_SES20_AppendAfterClose(t *testing.T) {
	s, _ := openStore(t, Options{})
	s.Close()
	if _, err := s.Append(userEntry("x")); err == nil {
		t.Fatal("Append after Close should error")
	}
}

func TestSession_SES21_CloseTwice(t *testing.T) {
	s, _ := openStore(t, Options{})
	if err := s.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestSession_SES22_SyncOption(t *testing.T) {
	for _, sync := range []bool{false, true} {
		s, _ := openStore(t, Options{Sync: sync})
		if _, err := s.Append(userEntry("x")); err != nil {
			t.Fatalf("Sync=%v Append: %v", sync, err)
		}
	}
}

func TestSession_SES23_NeverRewritten(t *testing.T) {
	s, path := openStore(t, Options{})
	var prefix []byte
	for i := 0; i < 20; i++ {
		before, _ := os.ReadFile(path)
		if prefix != nil && !bytes.HasPrefix(before, prefix) {
			t.Fatalf("iteration %d rewrote earlier bytes", i)
		}
		prefix = before
		if _, err := s.Append(userEntry(fmt.Sprintf("m%d", i))); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
}

func TestSession_SES24_LargeEntry(t *testing.T) {
	if testing.Short() {
		t.Skip("10 MiB entry")
	}
	s, path := openStore(t, Options{})
	content := strings.Repeat("x", 10<<20)
	e, err := s.Append(userEntry(content))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	s.Close()
	re, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer re.Close()
	got, ok := re.Get(e.ID)
	if !ok || got.Message == nil || len(got.Message.Content) != len(content) {
		t.Fatalf("large entry not reloaded (ok=%v)", ok)
	}
}

func TestSession_SES25_UnicodeEscapes(t *testing.T) {
	s, path := openStore(t, Options{})
	content := "héllo \u2028 world \"quoted\" \\ back"
	e, _ := s.Append(userEntry(content))
	s.Close()
	re, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer re.Close()
	got, _ := re.Get(e.ID)
	if got.Message.Content != content {
		t.Fatalf("content = %q, want %q", got.Message.Content, content)
	}
}

func TestSession_SES26_HeaderFields(t *testing.T) {
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s, _ := openStore(t, Options{Now: func() time.Time { return when }})
	h := s.Header()
	if h.ID == "" || h.CWD == "" || !h.Created.Equal(when) {
		t.Fatalf("header = %+v", h)
	}
}

func TestSession_SES27_EmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if s.Header().Version != Version {
		t.Fatalf("header = %+v, want a written header", s.Header())
	}
}

// A final line cut short by a crash is terminated on open. That repair adds a
// newline but no new line, so the count that numbers later warnings must not
// move: the next append is line 4 of "header, entry, junk, new entry".
func TestSession_SES28_TruncatedTailKeepsLineCount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	body := `{"type":"session","v":1,"id":"s"}` + "\n" +
		`{"type":"message","id":"a","message":{"role":"user","content":"a"}}` + "\n" +
		`{"type":"message","id":"b"`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	e, err := s.Append(userEntry("c"))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if want := len(fileLines(t, path)); e.line != want {
		t.Fatalf("appended entry is line %d, but it is line %d of the file", e.line, want)
	}
}

// Another process can append a line whose parent is itself. The load-time
// repair never sees it, so Branch must still terminate.
func TestSession_SES29_SelfParentAppendedByAnotherProcess(t *testing.T) {
	s, path := openStore(t, Options{})
	if _, err := s.Append(userEntry("a")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	line := `{"type":"message","id":"x","parent":"x","message":{"role":"user","content":"x"}}` + "\n"
	if _, err := f.WriteString(line); err != nil {
		t.Fatalf("write: %v", err)
	}
	f.Close()

	done := make(chan []Entry, 1)
	go func() {
		branch, _ := s.Branch("x")
		done <- branch
	}()
	select {
	case branch := <-done:
		if len(branch) != 1 || branch[0].ID != "x" {
			t.Fatalf("branch = %+v, want just x as a root", branch)
		}
		if len(s.Warnings()) == 0 {
			t.Fatal("want a warning for the self-parent")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Branch looped on a self-parent")
	}
}

// Two processes starting a session at the same new path must not both write a
// header: the second would be read as a corrupt entry forever.
func TestSession_SES30_ConcurrentCreateWritesOneHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	const n = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			s, err := Open(path, Options{})
			if err != nil {
				t.Errorf("Open: %v", err)
				return
			}
			s.Close()
		}()
	}
	close(start)
	wg.Wait()
	headers := 0
	for _, line := range fileLines(t, path) {
		if strings.Contains(line, `"type":"session"`) {
			headers++
		}
	}
	if headers != 1 {
		t.Fatalf("file has %d header lines, want 1", headers)
	}
}

// The loader repairs a self-parent the same way as a longer cycle.
func TestSession_SES31_SelfParentOnLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	body := `{"type":"session","v":1,"id":"s"}` + "\n" +
		`{"type":"message","id":"a","parent":"a","message":{"role":"user","content":"a"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	e, _ := s.Get("a")
	branch, err := s.Branch("a")
	if err != nil || e.Parent != "" || len(branch) != 1 || len(s.Warnings()) == 0 {
		t.Fatalf("entry = %+v branch = %v err = %v warnings = %v, want a root with a warning", e, branch, err, s.Warnings())
	}
}

// A process that crashed mid-append leaves an unterminated line. A store that
// was already open must not glue its next entry onto it.
func TestSession_SES32_AppendAfterAnotherProcessCrashed(t *testing.T) {
	s, path := openStore(t, Options{})
	if _, err := s.Append(userEntry("a")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := f.WriteString(`{"type":"message","id":"half"`); err != nil {
		t.Fatalf("write: %v", err)
	}
	f.Close()

	e, err := s.Append(userEntry("b"))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	for i, line := range fileLines(t, path)[1:] {
		if strings.Contains(line, `"half"`) && strings.Contains(line, `"b"`) {
			t.Fatalf("line %d glues the new entry to the junk: %s", i+2, line)
		}
	}
	reopened, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	if _, ok := reopened.Get(e.ID); !ok {
		t.Fatalf("entry %s lost after reopen", e.ID)
	}
}
