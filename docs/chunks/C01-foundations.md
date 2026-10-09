---
chunk: C01
title: Message, provider contract, testkit
packages: [./message, ./provider, ./testkit]
budgets: { ./message: 250, ./provider: 500, ./testkit: 1200 }
depends: [C00]
---

# C01 — Message, provider contract, testkit

## Goal

Finish the two foundation packages the whole backend rests on (`message` and the
`provider` contract) and build `testkit`, the fake provider, fake SSE server, fake
clock, and helpers that every later test uses.

## Read first

- `docs/ARCHITECTURE.md` section 3 (Core types).
- `docs/TESTING.md` sections 2 and 3.
- Existing code: `message/message.go`, `provider/provider.go`, `provider/model.go`,
  `provider/registry.go`.

## Files

| File | Change | Purpose |
|---|---|---|
| `message/message.go` | change | add `Reasoning`, `Name`; keep everything else |
| `message/validate.go` | create | `Validate` checks tool-call pairing |
| `message/message_test.go` | create | |
| `provider/provider.go` | change | `ProviderOptions`, `ReasoningEffort`, `Stop`, `ReasoningTokens`, `StreamUsage`, `Error` |
| `provider/options.go` | create | `MergeOptions`, `NormalizeFinish` |
| `provider/error.go` | create | the typed `Error` and `Classify` |
| `provider/*_test.go` | create | |
| `testkit/clock.go` | create | `Clock`, `FakeClock`, `RealClock` |
| `testkit/provider.go` | create | scripted in-process provider |
| `testkit/server.go` | create | scripted SSE HTTP server with faults |
| `testkit/fs.go` | create | temp-tree helpers |
| `testkit/golden.go` | create | golden-file comparison |
| `testkit/leaks.go` | create | goroutine leak check |
| `testkit/*_test.go` | create | testkit is tested as strictly as product code |

## Interfaces

### message

```go
type Message struct {
    Role       Role       `json:"role"`
    Content    string     `json:"content,omitempty"`
    Reasoning  string     `json:"reasoning,omitempty"`
    ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
    ToolCallID string     `json:"tool_call_id,omitempty"`
    Name       string     `json:"name,omitempty"`
}

// Validate checks that tool calls and results pair up. It returns nil for a valid
// transcript and an error naming the first offending index otherwise.
func Validate(msgs []Message) error
```

### provider

```go
type Request struct {
    Model           string
    Messages        []message.Message
    Tools           []ToolDef
    ToolChoice      any
    Temperature     *float64
    MaxTokens       int
    Stop            []string
    ReasoningEffort string            // "", "low", "medium", "high"
    ProviderOptions map[string]any    // merged into the wire body by the provider
    ExtraHeaders    map[string]string
}

type Usage struct {
    PromptTokens, CompletionTokens, ReasoningTokens, TotalTokens, CachedTokens int
    CostUSD    float64
    CostSource string // "provider", "catalog", or ""
}

const StreamUsage StreamEventType = "usage" // StreamEvent.Usage is set

type StreamEvent struct {
    Type     StreamEventType
    Text     string
    ToolCall *message.ToolCall
    Usage    *Usage
    Err      error
}

// Error is what providers return for a failed request.
type Error struct {
    Status     int           // HTTP status, 0 if none
    Code       string        // provider's code, if any
    Message    string
    Retryable  bool
    RetryAfter time.Duration // zero if not given
    Err        error         // underlying cause
}
func (e *Error) Error() string
func (e *Error) Unwrap() error
func Classify(status int, retryAfterHeader string) (retryable bool, retryAfter time.Duration)

func MergeOptions(base, over map[string]any) map[string]any
func NormalizeFinish(s string) string // "stop","length","tool_calls","content_filter","error"
```

### testkit

