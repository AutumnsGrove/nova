---
chunk: C20
title: "OpenCode Go, the second launch provider"
packages: [./provider/ssestream, ./provider/responses, ./provider/messages, ./provider/opencodego, ./mode/providers, ./auth/opencode, ./mode, ./tui/ui]
budgets: { ./provider/ssestream: 320, ./provider/responses: 480, ./provider/messages: 480, ./provider/opencodego: 560, ./mode/providers: 360, ./auth/opencode: 720, ./config: 1450, ./auth: 500, ./mode: 2150, ./tui/ui: 2350 }
depends: [C02, C03, C12]
---

# C20 — OpenCode Go

## Goal

Ship the second provider Nova launches with: **OpenCode Go**, a preset with its own model catalog
and per-model wire routing. Because some Go models speak only the OpenAI **Responses** API or the
Anthropic **Messages** API, this chunk adds those two streaming dialects as new packages over a
shared SSE transport, so a connect or idle timeout is handled the same way on every endpoint.

## Read first

- `provider/openaicompat/openaicompat.go`, `stream.go`, `errors.go` (the C02 result; C20 reuses its
  behaviour but does not edit it).
- `provider/openrouter/openrouter.go` and `catalog.go` (the preset pattern C20 follows).
- `provider/provider.go`, `provider/model.go`, `provider/registry.go`, `provider/error.go`, `provider/options.go`.
- `auth/resolve.go`; `config/config.go` (`ProviderConfig`); `mode/wire.go`; `mode/server.go` (`Options.Provider`).
- `docs/ARCHITECTURE.md` section 8; `docs/DECISIONS.md` D4, D23, D56.
- **Fetch and read, before writing any code.** Do not guess fields, endpoints, or headers:
  1. OpenCode Go: <https://dev.opencode.ai/docs/go> — the base URL, the models list, every model's
     endpoint, and the authentication header. The Zen page (<https://opencode.ai/docs/zen>) shows the
     `Authorization: Bearer` style; confirm it applies to Go.
  2. The OpenAI Responses streaming reference (`response.output_text.delta`,
     `response.output_item.added`, `response.function_call_arguments.delta`, `response.completed`,
     `response.failed`, `response.incomplete`) and its usage object.
  3. The Anthropic Messages streaming reference (`message_start`, `content_block_start`,
     `content_block_delta`, `message_delta`, `message_stop`, `ping`, `error`) and its usage object.

  The endpoints and event names below are from those pages as of 2026-10-11 and must be
  re-confirmed. If the pages cannot be fetched, **stop** and list the questions in `NEXTSTEPS.md`.

## Files

| File | Change | Purpose |
|---|---|---|
| `provider/ssestream/ssestream.go` | create | shared POST + SSE transport with connect/idle timeouts and `provider.Classify` |
| `provider/ssestream/ssestream_test.go` | create | transport and timeout tests |
| `provider/responses/responses.go` | create | the Responses-API provider |
| `provider/responses/wire.go` | create | request and event wire types |
| `provider/responses/responses_test.go` | create | uses `testkit.Server` |
| `provider/messages/messages.go` | create | the Anthropic Messages provider |
| `provider/messages/wire.go` | create | request and event wire types |
| `provider/messages/messages_test.go` | create | uses `testkit.Server` |
| `provider/opencodego/opencodego.go` | create | `New`, `APIFor`, the dialect router |
| `provider/opencodego/catalog.go` | create | code-defined catalog with endpoints and prices |
| `provider/opencodego/*_test.go` | create | catalog and routing |
| `mode/providers/providers.go` | create | `Build`, credential resolution, the cross-provider router |
| `mode/providers/providers_test.go` | create | config/env resolution and provider selection |
| `config/config.go` | change | `ProviderConfig.OpenCodeGo`, `OpenCodeGoConfig` |
| `auth/resolve.go` | change | `EnvOpenCodeGoKey`, `ResolveNamed`; `Resolve` delegates to it |
| `mode/wire.go` | change | `buildProvider` delegates to `providers.Build` |

The new dialects live in their own packages because the C02 guard (`scripts/matrixcheck`) treats a
package's test names as belonging to one spec; the same reason C14 uses `mode/serve`. Do not add
tests to `provider/openaicompat`, `auth`, `config`, or `mode`; their existing tests are the
regression suite, and their new code is exercised from `mode/providers`. Budgets are raised here and
must be confirmed by the human (see `NEXTSTEPS.md`); do not silently exceed them.

## Interfaces

```go
// package ssestream
type Config struct {
    HTTPClient     *http.Client  // nil means http.DefaultClient
    ConnectTimeout time.Duration // time to first response header; default 30s
    IdleTimeout    time.Duration // gap between events; default 120s
    MaxLineBytes   int           // one SSE line; default 8 MiB
}

// Event is one SSE event: the event name (empty for a plain data event) and the
// joined data payload without the "data:" prefix.
type Event struct {
    Name string
    Data []byte
}

type Stream struct{ /* private */ }

// Open posts body to url and returns an SSE stream. A non-2xx status is a
// *provider.Error built with provider.Classify. The caller must Close.
func Open(ctx context.Context, cfg Config, url string, headers http.Header, body []byte) (*Stream, error)

// Next returns the next event; io.EOF means the stream ended (a [DONE] line or
// the body closed). Context cancellation returns ctx.Err() unwrapped; a
// connect or idle timeout returns a retryable *provider.Error.
func (s *Stream) Next() (Event, error)
func (s *Stream) Close() error
```

```go
// package responses
type Config struct {
    Name            string // default "responses"
    BaseURL         string // "/responses" is appended
    APIKey          string
    APIKeyFunc      func(ctx context.Context) (string, error)
    Headers         map[string]string
    HTTPClient      *http.Client
    ConnectTimeout  time.Duration
    IdleTimeout     time.Duration
    MaxLineBytes    int
    MaxOutputTokens int // sent when the request sets none; default 4096
}
func New(cfg Config) *Provider
func (p *Provider) Name() string
func (p *Provider) Stream(ctx context.Context, req provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error)

// package messages
type Config struct {
    Name         string // default "messages"
    BaseURL      string // "/messages" is appended
    APIKey       string
    APIKeyFunc   func(ctx context.Context) (string, error)
    Headers      map[string]string
    HTTPClient   *http.Client
    ConnectTimeout time.Duration
    IdleTimeout    time.Duration
    MaxLineBytes   int
    MaxTokens      int // always sent; default 4096
}
func New(cfg Config) *Provider
func (p *Provider) Name() string
func (p *Provider) Stream(ctx context.Context, req provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error)
```

```go
// package opencodego
const (
    DefaultBaseURL = "https://opencode.ai/zen/go/v1"
    DefaultModel   = "opencode-go/glm-5.3"
    ProviderName   = "opencodego"
)

type Dialect string

const (
    DialectChat      Dialect = "chat_completions"
    DialectResponses Dialect = "responses"
    DialectMessages  Dialect = "messages"
)

type Config struct {
    APIKey         string
    APIKeyFunc     func(ctx context.Context) (string, error)
    BaseURL        string // default DefaultBaseURL
    HTTPClient     *http.Client
    ConnectTimeout time.Duration
    IdleTimeout    time.Duration
}

// New returns a provider.Provider named ProviderName that sends each request
// to the dialect its model uses.
func New(cfg Config) provider.Provider

// APIFor reports the dialect for a model id, defaulting to DialectChat.
func APIFor(model string) Dialect

var Catalog []provider.Model
func RegisterModels()
```

```go
// package providers (path mode/providers)
type Options struct {
    Config *config.Config
    Home   string
    Getenv func(string) string
    // Store is the OpenRouter credential store; nil means no stored key.
    Store auth.Store
}

// Build returns a provider.Provider that serves the OpenRouter and OpenCode Go
// catalogs and routes each request to the provider its model names.
func Build(opt Options) provider.Provider

// ResolveOpenCodeGoKey resolves the Go key from config, then the environment.
func ResolveOpenCodeGoKey(cfg *config.Config, getenv func(string) string) (key, source string, err error)
```

```go
// package auth
const EnvOpenCodeGoKey = "OPENCODE_GO_API_KEY"

// ResolveNamed is Resolve with a caller-chosen environment variable.
func ResolveNamed(cfgKey, envName string, env func(string) string, store Store) (key, source string, err error)
```

```go
// package config
type ProviderConfig struct {
    OpenRouter OpenRouterConfig `yaml:"openrouter"`
    OpenCodeGo OpenCodeGoConfig `yaml:"opencode_go"`
}
type OpenCodeGoConfig struct {
    APIKey  string `yaml:"api_key"`
    BaseURL string `yaml:"base_url"`
}
```

## Behaviour

### The shared transport

1. `ssestream.Open` sends `POST url` with the caller's headers, `Accept: text/event-stream`, and a
   `ConnectTimeout` that cancels the request if the response headers never arrive. The default
   client is `http.DefaultClient` when none is given.
2. `Next` reads lines up to `MaxLineBytes`; a longer line is a non-retryable `*provider.Error`
   naming the limit. Lines may end `\n` or `\r\n`. Comment lines (`:`) and blank lines are skipped.
   `data:` is accepted with or without a following space. `event:`, `id:`, and `retry:` set the
   event name or are ignored; multiple `data:` lines join with `\n`.
3. A `data: [DONE]` line ends the stream with `io.EOF`. The body closing with at least one event
   also returns `io.EOF`. A body that closes before any event returns a retryable "empty stream"
   error.
4. A gap longer than `IdleTimeout` between reads aborts with a retryable `*provider.Error`. A
   connection failure before the headers (refused, DNS, reset, connect timeout) is a retryable
   `*provider.Error` with `Err` set. Context cancellation returns `ctx.Err()` unwrapped.
5. A non-2xx status is a `*provider.Error` with `Status`, `Retryable`, and `RetryAfter` from
   `provider.Classify`; the message comes from `error.message` in a JSON body, else the first
   500 bytes with HTML tags stripped, read with a 1 MiB cap.
6. `Open` and `Next` never leak a goroutine or a connection: a failed or cancelled call closes the
   response body.

### OpenAI Responses API

7. The request body is JSON: `model`, `input`, `stream: true`, `tools` and `tool_choice` when set,
   `temperature` when set, `max_output_tokens` (the request's value, else `MaxOutputTokens`), and
   `reasoning: {effort}` when `ReasoningEffort` is set. `req.ProviderOptions` is merged at the top
   level with `provider.MergeOptions`.
8. The leading system message becomes the top-level `instructions` string. Other messages become
   `input` items: user/assistant text as `{role, content}`, assistant tool calls as
   `{type: "function_call", call_id, name, arguments}`, and tool results as
   `{type: "function_call_output", call_id, output}`. Tools are flattened to
   `{type: "function", name, description, parameters}`.
9. A `response.output_text.delta` appends `delta` and emits `StreamTextDelta`. A
   `response.reasoning_summary_text.delta` or `response.reasoning_text.delta` appends to
   `Message.Reasoning` and emits `StreamReasoningDelta`.
10. A `response.output_item.added` whose `item.type` is `function_call` starts a call keyed by
    `item.id` (falling back to `output_index`), carrying the `call_id` and `name`.
    `response.function_call_arguments.delta` appends `delta` for that id. Calls are emitted via
    `StreamToolCall` only at the terminal event, in `output_index` order.
11. `response.completed` is terminal. Its `response.usage` maps `input_tokens` → `PromptTokens`,
    `output_tokens` → `CompletionTokens`, `input_tokens_details.cached_tokens` → `CachedTokens`,
    `output_tokens_details.reasoning_tokens` → `ReasoningTokens`, and `total_tokens` → `TotalTokens`.
12. The finish reason is `stop` when completed, `length` when `response.incomplete` has
    `incomplete_details.reason == "max_output_tokens"`, and becomes `tool_calls` when a call was
    assembled; `provider.NormalizeFinish` is applied. `response.failed` and a top-level `error`
    event are non-retryable `*provider.Error`s whose message comes from `response.error.message` or
    `error.message`. Unknown event types are ignored. A stream that ends before `response.completed`
    is a retryable error; text and complete calls stay in the partial `Response`.

### Anthropic Messages API

13. The request body is JSON: `model`, `messages`, `stream: true`, and `max_tokens` (the request's
    value, else `MaxTokens`). The leading system message becomes the top-level `system` string.
    `tools` map to `{name, description, input_schema}`; `tool_choice` maps to `{type}` or
    `{type: "tool", name}`. `temperature` passes through when set.
14. Headers add `x-api-key: <key>` and `anthropic-version: 2023-06-01`. The key comes from
    `APIKeyFunc` first, else `APIKey`, and a failing `APIKeyFunc` is a non-retryable
    `*provider.Error` with no request sent.
15. Messages carry content blocks: text as `{type: "text", text}`; a tool call as
    `{type: "tool_use", id, name, input}`; a tool result as
    `{type: "tool_result", tool_use_id, content}`.
16. A `content_block_start` with `type: "text"` begins text, `"thinking"` begins reasoning, and
    `"tool_use"` begins a call keyed by `index` with the block's `id` and `name`.
    `content_block_delta` carries `text_delta`, `thinking_delta`, or `input_json_delta`
    (`partial_json`). Calls are emitted via `StreamToolCall` at the terminal event, in block-index
    order.
17. `message_start` supplies input usage (`message.usage.input_tokens`,
    `cache_read_input_tokens`); `message_delta` supplies `delta.stop_reason` and the final
    `usage.output_tokens`, summed across events.
18. `stop_reason` maps through `provider.NormalizeFinish`: `end_turn` → `stop`, `tool_use` →
    `tool_calls`, `max_tokens` → `length`, `stop_sequence` → `stop`; a missing reason with tool
    calls becomes `tool_calls`. `ping` is ignored. An `error` event is a non-retryable
    `*provider.Error` naming `error.message`. A stream that ends without `message_stop` is a
    retryable error; delivered text and complete calls stay in the partial `Response`.

### The OpenCode Go preset

19. `New` returns a `provider.Provider` named `opencodego` whose `Stream` dispatches on `req.Model`
    with `APIFor`. Internally it holds a chat-completions provider (`openaicompat`, unchanged), a
    Responses provider, and a Messages provider, all built from the same base URL, key function,
    HTTP client, and timeouts.
20. `APIFor` looks up the catalog's endpoint table and returns `DialectResponses`,
    `DialectMessages`, or `DialectChat`; a model not in the table defaults to `DialectChat`. The
    endpoint recorded for each catalog entry must equal `APIFor(entry.ID)`.
21. `DefaultBaseURL` is `https://opencode.ai/zen/go/v1`; a trailing slash is trimmed. The key is
    sent as `Authorization: Bearer <key>`, resolved per request through `APIKeyFunc` when set. A
    `User-Agent: nova/<version>` header is sent; no OpenCode header is forged.
22. `Catalog` is code-defined and small. Every entry has a stable ID of the form
    `opencode-go/<model-id>`, a display name, `Provider: "opencodego"`, the wire `Slug`, a positive
    context window, and per-million-token `InPerM`/`OutPerM` from the Go pricing table with the
    fetch date in a comment. It includes at least one model per dialect:
    `opencode-go/glm-5.3` (chat), `opencode-go/grok-4.7` (responses), and
    `opencode-go/claude-haiku-5-5` (messages). `RegisterModels` is called from `init`, as
    `openrouter` does. A model whose price depends on prompt size uses the lower tier, with a
    comment that cost is an estimate.
23. `provider.Model.Provider` for every entry is `opencodego`, so the cross-provider router can
    find it. Importing `provider/opencodego` (which `mode/providers` does) registers its models
    with no duplicate-id panic.

### Credentials, config, and wiring

24. `auth.ResolveNamed(cfgKey, envName, env, store)` is `Resolve` with `envName` instead of the
    OpenRouter constant. `Resolve` calls `ResolveNamed(cfgKey, EnvAPIKey, ...)` and keeps its
    exact behaviour and order.
25. `config.ProviderConfig` gains `OpenCodeGo`; `OpenCodeGoConfig` has `api_key` and `base_url`.
    The merge, env, and unknown-key-normalisation rules that apply to `OpenRouterConfig` apply to
    it too.
26. `ResolveOpenCodeGoKey` resolves `config.provider.opencode_go.api_key`, then
    `OPENCODE_GO_API_KEY`, trimming whitespace; nothing set returns `auth.ErrNoKey`. There is no
    store at launch. The base URL is `config.provider.opencode_go.base_url`, else
    `NOVA_OPENCODE_GO_BASE_URL`, else `DefaultBaseURL`.
27. `providers.Build` builds the OpenRouter provider (lazily resolving its key through
    `auth.Resolve` and `opt.Store`) and the OpenCode Go provider, then returns a router whose
    `Stream` looks up `provider.GetModel(req.Model).Provider`: an OpenCode Go model goes to that
    provider, an OpenRouter model to OpenRouter, and an unknown id falls back to OpenRouter.
    `Name()` returns `"router"`.
28. `mode/wire.go`'s `buildProvider` becomes a thin call to `providers.Build` with the config, the
    home directory, `os.Getenv`, and the OpenRouter store. `mode.Options.Provider`, when set, is
    still used as-is and no router is built.

### Network failures and timeouts

29. Every dialect inherits `ConnectTimeout` (default 30 s) and `IdleTimeout` (default 120 s) from
    `ssestream`. A connection failure before the headers, including a connect timeout, is a
    retryable `*provider.Error` with `Err` set.
30. An idle gap longer than `IdleTimeout` between events aborts with a retryable
    `*provider.Error`. Delivered text stays in the partial `Response`; no partial tool call is
    emitted, because calls are emitted only at the terminal event.
31. A stream cut off before its terminal event (`response.completed` or `message_stop`) is a
    retryable error; text and complete calls already assembled stay in the partial `Response`.
32. Context cancellation returns `ctx.Err()` unwrapped, during connect, mid-stream, or after the
    last event.
33. These errors reach the agent unchanged, so C09's retry policy (exponential backoff,
    `Retry-After`, `retry` events) applies to Responses and Messages calls exactly as it does to
    chat-completions. This chunk adds no second retry layer.

## Matrix

| ID | Case | Input / setup | Expected |
|---|---|---|---|
| RSP01 | plain text turn | `output_text.delta` x3, `response.completed` | 3 text deltas; content joined; finish `stop` |
| RSP02 | request body shape | any turn | `model`, `input`, `stream:true`, `tools`, `max_output_tokens`, `reasoning.effort` present |
| RSP03 | system becomes instructions | leading system message | top-level `instructions`; not in `input` |
| RSP04 | assistant call and tool result round-trip | transcript with a call and its result | `function_call` and `function_call_output` items |
| RSP05 | reasoning delta | `reasoning_summary_text.delta` / `reasoning_text.delta` | `Message.Reasoning` set; reasoning events |
| RSP06 | one function call, args in 3 fragments | added + 3 arg deltas + completed | one `StreamToolCall` at the end; args joined |
| RSP07 | two function calls by `output_index` | interleaved deltas | two calls in index order |
| RSP08 | empty arguments | added, no arg delta | arguments `{}` |
| RSP09 | invalid JSON arguments | `{"a":` fragments | kept as received |
| RSP10 | usage mapping | completed with input/output/cached/reasoning and total | every field mapped |
| RSP11 | incomplete for max tokens | `response.incomplete`, reason `max_output_tokens` | finish `length` |
| RSP12 | `response.failed` | failure event | non-retryable `*provider.Error`, message from body |
| RSP13 | top-level `error` event | error event | non-retryable `*provider.Error` |
| RSP14 | unknown event types | `response.created` and a `foo` event | ignored; no panic |
| RSP15 | finish `stop` with tool calls | completed after a function call | finish `tool_calls` |
| RSP16 | stream ends before completed | server closes after text | retryable; partial text kept |
| RSP17 | empty stream | no events | retryable "empty stream" |
| RSP18 | connect timeout | headers never sent; `ConnectTimeout` 50 ms | retryable `*provider.Error` |
| RSP19 | idle timeout | text delta, then silence; `IdleTimeout` 50 ms | retryable; text kept |
| MSG01 | plain text turn | `message_start`, text block, `message_delta(end_turn)`, `message_stop` | one text delta; content; finish `stop` |
| MSG02 | request body shape | any turn | `model`, `system`, `messages`, `tools.input_schema`, `max_tokens`, `stream:true` |
| MSG03 | headers | any request | `x-api-key` and `anthropic-version: 2023-06-01` |
| MSG04 | `max_tokens` always present | request with `MaxTokens` 0 | `max_tokens` equals `MaxTokens` default |
| MSG05 | thinking block | thinking start + `thinking_delta` | reasoning appended; reasoning event |
| MSG06 | tool use | `tool_use` block + `input_json_delta` fragments | one `StreamToolCall`; args joined |
| MSG07 | two tool blocks | blocks 0 and 1 | two calls in index order |
| MSG08 | tool result round-trip | assistant `tool_use` and a user `tool_result` | `tool_result` content block |
| MSG09 | `tool_choice` mapping | auto and a named tool | `{type:"auto"}` / `{type:"tool", name}` |
| MSG10 | usage across events | `message_start` input, two `message_delta` outputs | input and summed output |
| MSG11 | stop reason mapping | `end_turn`, `tool_use`, `max_tokens`, `stop_sequence` | `stop`, `tool_calls`, `length`, `stop` |
| MSG12 | `ping` ignored | ping between events | no event, no error |
| MSG13 | `error` event | error block | non-retryable `*provider.Error` |
| MSG14 | stream ends before `message_stop` | server closes after a text block | retryable; partial text kept |
| MSG15 | empty stream | no events | retryable "empty stream" |
| MSG16 | connect timeout | headers never sent; `ConnectTimeout` 50 ms | retryable `*provider.Error` |
| MSG17 | idle timeout | text block, then silence; `IdleTimeout` 50 ms | retryable; text kept |
| OPG01 | `New` names the provider | any config | `Name()` is `opencodego` |
| OPG02 | chat model routes to chat | `APIFor("opencode-go/glm-5.3")` | `DialectChat` |
| OPG03 | responses model routes to Responses | `APIFor("opencode-go/grok-4.7")` | `DialectResponses` |
| OPG04 | messages model routes to Messages | `APIFor("opencode-go/claude-haiku-5-5")` | `DialectMessages` |
| OPG05 | unknown model falls back | `APIFor("nope")` | `DialectChat` |
| OPG06 | base URL default and trimming | `BaseURL` `"…/v1/"` | trailing slash removed; default used when empty |
| OPG07 | bearer key per request | `APIKeyFunc` returns `k2` | server sees `Authorization: Bearer k2` |
| OPG08 | user agent | any request | `User-Agent` starts `nova/` |
| OPG09 | catalog entries valid | iterate `Catalog` | non-empty ID, name, slug; `Provider` `opencodego`; positive context and price |
| OPG10 | catalog endpoint matches routing | iterate `Catalog` | `APIFor(entry.ID)` equals the recorded dialect |
| OPG11 | model id shape | iterate `Catalog` | ID starts `opencode-go/`; slug is the trailing part |
| OPG12 | config key resolves | `provider.opencode_go.api_key` set | resolved with source `config` |
| OPG13 | env key resolves | `OPENCODE_GO_API_KEY` set | resolved with source `env` |
| OPG14 | config beats env | both set | config wins |
| OPG15 | no key | neither set, no store | `auth.ErrNoKey` |
| OPG16 | router picks OpenCode Go | `req.Model` is an OpenCode Go id | OpenCode Go server receives it |
| OPG17 | router picks OpenRouter | `req.Model` is an OpenRouter id | OpenRouter server receives it |
| OPG18 | unknown model falls back | `req.Model` not in a catalog | OpenRouter receives it; no panic |
| OPG19 | base URL override | config, then `NOVA_OPENCODE_GO_BASE_URL` | config wins; env used when config empty |
| OPG20 | both catalogs list | `RegisterModels` on both | `provider.Models()` has both providers, no duplicate id |
| TMO01 | connect timeout | headers never sent; `ConnectTimeout` 50 ms | retryable `*provider.Error` |
| TMO02 | idle timeout | one event, then silence; `IdleTimeout` 50 ms | retryable `*provider.Error` |
| TMO03 | connection refused | server closed | retryable `*provider.Error` with `Err` |
| TMO04 | cancel during connect | server stalls headers; cancel | `context.Canceled`, promptly |
| TMO05 | cancel mid-stream | cancel after one event | `context.Canceled` |
| TMO06 | retryable and non-retryable statuses | 429 with `Retry-After`, then 401 | retryable with `RetryAfter`; then not retryable |
| TMO07 | line over `MaxLineBytes` | limit 1 KiB | non-retryable error naming the limit |
| TMO08 | SSE line forms | comment, blank, CRLF, `data:` without a space | parsed or skipped correctly |
| TMO09 | `[DONE]` and clean close | `[DONE]`, then a body closing after one event | `io.EOF` both times |
| TMO10 | no goroutine or connection leak | after every failure mode above | `testkit.NoLeaks` passes |
| TMO11 | agent retries a timed-out dialect call | agent loop over a stalling OpenCode Go server | one `retry` event, then success; earlier usage kept |

| LGN01 | device login full flow | fake Console | key returned; the old key revoked |
| LGN02 | sign-in denied | `access_denied` | error naming denied |
| LGN03 | device code expired | `expired_token` | error naming expired |
| LGN04 | verification URL mapping | console alias host | host rewritten to opencode.ai |
| LGN05 | default key name | any host | `nova-<host>`, sanitised, at most 60 |
| LGN06 | device code request | fake Console | device and user code plus interval |
| LGN07 | key store | save then load | mode 0600; missing is `auth.ErrNoKey` |
| DSC01 | live discovery | fake models and models.dev | overlay registered; cache fresh; baseline kept |
| OPG21 | session header | any request | `x-opencode-session` sent |
| OPG22 | default model | `DefaultModel` | `opencode-go/deepseek-v4.1-flash`, in the catalog |

| USG01 | parse all three meters | `/api/go/status` JSON | five_hour, week, month with used/limit/percent |
| USG02 | percent clamped | used over limit | percent 100 |
| USG03 | month reset fallback | no month `resetsAt`, `access.endsAt` set | month resets at `endsAt` |
| USG04 | missing window skipped | only `fiveHour` | one meter |
| USG05 | micro-cents to dollars | 100,000,000 micro-cents | $1.00 |
| USG06 | malformed payload | not JSON | no meters, no panic |
| USG07 | fetch sends auth | key and org | `Authorization: Bearer` and `x-org-id` |
| USG08 | unauthorized | 401 | `unauthorized` error |
| USG09 | http failure | 500 | `http` error naming the status |
| USG10 | no subscription | no `access` | `no_subscription` error |
| USG11 | unrecognized | `access` with no meters | `unrecognized` error |
| USG12 | network failure | server closed | `network` error |
| USG13 | format USD | 0.15 and 12 | `$0.150` and `$12.00` |
| USG14 | format percent | 62.5 and 31 | `62.5%` and `31%` |
| USG15 | format bar | ratio 0.5, width 18 | 9 filled, 9 empty |
| USG16 | format countdown | future and past | `1h 12m` and `resets now` |
| USG17 | format meter row | one meter | aligned `label bar pct money countdown` |
| USG18 | describe error | each kind | friendly text |
| USG19 | footer status | three meters | `go 5h 62% (1h 12m) · wk 31% · mo 44%` |
| USG20 | no key | no credential | no request; "not signed in" |
| USG21 | `get_usage` command | server with a key and a fake Console | meters and the footer string |
| USG22 | `get_usage` without a key | no credential | empty meters and a sign-in message |
| USG23 | `/usage` overlay | `get_usage` returns meters | one row per meter; esc closes |
| USG24 | footer widget | session start, then every 5 min | `ui.status` under `opencode-go` with the footer |
| USG25 | refresh cadence | fake clock advances 5 min | exactly one refetch |
| USG26 | refresh without a key | no credential | no `ui.status` published, no request |

## Fuzz and property tests

| Name | Asserts |
|---|---|
| `FuzzSSEStream` | arbitrary bytes as an SSE body never panic or hang; the result is events or an error |
| `FuzzResponsesStream` | arbitrary bytes as a Responses SSE body never panic or hang |
| `FuzzMessagesStream` | the same for the Messages SSE body |
| `TestProperty_OPG_Routing` | `APIFor` and the catalog endpoint never disagree for any catalog id, and every id starts `opencode-go/` |

## Out of scope

- Migrating `openaicompat` onto `provider/ssestream`; it keeps its own transport for now, and C02's
  tests stay untouched. A later refactor can delete the duplicate.
- OpenCode Zen (pay-as-you-go) or any provider other than Go.
- `nova login --provider opencode-go`; the key comes from config or `OPENCODE_GO_API_KEY`.
- Fetching `/models` at runtime: the catalog is code-defined.
- Quota and monthly-limit tracking; the C10 usage ledger is the only accounting.
- Image/vision content parts, even for the vision models in the catalog.
- Retry and backoff (C09), and the HTTP transport timeouts of `nova serve` (C14).

## Acceptance

```
go run ./scripts/accept C20
```
