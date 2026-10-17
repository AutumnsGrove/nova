# Nova — Architecture

This is the architecture of Nova. It began as a plan that an early prototype
([`SPIKE.md`](SPIKE.md), now historical) de-risked, and the build-out that followed is
finished. Why each choice was made is in [`DECISIONS.md`](DECISIONS.md).

## 1. One binary, many front doors

```
   you at a terminal      CI / GitHub Action     a website or another program
          │                       │                          │
     ┌────▼────┐            ┌─────▼─────┐             ┌──────▼───────┐
     │   TUI   │            │print/json │             │ rpc / serve  │
     └────┬────┘            └─────┬─────┘             └──────┬───────┘
          └───────────┬───────────┴───────────────────────────┘
                      │   one JSONL protocol  (docs/PROTOCOL.md)
              ┌───────▼────────┐        ┌─────────────────────────┐
              │  Session API   │◀──────▶│  extension processes    │
              │ (agent + store)│  same  │  (any language)         │
              └───────┬────────┘ framing└─────────────────────────┘
                      │
     tools   providers   sessions   compaction   permissions   config
```

Every front door talks to the same session API through the same protocol. The TUI
is a client that happens to run in the same process, connected by an in-memory
pipe. A web backend is a client that happens to talk over stdio or HTTP. Because
there is one path, one set of tests covers all of them.

Go programs can also import the packages directly and skip the protocol. That is
the "library" use, and it is how Ivy, her-go, and polaris will embed Nova.

## 2. Layout

One module: `github.com/AutumnsGrove/nova`.

```
nova/
  message/        transcript types                              ≤ 250 lines
  media/          image decode, resize, bounds                  ≤ 320
  clipboard/      OS clipboard image reader (TUI support)        ≤ 180
  provider/       Provider interface, registry, model catalog   ≤ 500
    openaicompat/ chat-completions + SSE                        ≤ 600
    responses/    OpenAI Responses API + SSE                    ≤ 480
    messages/     Anthropic Messages API + SSE                  ≤ 480
    ssestream/    shared POST + SSE transport, timeouts         ≤ 320
    opencodego/   preset: catalog, endpoint routing             ≤ 320
    openrouter/   preset: routing options, headers, usage       ≤ 300
  auth/           OAuth PKCE login, key store                   ≤ 500
  tool/           Tool contract, registry, schema validation    ≤ 550
  tools/          fs (read write edit list)                     ≤ 1060
                  search (glob grep)                            ≤ 700
                  bash                                          ≤ 400
  permission/     allow/ask/deny policy                         ≤ 300
  proto/          wire types and JSONL framing                  ≤ 600
  agent/          the loop, hooks, queues, retry                ≤ 1080
  session/        JSONL tree store, branches, custom entries    ≤ 900
  compaction/     summarise-and-splice, context limit           ≤ 600
  usage/          usage ledger and cost fallback                ≤ 200
  config/         YAML config, trust, context files, skills     ≤ 1250
  mode/           server core, print, json, rpc                 ≤ 2320
    attach/       protocol attachments to content parts         ≤ 160
    serve/        the protocol over HTTP and SSE                ≤ 430
    action/       the GitHub review action                      ≤ 700
    providers/    build both providers, route by model          ≤ 320
  ext/            extension host, manifest, check, reload,
                  in-process API for bundled extensions         ≤ 3100
    install/      install, update, remove, lockfile, git        ≤ 660
    sdk/          the Go SDK for extensions                     ≤ 700
  testkit/        fake provider and server, clock, golden       ≤ 1200
  bundled/        in-tree extensions: tasks, ask, plan,
                  subagent, webfetch                            ≤ 2500 total
  tui/            the terminal UI                               ≤ 9200
    image/        inline image capability and Kitty encoding   ≤ 260
    cli/          fang-styled help and errors (full build only) ≤ 150
  cmd/nova/       main, cobra commands, doctor, version         ≤ 1260
  doctor/         environment checks                            ≤ 440
  diag/           crash reports and debug traces                ≤ 250  update/         replace the binary from a checkout or release ≤ 450
  scripts/        matrixcheck, linecount, importcheck, accept
  assets/         logo SVGs and the generator
  docs/           embedded topics, search, authoring context    ≤ 220
```