```go
type Clock interface {
    Now() time.Time
    Sleep(ctx context.Context, d time.Duration) error
    After(d time.Duration) <-chan time.Time
}
func RealClock() Clock
type FakeClock struct{ /* ... */ }
func NewFakeClock(start time.Time) *FakeClock
func (c *FakeClock) Advance(d time.Duration)        // fires due timers in order
func (c *FakeClock) BlockUntil(t testing.TB, n int) // wait until n sleepers/timers are pending

type Call struct {
    ID, Name, Args string
    SplitArgs      []string // if set, Args are emitted in these fragments
}
type Turn struct {
    Text       string
    TextChunks []string         // if set, used instead of Text
    Reasoning  string
    ToolCalls  []Call
    Usage      *provider.Usage
    Finish     string           // default: "tool_calls" if ToolCalls else "stop"
    Delay      time.Duration    // slept via the clock before the first event
    Err        error            // returned after ErrAfter events
    ErrAfter   int
    Hang       bool             // block until ctx is cancelled
}

type Provider struct{ /* implements provider.Provider */ }
func NewProvider(clock Clock, turns ...Turn) *Provider
func (p *Provider) Requests() []provider.Request // deep copies
func (p *Provider) Remaining() int

type Fault int
const (
    FaultNone Fault = iota
    FaultCutMidEvent    // close the connection in the middle of an SSE event
    FaultNoDone         // end the stream without [DONE]
    FaultMalformedJSON  // a data line that is not JSON
    FaultStall          // send headers, then nothing
    Fault429            // 429 with Retry-After: 1
    Fault500
    FaultHTML           // 502 with an HTML body
    FaultHugeChunk      // one chunk of 4 MiB
)
type ServerTurn struct {
    Turn
    Fault Fault
}
type Server struct{ URL string /* ... */ }
func NewServer(t testing.TB, turns ...ServerTurn) *Server // closed on t.Cleanup
func (s *Server) Requests() []RecordedRequest
type RecordedRequest struct {
    Method, Path string
    Header       http.Header
    Body         []byte
}

func TempTree(t testing.TB, files map[string]string) string // returns the root
func Symlink(t testing.TB, root, name, target string)
func WriteBinary(t testing.TB, root, name string, size int)
func Golden(t testing.TB, name, got string) // testdata/<name>.golden; -update rewrites
func NoLeaks(t testing.TB)                  // registers a cleanup that checks goroutines
```

## Behaviour

1. `message.Validate` accepts: an empty slice; user then assistant; an assistant with tool
   calls followed immediately by one tool message per call id, in any order, then anything.
   It rejects: a tool message with no preceding matching call; a tool message with an empty
   `ToolCallID`; a call with no result before the next assistant or user message; two tool
   results for one call; a system message anywhere but index 0.
2. `Message.Clone` copies `ToolCalls`; mutating the clone's slice leaves the original intact.
3. A message marshals to the OpenAI shape; empty fields are omitted; unknown fields in
   input are ignored on unmarshal.
4. `provider.MergeOptions` returns a **new** map. Keys in `over` replace keys in `base`,
   except when both values are `map[string]any`, in which case they merge recursively.
   Slices are replaced, not appended. `nil` inputs are treated as empty. Neither input is
   modified.
5. `provider.NormalizeFinish`: `stop`, `end_turn` → `stop`; `length`, `max_tokens` → `length`;
   `tool_calls`, `tool_use`, `function_call` → `tool_calls`; `content_filter`, `safety` →
   `content_filter`; `error` stays `error`; empty or any other value → `""`.
6. `provider.Classify`: 408, 409, 425, 429, 500, 502, 503, 504 are retryable; 400, 401, 403,
   404, 422 are not. `Retry-After` is parsed as seconds or an HTTP date; unparseable or
   negative gives zero; the result is capped at 5 minutes.
7. `Usage.Add` sums every numeric field. `CostSource` becomes `"provider"` if either side
   is `"provider"`, else `"catalog"` if either is `"catalog"`, else `""`.
8. `FakeClock` never reads the real time. `Advance` fires every timer whose deadline is
   `<=` the new time, in deadline order, then returns. `Sleep` returns `ctx.Err()` if the
   context is cancelled first.
9. `testkit.Provider.Stream` takes the next turn. With no turns left it returns an error
   containing `script exhausted`. It records a deep copy of each request before doing
   anything else. Events are emitted in this order: reasoning, text chunks, tool calls,
   usage. A tool call with `SplitArgs` is delivered as one `StreamToolCall` event only
   after it is fully assembled (matching the real provider contract). `Err` is returned
   after `ErrAfter` events have been delivered. `Hang` blocks until `ctx` is cancelled and
   then returns `ctx.Err()`. `Delay` uses the clock and honours `ctx`.
