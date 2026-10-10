package proto

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"unicode"
)

// ErrBadFrame reports a line that is not a usable protocol frame.
var ErrBadFrame = errors.New("proto: bad frame")

// ErrLineTooLong reports a line longer than the reader's limit. Once returned,
// the reader stays failed.
var ErrLineTooLong = errors.New("proto: line too long")

// defaultMaxLine is the documented 16 MiB line cap.
const defaultMaxLine = 16 << 20

// UnknownTypeError reports a frame whose type is empty or contains whitespace
// or control characters, so the caller can answer with an error response.
type UnknownTypeError struct {
	Type string
	ID   string
}

func (e *UnknownTypeError) Error() string {
	return fmt.Sprintf("proto: unknown frame type %q", e.Type)
}

// Writer serialises frames as one JSON object per line.
type Writer struct {
	mu sync.Mutex
	w  io.Writer
}

// NewWriter wraps w.
func NewWriter(w io.Writer) *Writer { return &Writer{w: w} }

// Write marshals v, appends one newline, and issues a single Write call so
// concurrent writers never interleave bytes. The standard encoder escapes
// newlines, U+2028, and U+2029 inside strings, so the only newline is the one
// appended here.
func (w *Writer) Write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err = w.w.Write(b)
	return err
}

// Reader splits a stream into protocol lines.
type Reader struct {
	r       *bufio.Reader
	maxLine int
	failed  error
}

// NewReader wraps r. A maxLine of zero means the 16 MiB default; a negative
// value is clamped to zero.
func NewReader(r io.Reader, maxLine int) *Reader {
	if maxLine < 0 {
		maxLine = 0
	}
	if maxLine == 0 {
		maxLine = defaultMaxLine
	}
	return &Reader{r: bufio.NewReaderSize(r, 4096), maxLine: maxLine}
}

// Next returns the next line without its newline, or io.EOF at a clean end. It
// skips empty lines, strips one trailing carriage return, and fails
// permanently with ErrLineTooLong after an over-long line.
func (r *Reader) Next() ([]byte, error) {
	if r.failed != nil {
		return nil, r.failed
	}
	for {
		line, err := r.read()
		if err != nil {
			if errors.Is(err, io.EOF) && len(line) > 0 {
				return line, nil
			}
			return nil, err
		}
		if len(line) == 0 {
			continue
		}
		return line, nil
	}
}

// read returns one line, growing the buffer only up to maxLine. The final line
// of a stream without a newline is returned with an io.EOF error.
func (r *Reader) read() ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.r.ReadSlice('\n')
		buf = append(buf, chunk...)
		if len(buf) > r.maxLine+1 {
			return nil, r.fail(ErrLineTooLong)
		}
		switch {
		case err == nil:
			return trimCR(buf[:len(buf)-1]), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if len(buf) == 0 {
				return nil, io.EOF
			}
			if len(buf) > r.maxLine {
				return nil, r.fail(ErrLineTooLong)
			}
			return trimCR(buf), io.EOF
		default:
			return nil, r.fail(err)
		}
	}
}

// fail records a permanent reader failure.
func (r *Reader) fail(err error) error {
	r.failed = err
	return err
}

// trimCR drops one trailing carriage return.
func trimCR(line []byte) []byte { return bytes.TrimSuffix(line, []byte{'\r'}) }

// Decode turns one line into a Frame. An unknown but clean type is a Command.
func Decode(line []byte) (Frame, error) {
	var probe struct {
		Type *string `json:"type"`
		ID   string  `json:"id"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		return Frame{}, fmt.Errorf("%w: %v", ErrBadFrame, err)
	}
	if probe.Type == nil {
		return Frame{}, fmt.Errorf("%w: missing type", ErrBadFrame)
	}
	typ := *probe.Type
	if typ == "" || strings.ContainsFunc(typ, invalidType) {
		return Frame{}, &UnknownTypeError{Type: typ, ID: probe.ID}
	}
	switch typ {
	case FrameHello:
		return decodeInto[Hello](line, func(v *Hello) Frame { return Frame{Hello: v} })
	case FrameResponse:
		return decodeInto[Response](line, func(v *Response) Frame { return Frame{Response: v} })
	case FrameUIRequest:
		return decodeInto[UIRequest](line, func(v *UIRequest) Frame { return Frame{UIRequest: v} })
	case FrameUIResponse:
		return decodeInto[UIResponse](line, func(v *UIResponse) Frame { return Frame{UIResponse: v} })
	}
	if eventNames[typ] {
		return decodeInto[Event](line, func(v *Event) Frame { return Frame{Event: v} })
	}
	return decodeInto[Command](line, func(v *Command) Frame { return Frame{Command: v} })
}

// decodeInto decodes line as T and wraps it with mk.
func decodeInto[T any](line []byte, mk func(*T) Frame) (Frame, error) {
	var v T
	if err := json.Unmarshal(line, &v); err != nil {
		return Frame{}, fmt.Errorf("%w: %v", ErrBadFrame, err)
	}
	return mk(&v), nil
}

// invalidType reports a rune that makes a frame type unusable.
func invalidType(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }
