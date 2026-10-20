---
chunk: C09
title: "The agent loop"
packages: [./agent]
budgets: { ./agent: 1150 }
depends: [C01, C04, C08]
---

# C09 — The agent loop

## Goal

The core of Nova: a loop that runs requests and tool calls to a final answer, with
hooks, steering and follow-up queues, retry, permissions, cancellation, and a typed
event stream, and that stays correct when the provider fails, a tool panics, a hook
hangs, or the user aborts. This is the highest-risk chunk. Its tests are reviewed by the
larger model before merge (see `docs/IMPLEMENTATION.md`).

## Read first

- `agent/agent.go`, `agent/events.go`, `agent/loop.go`, `agent/loop_test.go` (the prototype).
- `docs/ARCHITECTURE.md` section 4 (the loop, the hook table) and section 12 (errors).
- `docs/PROTOCOL.md` section "Events" (the exact event names and fields).
- `proto/frame.go` from C08 (`proto.Event`), `tool/*` and `permission/*` from C04,
  `testkit` from C01.
- `docs/TESTING.md` section 3, "Agent loop".

Known prototype defects this chunk fixes:

- Panic recovery only in parallel mode; no cancellation check inside tool dispatch.
- Cancellation mid-tool leaves an assistant message with tool calls and no results, which
  makes the transcript invalid for a resume.
- No retry, no steering, no queues, no permission step, no hook deadlines.
- Events carry an `error` value (not serialisable) and have no run id or sequence.
- A tool call with a duplicate or empty id corrupts the transcript.
- A turn that is cut off by a provider error loses the usage already accrued for earlier turns
  only by accident.

## Files

| File | Change | Purpose |
|---|---|---|
| `agent/agent.go` | change | `Config`, `Hooks`, `Result`, `Agent` |
| `agent/run.go` | create | the run loop |
| `agent/turn.go` | create | one provider request and its stream |
| `agent/tools.go` | create | tool dispatch (parallel, sequential, permission, hooks) |
| `agent/queue.go` | create | steering and follow-up queues |
| `agent/retry.go` | create | retry policy and backoff |
| `agent/hooks.go` | create | safe hook invocation with deadlines |
| `agent/events.go` | change | emit `proto.Event`; helpers |
| `agent/compat.go` | create | the old `Run(ctx, cfg, history)` function, as a wrapper |
| `agent/*_test.go` | create or change | uses `testkit` |

## Interfaces

