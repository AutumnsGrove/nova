package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/tool"
)

// edit runs the edit tool with pairs of old/new text.
func edit(t *testing.T, dir, path string, pairs ...[2]string) tool.Result {
	t.Helper()
	list := make([]map[string]string, len(pairs))
	for i, p := range pairs {
		list[i] = map[string]string{"oldText": p[0], "newText": p[1]}
	}
	return run(t, Edit(), &tool.Context{WorkDir: dir}, map[string]any{"path": path, "edits": list})
}

func TestEdit_EDT01_Single(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "hello world")
	res := edit(t, dir, "a.txt", [2]string{"world", "nova"})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	if got := readFile(t, filepath.Join(dir, "a.txt")); got != "hello nova" {
		t.Fatalf("content = %q", got)
	}
}

func TestEdit_EDT02_NotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	writeFile(t, path, "hello")
	res := edit(t, dir, "a.txt", [2]string{"missing", "x"})
	if !res.IsError || !strings.Contains(res.Content, "edit 1") {
		t.Fatalf("result = %+v, want edit 1 error", res)
	}
	if got := readFile(t, path); got != "hello" {
		t.Fatalf("content = %q, want unchanged", got)
	}
}

func TestEdit_EDT03_Duplicate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	writeFile(t, path, "x x")
	res := edit(t, dir, "a.txt", [2]string{"x", "y"})
	if !res.IsError || !strings.Contains(res.Content, "2 times") {
		t.Fatalf("result = %+v, want a duplicate error", res)
	}
	if got := readFile(t, path); got != "x x" {
		t.Fatalf("content = %q, want unchanged", got)
	}
}

func TestEdit_EDT04_EmptyOldText(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "abc")
	res := edit(t, dir, "a.txt", [2]string{"", "x"})
	if !res.IsError {
		t.Fatalf("result = %+v, want an error", res)
	}
}

func TestEdit_EDT05_EmptyEdits(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "abc")
	res, err := runRaw(t, Edit(), &tool.Context{WorkDir: dir}, `{"path":"a.txt","edits":[]}`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.IsError {
		t.Fatalf("result = %+v, want an error", res)
	}
}

func TestEdit_EDT06_TwoEdits(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "a b c")
	res := edit(t, dir, "a.txt", [2]string{"a", "A"}, [2]string{"c", "C"})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	if got := readFile(t, filepath.Join(dir, "a.txt")); got != "A b C" {
		t.Fatalf("content = %q", got)
	}
}

func TestEdit_EDT07_SecondFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	writeFile(t, path, "a b c")
	res := edit(t, dir, "a.txt", [2]string{"a", "A"}, [2]string{"missing", "X"})
	if !res.IsError {
		t.Fatalf("result = %+v, want an error", res)
	}
	if got := readFile(t, path); got != "a b c" {
		t.Fatalf("content = %q, want nothing written", got)
	}
}

func TestEdit_EDT08_MatchedAgainstOriginal(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "x y")
	res := edit(t, dir, "a.txt", [2]string{"x", "x y"}, [2]string{"y", "z"})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	if got := readFile(t, filepath.Join(dir, "a.txt")); got != "x y z" {
		t.Fatalf("content = %q", got)
	}
}

func TestEdit_EDT09_Overlap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	writeFile(t, path, "abc")
	res := edit(t, dir, "a.txt", [2]string{"abc", "X"}, [2]string{"b", "Y"})
	if !res.IsError || !strings.Contains(res.Content, "overlaps") {
		t.Fatalf("result = %+v, want an overlap error", res)
	}
	if got := readFile(t, path); got != "abc" {
		t.Fatalf("content = %q, want unchanged", got)
	}
}

func TestEdit_EDT10_Adjacent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "ab")
	res := edit(t, dir, "a.txt", [2]string{"a", "X"}, [2]string{"b", "Y"})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	if got := readFile(t, filepath.Join(dir, "a.txt")); got != "XY" {
		t.Fatalf("content = %q", got)
	}
}