Budgets are line counts of non-test Go and are enforced by a CI check (D37). They
are a design constraint: if a package outgrows its budget, split it or cut scope.
The core (everything except `tui/`, `bundled/`, and `testkit/`) should stay near 11,000 lines.
Pi's agent core is about 2,500 lines; Crush's UI alone is about 44,000.

### Layer rules

```
message ← provider ← tool ← proto ← agent ← mode ← cmd
   ▲         ▲         ▲              ▲
   │         │         │              └─ permission, usage, compaction, session
   └─────────┴─────────┴──── auth, config, ext (beside agent; never import tui)
```

- `message` imports nothing.
- `media` imports `message`. It is the one place image bytes are decoded,
  oriented, resized, and bounded.
- `provider` imports `message`. `tool` imports `message` and `provider`.
- `proto` imports `message` and `provider` and nothing above them.
- `permission` imports `tool`. `usage` imports `provider` and `session`.
- `tools/*` import `message`, `provider`, `tool`, and (for the shared glob engine)
  `permission`; they never import each other or a higher layer.
- `agent` imports `message`, `provider`, `tool`, `permission`, `proto`. `config`
  imports `permission` because it decodes the policy.
- `session` imports `message`, `provider`, `proto`. `compaction` imports `session`, `provider`, `message`.
- `ext` imports `proto`, `tool`, `agent`, `permission`. It never imports `tui`.
- `diag` is a stdlib-only leaf: it holds no Nova state, so `agent` and `ext` may
  import it for crash reporting without breaking the layering.
- `mode` wires everything (including `serve` and `action`). `cmd/nova` only parses flags and calls `mode`, `ext`, and `update`.
- `testkit` is test support: any package's `_test.go` may import it, but production
  code may not (D48).
- **`tui/` and `bundled/*` are the only packages allowed to import `charm.land/*`.**
  `scripts/importcheck` (C00) walks the import graph and fails if any other package does
  (D15), and also enforces the layer rules above.
- Dependencies follow the policy in `DECISIONS.md` D46: an allow-list in `scripts/importcheck`, each
  module tied to the packages that may import it. The core uses `gopkg.in/yaml.v3`; `cmd/nova` uses
  `cobra`; only `tui/`, `tui/cli`, and `bundled/` use UI libraries.

### Build flavours

| Build | Command | Contains |
|---|---|---|
| Full | `go build ./cmd/nova` | everything, TUI included |
| Minimal | `go build -tags notui ./cmd/nova` | no `tui/`, no bundled UI; print, json, rpc, serve |

`tui/` and the UI half of `bundled/*` carry `//go:build !notui`.

## 3. Core types

The prototype's types are the starting point. These are the changes needed.

### Message

The transcript stays OpenAI-shaped so the provider layer is thin. Additions:

```go
type Message struct {
    Role       Role       `json:"role"`
    Content    string     `json:"content,omitempty"`
    Reasoning  string     `json:"reasoning,omitempty"`   // assistant only; display and session, not re-sent unless the provider needs it
    ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
    ToolCallID string     `json:"tool_call_id,omitempty"`
    Name       string     `json:"name,omitempty"`         // tool name on a tool result
}
```

Content parts (images, documents) are designed but not built. The contract — the
`ContentPart` type, media on user messages only, normalization and bounds, and
inline rendering — is in [`IMAGE.md`](IMAGE.md); the session entry format leaves a
`parts` field free so it can be added without a migration.

### Provider

`Request` gains `ProviderOptions map[string]any` (merged into the wire body),
`ReasoningEffort string`, and a `Stop []string`. `Usage` gains `ReasoningTokens`.
`StreamEvent` gains `StreamUsage` (a mid-stream usage chunk) and stays otherwise
the same. `Response.FinishReason` is normalised to `stop`, `length`, `tool_calls`,
`content_filter`, or `error`.

### Events

The prototype's `agent.Event` holds an `error` value, which cannot cross a process
boundary. The target event type is plain data. It is defined in `proto` and
`agent` emits it directly. See [`PROTOCOL.md`](PROTOCOL.md) for the list.

### Tool

`tool.Result` gains `Details json.RawMessage` (structured data for renderers, not
sent to the model) and `Truncated bool`. `tool.Context` gains `Emit func(Update)`
so a long-running tool can stream progress, `SessionID`, and `Complete` for nested
model calls (D29). Annotations (`ReadOnly`, `Destructive`, `OpenWorld`,
`Parallel`) stay and feed the permission policy.