10. The final `provider.Response` has the assembled message, the turn's `Finish`
    (normalised), the usage, and the model name from the request.
11. `testkit.Server` speaks the OpenAI chat-completions SSE format: `data: {json}\n\n`
    lines with `choices[0].delta`, a final usage chunk when the turn has usage, and
    `data: [DONE]`. Tool calls are streamed as real deltas (name first, then argument
    fragments, with `index`). Each fault behaves exactly as named in its constant's comment.
    It records every request.
12. `TempTree` creates parent directories, uses mode 0o644 for files, and returns an
    absolute path inside `t.TempDir()`. `Golden` fails with a readable diff, and with
    `-update` writes the file and passes.
13. `NoLeaks` snapshots the goroutines running at call time and, in a cleanup, waits up
    to one second for the set to return to that baseline, then fails listing the leaked
    stacks.

## Matrix

| ID | Case | Input / setup | Expected |
|---|---|---|---|
| MSG01 | marshal omits empty fields | `User("hi")` | JSON is `{"role":"user","content":"hi"}` |
| MSG02 | round trip with tool calls | assistant with two calls | equal after marshal and unmarshal |
| MSG03 | unknown fields ignored | `{"role":"user","content":"x","extra":1}` | no error |
| MSG04 | `Clone` isolates `ToolCalls` | append to the clone's slice | original length unchanged |
| MSG05 | `Reasoning` and `Name` serialize | set both | present in JSON; absent when empty |
| MSG06 | validate empty | `nil` | `nil` |
| MSG07 | validate user, assistant | | `nil` |
| MSG08 | validate paired calls, results out of order | calls `a`,`b`; results `b`,`a` | `nil` |
| MSG09 | tool message with no preceding call | | error naming index 0 |
| MSG10 | tool message with empty `ToolCallID` | | error |
| MSG11 | call with no result before next user message | | error naming the call id |
| MSG12 | two results for one call | | error |
| MSG13 | system message at index 2 | | error |
| MSG14 | system message at index 0 | | `nil` |
| MSG15 | result for a call from an earlier assistant turn | `a1(c1)`, `u`, `tool(c1)` | error |
| PRV01 | `MergeOptions` returns a new map | modify the result | inputs unchanged |
| PRV02 | `MergeOptions` replaces scalars | `{a:1}` + `{a:2}` | `{a:2}` |
| PRV03 | `MergeOptions` merges nested maps | `{p:{x:1}}` + `{p:{y:2}}` | `{p:{x:1,y:2}}` |
| PRV04 | `MergeOptions` replaces slices | `{o:[1,2]}` + `{o:[3]}` | `{o:[3]}` |
| PRV05 | `MergeOptions` with nil inputs | `nil, nil` and `nil, m` and `m, nil` | empty map, copy of `m`, copy of `m` |
| PRV06 | `MergeOptions` map replaced by scalar and the reverse | | scalar/map wins from `over` |
| PRV07 | `NormalizeFinish` table | each alias in Behaviour 5, plus `""`, `"weird"` | as specified |
| PRV08 | `Classify` retryable statuses | 408 409 425 429 500 502 503 504 | retryable |
| PRV09 | `Classify` non-retryable statuses | 400 401 403 404 422 | not retryable |
| PRV10 | `Retry-After` seconds | `"3"` | 3s |
| PRV11 | `Retry-After` HTTP date | a date 10s ahead of a fixed now | about 10s (injectable now) |
| PRV12 | `Retry-After` garbage, negative, huge | `"abc"`, `"-5"`, `"99999"` | 0, 0, 5 minutes |
| PRV13 | `Usage.Add` sums all fields incl. reasoning | | sums |
| PRV14 | `Usage.Add` cost source precedence | provider+catalog, catalog+"" | provider, catalog |
| PRV15 | `Error` unwraps | `errors.Is(err, cause)` | true |
| PRV16 | registry duplicate panics | register the same name twice | panic |
| PRV17 | registry nil or unnamed panics | | panic |
| PRV18 | registry `Get` missing; `Names` sorted | | false; sorted |
| PRV19 | `RegisterModel` empty ID panics; replaces same ID | | panic; replaced |
| PRV20 | `Models` sorted by provider then id | | sorted |
| TKC01 | `FakeClock` does not read real time | `Now()` after `Advance(1h)` | start + 1h, instantly |
| TKC02 | `Advance` fires timers in deadline order | timers at 3s, 1s, 2s | fire 1s, 2s, 3s |
| TKC03 | `Sleep` returns on advance | | returns nil |
| TKC04 | `Sleep` honours cancellation | cancel before advance | `context.Canceled` |
| TKC05 | `BlockUntil` waits for sleepers | goroutine sleeps | returns once registered |
| TKC06 | timer exactly at the new time fires | deadline == now+d | fires |
| TKP01 | turns are consumed in order | 2 turns | responses match in order |
| TKP02 | exhausted script | 3rd call | error contains `script exhausted` |
| TKP03 | requests recorded as deep copies | mutate the original after the call | recorded value unchanged |
| TKP04 | event order | reasoning, text, tool call, usage | delivered in that order |
| TKP05 | split tool-call args emitted as one event | `SplitArgs` of 3 fragments | one `StreamToolCall` with joined args |
| TKP06 | `TextChunks` | 3 chunks | 3 text deltas; message content is the join |
| TKP07 | `Err` after N events | `ErrAfter: 2` | exactly 2 events then the error |
| TKP08 | `Hang` honours cancellation | cancel the context | returns `ctx.Err()` promptly |
| TKP09 | `Delay` uses the clock | delay 5s, fake clock | does not return until `Advance(5s)` |
| TKP10 | `Delay` honours cancellation | cancel mid-delay | returns `ctx.Err()` |
| TKP11 | default finish | text only; with tool calls | `stop`; `tool_calls` |
| TKP12 | nil `onEvent` allowed | | no panic |
| TKP13 | concurrent calls are safe | `-race`, 10 goroutines | no race; each gets a distinct turn |
| TKP14 | `Remaining` | | counts down |
| TKS01 | serves valid SSE | one text turn | `data:` lines, final `[DONE]`, parseable |
| TKS02 | tool calls streamed as deltas | one call, 3 arg fragments | name chunk then 3 argument chunks with `index` 0 |
| TKS03 | usage chunk | turn with usage | final chunk has `usage` and empty `choices` |
| TKS04 | `FaultCutMidEvent` | | the client sees EOF in the middle of a `data:` line |
| TKS05 | `FaultNoDone` | | stream closes cleanly without `[DONE]` |
| TKS06 | `FaultMalformedJSON` | | a `data:` line that is not valid JSON |
| TKS07 | `FaultStall` | | headers arrive; no body until the client gives up |
| TKS08 | `Fault429` | | status 429 and `Retry-After: 1` |
| TKS09 | `Fault500` | | status 500 with a JSON error body |
| TKS10 | `FaultHTML` | | status 502 and `text/html` body |
| TKS11 | `FaultHugeChunk` | | one data line of at least 4 MiB |
| TKS12 | requests recorded | two requests | method, path, headers, and body recorded |
| TKS13 | server closes on cleanup | | later connections refused; no goroutine leak |
| TKS14 | unknown path | `GET /nope` | 404 |
| TKF01 | `TempTree` creates nested files | `a/b/c.txt` | file exists with the content |
| TKF02 | `TempTree` is absolute and under the temp dir | | true |
| TKF03 | `Symlink` and `WriteBinary` | | symlink resolves; file has NULs and the size |
| TKF04 | `Golden` match and mismatch | | pass; fail with a diff |
| TKF05 | `Golden` with `-update` | | writes the file |
| TKF06 | `Golden` missing file without `-update` | | fails with a message naming the flag |
| TKL01 | `NoLeaks` passes when clean | | passes |
| TKL02 | `NoLeaks` detects a leaked goroutine | a goroutine blocked forever (run inside a sub-test harness) | reports a failure naming it |

## Fuzz and property tests

| Name | Asserts |
|---|---|
| `FuzzMergeOptions` | never panics; result has every key from both inputs; inputs unchanged |
| `FuzzValidate` | `Validate` never panics on arbitrary message slices |
| `FuzzClassify` | never panics; retry-after is within `[0, 5m]` |

## Out of scope

- `openaicompat` (C02), any real HTTP client logic, the model catalog contents (C03).
- Retry logic. It lives in the agent (C08). `provider.Error` only describes failures.

## Acceptance

```
go run ./scripts/accept C01
```
