package fs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/tool"
)

// readDetailsOf parses a read result's Details.
func readDetailsOf(t *testing.T, res tool.Result) readDetails {
	t.Helper()
	var d readDetails
	if err := json.Unmarshal(res.Details, &d); err != nil {
		t.Fatalf("unmarshal details: %v", err)
	}
	return d
}

// lines builds a file of n numbered lines.
func numberedFile(t *testing.T, dir, name string, n int) string {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	path := filepath.Join(dir, name)
	writeFile(t, path, b.String())
	return path
}

// TestRead_RDF01_Normal checks a normal three-line file.
func TestRead_RDF01_Normal(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "a\nb\nc")
	res := run(t, Read(), &tool.Context{WorkDir: dir}, map[string]any{"path": "a.txt"})
	for _, want := range []string{"1\ta\n", "2\tb\n", "3\tc\n"} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("content = %q, want %q", res.Content, want)
		}
	}
	if res.Truncated {
		t.Fatalf("content = %q, want no truncation", res.Content)
	}
}

// TestRead_RDF02_TrailingNewline checks a trailing newline adds no line.
func TestRead_RDF02_TrailingNewline(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "a\nb\n")
	res := run(t, Read(), &tool.Context{WorkDir: dir}, map[string]any{"path": "a.txt"})
	if strings.Contains(res.Content, "3\t") {
		t.Fatalf("content = %q, want two lines", res.Content)
	}
	if d := readDetailsOf(t, res); d.Total == nil || *d.Total != 2 {
		t.Fatalf("total = %v, want 2", d.Total)
	}
}

// TestRead_RDF03_NoTrailingNewline checks the final line counts.
func TestRead_RDF03_NoTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "a\nb")
	res := run(t, Read(), &tool.Context{WorkDir: dir}, map[string]any{"path": "a.txt"})
	if !strings.Contains(res.Content, "2\tb") {
		t.Fatalf("content = %q, want line 2", res.Content)
	}
}

// TestRead_RDF04_Empty checks an empty file.
func TestRead_RDF04_Empty(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "e.txt"), "")
	res := run(t, Read(), &tool.Context{WorkDir: dir}, map[string]any{"path": "e.txt"})
	if res.Content != "(empty file)" {
		t.Fatalf("content = %q, want (empty file)", res.Content)
	}
}

// TestRead_RDF05_OnlyNewline checks a file of one newline is one empty line.
func TestRead_RDF05_OnlyNewline(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "n.txt"), "\n")
	res := run(t, Read(), &tool.Context{WorkDir: dir}, map[string]any{"path": "n.txt"})
	if !strings.Contains(res.Content, "1\t\n") {
		t.Fatalf("content = %q, want one empty line", res.Content)
	}
}

// TestRead_RDF06_Offset checks reading starts at the offset.
func TestRead_RDF06_Offset(t *testing.T) {
	dir := t.TempDir()
	numberedFile(t, dir, "a.txt", 10)
	res := run(t, Read(), &tool.Context{WorkDir: dir}, map[string]any{"path": "a.txt", "offset": 4})
	if !strings.Contains(res.Content, "4\tline 4") || strings.Contains(res.Content, "3\tline 3") {
		t.Fatalf("content = %q, want it to start at line 4", res.Content)
	}
}

// TestRead_RDF07_Limit checks the limit and continue trailer.
func TestRead_RDF07_Limit(t *testing.T) {
	dir := t.TempDir()
	numberedFile(t, dir, "a.txt", 10)
	res := run(t, Read(), &tool.Context{WorkDir: dir}, map[string]any{"path": "a.txt", "limit": 3})
	if strings.Count(res.Content, "\t") != 3 {
		t.Fatalf("content = %q, want three lines", res.Content)
	}
	if !res.Truncated || !strings.Contains(res.Content, "offset=") {
		t.Fatalf("result = %+v, want a continue trailer", res)
	}
}

// TestRead_RDF08_OffsetBeyondEnd checks an offset past the end is an error.
func TestRead_RDF08_OffsetBeyondEnd(t *testing.T) {
	dir := t.TempDir()
	numberedFile(t, dir, "a.txt", 3)
	res := run(t, Read(), &tool.Context{WorkDir: dir}, map[string]any{"path": "a.txt", "offset": 10})
	if !res.IsError || !strings.Contains(res.Content, "3 lines") {
		t.Fatalf("result = %+v, want an error naming 3 lines", res)
	}
}