func TestEdit_EDT11_OutOfOrder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "abc")
	res := edit(t, dir, "a.txt", [2]string{"c", "C"}, [2]string{"a", "A"})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	if got := readFile(t, filepath.Join(dir, "a.txt")); got != "AbC" {
		t.Fatalf("content = %q", got)
	}
}

func TestEdit_EDT12_NoChange(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "abc")
	res := edit(t, dir, "a.txt", [2]string{"abc", "abc"})
	if !res.IsError || !strings.Contains(res.Content, "no changes") {
		t.Fatalf("result = %+v, want no changes", res)
	}
}

func TestEdit_EDT13_Deletion(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "abc")
	res := edit(t, dir, "a.txt", [2]string{"b", ""})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	if got := readFile(t, filepath.Join(dir, "a.txt")); got != "ac" {
		t.Fatalf("content = %q", got)
	}
}

func TestEdit_EDT14_RegexChars(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "a.b(c)*")
	res := edit(t, dir, "a.txt", [2]string{"a.b(c)*", "literal"})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	if got := readFile(t, filepath.Join(dir, "a.txt")); got != "literal" {
		t.Fatalf("content = %q", got)
	}
}

func TestEdit_EDT15_MultiLine(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "one\ntwo\nthree\n")
	res := edit(t, dir, "a.txt", [2]string{"one\ntwo", "1\n2"})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	if got := readFile(t, filepath.Join(dir, "a.txt")); got != "1\n2\nthree\n" {
		t.Fatalf("content = %q", got)
	}
}

func TestEdit_EDT16_CRLFWithLFText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	writeFile(t, path, "a\r\nb\r\n")
	res := edit(t, dir, "a.txt", [2]string{"a\nb", "x\ny"})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	got := readFile(t, path)
	if !strings.Contains(got, "\r\n") || strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Fatalf("content = %q, want CRLF preserved", got)
	}
	if got != "x\r\ny\r\n" {
		t.Fatalf("content = %q, want x\\r\\ny\\r\\n", got)
	}
}

func TestEdit_EDT17_CRLFExact(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "a\r\nb\r\n")
	res := edit(t, dir, "a.txt", [2]string{"a\r\nb", "x\r\ny"})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	if got := readFile(t, filepath.Join(dir, "a.txt")); got != "x\r\ny\r\n" {
		t.Fatalf("content = %q", got)
	}
}

func TestEdit_EDT18_LFWithCRLFText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	writeFile(t, path, "a\nb\n")
	res := edit(t, dir, "a.txt", [2]string{"a\r\nb", "x"})
	if !res.IsError {
		t.Fatalf("result = %+v, want not found", res)
	}
	if got := readFile(t, path); got != "a\nb\n" {
		t.Fatalf("content = %q, want unchanged", got)
	}
}

func TestEdit_EDT19_BOM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	writeFile(t, path, "\ufeffabc")
	res := edit(t, dir, "a.txt", [2]string{"b", "B"})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	if got := readFile(t, path); got != "\ufeffaBc" {
		t.Fatalf("content = %q, want the BOM preserved", got)
	}
}

func TestEdit_EDT20_Whitespace(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "a b")
	res := edit(t, dir, "a.txt", [2]string{"a  b", "x"})
	if !res.IsError {
		t.Fatalf("result = %+v, want not found", res)
	}
}

func TestEdit_EDT21_Case(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "abc")
	res := edit(t, dir, "a.txt", [2]string{"ABC", "x"})
	if !res.IsError {
		t.Fatalf("result = %+v, want not found", res)
	}
}

func TestEdit_EDT22_ChangedOnDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	writeFile(t, path, "abc")
	old := editWriteHook
	editWriteHook = func() { writeFile(t, path, "external change") }
	t.Cleanup(func() { editWriteHook = old })
	res := edit(t, dir, "a.txt", [2]string{"abc", "x"})
	if !res.IsError || !strings.Contains(res.Content, "changed on disk") {
		t.Fatalf("result = %+v, want a changed-on-disk error", res)
	}
}

