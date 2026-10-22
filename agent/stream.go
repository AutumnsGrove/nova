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

// StreamKind names the two live delta streams a filter or hook can see.
type StreamKind string

const (
	// StreamText is assistant answer text.
	StreamText StreamKind = "text"
	// StreamReasoning is the model's reasoning/thinking text.
	StreamReasoning StreamKind = "reasoning"
)

// StreamDelta is one live delta handed to Hooks.StreamDelta, after any
// Config.TextFilter or Config.ReasoningFilter has processed it.
type StreamDelta struct {
	Kind StreamKind
	Text string
}

// DeltaAction is what a StreamDelta hook decides for one delta.
type DeltaAction int

const (
	// DeltaPass shows the delta; StreamDecision.Text, when non-empty, replaces
	// it. Any text the hook held earlier is released first.
	DeltaPass DeltaAction = iota
	// DeltaHold buffers the delta until a later Pass or Drop, or until the end
	// of the message, where it is released.
	DeltaHold
	// DeltaDrop discards the delta and anything the hook held before it.
	DeltaDrop
)

// StreamDecision is what Hooks.StreamDelta returns for one delta.
type StreamDecision struct {
	Action DeltaAction
	// Text replaces the delta on DeltaPass. Empty keeps the filtered text.
	Text string
}