// TestRead_RDF09_OffsetOnePastEnd checks one past the end is an error.
func TestRead_RDF09_OffsetOnePastEnd(t *testing.T) {
	dir := t.TempDir()
	numberedFile(t, dir, "a.txt", 3)
	res := run(t, Read(), &tool.Context{WorkDir: dir}, map[string]any{"path": "a.txt", "offset": 4})
	if !res.IsError {
		t.Fatalf("result = %+v, want an error", res)
	}
}

// TestRead_RDF10_OffsetZero checks zero and negative offsets are errors.
func TestRead_RDF10_OffsetZero(t *testing.T) {
	dir := t.TempDir()
	numberedFile(t, dir, "a.txt", 3)
	// A pointer-sized zero must bypass the omitted-offset default.
	for _, offset := range []int{0, -1} {
		raw := fmt.Sprintf(`{"path":"a.txt","offset":%d}`, offset)
		res, err := runRaw(t, Read(), &tool.Context{WorkDir: dir}, raw)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if !res.IsError {
			t.Fatalf("offset %d: result = %+v, want an error", offset, res)
		}
	}
}

// TestRead_RDF11_LimitZero checks a zero limit uses the default.
func TestRead_RDF11_LimitZero(t *testing.T) {
	dir := t.TempDir()
	numberedFile(t, dir, "a.txt", 5)
	res := run(t, Read(), &tool.Context{WorkDir: dir}, map[string]any{"path": "a.txt", "limit": 0})
	if strings.Contains(res.Content, "more lines") {
		t.Fatalf("content = %q, want all five lines", res.Content)
	}
}

// TestRead_RDF12_LimitAboveMax checks a limit above the max is capped.
func TestRead_RDF12_LimitAboveMax(t *testing.T) {
	dir := t.TempDir()
	numberedFile(t, dir, "a.txt", maxReadLines+1000)
	res := run(t, Read(), &tool.Context{WorkDir: dir}, map[string]any{"path": "a.txt", "limit": 5000})
	if strings.Count(res.Content, "\n") < maxReadLines {
		t.Fatalf("only %d lines returned, want %d", strings.Count(res.Content, "\n"), maxReadLines)
	}
	if !res.Truncated {
		t.Fatalf("result = %+v, want truncation", res)
	}
}

// TestRead_RDF13_LineCap checks a long file stops at the line cap.
func TestRead_RDF13_LineCap(t *testing.T) {
	dir := t.TempDir()
	numberedFile(t, dir, "a.txt", maxReadLines+1000)
	res := run(t, Read(), &tool.Context{WorkDir: dir}, map[string]any{"path": "a.txt"})
	if !res.Truncated || !strings.Contains(res.Content, "1000 more lines") {
		t.Fatalf("result = %+v, want a 1000-more trailer", res)
	}
}

// TestRead_RDF14_ByteCap checks a byte-heavy file stops by bytes.
func TestRead_RDF14_ByteCap(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString(strings.Repeat("x", 500))
		b.WriteByte('\n')
	}
	writeFile(t, filepath.Join(dir, "a.txt"), b.String())
	res := run(t, Read(), &tool.Context{WorkDir: dir}, map[string]any{"path": "a.txt"})
	if !res.Truncated || len(res.Content) > maxReadBytes+2000 {
		t.Fatalf("len = %d, truncated = %v, want a byte-capped read", len(res.Content), res.Truncated)
	}
}

// TestRead_RDF15_LongLine checks a long line is cut with its real length.
func TestRead_RDF15_LongLine(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), strings.Repeat("y", 10000))
	res := run(t, Read(), &tool.Context{WorkDir: dir}, map[string]any{"path": "a.txt"})
	if !strings.Contains(res.Content, "line truncated, 10000 chars") {
		t.Fatalf("content = %q, want the truncation marker", res.Content)
	}
}