func TestEdit_EDT23_TooBig(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "hello world")
	old := maxFileBytes
	maxFileBytes = 5
	t.Cleanup(func() { maxFileBytes = old })
	res := edit(t, dir, "a.txt", [2]string{"hello", "x"})
	if !res.IsError {
		t.Fatalf("result = %+v, want an error", res)
	}
}

func TestEdit_EDT24_MissingDirBinary(t *testing.T) {
	dir := t.TempDir()
	if res := edit(t, dir, "missing.txt", [2]string{"a", "b"}); !res.IsError {
		t.Fatalf("missing: result = %+v", res)
	}
	if res := edit(t, dir, ".", [2]string{"a", "b"}); !res.IsError {
		t.Fatalf("dir: result = %+v", res)
	}
	writeFile(t, filepath.Join(dir, "b.bin"), "abc\x00def")
	if res := edit(t, dir, "b.bin", [2]string{"abc", "x"}); !res.IsError {
		t.Fatalf("binary: result = %+v", res)
	}
}

func TestEdit_EDT25_Permissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.sh")
	writeFile(t, path, "abc")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	edit(t, dir, "s.sh", [2]string{"abc", "x"})
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("mode = %04o, want 0755", got)
	}
}

func TestEdit_EDT26_AtomicOnFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	writeFile(t, path, "abc")
	old := editWriteHook
	editWriteHook = func() { os.Chmod(dir, 0o500) }
	t.Cleanup(func() { editWriteHook = old; os.Chmod(dir, 0o755) })
	res := edit(t, dir, "a.txt", [2]string{"abc", "x"})
	if !res.IsError {
		t.Fatalf("result = %+v, want an error", res)
	}
	if got := readFile(t, path); got != "abc" {
		t.Fatalf("content = %q, want unchanged", got)
	}
}

func TestEdit_EDT27_DetailsDiff(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "a\nb\nc\n")
	res := edit(t, dir, "a.txt", [2]string{"b", "B"})
	var details struct {
		Path    string `json:"path"`
		Added   int    `json:"added"`
		Removed int    `json:"removed"`
		Diff    string `json:"diff"`
	}
	if err := json.Unmarshal(res.Details, &details); err != nil {
		t.Fatalf("unmarshal details: %v", err)
	}
	if details.Added != 1 || details.Removed != 1 || !strings.Contains(details.Diff, "-b") || !strings.Contains(details.Diff, "+B") {
		t.Fatalf("details = %+v", details)
	}
}

func TestEdit_EDT28_DiffOmitted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n")
	old := maxDiffLines
	maxDiffLines = 5
	t.Cleanup(func() { maxDiffLines = old })
	res := edit(t, dir, "a.txt", [2]string{"5", "five"})
	var details struct {
		Omitted bool `json:"diff_omitted"`
	}
	if err := json.Unmarshal(res.Details, &details); err != nil {
		t.Fatalf("unmarshal details: %v", err)
	}
	if !details.Omitted {
		t.Fatalf("details = %+v, want diff_omitted", details)
	}
}

func TestEdit_EDT29_Concurrent(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	path := filepath.Join(dir, "a.txt")
	writeFile(t, path, b.String())

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			raw, _ := json.Marshal(map[string]any{
				"path":  "a.txt",
				"edits": []map[string]string{{"oldText": fmt.Sprintf("line %d\n", i), "newText": fmt.Sprintf("LINE %d\n", i)}},
			})
			if res, err := Edit().Run(context.Background(), raw, &tool.Context{WorkDir: dir}); err != nil || res.IsError {
				t.Errorf("edit %d: res=%+v err=%v", i, res, err)
			}
		}(i)
	}
	wg.Wait()
	got := readFile(t, path)
	for i := 0; i < 20; i++ {
		if !strings.Contains(got, fmt.Sprintf("LINE %d", i)) {
			t.Fatalf("edit %d missing:\n%s", i, got)
		}
	}
}