```go
type Config struct {
    Provider provider.Provider
    Model    string
    System   string
    Tools    *tool.Registry
    Policy   *permission.Policy     // nil means allow everything
    // Ask resolves a permission "ask". Nil means there is no one to ask (headless).
    Ask      func(ctx context.Context, q Question) (allow bool, err error)

    WorkDir  string
    Roots    []string
    ToolData any
    SessionID string

    MaxTurns        int               // 0 or negative means unlimited; >0 caps requests
    ToolExecution   ToolExecutionMode // Parallel (default) or Sequential
    MaxParallel     int               // default 8
    Temperature     *float64
    MaxTokens       int
    ReasoningEffort string
    ProviderOptions map[string]any
    Retry           RetryPolicy
    HookTimeout     time.Duration     // default 5s
    Clock           Clock             // default real; tests inject testkit's

    Hooks Hooks
    // Emit receives every event, synchronously, in order. Seq, TS, and Run are filled in.
    Emit func(proto.Event)
    // OnMessage is called synchronously each time a message joins the transcript.
    // A non-nil error aborts the run with reason "error".
    OnMessage func(m message.Message, u *provider.Usage) error
    // Complete is given to tools as tool.Context.Complete (nested model calls).
    Complete func(ctx context.Context, req provider.Request) (*provider.Response, error)
}

type Question struct{ Tool, Reason string; Args json.RawMessage; Rule int }

type Hooks struct {
    BeforeProviderRequest func(ctx context.Context, req *provider.Request)
    TransformContext      func(ctx context.Context, msgs []message.Message) []message.Message
    BeforeTurn            func(ctx context.Context, msgs []message.Message) ([]message.Message, error) // may replace the working transcript (compaction)
    MessageEnd            func(ctx context.Context, m message.Message) message.Message
    ToolCall              func(ctx context.Context, c ToolCall) *CallDecision
    ToolResult            func(ctx context.Context, c ToolCall, r tool.Result) tool.Result
    TurnEnd               func(ctx context.Context, t Turn) TurnDecision
    AgentEnd              func(ctx context.Context, r *Result)
}
type ToolCall struct{ ID, Name string; Arguments json.RawMessage }
type CallDecision struct {
    Block     bool
    Reason    string
    Arguments json.RawMessage // if non-nil and not blocking: replaces the arguments
}
type Turn struct {
    Index   int
    Message message.Message
    Results []tool.Result
}
type TurnDecision struct {
    End      bool               // stop after this turn
    Continue bool               // run one more turn even if the model gave a final answer
    Inject   []message.Message  // user messages appended before the next request
}

type RetryPolicy struct {
    Max    int           // default 3 retries (so 4 attempts); negative disables
    Base   time.Duration // default 1s
    Cap    time.Duration // default 30s
    Jitter float64       // fraction of the delay; 0 means the default 0.2, negative means none
}
type Clock interface {
    Now() time.Time
    Sleep(ctx context.Context, d time.Duration) error
}

type Result struct {
    Messages  []message.Message // the full transcript, history included
    New       []message.Message // messages produced by this run
    Answer    string
    Turns     int               // completed turns, plus the failed one when the run ends in error
    Usage     provider.Usage
    Reason    string // done | exhausted | aborted | error
    Exhausted bool
}

type Agent struct{ /* ... */ }
func New(cfg Config) *Agent
func (a *Agent) Run(ctx context.Context, history []message.Message, prompt message.Message) (*Result, error)
func (a *Agent) Steer(text string) error     // ErrNotRunning when idle
func (a *Agent) FollowUp(text string) error  // ErrNotRunning when idle
func (a *Agent) Abort() (steering, followUp []string) // cancel the run; returns the queued messages
func (a *Agent) Queued() (steering, followUp int)
func (a *Agent) Running() bool

var ErrNotRunning = errors.New("agent: not running")
var ErrBusy = errors.New("agent: a run is already in progress")

// Compatibility with the prototype.
func Run(ctx context.Context, cfg Config, history []message.Message) (*Result, error)
```

The prototype's `Config` fields `BeforeToolCall`, `AfterToolCall`, `FinishTurn`, `OnEvent`,
`OnTextDelta`, `OnReasoningDelta`, and `TransformContext` are removed; their roles move to
`Hooks` and `Emit`. `Run` (the function) builds an `Agent`, appends nothing, and runs the
history as given, as before.

## Behaviour

### Run

1. `Agent.Run` while a run is active returns `ErrBusy` immediately. A missing provider or model is
   an error before any event is emitted.
2. The working transcript is `[system?] + history + prompt`. The prompt is a user message; it
   is passed to `OnMessage` before the first request.
3. `MaxTurns` counts provider requests and is off by default: zero or negative means unlimited.
   When a caller sets a positive budget, reaching it ends the run with
   `Reason: "exhausted"`, `Exhausted: true`, and **no error**.
4. Exactly one `run_start` and exactly one `run_end` are emitted per run, including for
   errors, aborts, and exhaustion. Nothing is emitted after `run_end`, and the agent is already idle (`Running()` false, `Steer` and `FollowUp` refused, a new `Start` accepted) when `run_end` is delivered. `Seq` is strictly
   increasing across the agent's lifetime; `Run` is `r_<n>`; `TS` comes from `Clock`.