## 4. The agent loop

One run is synchronous. A caller that wants concurrency runs it in a goroutine and
reads events.

```
prompt in
  │
  ▼
build request: system + active branch + tools + provider options
  │   hooks: transform_context, before_provider_request
  ▼
provider.Stream ──▶ text / reasoning / tool-call deltas ──▶ events
  │
  ├── no tool calls ───────────────────────────────▶ end of run
  │                                                    (follow-up queue? start next)
  └── tool calls
        │  for each call, in parallel unless declared otherwise:
        │    permission policy ─▶ before_tool_call hook ─▶ run ─▶ after_tool_call hook
        ▼
      append results in call order ─▶ turn_end hook ─▶ steering queue? ─▶ next turn
```

Rules the loop must keep, each of which has tests in [`TESTING.md`](TESTING.md):

- **Cancellation.** `ctx` cancel aborts the provider call and is passed to every tool.
  A cancelled run returns the partial transcript and `ctx.Err()`.
- **Panics.** A panic in a tool becomes an error result, in sequential mode as well
  as parallel. (The prototype recovers only in parallel mode. That is a known bug.)
- **Order.** Results are appended in call order regardless of completion order.
- **Spend is never lost.** If the provider fails, the usage already accrued is
  returned with the error.
- **Steering and follow-up.** Two queues, as in Pi. A *steering* message is injected
  after the current assistant turn finishes its tool calls. A *follow-up* message is
  run after the agent has finished all pending work. Abort returns queued messages
  to the caller.
- **Retry.** A transient provider failure retries with exponential backoff, up to a
  configured count, and emits `retry` events. This includes a network timeout: a
  connect or idle timeout is a retryable error on every provider dialect, and the
  partial response is kept.
- **Turn budget.** A caller-set `MaxTurns` ends the run with `Exhausted=true`; it never errors. There
  is no default cap, so an unbounded run is stopped only by the abort path (user cancel, `esc`, or
  context cancellation).
- **Hooks never crash the loop.** A hook that panics or exceeds its deadline is
  skipped, logged, and reported as an `extension_error` event.

### Hook points

These names are shared by Go callbacks, bundled extensions, and the protocol.

| Hook | Can do |
|---|---|
| `input` | rewrite or swallow a user prompt before it is recorded |
| `before_agent_start` | add to the system prompt or active tools |
| `transform_context` | rewrite the message list for one request |
| `before_provider_request` | mutate the wire payload (pinning, headers) |
| `provider_stream_event` | observe raw stream events |
| `message_end` | replace a finalised assistant message |
| `tool_call` | block, or rewrite arguments |
| `tool_result` | replace a result; handlers compose in order |
| `turn_end` | request one more turn |
| `agent_end` | observe the end of a run |
| `session_start`, `session_shutdown` | start or release resources |
| `compaction_before` | cancel or supply a summary |

## 5. Sessions

A session is a JSONL file. Each line is one entry with an `id` and a `parent`.
The active leaf selects the branch that supplies model context. Continuing from an
older entry makes a new branch without deleting the old one.

Entry types: `message`, `model_change`, `compaction`, `branch_summary`, `custom`
(an extension's data), `custom_message` (an extension's visible message),
`label`, and `session_info`. A `custom` entry marked `keep: true` survives
compaction. `session.Manager` is the only authority for the model-facing context;
assigning an in-memory slice never replaces the persisted one.

## 6. Compaction and the context limit

`context_limit` is a per-session setting, not a model property (D25). Compaction
triggers when estimated context exceeds `context_limit - reserve`.

1. Estimate tokens (provider usage when present, otherwise a character heuristic).
2. Find a cut point at a user-message boundary. **Never inside a tool call and its
   result.**
3. Summarise the older span with the session model.
4. Insert a `compaction` entry that records what it replaced. Keep recent turns whole.
5. Prune stale tool output at request time without dropping the summary.

If summarisation fails, the run continues without compaction and an event reports
it. A failed compaction never loses entries.

## 7. Permissions

A declarative policy decides what happens to each tool call before it runs.