func TestEdit_EDT30_EditAndWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	writeFile(t, path, "abcdef")
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		raw, _ := json.Marshal(map[string]any{"path": "a.txt", "edits": []map[string]string{{"oldText": "abc", "newText": "ABC"}}})
		if _, err := Edit().Run(context.Background(), raw, &tool.Context{WorkDir: dir}); err != nil {
			t.Errorf("edit: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		raw, _ := json.Marshal(map[string]any{"path": "a.txt", "content": "written"})
		if _, err := Write().Run(context.Background(), raw, &tool.Context{WorkDir: dir}); err != nil {
			t.Errorf("write: %v", err)
		}
	}()
	wg.Wait()
	_ = readFile(t, path)
}

func TestEdit_EDT31_Unicode(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "héllo wörld")
	res := edit(t, dir, "a.txt", [2]string{"wörld", "noVa"})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	if got := readFile(t, filepath.Join(dir, "a.txt")); got != "héllo noVa" {
		t.Fatalf("content = %q", got)
	}
}

func TestEdit_EDT32_StartAndEnd(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "start middle end")
	res := edit(t, dir, "a.txt", [2]string{"start", "BEGIN"}, [2]string{"end", "END"})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	if got := readFile(t, filepath.Join(dir, "a.txt")); got != "BEGIN middle END" {
		t.Fatalf("content = %q", got)
	}
}

// TestEdit_EDT34_OverlappingOccurrences checks text that occurs twice with the
// matches overlapping is ambiguous, even though a non-overlapping count says one.
func TestEdit_EDT34_OverlappingOccurrences(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	writeFile(t, path, "aaa")
	res := edit(t, dir, "a.txt", [2]string{"aa", "b"})
	if !res.IsError || !strings.Contains(res.Content, "2 times") {
		t.Fatalf("result = %+v, want an ambiguity error naming 2 occurrences", res)
	}
	if got := readFile(t, path); got != "aaa" {
		t.Fatalf("content = %q, want unchanged", got)
	}
}

// TestEdit_EDT35_ThroughSymlink checks editing a symlink updates the file it
// points to and leaves the link in place, as write does.
func TestEdit_EDT35_ThroughSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.txt")
	link := filepath.Join(dir, "link.txt")
	writeFile(t, real, "hello")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	res := edit(t, dir, "link.txt", [2]string{"hello", "bye"})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	if got := readFile(t, real); got != "bye" {
		t.Fatalf("target content = %q, want bye", got)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the symlink was replaced by a regular file (err %v)", err)
	}
}

