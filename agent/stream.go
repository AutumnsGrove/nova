package agent

import "context"

// StreamFilter rewrites or holds the live text of one message stream. A filter
// is created fresh for each provider attempt by Config.TextFilter or
// Config.ReasoningFilter, so a retry starts clean. The final assistant message
// and the session keep the original text; a filter only controls what is shown
// live.
//
// Delta returns the text to show now, or "" to hold it. Flush returns text held
// at the end of a successful message, or "" to drop it. Both receive the run's
// context, so a filter can stop when the run is cancelled or its deadline
// passes. A filter must not start goroutines that outlive the call; a panic is
// recovered, reported with an extension_error, and the text passes through.
type StreamFilter interface {
	Delta(ctx context.Context, text string) string
	Flush(ctx context.Context) string
}