// TestRead_RDF16_CRLF checks CRLF endings are stripped and reported.
func TestRead_RDF16_CRLF(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "a\r\nb\r\n")
	res := run(t, Read(), &tool.Context{WorkDir: dir}, map[string]any{"path": "a.txt"})
	if strings.Contains(res.Content, "\r") {
		t.Fatalf("content = %q, want no carriage returns", res.Content)
	}
	if d := readDetailsOf(t, res); d.LineEndings != "crlf" {
		t.Fatalf("line_endings = %q, want crlf", d.LineEndings)
	}
}

// TestRead_RDF17_Mixed checks mixed endings are reported.
func TestRead_RDF17_Mixed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "a\r\nb\n")
	res := run(t, Read(), &tool.Context{WorkDir: dir}, map[string]any{"path": "a.txt"})
	if d := readDetailsOf(t, res); d.LineEndings != "mixed" {
		t.Fatalf("line_endings = %q, want mixed", d.LineEndings)
	}
}

// TestRead_RDF18_BOM checks a BOM is stripped from line 1 and reported.
func TestRead_RDF18_BOM(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "\ufeffa")
	res := run(t, Read(), &tool.Context{WorkDir: dir}, map[string]any{"path": "a.txt"})
	if strings.Contains(res.Content, "\ufeff") {
		t.Fatalf("content = %q, want the BOM stripped", res.Content)
	}
	if d := readDetailsOf(t, res); !d.BOM {
		t.Fatal("bom = false, want true")
	}
}

// TestRead_RDF19_Binary checks a NUL in the sniff window is a binary error.
func TestRead_RDF19_Binary(t *testing.T) {
	dir := t.TempDir()
	data := append([]byte(strings.Repeat("a", 10)), 0, 'b')
	writeFile(t, filepath.Join(dir, "b.bin"), string(data))
	res := run(t, Read(), &tool.Context{WorkDir: dir}, map[string]any{"path": "b.bin"})
	if !res.IsError || !strings.Contains(res.Content, "looks like a binary file") {
		t.Fatalf("result = %+v, want a binary error", res)
	}
	if !strings.Contains(res.Content, fmt.Sprintf("%d bytes", len(data))) {
		t.Fatalf("content = %q, want the size", res.Content)
	}
}

// TestRead_RDF20_NulAfterWindow checks a NUL past the sniff window is text.
func TestRead_RDF20_NulAfterWindow(t *testing.T) {
	dir := t.TempDir()
	data := []byte(strings.Repeat("a", binarySniff+1000))
	data[binarySniff+10] = 0
	writeFile(t, filepath.Join(dir, "a.txt"), string(data))
	res := run(t, Read(), &tool.Context{WorkDir: dir}, map[string]any{"path": "a.txt"})
	if res.IsError {
		t.Fatalf("result = %+v, want it treated as text", res)
	}
}

// TestRead_RDF21_InvalidUTF8 checks invalid bytes are replaced and noted.
func TestRead_RDF21_InvalidUTF8(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "a\xff\xfeb")
	res := run(t, Read(), &tool.Context{WorkDir: dir}, map[string]any{"path": "a.txt"})
	if !strings.Contains(res.Content, "not valid UTF-8") {
		t.Fatalf("content = %q, want the UTF-8 note", res.Content)
	}
}

// TestRead_RDF22_Directory checks a directory is an error.
func TestRead_RDF22_Directory(t *testing.T) {
	dir := t.TempDir()
	res := run(t, Read(), &tool.Context{WorkDir: dir}, map[string]any{"path": "."})
	if !res.IsError || !strings.Contains(res.Content, "is a directory; use list") {
		t.Fatalf("result = %+v, want the directory error", res)
	}
}

// TestRead_RDF23_Missing checks a missing file is an error.
func TestRead_RDF23_Missing(t *testing.T) {
	dir := t.TempDir()
	res := run(t, Read(), &tool.Context{WorkDir: dir}, map[string]any{"path": "missing.txt"})
	if !res.IsError {
		t.Fatalf("result = %+v, want an error", res)
	}
}

