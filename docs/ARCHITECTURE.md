# Nova — Architecture

This document describes the intended architecture. The M0 prototype at the
repository root implements a subset (see [`SPIKE.md`](SPIKE.md)); the package
layout and interfaces below are the target.

## 1. Layers

Nova is layered so that each layer depends only on the ones below it.

```
┌──────────────────────────────────────────────────────────────┐
│ cmd/nova, tui/            CLI and terminal frontend          │
├──────────────────────────────────────────────────────────────┤
│ extension/                in-process registry, MCP, skills   │
├──────────────────────────────────────────────────────────────┤
│ agent/                    the loop, hooks, events, queues    │
│ session/ compaction/      transcript tree, context management │
├──────────────────────────────────────────────────────────────┤
│ tool/  tools/*            tool contract, registry, core tools │
├──────────────────────────────────────────────────────────────┤
│ provider/                 Provider interface + registry       │
│   openaicompat/ openrouter/ ollama/ ...                       │
├──────────────────────────────────────────────────────────────┤
│ message/                  canonical transcript types          │
└──────────────────────────────────────────────────────────────┘
```

Rules:

- `message` depends on nothing.
- `provider` depends on `message`.
- `tool` depends on `message` and `provider` (to emit tool definitions).
- `agent` depends on `message`, `provider`, and `tool`.
- `tools/*` depend on `tool` only.
- `session`, `compaction`, and `extension` sit beside `agent`.
- No lower layer imports an upper layer.

## 2. Package layout

```
nova/
  message/            Message, ToolCall, FunctionCall, roles
  provider/
    provider.go       Provider interface, Request, Response, StreamEvent, Usage
    registry.go       Register/Get/Names
    model.go          code-defined model catalog (id, slug, context, pricing)
    openaicompat/     chat completions + SSE + tool calls
    openrouter/       preset over OpenAI-compatible
    ollama/           (M4) local preset
  tool/
    tool.go           Tool interface, Info, Result, Context
    registry.go       Register/Get/Defs/Execute
    manifest.go       (M1) YAML manifest loader
  tools/
    fs/               read, write, edit, list
    search/           (M1) glob, grep
    bash/             bash (timeout, output cap, optional exec policy)
  agent/
    agent.go          Config, hooks, Result, Turn
    loop.go           the run loop
    events.go         typed Event stream
    queues.go         (M2) steering and follow-up queues
  session/
    session.go        tree entries, active branch
    store.go          JSONL persistence
    manager.go        context reconstruction, compaction application
  compaction/
    compaction.go     summarize-and-splice
  extension/
    extension.go      Extension interface + host registry
    mcp/              (M4) MCP client
  config/
    config.go         config file, model selection, provider wiring
  tui/                (M3) Bubble Tea frontend
  cmd/nova/           runnable binary
```

## 3. Core interfaces

### 3.1 Transcript

The canonical message type maps 1:1 onto the OpenAI wire format. System prompts
are carried as the leading system message, exactly as the model sees them, so
`convertToLlm` is unnecessary for the common case. `TransformContext` is the
seam for anything richer.

```go
package message

type Role string
const (
    RoleSystem    Role = "system"
    RoleUser      Role = "user"
    RoleAssistant Role = "assistant"
    RoleTool      Role = "tool"
)

type Message struct {
    Role       Role       `json:"role"`
    Content    string     `json:"content,omitempty"`
    ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
    ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
    ID       string       `json:"id"`
    Type     string       `json:"type,omitempty"`
    Function FunctionCall `json:"function"`
}

type FunctionCall struct {
    Name      string `json:"name"`
    Arguments string `json:"arguments"` // raw JSON
}
```

**Open question:** rich content parts (images, documents, cache markers) may
require a `[]ContentPart` model before M4. Until then, tool results that carry
images are a known limitation.

### 3.2 Provider

```go
package provider

type Provider interface {
    Name() string
    Stream(ctx context.Context, req Request, onEvent func(StreamEvent)) (*Response, error)
}

type Request struct {
    Model        string
    Messages     []message.Message
    Tools        []ToolDef
    ToolChoice   any
    Temperature  *float64
    MaxTokens    int
    ExtraHeaders map[string]string
}

type Response struct {
    Message      message.Message
    FinishReason string
    Usage        Usage
    Model        string
}

type StreamEventType string
const (
    StreamTextDelta      StreamEventType = "text_delta"
    StreamReasoningDelta StreamEventType = "reasoning_delta"
    StreamToolCall       StreamEventType = "tool_call"
    StreamError          StreamEventType = "error"
)
```

