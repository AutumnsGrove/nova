// Package agent runs the tool-calling loop: request a completion, execute any
// tool calls, feed the results back, and repeat until the model answers, the
// turn budget runs out, the context is cancelled, or the provider fails.
//
// One Agent runs one loop at a time. A caller that wants concurrency runs it in
// a goroutine and reads events through Config.Emit; steering and follow-up
// messages may be queued from any goroutine.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/AutumnsGrove/nova/decide"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/permission"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/tool"
)

// ToolExecutionMode controls how the tool calls in one assistant message run.
type ToolExecutionMode string

const (
	// Sequential runs tool calls one at a time.
	Sequential ToolExecutionMode = "sequential"
	// Parallel runs consecutive parallel-capable calls concurrently (default).
	Parallel ToolExecutionMode = "parallel"
)

// ToolCall is the subset of a tool call passed to the ToolCall and ToolResult
// hooks.
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// CallDecision is what the ToolCall hook returns. Block stops the call;
// Arguments, when non-nil and not blocking, replaces the call's arguments.
type CallDecision struct {
	Block     bool
	Reason    string
	Arguments json.RawMessage
}

// Turn describes a completed turn passed to the TurnEnd hook.
type Turn struct {
	Index   int
	Message message.Message
	Results []tool.Result
}

// TurnDecision is what the TurnEnd hook returns. End stops the run; Continue
// runs one more turn even after a final answer; Inject messages are appended
// before the next request.
type TurnDecision struct {
	End      bool
	Continue bool
	Inject   []message.Message
}

// Question is a permission prompt handed to Ask.
type Question struct {
	Tool   string
	Reason string
	Args   json.RawMessage
	Rule   int
}

// Hooks are the loop's optional extension points. Every hook is called with the
// run's context and runs under HookTimeout; a hook that panics or exceeds its
// deadline is abandoned and reported with an extension_error event.
type Hooks struct {
	BeforeProviderRequest func(ctx context.Context, req *provider.Request)
	TransformContext      func(ctx context.Context, msgs []message.Message) []message.Message
	BeforeTurn            func(ctx context.Context, msgs []message.Message) ([]message.Message, error)
	MessageEnd            func(ctx context.Context, m message.Message) message.Message
	ToolCall              func(ctx context.Context, c ToolCall) *CallDecision
	ToolResult            func(ctx context.Context, c ToolCall, r tool.Result) tool.Result
	TurnEnd               func(ctx context.Context, t Turn) TurnDecision
	AgentEnd              func(ctx context.Context, r *Result)
}

// RetryPolicy configures how a retryable provider failure is retried.
type RetryPolicy struct {
	Max    int           // default 3 retries (so 4 attempts); negative disables
	Base   time.Duration // default 1s
	Cap    time.Duration // default 30s
	Jitter float64       // fraction of the delay added at random; 0 means the default 0.2, negative means none
}

// Clock is the time seam, so tests can drive time with a fake clock.
type Clock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) error
}

