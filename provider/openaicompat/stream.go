package openaicompat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/provider/ssestream"
)

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
func (p *Provider) consume(ctx context.Context, stream *ssestream.Stream, req provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error) {
	result := &provider.Response{Model: req.Model}
	result.Message.Role = message.RoleAssistant

	var text, reasoning strings.Builder
	asm := &assembler{calls: map[int]*callAcc{}}
	var sawChunk, sawFinish bool

	// partial snapshots the delivered text, so an error still carries what the
	// caller already saw.
	partial := func() *provider.Response {
		result.Message.Content = text.String()
		result.Message.Reasoning = reasoning.String()
		return result
	}
	fail := func(message string, retryable bool, cause error) (*provider.Response, error) {
		return partial(), &provider.Error{Message: message, Retryable: retryable, Err: cause}
	}

	for {
		ev, err := stream.Next()
		if err != nil {
			if ctx.Err() != nil {
				return partial(), ctx.Err()
			}
			if errors.Is(err, io.EOF) {
				break
			}
			var tooLong *ssestream.LineTooLongError
			var perr *provider.Error
			switch {
			case errors.Is(err, ssestream.ErrTruncatedLine):
				return fail("stream ended mid-event", true, err)
			case errors.As(err, &tooLong):
				return fail(tooLong.Error(), false, nil)
			case errors.As(err, &perr):
				return partial(), perr
			case errors.Is(err, os.ErrDeadlineExceeded), os.IsTimeout(err):
				return fail("stream idle timeout", true, err)
			default:
				return fail(fmt.Sprintf("read stream: %v", err), true, err)
			}
		}
		data := string(ev.Data)
		if data == "" {
			continue
		}

		var chunk chatChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return fail(fmt.Sprintf("invalid JSON on line %d: %v", ev.Line, err), false, nil)
		}
		// An error object can arrive on the first chunk (a rejected request) or
		// after output has started (an upstream failure), and either ends the turn.
		if chunk.Error != nil {
			return partial(), streamErrorObject(chunk.Error)
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
			if result.FinishReason == "error" {
				return partial(), &provider.Error{Status: http.StatusOK, Message: "provider ended the response with an error"}
			}
		}
		appendDelta(&text, &reasoning, asm, choice.Delta, onEvent)
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
	if !stream.SawDone() && !sawFinish {
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
