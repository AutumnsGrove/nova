package openaicompat

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
)

// lineReader reads SSE lines while tracking the line number for error
// messages. bufio.Scanner cannot report the line or tell "too long" from "read
// failed", and both matter to the matrix.
type lineReader struct {
	r    *bufio.Reader
	max  int
	line int
}

// newLineReader sizes the buffer so it holds a line of exactly max bytes plus
// its terminator; ErrBufferFull therefore means the line is genuinely over max.
func newLineReader(r io.Reader, max int) *lineReader {
	return &lineReader{r: bufio.NewReaderSize(r, max+2), max: max}
}

// next returns the next line without its terminator, or io.EOF at the end. A
// line over max is an *errLineTooLong.
func (lr *lineReader) next() (string, error) {
	lr.line++
	frag, err := lr.r.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return "", &errLineTooLong{limit: lr.max}
	}
	if err != nil && len(frag) == 0 {
		return "", err
	}
	line := strings.TrimSuffix(string(frag), "\n")
	return strings.TrimSuffix(line, "\r"), nil
}

// lineResult carries one line, or the error that ended the read, to the
// consumer goroutine. num is the 1-based line number for error messages.
type lineResult struct {
	line string
	num  int
	err  error
}

// streamReader reads the blocking body on its own goroutine and feeds the
// caller over a channel. The caller selects on the channel and a timer, so a
// stalled server cannot pin it inside a blocked Read, and the caller is the
// only goroutine that ever invokes onEvent.
type streamReader struct {
	lines chan lineResult
	quit  chan struct{}
	done  chan struct{}
	body  io.Closer
	once  sync.Once
}