Contract: `Stream` must honor `ctx`, call `onEvent` for each chunk (nil is
allowed), return a finalized `Response`, and return a non-nil error on failure.

Providers are registered in a process-wide registry, mirroring pi's
`registerProvider`:

```go
provider.Register(p)                 // panics on duplicate/unnamed
p, ok := provider.Get("openrouter")
```

The first implementation is `openaicompat` (standard library only). `openrouter`
is a preset. A model catalog (`provider.RegisterModel`) holds stable ids, wire
slugs, context windows, and pricing.

#### Provider decorators

Because a `Provider` is just an interface, cross-cutting concerns wrap it. This
is how ivy's `llm.Gate` (policy, caps, ledger, vetting) attaches without Nova
knowing about it:

```go
type gatedProvider struct {
    gate *llm.Gate
    inner provider.Provider
}

func (g *gatedProvider) Name() string { return g.inner.Name() }
func (g *gatedProvider) Stream(ctx context.Context, req provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error) {
    // enforce account policy, monthly cap, vetting; write the ledger row
    return g.inner.Stream(ctx, req, onEvent)
}
```

### 3.3 Tool

```go
package tool

type Info struct {
    Name        string
    Description string
    Parameters  map[string]any // JSON Schema

    Parallel    bool // may run concurrently with sibling calls
    ReadOnly    bool // does not mutate state
    Destructive bool // may destroy data
    OpenWorld   bool // reaches beyond the local machine
}

type Context struct {
    WorkDir string
    Data    any // opaque application state (her-go tools.Context analogue)
}

type Result struct {
    Content string
    IsError bool
}

type Tool interface {
    Info() Info
    Run(ctx context.Context, args json.RawMessage, tctx *Context) (Result, error)
}
```

`Context.Data` is the escape hatch: her-go, polaris, and ivy hang their own
dependency bundles off it (stores, clients, callbacks) without Nova importing
any of them.

The registry:

```go
reg := tool.NewRegistry()
reg.MustRegister(fs.Read())
reg.MustRegister(bash.Bash())
defs := reg.Defs()          // offered to the model
res  := reg.Execute(ctx, name, args, tctx) // never returns a fatal error
```

Unknown tools and handler errors become `IsError` results, so the model can
self-correct.

### 3.4 Agent loop

```go
type Config struct {
    Provider provider.Provider
    Model    string
    System   string
    Tools    *tool.Registry
    WorkDir  string
    ToolData any

    MaxTurns      int                // default 25
    ToolExecution ToolExecutionMode  // Sequential | Parallel
    Temperature   *float64
    MaxTokens     int

    TransformContext func([]message.Message) []message.Message
    BeforeToolCall   func(ToolCall) *Block
    AfterToolCall    func(ToolCall, tool.Result) tool.Result
    FinishTurn       func(Turn) TurnDecision
    OnEvent          func(Event)
    OnTextDelta      func(string)
    OnReasoningDelta func(string)
}
```

One run:

```
system + history
  │
  ├─▶ provider.Stream ──▶ text/reasoning deltas ──▶ OnEvent / OnTextDelta
  │        │
  │        └─▶ assistant message (+ tool calls?)
  │
  ├─ no tool calls ─────────────▶ final answer, done
  │
  └─ tool calls ──▶ BeforeToolCall ──▶ execute ──▶ AfterToolCall
                        │                              │
                        └── blocked? error result ─────┘
                                       │
                              append tool results
                                       │
                                FinishTurn hook
                                       │
                                  next turn ──┐
                                              │
  ◀───────────────────────────────────────────┘
```

Hooks are nil-safe. This is the mechanism that absorbs each project's domain
behavior:

| Project behavior | Hook |
|---|---|
| her-go trace lines, continuation windows | `OnEvent`, `FinishTurn` |
| her-go PII redaction of tool output | `AfterToolCall` |
| polaris research nudges | `FinishTurn` |
| polaris pseudo tool-call recovery | `TransformContext` + `AfterToolCall` |
| permission gating | `BeforeToolCall` |
| ivy spend caps / policy | provider decorator (below the loop) |
| context pruning / injection | `TransformContext` |

### 3.5 Events

```go
type EventType string
const (
    EventTurnStart      EventType = "turn_start"
    EventTextDelta      EventType = "text_delta"
    EventReasoningDelta EventType = "reasoning_delta"
    EventToolCall       EventType = "tool_call"
    EventToolResult     EventType = "tool_result"
    EventTurnEnd        EventType = "turn_end"
    EventError          EventType = "error"
    EventDone           EventType = "done"
)
```