5. Event order for a turn: `turn_start`, `message_start`, then any of `thinking_*` and
   `text_delta`, then `tool_call` for each assembled call, `message_end`, then for each call
   (in completion order for parallel calls) `permission`, `tool_start`, `tool_update`\*, `tool_end`,
   then `turn_end`. `usage` follows each `message_end`.
6. Thinking: `thinking_start` on the first reasoning delta of a turn, `thinking_delta` for each,
   and `thinking_end` (with `ms` from the clock and `tokens` null unless known) when the first
   text or tool-call event arrives or the stream ends.

### Turn

7. The request is built from the transcript, the registry's definitions, and the config. Then,
   in order: `BeforeTurn` (may replace the working transcript; an error ends the run),
   `TransformContext`, `BeforeProviderRequest`. A hook cannot mutate the stored transcript
   except through `BeforeTurn`'s return value.
8. A hook that panics is recovered; a hook that exceeds `HookTimeout` (it runs in its own
   goroutine) is abandoned; either way the hook is treated as having returned its zero value,
   the loop continues, and an `extension_error` event names the hook. A hook receives the run's
   context and should honour it.
9. The assistant message is built from the response. If the response content is empty but text
   deltas arrived, the content is the concatenated deltas. `MessageEnd` may replace the message;
   its role is forced back to `assistant`.
10. `OnMessage` is called for the assistant message, then (after all tools finish) for each tool
    result in **call order**.
11. If the assistant message has no tool calls: if a steering or follow-up message is queued, or
    `TurnEnd` returned `Continue`, the run continues (see queues); otherwise the run ends with
    `Reason: "done"`.

### Tool calls

12. **IDs.** A call with an empty id gets `call_<turn>_<index>`. A call whose id duplicates an
    earlier one in the same message gets `<id>_dup<n>`. The assistant message in the transcript is
    rewritten to match, so every call has a unique id and exactly one result.
13. **Dispatch.** For each call: emit `tool_call` (at assembly), apply the permission policy,
    run the `ToolCall` hook, emit `tool_start`, execute through the registry (which validates
    arguments and recovers panics), run the `ToolResult` hook, emit `tool_end`.
14. **Permission.** With a `Policy`, `Decide` is called with `interactive = (Ask != nil)`. `deny`
    produces the result `error: blocked by policy: <reason>`; `ask` calls `Ask`; a false answer or
    an error from `Ask` produces `error: not approved: <reason>`. A `permission` event records every
    decision. A tool call blocked this way is not executed and the `ToolCall` hook is not run.
15. **`ToolCall` hook.** `Block` produces `error: blocked: <reason>` (the tool is not run).
    A non-nil `Arguments` replaces the arguments (and is revalidated by the registry).
16. **Execution mode.** In `Parallel` mode, consecutive calls whose tools declare `Parallel` run
    concurrently up to `MaxParallel`. A call whose tool is not parallel runs alone, in call
    order, acting as a barrier. `Sequential` runs one at a time. In every mode results are
    appended in call order, whatever the completion order.
17. A panic anywhere in a call, including in a hook, becomes that call's error result. It never
    reaches the run.
18. Tool progress (`tool.Context.Emit`) becomes `tool_update` events for that call, in order,
    and is dropped (not blocked on) if the consumer is slow.
19. Empty tool output is recorded as `(empty result)`.
20. `FinishTurn`-style behaviour: `TurnEnd` runs after the tool results are appended; `End` stops
    the run with reason `done`; `Inject` messages are appended (each through `OnMessage`) before
    the next request.

### Queues

21. `Steer(text)` queues a message. After the current turn's tool results are appended, **all**
    queued steering messages are appended as user messages (in order) before the next request.
    If the model gave a final answer and steering is queued, the run continues with it.
22. `FollowUp(text)` queues a message that is appended only when the agent would otherwise end the
    run (no tool calls, no steering). It is then appended and the run continues. Follow-ups are
    appended **one at a time**: each runs to completion before the next is appended.
