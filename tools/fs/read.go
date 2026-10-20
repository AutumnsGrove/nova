package fs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/AutumnsGrove/nova/media"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/tool"
)

// utf8BOM is the byte-order mark some editors put at the start of a file.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// maxImageBytes caps an image read before it is decoded; media.Normalize then
// bounds the decoded size and the stored bytes.
const maxImageBytes = 64 << 20

// imageExts are the extensions read treats as an image candidate. The decoded
// header decides whether the file really is one.
var imageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".webp": true, ".tif": true, ".tiff": true, ".bmp": true,
}

// isImagePath reports whether a path's extension is an image candidate.
func isImagePath(path string) bool {
	return imageExts[strings.ToLower(filepath.Ext(path))]
}

// readImage decodes and normalizes an image file into a result the model can
// see. media.Normalize applies the one set of size and format limits.
func readImage(abs string) tool.Result {
	info, err := os.Stat(abs)
	if err != nil {
		return tool.Errorf("%v", err)
	}
	if info.Size() > maxImageBytes {
		return tool.Errorf("%s is %d bytes; images over %d MiB are not read", abs, info.Size(), maxImageBytes>>20)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return tool.Errorf("%v", err)
	}
	res, err := media.Normalize(data, "", filepath.Base(abs))
	if err != nil {
		return tool.Errorf("%v", err)
	}
	text := fmt.Sprintf("Read image file [%s]", res.Part.MimeType)
	if len(res.Hints) > 0 {
		text += "\n" + strings.Join(res.Hints, "\n")
	}
	return tool.Result{Content: text, Parts: []message.ContentPart{res.Part}}
}

// readDetails is the structured data read attaches to Result.Details.
type readDetails struct {
	Path        string `json:"path"`
	Start       int    `json:"start"`
	End         int    `json:"end"`
	Total       *int   `json:"total"`
	LineEndings string `json:"line_endings"`
	BOM         bool   `json:"bom"`
}

