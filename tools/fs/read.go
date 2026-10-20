package fs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	info, err := statRegular(abs)
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
		return renderRead(ctx, abs, offset, limit), nil
	})
}

// ctxCheckEvery is how many lines scanPage reads between context checks. A
// context check takes a lock, so doing it per line would cost more than the scan.
const ctxCheckEvery = 4096

// renderRead streams abs and renders lines offset..offset+limit-1, stopping early
// at the byte cap while still counting the remaining lines.
func renderRead(ctx context.Context, abs string, offset, limit int) tool.Result {
	info, err := statRegular(abs)
	if err != nil {
		return tool.Errorf("%v", err)
	}
	f, err := os.Open(abs)
	if err != nil {
		return tool.Errorf("%v", err)
	}
	defer f.Close()
	prefix, err := io.ReadAll(io.LimitReader(f, tool.BinarySniffBytes))
	if err != nil {
		return tool.Errorf("%v", err)
	}
	if tool.LooksBinary(prefix) {
		return tool.Errorf("%s looks like a binary file (%d bytes)", abs, info.Size())
	}
	lr := &lineReader{r: bufio.NewReader(io.MultiReader(bytes.NewReader(prefix), f)), maxRunes: maxLineChars}
	page, err := scanPage(ctx, lr, offset, limit)
	if err != nil {
		return tool.Errorf("%v", err)
	}
	return page.result(abs, offset, bytes.HasPrefix(prefix, utf8BOM))
}

// pageScan is what scanning a file for one page of lines found.
type pageScan struct {
	text        strings.Builder // the rendered page
	lines       int             // lines seen; the file's total when counted
	counted     bool            // the scan reached the end of the file
	lastLine    int             // number of the last rendered line, 0 when none
	lf, crlf    bool            // line-ending styles seen
	invalidUTF8 bool            // a rendered line held invalid UTF-8
}

// scanPage reads every line, so the total is known, but renders only the
// requested page: lines offset.. up to limit lines or the byte cap. A read error
// ends the scan with counted false, because a partial count would mislead.
func scanPage(ctx context.Context, lr *lineReader, offset, limit int) (*pageScan, error) {
	p := &pageScan{}
	full := false
	for {
		if p.lines%ctxCheckEvery == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		line, ending, err := lr.next()
		if err != nil {
			p.counted = errors.Is(err, io.EOF)
			break
		}
		p.lines++
		switch ending {
		case "lf":
			p.lf = true
		case "crlf":
			p.crlf = true
		}
		if full || p.lines < offset {
			continue
		}
		if p.lines == 1 {
			line = strings.TrimPrefix(line, "\uFEFF")
		}
		if !utf8.ValidString(line) {
			p.invalidUTF8 = true
			line = strings.ToValidUTF8(line, "\uFFFD")
		}
		fmt.Fprintf(&p.text, "%d\t%s\n", p.lines, line)
		p.lastLine = p.lines
		full = p.lastLine-offset+1 >= limit || p.text.Len() >= maxReadBytes
	}
	return p, nil
}

// result shapes a scan into the tool result: the page plus a footer saying how to
// continue, or an error when the offset is past the end.
func (p *pageScan) result(abs string, offset int, bom bool) tool.Result {
	details := readDetails{Path: abs, Start: offset, End: p.lastLine, LineEndings: endings(p.lf, p.crlf), BOM: bom}
	if p.counted {
		details.Total = &p.lines
	}
	raw, _ := json.Marshal(details)
	if p.counted && p.lines == 0 {
		return tool.Result{Content: "(empty file)", Details: raw}
	}
	if p.counted && offset > p.lines {
		return tool.Errorf("offset %d is beyond the end of the file (%d lines)", offset, p.lines)
	}
	content := p.text.String()
	truncated := true
	switch {
	case !p.counted:
		content += "... (more lines remain)\n"
	case p.lastLine < p.lines:
		content += fmt.Sprintf("... (%d more lines; continue with offset=%d)\n", p.lines-p.lastLine, p.lastLine+1)
	default:
		truncated = false
	}
	if p.invalidUTF8 {
		content += "(some bytes were not valid UTF-8)\n"
	}
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

// next returns the next line's display text and its ending, "lf" or "crlf" (empty
// for a last line with no newline). A line that ends in EOF without a newline is
// returned with a nil error; a subsequent call returns io.EOF. A read error is
// returned as is, and the partial line is dropped.
func (lr *lineReader) next() (line, ending string, err error) {
	if _, err := lr.r.Peek(1); err != nil {
		return "", "", err
	}
	var kept, pending []byte
	runes := 0
	prev := byte(0)
	for {
		chunk, readErr := lr.r.ReadSlice('\n')
		newline := readErr == nil
		kept, pending, runes, prev = lr.feed(bytes.TrimSuffix(chunk, []byte("\n")), kept, pending, runes, prev)
		if newline {
			ending = "lf"
			if prev == '\r' {
				ending = "crlf"
				runes--
				kept = bytes.TrimSuffix(kept, []byte("\r"))
			}
			break
		}
		if errors.Is(readErr, bufio.ErrBufferFull) {
			continue
		}
		if !errors.Is(readErr, io.EOF) {
			return "", "", readErr
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
	return line, ending, nil
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