23. Both return `ErrNotRunning` when idle. Both are safe from any goroutine. Each emits
    `queue_update`, as does each message the loop takes off a queue. After the run has made its last
    decision to end, both return `ErrNotRunning` too, so nothing is accepted and then lost. `Abort` emits no
    event (its response carries the queues), because the run may already have ended.
23a. When a run ends for any reason, both queues are cleared. What was still queued is reported as
    `dropped_queue` (`steering` and `follow_up` texts) on `run_end`, omitted when empty, so a client can
    restore it; nothing leaks into the next run. A cancelled run does not drain steering into the
    transcript first. Messages `Abort` already returned are not reported again.
24. `Abort` cancels the run's context and returns the unconsumed steering and follow-up
    messages, emptying the queues. Calling it when idle returns two empty slices. Calling it
    twice is harmless.

### Retry

25. A `*provider.Error` with `Retryable` is retried up to `Retry.Max` times with delay
    `min(Cap, Base * 2^attempt)` plus up to `Jitter` of random jitter; if the error has
    `RetryAfter`, the delay is at least that (capped at 5 minutes). Delays use `Clock.Sleep`
    and honour cancellation.
26. Before each retry emit `retry {attempt, max, delay_ms, error}`. Partial output from the failed
    attempt is discarded: it is not in the transcript and clients must drop it.
27. A non-retryable error, or exhausted retries, ends the run with `Reason: "error"`: emit `error`,
    then `run_end`. The returned `Result` includes everything accrued so far, and the error is returned.