The same stream feeds the CLI, the TUI, tests, and any embedding application.

## 4. Concurrency model

- One agent run is synchronous (`Run` blocks). Callers that want concurrency run
  it in a goroutine and consume events.
- Tool calls within one assistant message run in parallel by default. Results
  are stored by index so the transcript is deterministic even though completion
  order is not.
- Parallel dispatch recovers panics per call and converts them to error results;
  a panic in one tool never takes down the run.
- Tools declare `Parallel`; a future dispatch policy can force non-parallel
  tools to run alone or serialize a mutable resource.
- `ctx` cancellation aborts in-flight provider requests and is threaded into
  every tool.

## 5. Sessions (M2)

Following pi, a session is a tree of entries in a JSONL file. Each entry has an
`id` and optional `parentId`; the active leaf selects the branch that supplies
model context. Continuing from an older entry creates a new branch without
deleting the old one. Compaction inserts a summary entry that later requests
honor.

```
session.jsonl
  e1 user "hi"
  e2 assistant "hello"
  e3 user "summarize"
  e4 assistant ...
  e5 compaction summary ──▶ replaces e1..e4 in model context for this branch
  e6 user "now do X"       (parent e5)
```

`session.Manager` is authoritative for the model-facing context; assigning an
in-memory message slice does not replace persisted context. This is what makes
resume, fork, and branch navigation consistent.

## 6. Compaction (M2)

Summarize-and-splice, matching the approach already proven in her-go and
polaris:

1. Estimate context tokens.
2. When over the threshold, find a cut point at a user-message boundary (never
   inside a tool-call/tool-result pair).
3. Summarize the older span into a structured summary.
4. Replace the span with a summary entry; keep recent turns in full fidelity.
5. Prune stale tool results at request time without dropping the summary.

## 7. Tool authoring

Two paths, one registry.

**Go:**

```go
tool.Register(tool.New(tool.Info{
    Name: "weather", Description: "Current weather for a city.",
    ReadOnly: true, Parallel: true,
    Parameters: map[string]any{ /* JSON Schema */ },
}, handler))
```

**YAML manifest (her-go shape):**

```yaml
# tools/reply/tool.yaml
name: reply
agent: [main]
hot: true
description: >-
  Send the final user-facing reply. Call exactly once per turn.
parameters:
  type: object
  properties:
    text: { type: string, description: "The reply text" }
  required: [text]
trace:
  emoji: "💬"
  format: "{{.text | escape}}"
```

`manifest.go` parses the file into a `tool.Info` and binds it to a Go handler
registered under the same name. Manifests add things code-only tools don't need
declaratively: which agents may use the tool (`agent`), whether it is always
loaded or deferred (`hot`), category grouping, and trace formatting.

## 8. Extensions (M4)

v1 commitment: **compile-time in-process registration**, plus MCP for external
tools. No `.so` plugins (brittle, toolchain-locked) and no scripting host yet.

```go
type Extension interface {
    Init(h *Host) error
}

type Host struct { /* registries, config, event bus */ }

func (h *Host) RegisterTool(t tool.Tool)
func (h *Host) RegisterProvider(p provider.Provider)
func (h *Host) RegisterCommand(c Command)
func (h *Host) On(event EventType, fn Handler)
```

An extension is a Go package whose `init` registers it. MCP covers the case
where the tool lives in another language or process, and gives us the ecosystem
standard for free.

A scripting host (Starlark or JS) is an explicit post-v1 option; the `Host` API
is designed so it can be added without changing extension authors' code.

## 9. Error model

- **Provider failure** during a run: return the partial `Result` (with the cost
  already accrued) plus the error. Never discard spend.
- **Tool failure**: a `Result{IsError: true}`; the model sees it and may retry.
- **Unknown tool / bad JSON args**: error result before dispatch, so the model
  can self-correct.
- **Panic in a tool**: recovered, converted to an error result, logged.
- **Context cancellation**: returns promptly with `ctx.Err()`.

## 10. Security and cost

- Tool annotations (`ReadOnly`, `Destructive`, `OpenWorld`) drive a
  user-supplied permission hook; Nova does not prompt on its own.
- Path containment is opt-in per `tool.Context` (a future `Roots` field), since
  some agents legitimately roam the filesystem.
- Provider credentials resolve per request (so OAuth/short-lived tokens can
  refresh mid-run).
- Usage and cost are accumulated on every run; ivy's cap/ledger logic attaches
  as a provider decorator.