// Read returns the read tool. It streams a file, numbers its lines, and pages
// through large files with offset and limit.
func Read() tool.Tool {
	return tool.New(tool.Info{
		Name:        "read",
		Description: "Read a UTF-8 text file or an image (png, jpeg, gif, webp, tiff, bmp). Text comes back with line numbers; an image comes back as an attachment the model can see. Use offset and limit to page through large text files.",
		ReadOnly:    true,
		Parallel:    true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":   tool.StringProp("File path (relative to the working directory, or absolute)"),
				"offset": tool.IntProp("1-indexed line to start from (default 1)"),
				"limit":  tool.IntProp("Maximum number of lines to read (default 2000, max 2000)"),
			},
			"required": []string{"path"},
		},
	}, func(ctx context.Context, args json.RawMessage, tctx *tool.Context) (tool.Result, error) {
		var in struct {
			Path   string `json:"path"`
			Offset *int   `json:"offset"`
			Limit  int    `json:"limit"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return tool.Result{}, fmt.Errorf("invalid arguments: %w", err)
		}
		abs, err := tool.ResolvePath(tctx, in.Path)
		if err != nil {
			return tool.Errorf("%v", err), nil
		}
		if isImagePath(abs) {
			return readImage(abs), nil
		}
		offset := 1
		if in.Offset != nil {
			offset = *in.Offset
		}
		if offset < 1 {
			return tool.Errorf("offset %d is invalid; it must be at least 1", offset), nil
		}
		limit := in.Limit
		if limit <= 0 {
			limit = maxReadLines
		}
		if limit > maxReadLines {
			limit = maxReadLines
		}
		return renderRead(abs, offset, limit), nil
	})
}

// renderRead streams abs and renders lines offset..offset+limit-1, stopping early
// at the byte cap while still counting the remaining lines.
func renderRead(abs string, offset, limit int) tool.Result {
	f, err := os.Open(abs)
	if err != nil {
		return tool.Errorf("%v", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return tool.Errorf("%v", err)
	}
	if info.IsDir() {
		return tool.Errorf("%s is a directory; use list", abs)
	}
	prefix, err := io.ReadAll(io.LimitReader(f, int64(binarySniff)))
	if err != nil {
		return tool.Errorf("%v", err)
	}
	if bytes.IndexByte(prefix, 0) >= 0 {
		return tool.Errorf("%s looks like a binary file (%d bytes)", abs, info.Size())
	}
	bom := bytes.HasPrefix(prefix, utf8BOM)
	reader := &lineReader{r: bufio.NewReader(io.MultiReader(bytes.NewReader(prefix), f)), maxRunes: maxLineChars}

	var out strings.Builder
	lineNo, emitted, lastLine := 0, 0, 0
	lf, crlf := false, false
	invalidUTF8 := false
	totalKnown := true
	emitting := true
	var total int

	for {
		line, _, ending, readErr := reader.next()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			totalKnown = false
			break
		}
		lineNo++
		switch ending {
		case "lf":
			lf = true
		case "crlf":
			crlf = true
		}
		if lineNo == 1 {
			line = strings.TrimPrefix(line, "\uFEFF")
		}
		if !utf8.ValidString(line) {
			invalidUTF8 = true
			line = strings.ToValidUTF8(line, "\uFFFD")
		}
		if lineNo < offset || !emitting || emitted >= limit {
			continue
		}
		fmt.Fprintf(&out, "%d\t%s\n", lineNo, line)
		emitted++
		lastLine = lineNo
		if out.Len() >= maxReadBytes {
			emitting = false
		}
	}
	if totalKnown {
		total = lineNo
	}

	if totalKnown && total == 0 {
		details, _ := json.Marshal(readDetails{Path: abs, Start: offset, End: 0, Total: &total, LineEndings: endings(lf, crlf), BOM: bom})
		return tool.Result{Content: "(empty file)", Details: details}
	}
	if totalKnown && offset > total {
		return tool.Errorf("offset %d is beyond the end of the file (%d lines)", offset, total)
	}

	content := out.String()
	truncated := false
	if !totalKnown {
		content += "... (more lines remain)\n"
		truncated = true
	} else if lastLine < total {
		fmt.Fprintf(&out, "... (%d more lines; continue with offset=%d)\n", total-lastLine, lastLine+1)
		content = out.String()
		truncated = true
	}
	if invalidUTF8 {
		content += "(some bytes were not valid UTF-8)\n"
	}
	details := readDetails{Path: abs, Start: offset, End: lastLine, LineEndings: endings(lf, crlf), BOM: bom}
	if totalKnown {
		details.Total = &total
	}
	raw, _ := json.Marshal(details)
	return tool.Result{Content: content, Truncated: truncated, Details: raw}
}

// endings reports the line-ending style seen while reading.
func endings(lf, crlf bool) string {
	switch {
	case lf && crlf:
		return "mixed"
	case crlf:
		return "crlf"
	default:
		return "lf"
	}
}

// lineReader reads one line at a time, counting runes without keeping a long
// line in memory: only the first maxRunes runes are retained for display. It
// reads in chunks so a multi-megabyte line is scanned without a byte-at-a-time
// syscall and without being buffered whole.
type lineReader struct {
	r        *bufio.Reader
	maxRunes int
}

// next returns the next line's display text and rune count. A line that ends in
// EOF without a newline is returned with a nil error; a subsequent call returns
// io.EOF.
func (lr *lineReader) next() (line string, runes int, ending string, err error) {
	if _, peekErr := lr.r.Peek(1); peekErr != nil {
		return "", 0, "", peekErr
	}
	var kept, pending []byte
	prev := byte(0)
	for {
		chunk, readErr := lr.r.ReadSlice('\n')
		newline := readErr == nil
		if i := bytes.IndexByte(chunk, '\n'); i >= 0 {
			chunk = chunk[:i]
		}
		kept, pending, runes, prev = lr.feed(chunk, kept, pending, runes, prev)
		if newline {
			ending = "lf"
			if prev == '\r' {
				ending = "crlf"
				runes--
				kept = bytes.TrimSuffix(kept, []byte("\r"))
			}
			break
		}
		if readErr == bufio.ErrBufferFull {
			continue
		}
		if readErr != nil && readErr != io.EOF {
			err = readErr
		}
		break
	}
	if len(pending) > 0 {
		runes++
		if runes <= lr.maxRunes {
			kept = append(kept, pending...)
		}
	}
	line = string(kept)
	if runes > lr.maxRunes {
		line += fmt.Sprintf("… (line truncated, %d chars)", runes)
	}
	return line, runes, ending, nil
}

// feed consumes one chunk of a line, counting runes and retaining at most
// maxRunes of them. It has a fast path for ASCII runs so a multi-megabyte line
// of ordinary text is not walked one byte at a time.
func (lr *lineReader) feed(chunk, kept, pending []byte, runes int, prev byte) ([]byte, []byte, int, byte) {
	if len(chunk) == 0 {
		return kept, pending, runes, prev
	}
	if len(pending) == 0 {
		i := 0
		for i < len(chunk) && chunk[i] < utf8.RuneSelf {
			i++
		}
		if i > 0 {
			before := runes
			runes += i
			if before < lr.maxRunes {
				n := lr.maxRunes - before
				if n > i {
					n = i
				}
				kept = append(kept, chunk[:n]...)
			}
			prev = chunk[i-1]
			chunk = chunk[i:]
		}
	}
	for _, b := range chunk {
		prev = b
		pending = append(pending, b)
		if utf8.FullRune(pending) {
			runes++
			if runes <= lr.maxRunes {
				kept = append(kept, pending...)
			}
			pending = pending[:0]
		}
	}
	return kept, pending, runes, prev
}
