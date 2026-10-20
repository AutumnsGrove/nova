package proto

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// oneByteReader yields one byte per Read.
type oneByteReader struct {
	data []byte
}

func (r *oneByteReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	p[0] = r.data[0]
	r.data = r.data[1:]
	return 1, nil
}

// chunkReader yields fixed-size chunks.
type chunkReader struct {
	data []byte
	n    int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := r.n
	if n > len(p) {
		n = len(p)
	}
	if n > len(r.data) {
		n = len(r.data)
	}
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

// partialErrReader returns data once, then a hard error.
type partialErrReader struct {
	data []byte
}

var errBoom = errors.New("boom")

func (r *partialErrReader) Read(p []byte) (int, error) {
	if r.data == nil {
		return 0, errBoom
	}
	n := copy(p, r.data)
	r.data = nil
	return n, nil
}

func TestProto_FRM01_SingleNewline(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.Write(Command{Type: CmdPrompt, Text: "a\nb"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if buf.Len() == 0 || buf.Bytes()[buf.Len()-1] != '\n' {
		t.Fatalf("output = %q, want a trailing newline", buf.Bytes())
	}
	if n := bytes.Count(buf.Bytes(), []byte{'\n'}); n != 1 {
		t.Fatalf("output has %d newlines, want 1: %q", n, buf.Bytes())
	}
}

func TestProto_FRM02_LineSeparatorsEscaped(t *testing.T) {
	var buf bytes.Buffer
	if err := NewWriter(&buf).Write(Command{Type: CmdPrompt, Text: "a\u2028b\u2029c"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if bytes.Contains(buf.Bytes(), []byte("\u2028")) || bytes.Contains(buf.Bytes(), []byte("\u2029")) {
		t.Fatalf("output = %q, want U+2028/U+2029 escaped", buf.Bytes())
	}
}

func TestProto_FRM03_ConcurrentWriters(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	var wg sync.WaitGroup
	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if err := w.Write(Command{Type: CmdPrompt, ID: string(rune('a' + g)), Text: "x"}); err != nil {
					t.Errorf("Write: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	lines := bytes.Split(bytes.TrimSuffix(buf.Bytes(), []byte{'\n'}), []byte{'\n'})
	if len(lines) != 2000 {
		t.Fatalf("got %d lines, want 2000", len(lines))
	}
	for _, line := range lines {
		if !json.Valid(line) {
			t.Fatalf("interleaved or invalid line: %q", line)
		}
	}
}

func TestProto_FRM04_SplitOnNewlineOnly(t *testing.T) {
	in := []byte("{\"type\":\"prompt\",\"text\":\"a\u2028b\"}\n")
	line, err := NewReader(bytes.NewReader(in), 0).Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !bytes.Equal(line, bytes.TrimSuffix(in, []byte{'\n'})) {
		t.Fatalf("line = %q, want one line", line)
	}
}

func TestProto_FRM05_CRLF(t *testing.T) {
	line, err := NewReader(bytes.NewReader([]byte("line\r\n")), 0).Next()
	if err != nil || string(line) != "line" {
		t.Fatalf("line = %q err = %v, want line", line, err)
	}
}

func TestProto_FRM06_EmptyLinesSkipped(t *testing.T) {
	line, err := NewReader(bytes.NewReader([]byte("\n\nline\n")), 0).Next()
	if err != nil || string(line) != "line" {
		t.Fatalf("line = %q err = %v, want line", line, err)
	}
}

func TestProto_FRM07_TwoFramesOneRead(t *testing.T) {
	r := NewReader(bytes.NewReader([]byte("a\nb\n")), 0)
	first, _ := r.Next()
	second, _ := r.Next()
	if string(first) != "a" || string(second) != "b" {
		t.Fatalf("got %q, %q, want a, b", first, second)
	}
}

func TestProto_FRM08_FrameInManyReads(t *testing.T) {
	line, err := NewReader(&oneByteReader{data: []byte("abc\n")}, 0).Next()
	if err != nil || string(line) != "abc" {
		t.Fatalf("line = %q err = %v, want abc", line, err)
	}
}

func TestProto_FRM09_FinalLineWithoutNewline(t *testing.T) {
	r := NewReader(bytes.NewReader([]byte("abc")), 0)
	line, err := r.Next()
	if err != nil || string(line) != "abc" {
		t.Fatalf("line = %q err = %v, want abc", line, err)
	}
	if _, err := r.Next(); err != io.EOF {
		t.Fatalf("second Next err = %v, want io.EOF", err)
	}
}

func TestProto_FRM10_LineLimit(t *testing.T) {
	atLimit := bytes.Repeat([]byte("a"), 100)
	if line, err := NewReader(bytes.NewReader(append(atLimit, '\n')), 100).Next(); err != nil || len(line) != 100 {
		t.Fatalf("at limit: line len %d err %v, want ok", len(line), err)
	}
	over := bytes.Repeat([]byte("a"), 101)
	if _, err := NewReader(bytes.NewReader(append(over, '\n')), 100).Next(); !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("over limit err = %v, want ErrLineTooLong", err)
	}
}

func TestProto_FRM11_StaysFailed(t *testing.T) {
	r := NewReader(bytes.NewReader(bytes.Repeat([]byte("a"), 200)), 100)
	if _, err := r.Next(); !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("first err = %v, want ErrLineTooLong", err)
	}
	if _, err := r.Next(); !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("second err = %v, want the same failure", err)
	}
}

// infiniteReader yields 'a' forever.
type infiniteReader struct{ n int }

func (r *infiniteReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	r.n += len(p)
	return len(p), nil
}

func TestProto_FRM12_HostileNoNewline(t *testing.T) {
	ir := &infiniteReader{}
	if _, err := NewReader(ir, 1000).Next(); !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("err = %v, want ErrLineTooLong", err)
	}
	if ir.n > 1000+8192 {
		t.Fatalf("read %d bytes, want memory bounded near maxLine", ir.n)
	}
}

func TestProto_FRM13_CleanEOF(t *testing.T) {
	if _, err := NewReader(bytes.NewReader(nil), 0).Next(); err != io.EOF {
		t.Fatalf("err = %v, want io.EOF", err)
	}
}

func TestProto_FRM14_ReaderError(t *testing.T) {
	if _, err := NewReader(&partialErrReader{data: []byte("ab")}, 0).Next(); !errors.Is(err, errBoom) {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestProto_PRT01_DecodeCommand(t *testing.T) {
	f, err := Decode([]byte(`{"type":"prompt","id":"1","text":"hi"}`))
	if err != nil || f.Command == nil || f.Command.ID != "1" || f.Command.Text != "hi" {
		t.Fatalf("frame = %+v err = %v", f, err)
	}
}

func TestProto_PRT02_UnknownCommand(t *testing.T) {
	f, err := Decode([]byte(`{"type":"frobnicate"}`))
	if err != nil || f.Command == nil || f.Command.Type != "frobnicate" {
		t.Fatalf("frame = %+v err = %v, want a Command", f, err)
	}
}

func TestProto_PRT03_DecodeEachEvent(t *testing.T) {
	names := []string{
		EventRunStart, EventTurnStart, EventMessageStart, EventTextDelta, EventThinkingStart,
		EventThinkingDelta, EventThinkingEnd, EventToolCall, EventToolStart, EventToolUpdate,
		EventToolEnd, EventPermission, EventMessageEnd, EventTurnEnd, EventUsage, EventRetry,
		EventCompaction, EventQueueUpdate, EventRunEnd, EventError, EventExtensionError,
		EventUIExpired, EventSessionChanged, EventStateChanged,
	}
	for _, name := range names {
		f, err := Decode([]byte(`{"type":"` + name + `"}`))
		if err != nil || f.Event == nil || f.Event.Type != name {
			t.Fatalf("%s: frame = %+v err = %v", name, f, err)
		}
	}
	f, err := Decode([]byte(`{"type":"tool_call","run":"r","call":{"id":"c","name":"read","arguments":{"path":"x"}}}`))
	if err != nil || f.Event == nil || f.Event.Call == nil || f.Event.Call.Name != "read" {
		t.Fatalf("tool_call frame = %+v err = %v", f, err)
	}
}

func TestProto_PRT04_DecodeFamilies(t *testing.T) {
	if f, err := Decode([]byte(`{"type":"response","command":"prompt","ok":true}`)); err != nil || f.Response == nil || !f.Response.OK {
		t.Fatalf("response = %+v err = %v", f, err)
	}
	if f, err := Decode([]byte(`{"type":"hello","version":1,"client":"t"}`)); err != nil || f.Hello == nil || f.Hello.Version != 1 {
		t.Fatalf("hello = %+v err = %v", f, err)
	}
	if f, err := Decode([]byte(`{"type":"ui_request","id":"u","method":"select"}`)); err != nil || f.UIRequest == nil || f.UIRequest.Method != "select" {
		t.Fatalf("ui_request = %+v err = %v", f, err)
	}
	if f, err := Decode([]byte(`{"type":"ui_response","id":"u","ok":true}`)); err != nil || f.UIResponse == nil || !f.UIResponse.OK {
		t.Fatalf("ui_response = %+v err = %v", f, err)
	}
}

func TestProto_PRT05_InvalidJSON(t *testing.T) {
	if _, err := Decode([]byte(`{`)); !errors.Is(err, ErrBadFrame) {
		t.Fatalf("err = %v, want ErrBadFrame", err)
	}
}

// A caller that wants the position of the syntax error must be able to reach
// the JSON error behind ErrBadFrame, not only its text.
func TestProto_PRT20_BadFrameKeepsCause(t *testing.T) {
	_, err := Decode([]byte(`{`))
	var syn *json.SyntaxError
	if !errors.Is(err, ErrBadFrame) || !errors.As(err, &syn) {
		t.Fatalf("syntax err = %v, want ErrBadFrame wrapping *json.SyntaxError", err)
	}
	_, err = Decode([]byte(`{"type":"prompt","text":5}`))
	var typ *json.UnmarshalTypeError
	if !errors.Is(err, ErrBadFrame) || !errors.As(err, &typ) {
		t.Fatalf("type err = %v, want ErrBadFrame wrapping *json.UnmarshalTypeError", err)
	}
}

func TestProto_PRT06_NotAnObject(t *testing.T) {
	for _, in := range []string{`[1]`, `"x"`, `5`, `null`} {
		if _, err := Decode([]byte(in)); !errors.Is(err, ErrBadFrame) {
			t.Fatalf("%s: err = %v, want ErrBadFrame", in, err)
		}
	}
}

func TestProto_PRT07_MissingType(t *testing.T) {
	if _, err := Decode([]byte(`{}`)); !errors.Is(err, ErrBadFrame) {
		t.Fatalf("err = %v, want ErrBadFrame", err)
	}
}

func TestProto_PRT08_TypeNotString(t *testing.T) {
	if _, err := Decode([]byte(`{"type":5}`)); !errors.Is(err, ErrBadFrame) {
		t.Fatalf("err = %v, want ErrBadFrame", err)
	}
}

func TestProto_PRT09_UnknownType(t *testing.T) {
	for _, in := range []string{`{"type":"","id":"x"}`, `{"type":"a b","id":"x"}`, `{"type":"a\n","id":"x"}`} {
		_, err := Decode([]byte(in))
		var ute *UnknownTypeError
		if !errors.As(err, &ute) {
			t.Fatalf("%s: err = %v, want UnknownTypeError", in, err)
		}
		if ute.ID != "x" {
			t.Fatalf("%s: id = %q, want x", in, ute.ID)
		}
	}
}

func TestProto_PRT10_UnknownFieldsIgnored(t *testing.T) {
	f, err := Decode([]byte(`{"type":"prompt","text":"hi","future":{"a":1}}`))
	if err != nil || f.Command == nil || f.Command.Text != "hi" {
		t.Fatalf("frame = %+v err = %v", f, err)
	}
}

func TestProto_PRT11_WrongFieldType(t *testing.T) {
	_, err := Decode([]byte(`{"type":"prompt","text":5}`))
	if !errors.Is(err, ErrBadFrame) || !strings.Contains(err.Error(), "text") {
		t.Fatalf("err = %v, want ErrBadFrame naming text", err)
	}
}

func TestProto_PRT13_EventOmitsEmpty(t *testing.T) {
	b, err := json.Marshal(Event{Type: EventRunStart, Seq: 1, Run: "r"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"type", "seq", "run"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("%s missing from %s", key, b)
		}
	}
	for _, key := range []string{"text", "call", "result", "error"} {
		if _, ok := m[key]; ok {
			t.Fatalf("%s should be omitted from %s", key, b)
		}
	}
}

func TestProto_PRT14_TurnZeroPreserved(t *testing.T) {
	zero := 0
	b, err := json.Marshal(Event{Type: EventTurnStart, Turn: &zero})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"turn":0`) {
		t.Fatalf("marshal = %s, want turn 0 present", b)
	}
}

func TestProto_PRT15_UsageReasoning(t *testing.T) {
	f, err := Decode([]byte(`{"type":"usage","usage":{"prompt_tokens":1,"completion_tokens":2,"reasoning_tokens":5,"total_tokens":3}}`))
	if err != nil || f.Event == nil || f.Event.Usage == nil || f.Event.Usage.ReasoningTokens != 5 {
		t.Fatalf("frame = %+v err = %v", f, err)
	}
}

func TestProto_PRT16_LargeFrame(t *testing.T) {
	text := strings.Repeat("x", 15<<20)
	line, _ := json.Marshal(Command{Type: CmdPrompt, Text: text})
	got, err := NewReader(bytes.NewReader(append(line, '\n')), 0).Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	f, err := Decode(got)
	if err != nil || len(f.Command.Text) != len(text) {
		t.Fatalf("err = %v len = %d, want %d", err, len(f.Command.Text), len(text))
	}
}

func TestProto_PRT17_ResponseShapes(t *testing.T) {
	ok := Response{Type: FrameResponse, Command: CmdGetState, OK: true, Data: json.RawMessage(`{"a":1}`)}
	b, _ := json.Marshal(ok)
	if !json.Valid(b) || bytes.Contains(b, []byte(`"error"`)) {
		t.Fatalf("ok response = %s", b)
	}
	bad := Response{Type: FrameResponse, Command: CmdGetState, OK: false, Error: &Error{Code: CodeBusy, Message: "busy"}}
	b, _ = json.Marshal(bad)
	if bytes.Contains(b, []byte(`"data"`)) {
		t.Fatalf("error response = %s, want no data", b)
	}
}

func TestProto_PRT18_DetailsPreserved(t *testing.T) {
	raw := []byte(`{"a": 1, "b": [ 2, 3 ]}`)
	src := []byte(`{"type":"tool_end","result":{"content":"x","is_error":false,"details":` + string(raw) + `}}`)
	f, err := Decode(src)
	if err != nil || f.Event == nil || f.Event.Result == nil {
		t.Fatalf("frame = %+v err = %v", f, err)
	}
	if !bytes.Equal(f.Event.Result.Details, raw) {
		t.Fatalf("details = %q, want %q", f.Event.Result.Details, raw)
	}
}

func TestProto_FRM_SplitCRLF(t *testing.T) {
	// A final CRLF line must not leave a stray CR.
	r := NewReader(bytes.NewReader([]byte("a\r\nb\r\n")), 0)
	a, _ := r.Next()
	b, _ := r.Next()
	if string(a) != "a" || string(b) != "b" {
		t.Fatalf("got %q, %q, want a, b", a, b)
	}
}

func FuzzDecode(f *testing.F) {
	f.Add([]byte(`{"type":"prompt","text":"hi"}`))
	f.Add([]byte(`{"type":"tool_end","result":{"content":"x"}}`))
	f.Add([]byte(`[1,2,3]`))
	f.Add([]byte(``))
	f.Fuzz(func(t *testing.T, line []byte) {
		if !utf8.Valid(line) {
			return
		}
		Decode(line)
	})
}

func FuzzReader(f *testing.F) {
	f.Add([]byte("a\nb\nc"), uint8(1))
	f.Add([]byte("\r\n\n"), uint8(2))
	f.Add([]byte("hello"), uint8(3))
	f.Fuzz(func(t *testing.T, data []byte, chunk uint8) {
		if chunk == 0 {
			chunk = 1
		}
		if len(data) > 1<<16 {
			t.Skip()
		}
		base, err := readAll(NewReader(bytes.NewReader(data), len(data)+1))
		if err != nil {
			t.Fatalf("base: %v", err)
		}
		chunked, err := readAll(NewReader(&chunkReader{data: data, n: int(chunk)}, len(data)+1))
		if err != nil {
			t.Fatalf("chunked: %v", err)
		}
		if len(base) != len(chunked) {
			t.Fatalf("chunking changed the lines: %q vs %q", base, chunked)
		}
		for i := range base {
			if !bytes.Equal(base[i], chunked[i]) {
				t.Fatalf("line %d differs: %q vs %q", i, base[i], chunked[i])
			}
		}
	})
}

// readAll drains a reader, returning the non-empty lines.
func readAll(r *Reader) ([][]byte, error) {
	var out [][]byte
	for {
		line, err := r.Next()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		out = append(out, line)
	}
}

func TestProto_LGP25_LoginEndDecodesAsEvent(t *testing.T) {
	var buf bytes.Buffer
	ev := Event{Type: EventLoginEnd, ID: "openrouter", Error: &Error{Code: CodeProviderError, Message: "denied"}}
	if err := NewWriter(&buf).Write(ev); err != nil {
		t.Fatalf("Write: %v", err)
	}
	f, err := Decode(bytes.TrimSpace(buf.Bytes()))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if f.Event == nil || f.Event.ID != "openrouter" || f.Event.Error == nil || f.Event.Error.Code != CodeProviderError {
		t.Fatalf("login_end decoded as %+v", f)
	}
}
