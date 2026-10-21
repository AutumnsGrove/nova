// Package compaction keeps a long session inside the model's context window. It
// estimates size, decides when to compact against a per-session limit, cuts at a
// safe turn boundary, and summarises the old span.
package compaction

import (
	"unicode/utf8"

	"github.com/AutumnsGrove/nova/message"
)

// imageTokens is what one image part costs in the estimate: a fixed placeholder,
// not its base64 length, so a screenshot still moves compaction (docs/IMAGE.md).
const imageTokens = 1000

// Estimate approximates the token count of a transcript. It is a deliberate
// heuristic (four runes per token) rather than a real tokeniser: it must be
// cheap, monotonic, and linear.
func Estimate(msgs []message.Message) int {
	total := 0
	for i := range msgs {
		total += EstimateMessage(msgs[i])
	}
	return total
}

// EstimateMessage is Estimate for one message: four tokens of overhead plus the
// content and every tool call's name and arguments at four runes per token, and
// a fixed cost for each image part.
func EstimateMessage(m message.Message) int {
	n := 4 + estimateRunes(m.Content) + imageTokens*countImages(m)
	for _, tc := range m.ToolCalls {
		n += estimateRunes(tc.Function.Name)
		n += estimateRunes(tc.Function.Arguments)
	}
	return n
}

// countImages is the number of image parts on m.
func countImages(m message.Message) int {
	n := 0
	for _, p := range m.Parts {
		if p.Type == message.PartImage {
			n++
		}
	}
	return n
}

// estimateRunes converts a rune count to the token estimate, rounding up.
func estimateRunes(s string) int {
	return (utf8.RuneCountInString(s) + 3) / 4
}