```yaml
permissions:
  default: ask            # allow | ask | deny
  headless: deny          # what "ask" becomes when there is no one to ask
  rules:
    - tool: read          # tool name or glob
      action: allow
    - tool: bash
      match: { command: "git status*" }
      action: allow
    - tool: write
      paths: ["**/.env*", ".git/**"]
      action: deny
```

`ask` becomes a `ui.confirm` request to the client. With no client attached (print
mode, the GitHub Action), `ask` resolves to `headless`. Annotations on the tool give
the defaults: read-only tools are allowed, destructive ones ask.

## 8. Providers and auth

Nova ships two core providers at launch. `openrouter` is a preset over
`openaicompat` (chat completions with SSE and tool calls) that adds routing
options, the attribution headers, and OpenRouter's `cost` field. `opencodego` is
the second: a preset with its own catalog that routes each model to one of three
streaming dialects, `provider/responses` (the OpenAI Responses API),
`provider/messages` (the Anthropic Messages API), or `openaicompat`. All three
dialects sit on `provider/ssestream`, which owns the connect timeout (30 s), the
idle timeout (120 s), the SSE line reader, and the `provider.Classify` error
mapping, so a network timeout is retryable and identical on every endpoint.
`mode/providers` builds both providers and picks one by the model's catalog entry.

`auth` owns login. `nova login` runs OpenRouter's OAuth PKCE flow with a loopback
redirect (or prints a URL with `--no-browser`) and stores the key in the user's
config directory with mode 0600. Resolution order: explicit config, `OPENROUTER_API_KEY`,
stored key. OpenCode Go resolves `provider.opencode_go.api_key` then
`OPENCODE_GO_API_KEY`; it has no OAuth flow at launch. Credentials resolve per
request so a refreshed token takes effect mid-run.

Models are a code-defined catalog (stable id, wire slug, context window, pricing)
so a stored choice keeps resolving when a provider renames a slug, and cost has a
fallback when a response reports none.

## 9. Configuration and resources

| Resource | Where | Loaded |
|---|---|---|
| Config | `~/.config/nova/config.yaml`, `.nova/config.yaml` | at start and on `/reload` |
| Context files | `AGENTS.md`, `CLAUDE.md` up the directory tree | into the system prompt |
| Skills | `~/.config/nova/skills`, `.nova/skills`: `SKILL.md` per directory | description in the prompt; body on demand |
| Prompt templates | `.nova/prompts/*.md` | expanded from `/name args` |
| Themes | `~/.config/nova/themes/*.yaml` | the TUI only |
| Extensions | `~/.config/nova/extensions`, `.nova/extensions` | at start and on `/reload` |

Project resources load only after the project is **trusted**. The first run in a new
directory asks; headless modes treat an untrusted project as having no project
extensions or hooks.

## 10. Modes

| Mode | Flag | Behaviour |
|---|---|---|
| TUI | default when stdin is a terminal | interactive |
| Print | `-p "prompt"` | one run, final answer on stdout |
| JSON | `--mode json` | one run, events as JSONL on stdout |
| RPC | `--mode rpc` | long-lived; commands in, events and responses out, on stdio |
| Serve | `nova serve --listen :8080` | the protocol over HTTP: `POST /v1/commands`, SSE `GET /v1/events` |
| Action | `nova action review` | print mode with a CI preset; reads the PR, posts review comments |

The GitHub Action is a thin `action.yml` that installs the binary and runs
`nova action review`. It uses the minimal build and the headless permission default.

## 11. Concurrency

- Tool calls in one assistant message run in parallel unless a tool declares
  otherwise. Results are stored by index.
- File-mutating tools serialise per path.
- `session.Manager` is safe for concurrent readers and serialises writers.
- Each extension process has one reader and one writer goroutine per connection.
  A slow extension never blocks the loop past its hook deadline.

## 12. Error model

| Failure | Result |
|---|---|
| Provider error | partial result with usage so far, plus the error |
| Tool error | `IsError` result; the model sees it and may retry |
| Unknown tool or bad JSON arguments | error result before dispatch |
| Panic in a tool or hook | recovered; error result or `extension_error` event |
| Cancellation | prompt return with `ctx.Err()` |
| Extension crash | its tools unregister, an event is emitted, restart per policy |
| Corrupt session line | the loader skips it, reports it, and loads the rest |