// TestRead_RDF24_PermissionDenied checks an unreadable file is an error.
func TestRead_RDF24_PermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	writeFile(t, path, "x")
	if err := os.Chmod(path, 0); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	res := run(t, Read(), &tool.Context{WorkDir: dir}, map[string]any{"path": "secret.txt"})
	if !res.IsError {
		t.Fatalf("result = %+v, want an error", res)
	}
}

// TestRead_RDF25_Symlink checks a symlink reads its target.
func TestRead_RDF25_Symlink(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "real.txt"), "hello")
	if err := os.Symlink(filepath.Join(dir, "real.txt"), filepath.Join(dir, "link.txt")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	res := run(t, Read(), &tool.Context{WorkDir: dir}, map[string]any{"path": "link.txt"})
	if !strings.Contains(res.Content, "hello") {
		t.Fatalf("content = %q, want hello", res.Content)
	}
}

// TestRead_RDF26_DanglingSymlink checks a dangling symlink is an error.
func TestRead_RDF26_DanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(dir, "missing"), filepath.Join(dir, "dangling")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	res := run(t, Read(), &tool.Context{WorkDir: dir}, map[string]any{"path": "dangling"})
	if !res.IsError {
		t.Fatalf("result = %+v, want an error", res)
	}
}

// TestRead_RDF27_SymlinkLoop checks a symlink loop is an error, not a hang.
func TestRead_RDF27_SymlinkLoop(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	if err := os.Symlink(b, a); err != nil {
		t.Fatalf("Symlink a: %v", err)
	}
	if err := os.Symlink(a, b); err != nil {
		t.Fatalf("Symlink b: %v", err)
	}
	res := run(t, Read(), &tool.Context{WorkDir: dir}, map[string]any{"path": "a"})
	if !res.IsError {
		t.Fatalf("result = %+v, want an error", res)
	}
}

// TestRead_RDF28_HugeStreaming checks a huge sparse file is streamed: limit 10
// returns quickly and the returned content stays small.
func TestRead_RDF28_HugeStreaming(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "huge.txt")
	// A text prefix longer than the sniff window, then a sparse hole so the
	// file is large without writing it.
	var prefix strings.Builder
	for i := 0; i < binarySniff; i++ {
		prefix.WriteString("x\n")
	}
	writeFile(t, path, prefix.String())
	const size = 200 << 20
	if err := os.Truncate(path, size); err != nil {
		t.Fatalf("Truncate: %v", err)
	}
	began := time.Now()
	res := run(t, Read(), &tool.Context{WorkDir: dir}, map[string]any{"path": "huge.txt", "limit": 10})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	if len(res.Content) > maxReadBytes+maxLineChars*4 {
		t.Fatalf("content length %d, want it bounded", len(res.Content))
	}
	if elapsed := time.Since(began); elapsed > 30*time.Second {
		t.Fatalf("read took %s", elapsed)
	}
}

// TestRead_RDF29_StreamEndsEarly checks a stream that ends before its size
// returns what was read without panicking.
func TestRead_RDF29_StreamEndsEarly(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	go func() {
		w, err := os.OpenFile(fifo, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		w.WriteString("a\nb\n")
		w.Close()
	}()
	res := run(t, Read(), &tool.Context{WorkDir: dir}, map[string]any{"path": "pipe"})
	if res.IsError || !strings.Contains(res.Content, "2\tb") {
		t.Fatalf("result = %+v, want the two lines", res)
	}
}

// TestRead_RDF30_Details checks the structured details.
func TestRead_RDF30_Details(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "a\nb\nc\n")
	res := run(t, Read(), &tool.Context{WorkDir: dir}, map[string]any{"path": "a.txt"})
	d := readDetailsOf(t, res)
	if !strings.HasSuffix(d.Path, "a.txt") || d.Start != 1 || d.End != 3 || d.Total == nil || *d.Total != 3 {
		t.Fatalf("details = %+v", d)
	}
	if d.LineEndings != "lf" || d.BOM {
		t.Fatalf("details = %+v", d)
	}
}

// TestRead_RDF31_Annotations checks the tool's annotations.
func TestRead_RDF31_Annotations(t *testing.T) {
	info := Read().Info()
	if !info.ReadOnly || !info.Parallel {
		t.Fatalf("annotations = %+v, want read-only and parallel", info)
	}
}
