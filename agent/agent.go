package agent

import (
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/tool"
)

// ToolExecutionMode controls how the tool calls in one assistant message run.
type ToolExecutionMode string

const (
	// Sequential runs tool calls one at a time.
	Sequential ToolExecutionMode = "sequential"
	// Parallel runs tool calls concurrently (default).
	Parallel ToolExecutionMode = "parallel"
)

// ToolCall is the subset of a tool call passed to hooks.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// Block is returned by BeforeToolCall to prevent a tool from running.
type Block struct {
	Reason string
}

// Turn describes a completed turn passed to FinishTurn.
type Turn struct {
	Index   int
	Message message.Message
	Results []tool.Result
}

// TurnDecision is returned by FinishTurn. End stops the loop after the turn.
type TurnDecision struct {
	End bool
}

// Config configures one agent run.
type Config struct {
	// Provider and Model select the model backend.
	Provider provider.Provider
	Model    string

	// System is the system prompt, sent as the leading system message.
	System string

	// Tools is the tool registry offered to the model.
	Tools *tool.Registry

	// WorkDir is the directory relative tool paths resolve against.
	WorkDir string

	// ToolData is passed to tools via tool.Context.Data, for embedding
	// applications to inject their own dependencies.
	ToolData any

	// MaxTurns bounds loop iterations. Defaults to 25.
	MaxTurns int

	// ToolExecution selects sequential or parallel dispatch. Defaults to parallel.
	ToolExecution ToolExecutionMode

	// Temperature and MaxTokens are passed to the provider when set.
	Temperature *float64
	MaxTokens   int

	// -- Hooks (all optional) --

	// TransformContext can rewrite the message list right before each
	// provider request (pruning, injecting context). It must not mutate the
	// input in place.
	TransformContext func([]message.Message) []message.Message
	// BeforeToolCall can block a call; return nil to allow it.
	BeforeToolCall func(ToolCall) *Block
	// AfterToolCall can replace a tool result before it is recorded.
	AfterToolCall func(ToolCall, tool.Result) tool.Result
	// FinishTurn runs after each turn's tool results are appended. Return
	// TurnDecision{End: true} to stop.
	FinishTurn func(Turn) TurnDecision
	// OnEvent receives every lifecycle event.
	OnEvent func(Event)
	// OnTextDelta and OnReasoningDelta are convenience streaming callbacks.
	OnTextDelta      func(string)
	OnReasoningDelta func(string)
}

// Result is the outcome of a run.
type Result struct {
	// Messages is the full transcript including the initial history.
	Messages []message.Message
	// Answer is the final assistant text.
	Answer string
	// Turns is the number of provider requests made.
	Turns int
	// Usage is the accumulated usage across every provider call.
	Usage provider.Usage
	// Exhausted reports that the loop hit MaxTurns without a final answer.
	Exhausted bool
}