28. `ctx` cancellation is never retried and returns `context.Canceled` (or the context's error).

### Cancellation

29. Cancelling during the provider call discards the partial assistant message and ends the run
    `aborted`. Cancelling during tools passes the cancelled context to every tool; the loop waits for
    them to return (they are required to honour the context), then appends results for **every**
    call of the already-recorded assistant message, using `error: cancelled` for those that had not
    finished, so the transcript stays valid. Then it ends `aborted`.
30. A run cancelled before the first request emits `run_start` and `run_end` only.

### Usage

31. Usage is summed over all successful responses. `message_end` carries that response's usage;
    `usage` carries it with the running total; `run_end` carries the total. A provider that reports
    no usage contributes zero. When a later call fails, earlier usage is still in the result.

## Matrix

| ID | Case | Input / setup | Expected |
|---|---|---|---|
| LPE01 | text-only run | one turn "hi" | events: run_start, turn_start, message_start, text_delta+, message_end, usage, turn_end, run_end(done) |
| LPE02 | exactly one run_start and one run_end | every scenario below | verified for each |
| LPE03 | nothing after run_end | | verified |
| LPE04 | `Seq` strictly increasing; `Run` constant within a run | two runs | seq continues; run ids differ |
| LPE05 | `TS` from the clock | fake clock | matches |
| LPE06 | tool turn event order | one call | tool_call before message_end; tool_start before tool_end; turn_end last |
| LPE07 | thinking events | reasoning then text | thinking_start, delta(s), thinking_end(ms) before the first text_delta |
| LPE08 | thinking with no text (tool call only) | reasoning then a call | thinking_end before tool_call |
| LPE09 | thinking duration | fake clock advanced 3 s | `ms` is 3000 |
| LPE10 | `Emit` nil | | no panic |
| LPE11 | no tools registered, model calls one | | error result `unknown tool`; run continues |
| LPE12 | `MaxTurns` reached | model always calls a tool, max 3 | 3 turns; `Exhausted`; reason exhausted; **no error** |
| LPE13 | `MaxTurns` zero and negative | | unlimited; runs past 100 turns and ends `done` |
| LPE14 | `MaxTurns` exactly enough | final answer on turn 3 of max 3 | done, not exhausted |
| LPE15 | missing provider or model | | error before any event |
| LPE16 | `Run` while running | second call | `ErrBusy` |
| LPE17 | prompt passes through `OnMessage` first | | first call is the user message |
| LPE18 | compat `Run(ctx, cfg, history)` | prototype test cases | same results as before |
| LPE19 | final answer text | | `Result.Answer` |
| LPE20 | `New` contains only this run's messages | | excludes history |
| LPE21 | the provider panics | `Stream` panics | the run ends `error` with one `run_end`; the process survives |
| LPE22 | `run_end` is delivered to an idle agent | from the `Emit` callback on `run_end`: `Running()`, `Steer`, `FollowUp`, `Start` | `Running()` false; `ErrNotRunning` twice; `Start` accepted and its run completes |
| LPE23 | a run started in reaction to `run_end` | `Start` from the `run_end` callback | the new run's `run_start` comes after the old `run_end` |
| LPT01 | one tool call | | result appended after the assistant message |
| LPT02 | many calls, parallel | 5 slow `Parallel` tools | run concurrently (peak concurrency > 1) |
| LPT03 | completion order differs from call order | call 1 slow | results still appended in call order |
| LPT04 | sequential mode | | never more than one at a time |
| LPT05 | non-parallel tool is a barrier | P, N, P | N runs alone, between |
| LPT06 | `MaxParallel` | 20 calls, max 3 | peak concurrency 3 |
| LPT07 | tool panics, parallel mode | | error result; other calls complete; run continues |
| LPT08 | tool panics, sequential mode | | error result; run continues |
| LPT09 | tool returns an error | | error result |
| LPT10 | tool returns empty content | | `(empty result)` |
| LPT11 | tool returns enormous content | 50 MiB | recorded as returned (truncation is the tool's job); no panic |
| LPT12 | empty call id | | `call_<turn>_<i>`; unique |
| LPT13 | duplicate call ids in one message | two calls id `x` | second becomes `x_dup2`; transcript valid (`message.Validate`) |
| LPT14 | invalid JSON arguments | `{"a":` | error result `invalid ...`; run continues |
| LPT15 | arguments `null` and empty | | treated as `{}` |
| LPT16 | unknown tool | | error result listing known tools |
| LPT17 | tool call after a final answer in a later turn | | handled as a normal turn |
| LPT18 | tool progress | tool emits 3 updates | 3 `tool_update` events in order |
| LPT19 | slow consumer of progress | `Emit` blocks | tool is not blocked; some updates may drop; final result correct |
| LPT20 | transcript validity after every case in this group | | `message.Validate` passes |
| LPT21 | progress after the tool returned | a tool keeps `Context.Emit` and calls it after the run | dropped; no panic |
| LPT22 | `ToolResult` hook blanks a result | hook returns an empty result | `tool_end` and the transcript both say `(empty result)` |
| LPT23 | truncated JSON arguments | a call whose arguments are `{"a":` | every event still marshals; the call gets an `invalid arguments` result |
| LPH01 | `ToolCall` hook blocks | | `error: blocked: <reason>`; tool not run |
| LPH02 | `ToolCall` hook rewrites arguments | | tool receives the new arguments |
| LPH03 | `ToolCall` hook returns nil | | normal |
| LPH04 | `ToolResult` hook replaces a result | | replaced |
| LPH05 | hook panics | each hook in turn | recovered; `extension_error` event; run continues |
| LPH06 | hook exceeds the deadline | `HookTimeout` 50 ms, hook sleeps | abandoned; zero value used; `extension_error`; run continues |
| LPH07 | hook honours cancellation | ctx cancelled | returns; no leak |
| LPH08 | `TransformContext` | removes the first message | provider sees the transformed list; stored transcript unchanged |
| LPH09 | `BeforeProviderRequest` | sets an option | provider request has it |
| LPH10 | `BeforeTurn` replaces the transcript | | later requests use the replacement; `OnMessage` not called for it |
| LPH11 | `BeforeTurn` returns an error | | run ends `error`; events complete |
| LPH12 | `MessageEnd` replaces the message | also tries to change the role | content replaced; role forced to assistant |
| LPH13 | `TurnEnd` End | | run ends `done` after that turn |
| LPH14 | `TurnEnd` Continue after a final answer | | one more turn runs |
| LPH15 | `TurnEnd` Inject | two messages | appended through `OnMessage` before the next request |
| LPH16 | `AgentEnd` called once with the result | | once |
| LPH17 | `OnMessage` returns an error | | run ends `error`; error returned |
| LPH18 | hooks nil | | no panic |
| LPH19 | `AgentEnd` on an aborted run | the run context is cancelled | called once, before `run_end`, with a live context |
| LPH20 | `ToolResult` for a policy-blocked call | the policy denies the call | the hook still runs, with the error result |
| LPP01 | no policy | | everything runs |
| LPP02 | deny rule | | `error: blocked by policy: …`; tool not run; `ToolCall` hook not run |
| LPP03 | allow rule | | runs |
| LPP04 | ask, `Ask` returns true | | runs |
| LPP05 | ask, `Ask` returns false | | `error: not approved: …` |
| LPP06 | ask, `Ask` returns an error | | `error: not approved: …`; run continues |
| LPP07 | ask with nil `Ask` (headless) | `Headless` deny | blocked |
| LPP08 | `permission` event for every decision | | emitted with the decision and reason |
| LPP09 | `Ask` honours cancellation | cancel while waiting | returns; run aborts |
| LPP10 | concurrent asks for parallel calls | 3 calls need approval | each asked; no deadlock |
| LPP11 | unknown policy action fails closed | `Policy{Default: "alow"}` | `error: blocked by policy: ...`; tool not run |
| LPQ01 | steer during a tool call | | injected after the turn's results, before the next request |
| LPQ02 | steer during streaming | | same |
| LPQ03 | several steering messages | | all appended in order |
| LPQ04 | steer after the final answer was produced but before `run_end` | | run continues with it |
| LPQ05 | follow-up when the agent would end | | appended; run continues |
| LPQ06 | two follow-ups | | run to completion one after the other |
| LPQ07 | steering and follow-up both queued | | steering first |
| LPQ08 | `Steer` and `FollowUp` when idle | | `ErrNotRunning` |
| LPQ09 | `queue_update` events | | emitted on every change |
| LPQ10 | `Abort` returns queued messages | | both slices, in order; queues empty |
| LPQ11 | `Abort` when idle; `Abort` twice | | empty slices; no panic |
| LPQ12 | concurrent `Steer` and `FollowUp` calls | `-race` | no race; none lost |
| LPQ13 | `queue_update` when steering is consumed | steer during a tool call | a second event with `steering` 0 follows the injection |
| LPQ14 | `queue_update` when a follow-up is consumed | follow-up during a turn | a second event with `follow_up` 0 follows the pop |
| LPQ15 | `queue_update` order under concurrency | 50 concurrent `Steer` calls | the last event reports 50 |
| LPQ16 | no message accepted once the run is ending | `Steer` and `FollowUp` from `AgentEnd` | `ErrNotRunning`; queues empty |
| LPQ17 | unconsumed queues at the end of a run | steer twice and follow up, then cancel the context | `run_end.dropped_queue` lists them in order; queues empty; the next run does not see them |
| LPQ18 | nothing dropped | a normal run | no `dropped_queue` key in the JSON |
| LPQ19 | `Abort` already returned the queue | steer, then `Abort` | `run_end` carries no `dropped_queue` |
| LPR01 | retryable error then success | 500 then ok | success; one `retry` event |
| LPR02 | retries exhausted | always 500, `Max` 2 | 3 attempts; reason error; error returned |
| LPR03 | non-retryable error | 401 | one attempt; reason error |
| LPR04 | backoff delays | fake clock | `Base`, `2*Base`, ... capped at `Cap` (jitter 0) |
| LPR05 | `Retry-After` respected | 5 s | delay at least 5 s |
| LPR06 | `Retry-After` huge | | capped at 5 minutes |
| LPR07 | cancel during backoff | | returns promptly; reason aborted |
| LPR08 | partial output then retryable error | text delivered, then error | `retry` event; partial text not in the transcript; final transcript has only the successful message |
| LPR09 | `Max` negative disables | | one attempt |
| LPR10 | jitter bounds | many runs | delay within `[d, d*(1+Jitter)]` |
| LPR11 | error on a later turn after tools ran | | transcript valid (ends with tool results); earlier usage kept |
| LPR12 | `Jitter` zero is the default, negative is none | `RetryPolicy{Base: 1s}` and `Jitter: -1` | zero gives delays in [1s, 1.2s] with some jitter; negative gives exactly 1s |
| LPC01 | cancel before the first request | | only run_start and run_end; reason aborted; `context.Canceled` |
| LPC02 | cancel during streaming | `Hang` turn | partial message discarded; reason aborted |
| LPC03 | cancel during a tool | slow tool honouring ctx | results for **all** calls appended (`error: cancelled` for the unfinished); transcript valid; reason aborted |
| LPC04 | cancel between turns | | aborted; transcript valid |
| LPC05 | cancel while parallel tools run | 5 tools | all stop; all get results |
| LPC06 | no goroutine leak after every cancellation case | | `NoLeaks` |
| LPC07 | tool that ignores cancellation | sleeps 200 ms | the loop waits for it; documented; no leak after it returns |
| LPU01 | usage summed over turns | 3 turns | sum |
| LPU02 | usage kept when a later call fails | | included in the result |
| LPU03 | provider reports no usage | | zero, no error |
| LPU04 | `message_end` and `usage` events carry usage and totals | | verified |
| LPU05 | reasoning tokens summed | | included |
| LPU06 | `run_end` carries the total | | verified |
| LPI01 | text-only model strips images from the request | a prompt with an image, no modalities | no image part on the wire; omission note in the text; transcript keeps the part |
| LPI02 | vision model keeps images | `InputModalities` text+image | image part still on the wire |
| LPI03 | `InputModalitiesFunc` is read per request | a text-only first run, then vision | first request stripped, second kept |
| LPI04 | a tool's `Result.Parts` becomes a synthetic user message | a tool that returns an image and a caption | after the tool result, one synthetic user message with the image and caption |
| LPI05 | two tools' images combine into one message | two image tools in one batch | one synthetic message with both parts |
| LPI06 | `Context.Inject` queues a message after the batch | an injected user message with an image | recorded after the tool result, image intact |
| LPI07 | a tool's nested usage is counted | `Context.AddUsage` with 12 tokens and a cost | run usage and a `usage` event include it |
| LPI08 | a steered message keeps its image | steer a user message with an image during a tool turn | the next request carries the image |

## Fuzz and property tests

| Name | Asserts |
|---|---|
| `TestProperty_LP_TranscriptAlwaysValid` | for random scripts (tool counts, panics, errors, cancels at random points, duplicate ids), the final transcript passes `message.Validate` and has exactly one `run_start` and one `run_end` |
| `TestProperty_LP_Order` | results are always appended in call order regardless of random completion order |
| `TestProperty_LP_NoEventAfterEnd` | no event follows `run_end` |
| `FuzzToolCallAssembly` | arbitrary tool-call ids and names never corrupt the transcript |

## Out of scope

- Compaction and the context limit (C10). Persistence (the caller supplies `OnMessage`).
- Loading hooks from extensions (C13). Sub-agents (a bundled extension, C18).
- Steering through the protocol (C12 maps commands to these methods).

## Acceptance

```
go run ./scripts/accept C09
```
