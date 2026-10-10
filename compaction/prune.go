package compaction

import (
	"fmt"
	"unicode/utf8"

	"github.com/AutumnsGrove/nova/message"
)

// PruneToolOutput returns a copy of msgs in which every tool result older than
// the last keepLast results and longer than maxChars is replaced by a short
// marker. The input is not modified and the tool call id and name are kept, so
// the transcript still validates.
func PruneToolOutput(msgs []message.Message, keepLast, maxChars int) []message.Message {
	out := make([]message.Message, len(msgs))
	copy(out, msgs)

	total := 0
	for i := range out {
		if out[i].Role == message.RoleTool {
			total++
		}
	}
	remaining := total
	for i := range out {
		if out[i].Role != message.RoleTool {
			continue
		}
		remaining--
		if remaining < keepLast {
			continue // among the most recent keepLast results
		}
		n := utf8.RuneCountInString(out[i].Content)
		if n > maxChars {
			out[i].Content = fmt.Sprintf("[output pruned: %d characters; run the tool again if you need it]", n)
		}
	}
	return out
}
