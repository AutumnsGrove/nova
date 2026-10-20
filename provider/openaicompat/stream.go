package openaicompat

import (
	"net/http"
	"strings"

	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/provider/ssestream"
)

// consume reads the SSE body, emits streaming events, and returns the response.
// On any failure after the stream has started it returns the partial response
// together with the error, so the caller keeps what the model already said.
func consume(stream *ssestream.Stream, model string, onEvent func(provider.StreamEvent)) (*provider.Response, error) {
	c := provider.NewCollector(model, onEvent)
	var sawChunk bool
	finish := ""

	err := stream.Each(func(ev ssestream.Event) (bool, error) {
		var chunk chatChunk
		if err := ev.Decode(&chunk); err != nil {
			return false, err
		}
		// An error object can arrive on the first chunk (a rejected request) or
		// after output has started (an upstream failure), and either ends the turn.
		if chunk.Error != nil {
			return false, streamErrorObject(chunk.Error)
		}
		sawChunk = true
		c.SetModel(chunk.Model)
		if chunk.Usage != nil {
			c.Usage(usageFromRaw(chunk.Usage))
		}
		if len(chunk.Choices) == 0 {
			return false, nil
		}
		choice := chunk.Choices[0]
		if choice.FinishReason != "" {
			finish = choice.FinishReason
			if provider.NormalizeFinish(finish) == "error" {
				return false, &provider.Error{Status: http.StatusOK, Message: "provider ended the response with an error"}
			}
		}
		c.Text(choice.Delta.Content)
		c.Reasoning(reasoningText(choice.Delta))
		for _, tc := range choice.Delta.ToolCalls {
			c.ToolCall(tc.indexOrZero(), tc.ID, tc.Type, tc.Function.Name, tc.Function.Arguments)
		}
		return false, nil
	})
	switch {
	case err != nil:
		return c.Partial(), err
	case !sawChunk:
		return c.Partial(), &provider.Error{Message: "empty stream", Retryable: true}
	case !stream.SawDone() && finish == "":
		return c.Partial(), &provider.Error{Message: "stream ended before completion", Retryable: true}
	}
	return c.Finish(finish), nil
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