// TestEdit_EDT36_NotRegular checks edit refuses a file that is not a regular
// file instead of blocking on it.
func TestEdit_EDT36_NotRegular(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan tool.Result, 1)
	go func() { done <- edit(t, dir, "pipe", [2]string{"a", "b"}) }()
	select {
	case res := <-done:
		if !res.IsError {
			t.Fatalf("result = %+v, want an error", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("edit blocked on a named pipe")
	}
}

// occurrences counts every position sub starts at, overlapping ones included.
func occurrences(s, sub string) int {
	n := 0
	for i := range s {
		if strings.HasPrefix(s[i:], sub) {
			n++
		}
	}
	return n
}

// referenceEdit applies edits to content the way the tool should: matched
// against the original, exactly once, non-overlapping, and changed.
func referenceEdit(content string, pairs [][2]string) (string, bool) {
	type span struct {
		start, end int
		newText    string
	}
	var spans []span
	for _, p := range pairs {
		if p[0] == "" || occurrences(content, p[0]) != 1 {
			return "", false
		}
		start := strings.Index(content, p[0])
		spans = append(spans, span{start, start + len(p[0]), p[1]})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	for i := 1; i < len(spans); i++ {
		if spans[i].start < spans[i-1].end {
			return "", false
		}
	}
	var b strings.Builder
	prev := 0
	changed := false
	for _, s := range spans {
		b.WriteString(content[prev:s.start])
		b.WriteString(s.newText)
		if content[s.start:s.end] != s.newText {
			changed = true
		}
		prev = s.end
	}
	b.WriteString(content[prev:])
	if !changed {
		return "", false
	}
	return b.String(), true
}

// TestEdit_EDT33_Property applies random edits and checks the result.
func TestEdit_EDT33_Property(t *testing.T) {
	rng := rand.New(rand.NewSource(33))
	for i := 0; i < 200; i++ {
		dir := t.TempDir()
		content := randomText(rng)
		path := filepath.Join(dir, "a.txt")
		writeFile(t, path, content)
		pairs := [][2]string{{randomWord(rng), randomWord(rng)}}
		if rng.Intn(2) == 0 {
			pairs = append(pairs, [2]string{randomWord(rng), randomWord(rng)})
		}
		res := edit(t, dir, "a.txt", pairs...)
		want, wantOK := referenceEdit(content, pairs)
		if !wantOK {
			if !res.IsError {
				t.Fatalf("case %d: tool succeeded but reference failed: pairs=%v content=%q", i, pairs, content)
			}
			if got := readFile(t, path); got != content {
				t.Fatalf("case %d: failed edit changed the file", i)
			}
			continue
		}
		if res.IsError {
			t.Fatalf("case %d: tool failed: %+v pairs=%v content=%q", i, res, pairs, content)
		}
		if got := readFile(t, path); got != want {
			t.Fatalf("case %d: got %q, want %q", i, got, want)
		}
	}
}

// randomWord returns a short word that may appear in randomText.
func randomWord(rng *rand.Rand) string {
	words := []string{"line", "0", "1", "2", "3", "4", "changed", "added"}
	return words[rng.Intn(len(words))]
}

// FuzzEditApply checks the edit tool never panics, and on success matches the
// reference application while on failure leaving the file unchanged.
func FuzzEditApply(f *testing.F) {
	f.Add("a\nb\nc\n", "a", "A", "c", "C")
	f.Add("abc", "abc", "abc", "", "")
	f.Add("x y", "x", "x y", "y", "z")
	f.Fuzz(func(t *testing.T, content, old1, new1, old2, new2 string) {
		if len(content) > 4096 || len(old1) > 256 || len(new1) > 256 || len(old2) > 256 || len(new2) > 256 {
			return
		}
		// The tool refuses binary content, which the reference does not model.
		window := content
		if len(window) > tool.BinarySniffBytes {
			window = window[:tool.BinarySniffBytes]
		}
		if strings.IndexByte(window, 0) >= 0 {
			return
		}
		// Tool arguments cross JSON, which cannot carry invalid UTF-8; marshal
		// replaces bad bytes with U+FFFD, so match that before comparing.
		old1 = strings.ToValidUTF8(old1, "\uFFFD")
		new1 = strings.ToValidUTF8(new1, "\uFFFD")
		old2 = strings.ToValidUTF8(old2, "\uFFFD")
		new2 = strings.ToValidUTF8(new2, "\uFFFD")
		dir := t.TempDir()
		path := filepath.Join(dir, "a.txt")
		writeFile(t, path, content)
		pairs := [][2]string{{old1, new1}, {old2, new2}}
		raw, _ := json.Marshal(map[string]any{
			"path":  "a.txt",
			"edits": []map[string]string{{"oldText": old1, "newText": new1}, {"oldText": old2, "newText": new2}},
		})
		res, err := Edit().Run(context.Background(), raw, &tool.Context{WorkDir: dir})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		got := readFile(t, path)
		want, wantOK := referenceEdit(content, pairs)
		if wantOK {
			if res.IsError {
				t.Fatalf("tool failed but reference succeeded: %+v", res)
			}
			if got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		} else if !res.IsError || got != content {
			t.Fatalf("reference failed but tool result was %+v, file %q", res, got)
		}
	})
}