// Config configures one agent.
type Config struct {
	// Provider and Model select the model backend. Both are required.
	Provider provider.Provider
	Model    string
	// ModelID is the stable catalog id Model was derived from, for the router.
	// Wire providers ignore it; empty means Model is the catalog id.
	ModelID string

	// System is the system prompt, sent as the leading system message.
	System string

	// Tools is the registry offered to the model.
	Tools *tool.Registry

	// Policy decides each tool call before dispatch. Nil allows everything.
	Policy *permission.Policy
	// Ask resolves a permission "ask". Nil means there is no one to ask, so
	// the policy's headless action applies. Parallel tool calls may ask at the
	// same time, so Ask must be safe for concurrent use.
	Ask func(ctx context.Context, q Question) (allow bool, err error)

	// WorkDir is the directory relative tool paths resolve against.
	WorkDir   string
	Roots     []string
	ToolData  any
	SessionID string

	MaxTurns        int               // 0 or negative means unlimited; >0 caps provider requests
	ToolExecution   ToolExecutionMode // default Parallel
	MaxParallel     int               // default 8
	Temperature     *float64
	MaxTokens       int
	ReasoningEffort string
	ProviderOptions map[string]any
	// InputModalities lists the input kinds the model accepts, such as "text"
	// and "image". Empty means text only, the safe default. When the model does
	// not take images, image parts are dropped from the request with a note; the
	// transcript keeps them so the UI still shows what the user sent.
	InputModalities []string
	// InputModalitiesFunc, when non-nil, returns the current model's input kinds
	// and takes precedence over InputModalities. A server that can switch models
	// mid-session uses it so the gate follows the switch.
	InputModalitiesFunc func() []string
	Retry               RetryPolicy
	HookTimeout         time.Duration // default 5s
	Clock               Clock         // default real; tests inject a fake

	// PromptCache overrides whether requests carry an explicit prompt-cache
	// marker. Nil means decide from the model catalog (see
	// provider.RequestCacheControl), so every caller gets the right answer by
	// default and a cost-sensitive embedder can still turn it off for a
	// gateway that rejects the field.
	PromptCache *bool

	Hooks Hooks

	// Emit receives every event, synchronously, in order. Seq, TS, and Run
	// are filled in; it may be nil.
	Emit func(proto.Event)
	// OnMessage is called synchronously each time a message joins the
	// transcript. A non-nil error aborts the run with reason "error".
	OnMessage func(m message.Message, u *provider.Usage) error
	// Complete is given to tools as tool.Context.Complete, for nested model
	// calls.
	Complete func(ctx context.Context, req provider.Request) (*provider.Response, error)
	// Decide is given to tools as tool.Context.Decide and to hooks as a cheap
	// typed-answer call. It may be nil. A decision model may make a permission
	// call stricter, never grant approval (D41).
	Decide func(ctx context.Context, req decide.Request) (*decide.Response, error)
}

// Result is the outcome of a run.
type Result struct {
	// Messages is the full transcript, history included.
	Messages []message.Message
	// New is only the messages produced by this run.
	New []message.Message
	// Answer is the last non-empty assistant text.
	Answer string
	// Turns is the number of turns this run completed, plus the one whose
	// request failed when the run ends in error; an abort does not count.
	Turns int
	// Usage is the usage summed over every successful response.
	Usage provider.Usage
	// Reason is done, exhausted, aborted, or error.
	Reason string
	// Exhausted reports that the turn budget ran out before a final answer.
	Exhausted bool
}

// ErrNotRunning is returned by Steer and FollowUp when no run is active.
var ErrNotRunning = errors.New("agent: not running")

// ErrBusy is returned by Run while another run is already active.
var ErrBusy = errors.New("agent: a run is already in progress")

// Agent runs one turn loop at a time and holds the steering and follow-up
// queues. Its methods are safe for concurrent use.
type Agent struct {
	cfg   Config
	clock Clock

	mu      sync.Mutex
	running bool
	// closing is set once the run has made its last decision, so a message
	// queued after that is refused instead of being accepted and then lost.
	closing  bool
	cancel   context.CancelFunc
	runCount int
	runID    string
	steering []message.Message
	followUp []message.Message

	emitMu sync.Mutex
	seq    int64
}

// New builds an agent. A nil Clock means the real clock.
func New(cfg Config) *Agent {
	clock := cfg.Clock
	if clock == nil {
		clock = realClock{}
	}
	return &Agent{cfg: cfg, clock: clock}
}

// realClock is the wall-clock Clock used when none is injected.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Reconfigure changes the agent's configuration between runs. A run works from
// the copy it took when it began, so it is refused with ErrBusy while one is
// active.
func (a *Agent) Reconfigure(fn func(*Config)) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		return ErrBusy
	}
	fn(&a.cfg)
	return nil
}