// startStreamReader spawns the reader goroutine.
func startStreamReader(body io.ReadCloser, max int) *streamReader {
	sr := &streamReader{
		lines: make(chan lineResult),
		quit:  make(chan struct{}),
		done:  make(chan struct{}),
		body:  body,
	}
	go func() {
		defer close(sr.done)
		lr := newLineReader(body, max)
		for {
			line, err := lr.next()
			select {
			case sr.lines <- lineResult{line: line, num: lr.line, err: err}:
			case <-sr.quit:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return sr
}

// stop closes the body so a blocked Read returns, then waits for the goroutine.
// It is idempotent and safe to defer.
func (sr *streamReader) stop() {
	sr.once.Do(func() {
		close(sr.quit)
		_ = sr.body.Close()
	})
	<-sr.done
}

// callAcc accumulates one tool call across its deltas.
type callAcc struct {
	id, name, typ string
	args          strings.Builder
}

// assembler groups streamed tool-call fragments by index. It emits nothing
// until the stream ends, so the model never sees half an argument list.
type assembler struct {
	calls     map[int]*callAcc
	order     []int
	generated int
}

func newAssembler() *assembler {
	return &assembler{calls: map[int]*callAcc{}}
}

// add folds one delta into the call at its index.
func (a *assembler) add(tc toolCallDelta) {
	idx := 0
	if tc.Index != nil {
		idx = *tc.Index
	}
	acc := a.calls[idx]
	if acc == nil {
		acc = &callAcc{}
		a.calls[idx] = acc
		a.order = append(a.order, idx)
	}
	// The first non-empty id wins: later fragments repeat it or omit it, and a
	// stray different id must not rename a call already keyed by index.
	if acc.id == "" && tc.ID != "" {
		acc.id = tc.ID
	}
	if acc.name == "" && tc.Function.Name != "" {
		acc.name = tc.Function.Name
	}
	if acc.typ == "" && tc.Type != "" {
		acc.typ = tc.Type
	}
	if tc.Function.Arguments != "" {
		acc.args.WriteString(tc.Function.Arguments)
	}
}

// assembled finalizes the calls in index order. It counts calls whose name
// never arrived, because those are dropped and reported rather than executed.
func (a *assembler) assembled() (calls []message.ToolCall, dropped int) {
	sort.Ints(a.order)
	for _, idx := range a.order {
		acc := a.calls[idx]
		if acc.name == "" {
			dropped++
			continue
		}
		id := acc.id
		if id == "" {
			id = fmt.Sprintf("call_%d_%d", idx, a.generated)
			a.generated++
		}
		typ := acc.typ
		if typ == "" {
			typ = "function"
		}
		args := acc.args.String()
		if args == "" {
			args = "{}"
		}
		calls = append(calls, message.ToolCall{
			ID:       id,
			Type:     typ,
			Function: message.FunctionCall{Name: acc.name, Arguments: args},
		})
	}
	return calls, dropped
}

// consume reads the SSE body, emits streaming events, and returns the response.
// On any failure after the stream has started it returns the partial response
// together with the error, so the caller keeps what the model already said.
func (p *Provider) consume(ctx context.Context, body io.ReadCloser, req provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error) {
	result := &provider.Response{Model: req.Model}
	result.Message.Role = message.RoleAssistant

	sr := startStreamReader(body, p.cfg.MaxLineBytes)
	defer sr.stop()

	timer := time.NewTimer(p.cfg.IdleTimeout)
	defer timer.Stop()

	var text, reasoning strings.Builder
	asm := newAssembler()
	var sawChunk, sawFinish, sawDone, firstChunk bool = false, false, false, true

	// partial snapshots the delivered text, so an error still carries what the
	// caller already saw.
	partial := func() *provider.Response {
		result.Message.Content = text.String()
		result.Message.Reasoning = reasoning.String()
		return result
	}

streamLoop:
	for {
		select {
		case <-ctx.Done():
			return partial(), ctx.Err()
		case <-timer.C:
			return partial(), &provider.Error{Message: "stream idle timeout", Retryable: true}
		case res := <-sr.lines:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(p.cfg.IdleTimeout)

			if res.err != nil {
				if ctx.Err() != nil {
					return partial(), ctx.Err()
				}
				if errors.Is(res.err, io.EOF) {
					break streamLoop
				}
				var tooLong *errLineTooLong
				switch {
				case errors.As(res.err, &tooLong):
					return partial(), tooLong
				case errors.Is(res.err, os.ErrDeadlineExceeded), os.IsTimeout(res.err):
					return partial(), &provider.Error{Message: "stream idle timeout", Retryable: true, Err: res.err}
				default:
					return partial(), &provider.Error{Message: fmt.Sprintf("read stream: %v", res.err), Retryable: true, Err: res.err}
				}
			}

			data, ok := sseData(res.line)
			if !ok || data == "" {
				continue
			}
			if data == "[DONE]" {
				sawDone = true
				break streamLoop
			}

			var chunk chatChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				return partial(), &provider.Error{
					Message:   fmt.Sprintf("invalid JSON on line %d: %v", res.num, err),
					Retryable: false,
				}
			}
			if firstChunk {
				firstChunk = false
				if chunk.Error != nil {
					return partial(), streamErrorObject(chunk.Error)
				}
			}
			sawChunk = true
			if chunk.Model != "" {
				result.Model = chunk.Model
			}
			if chunk.Usage != nil {
				usage := usageFromRaw(chunk.Usage)
				result.Usage = usage
				if onEvent != nil {
					delivered := usage
					onEvent(provider.StreamEvent{Type: provider.StreamUsage, Usage: &delivered})
				}
			}
			if len(chunk.Choices) == 0 {
				continue
			}
			choice := chunk.Choices[0]
			if choice.FinishReason != "" {
				result.FinishReason = provider.NormalizeFinish(choice.FinishReason)
				sawFinish = true
			}
			appendDelta(&text, &reasoning, asm, choice.Delta, onEvent)
		}
	}

	calls, dropped := asm.assembled()
	result.Message.Content = text.String()
	result.Message.Reasoning = reasoning.String()
	if len(calls) > 0 && result.FinishReason == "stop" {
		result.FinishReason = "tool_calls"
	}

	if !sawChunk {
		return result, &provider.Error{Message: "empty stream", Retryable: true}
	}
	if !sawDone && !sawFinish {
		return result, &provider.Error{Message: "stream ended before completion", Retryable: true}
	}

	result.Message.ToolCalls = calls
	if dropped > 0 && onEvent != nil {
		onEvent(provider.StreamEvent{
			Type: provider.StreamError,
			Err:  fmt.Errorf("openaicompat: dropped %d tool call(s) with no name", dropped),
		})
	}
	for i := range calls {
		call := calls[i]
		if onEvent != nil {
			onEvent(provider.StreamEvent{Type: provider.StreamToolCall, ToolCall: &call})
		}
	}
	return result, nil
}

// appendDelta emits a text or reasoning delta and records tool-call fragments.
func appendDelta(text, reasoning *strings.Builder, asm *assembler, d delta, onEvent func(provider.StreamEvent)) {
	if d.Content != "" {
		text.WriteString(d.Content)
		if onEvent != nil {
			onEvent(provider.StreamEvent{Type: provider.StreamTextDelta, Text: d.Content})
		}
	}
	if r := reasoningText(d); r != "" {
		reasoning.WriteString(r)
		if onEvent != nil {
			onEvent(provider.StreamEvent{Type: provider.StreamReasoningDelta, Text: r})
		}
	}
	for _, tc := range d.ToolCalls {
		asm.add(tc)
	}
}

// reasoningText picks the provider's preferred reasoning field, falling back to
// the text of reasoning_details entries.
func reasoningText(d delta) string {
	if d.ReasoningContent != "" {
		return d.ReasoningContent
	}
	if d.Reasoning != "" {
		return d.Reasoning
	}
	var b strings.Builder
	for _, detail := range d.ReasoningDetails {
		b.WriteString(detail.Text)
	}
	return b.String()
}

// sseData extracts a `data:` payload. An optional single space after the colon
// is accepted; comments, blank lines, and every other SSE field report false.
func sseData(line string) (string, bool) {
	rest, ok := strings.CutPrefix(line, "data:")
	if !ok {
		return "", false
	}
	return strings.TrimPrefix(rest, " "), true
}
