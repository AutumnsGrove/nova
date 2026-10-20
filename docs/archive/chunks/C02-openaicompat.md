---
chunk: C02
title: "openaicompat streaming provider"
packages: [./provider/openaicompat]
budgets: { ./provider/openaicompat: 600 }
depends: [C01]
---

# C02 — `openaicompat`

## Goal

Make the OpenAI-compatible chat-completions provider robust: correct streaming
assembly, typed errors, provider options, reasoning and usage handling, and sound
behaviour when the network or the stream misbehaves. The prototype works on the happy
path; this chunk makes it correct on every path in `TESTING.md` section 3.

## Read first

- `provider/openaicompat/openaicompat.go` and its test (the prototype, 303 lines).
- `provider/provider.go` as changed in C01.
- `docs/TESTING.md` section 3, "Provider and streaming".

## Files

| File | Change | Purpose |
|---|---|---|
| `provider/openaicompat/openaicompat.go` | change | `Config`, `New`, `Stream` |
| `provider/openaicompat/stream.go` | create | SSE reading and tool-call assembly |
| `provider/openaicompat/wire.go` | create | request and chunk wire types |
| `provider/openaicompat/errors.go` | create | HTTP and body error classification |
| `provider/openaicompat/*_test.go` | create or change | uses `testkit.Server` |

Keep the `Config` fields that exist. The prototype's `streamOptions`, `chatRequest`, and
`chatChunk` types may be moved to `wire.go`; keep their JSON tags.

## Interfaces

```go
type Config struct {
    Name       string            // default "openaicompat"
    BaseURL    string            // no trailing slash; "/chat/completions" is appended
    APIKey     string            // "Authorization: Bearer <key>" when non-empty
    APIKeyFunc func(ctx context.Context) (string, error) // optional; wins over APIKey; called per request
    Headers    map[string]string
    HTTPClient *http.Client
    // ConnectTimeout bounds the time to first response header. Default 30s. Zero means default.
    ConnectTimeout time.Duration
    // IdleTimeout bounds the gap between stream chunks. Default 120s. Zero means default.
    IdleTimeout time.Duration
    // MaxLineBytes bounds one SSE line. Default 8 MiB.
    MaxLineBytes int
    // SendReasoning re-sends Message.Reasoning to the provider. Default false.
    SendReasoning bool
}

func New(cfg Config) *Provider
func (p *Provider) Name() string
func (p *Provider) BaseURL() string
func (p *Provider) Stream(ctx context.Context, req provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error)
```

## Behaviour

### Request

1. The body is JSON: `model`, `messages`, `stream: true`, `stream_options: {include_usage: true}`,
   `tools` and `tool_choice` when set, `temperature` when set, `max_tokens` when positive,
   `stop` when non-empty, `reasoning_effort` when non-empty.
2. `req.ProviderOptions` is **merged into the top level of the body** with
   `provider.MergeOptions`, after the fields above are set, so an option can add a field
   (OpenRouter's `provider`, `reasoning`) or override one. Options never remove fields.
3. `Message.Reasoning` is **not** sent back unless `Config.SendReasoning` is set (a bool
   field to add; default false). Tool messages include `tool_call_id`; `name` is sent only
   when non-empty.
4. Headers: `Content-Type: application/json`, `Accept: text/event-stream`, the configured
   `Headers`, then `req.ExtraHeaders` (which win), then `Authorization`. The key comes from
   `APIKeyFunc` if set, else `APIKey`. A failing `APIKeyFunc` returns a non-retryable error.
5. The request honours `ctx`. If `ctx` is already cancelled, no request is made.

### Response and errors

6. A non-2xx status returns a `*provider.Error` with `Status`, `Retryable` and `RetryAfter`
   from `provider.Classify`, and `Message` taken from the body: for a JSON body, the
   `error.message` field (or `message`), else the first 500 bytes of the body with HTML
   tags stripped. The body is read with a 1 MiB cap.
7. A 2xx response whose stream's first event is a JSON object with a top-level `error`
   field is an error: `Status` 200, `Code` from `error.code`, not retryable unless the
   code is `rate_limit_exceeded` or the message mentions overload.
8. Connection failures (refused, DNS, reset before any data, timeout to first byte) are
   `*provider.Error` with `Retryable: true` and `Err` set. Context cancellation returns
   `ctx.Err()` unwrapped (not a `*provider.Error`).
9. A stream that ends **before any chunk** is a retryable error ("empty stream").

### Streaming

10. Read lines up to `MaxLineBytes`; a longer line is a non-retryable error naming the limit.
    Lines may end `\n` or `\r\n`. Ignore comment lines (`:`) and blank lines. Handle `data:`
    with or without a following space. Ignore `event:`, `id:`, `retry:`.
11. `data: [DONE]` ends the stream. A stream that ends without `[DONE]` is **accepted** if
    at least one chunk with a finish reason arrived; otherwise it is a retryable error
    ("stream ended before completion") and any events already delivered remain delivered.
12. A line that is not valid JSON is a non-retryable error naming the line number; text
    already delivered is kept in the returned (partial) `Response` along with the error.
13. Each chunk may have zero choices (usage-only chunks); `choices[0].delta` fields may be
    `null` or absent. Only `choices[0]` is used.
14. Text: `delta.content` → `StreamTextDelta` and appended to the message. Reasoning: take
    `delta.reasoning_content`, else `delta.reasoning`, else the `text` of the entries in
    `delta.reasoning_details`; emit `StreamReasoningDelta` and append to `Message.Reasoning`.
15. Tool calls arrive as deltas keyed by `index`. The first delta for an index carries the
    id and name; later ones carry argument fragments. Assemble per index. A delta with a
    new non-empty `id` for an existing index replaces nothing: the first id wins. A tool call
    with no id gets a generated id `call_<index>_<n>`. Calls are emitted via `StreamToolCall`
    **only when the stream ends**, in index order, so partial arguments are never delivered.
16. Assembled arguments that are empty become `{}`. Arguments that are not valid JSON are
    **kept as received** (the loop reports the error to the model); they are not rewritten.
17. A tool call with an empty name is dropped from the message and reported with a
    `StreamError` event (not an error return).
18. `finish_reason` is recorded from the last chunk that has one and normalised with
    `provider.NormalizeFinish`. If the message has tool calls and the finish reason is
    `stop`, it becomes `tool_calls`.
19. Usage: use the last `usage` object seen. Map `prompt_tokens`, `completion_tokens`,
    `total_tokens`, `prompt_tokens_details.cached_tokens`,
    `completion_tokens_details.reasoning_tokens`, and a top-level numeric `cost` (set
    `CostSource: "provider"`). Emit a `StreamUsage` event when usage arrives. Missing usage
    leaves a zero `Usage`.
20. `Response.Model` is the `model` field from the stream if present, else the request model.
21. An idle gap longer than `IdleTimeout` aborts with a retryable error.
22. Unicode split across network reads is reassembled correctly (read by line, not by
    byte count).
23. `onEvent` may be nil. `onEvent` is called from the calling goroutine only.

## Matrix

| ID | Case | Input / setup | Expected |
|---|---|---|---|
| OAI01 | plain text turn | server: "Hello" in 3 chunks, usage | events: 3 text deltas; response content `Hello`; finish `stop` |
| OAI02 | request body shape | any turn | body has `stream:true`, `stream_options.include_usage:true`, model, messages |
| OAI03 | tools and tool_choice sent | request with tools | `tools` and `tool_choice` in body |
| OAI04 | optional fields omitted when unset | no temperature, max tokens 0 | keys absent |
| OAI05 | stop and reasoning_effort sent | set both | present |
| OAI06 | ProviderOptions merged at top level | `{provider:{order:["a"]}}` | body has `provider.order` |
| OAI07 | ProviderOptions nested merge | options override nothing destructively | existing fields intact |
| OAI08 | reasoning not re-sent by default | assistant message with `Reasoning` | body message has no `reasoning` |
| OAI09 | headers precedence | config header X=1, request header X=2 | server sees `X: 2` |
| OAI10 | authorization from `APIKeyFunc` | func returns `k2`, `APIKey` is `k1` | server sees `Bearer k2` |
| OAI11 | `APIKeyFunc` error | func returns error | non-retryable `*provider.Error`; no request sent |
| OAI12 | context already cancelled | | returns `context.Canceled`; server saw no request |
| OAI13 | 401 | | `*provider.Error` Status 401, not retryable, message from body |
| OAI14 | 429 with Retry-After | `Fault429` | retryable, `RetryAfter` 1s |
| OAI15 | 500 | `Fault500` | retryable |
| OAI16 | 502 HTML body | `FaultHTML` | retryable; message is tag-stripped text, at most 500 bytes |
| OAI17 | 200 with error object first | body `{"error":{"message":"overloaded"}}` | error; retryable for overload |
| OAI18 | 200 with error object, other code | `{"error":{"code":"invalid_api_key"}}` | non-retryable |
| OAI19 | connection refused | server closed | retryable `*provider.Error` with `Err` |
| OAI20 | cancel during connect | server stalls headers; cancel | returns `context.Canceled` promptly |
| OAI21 | cancel mid-stream | `FaultStall` after 1 chunk; cancel | returns `context.Canceled`; partial text delivered |
| OAI22 | cancel after last chunk | cancel as `[DONE]` is sent | either full response or `context.Canceled`; never a panic or leak |
| OAI23 | empty stream | server closes with no chunks | retryable error |
| OAI24 | missing `[DONE]` but finish seen | `FaultNoDone` | accepted; full response |
| OAI25 | missing `[DONE]` and no finish | stream cut after text, no finish | retryable error; delivered text kept in the partial response |
| OAI26 | cut mid-event | `FaultCutMidEvent` | retryable error; no partial tool calls emitted |
| OAI27 | malformed JSON line | `FaultMalformedJSON` | non-retryable error naming the line; earlier text kept |
| OAI28 | comment and blank lines | `: ping` lines between chunks | ignored |
| OAI29 | `data:` without a space | `data:{"..."}` | parsed |
| OAI30 | CRLF line endings | | parsed |
| OAI31 | `event:` and `id:` lines | | ignored |
| OAI32 | usage-only final chunk with empty choices | | usage captured; no panic |
| OAI33 | `choices` missing or `delta` null | | ignored; no panic |
| OAI34 | `content` is null | | ignored |
| OAI35 | reasoning via `reasoning_content` | | reasoning deltas; `Message.Reasoning` set |
| OAI36 | reasoning via `reasoning` | | same |
| OAI37 | reasoning via `reasoning_details` | entries with `text` | same |
| OAI38 | single tool call, args in 3 fragments | | one `StreamToolCall` after the stream; args joined |
| OAI39 | two tool calls interleaved by index | frags for 0,1,0,1 | two calls in index order, each correct |
| OAI40 | tool call with no id | | generated id `call_<index>_<n>`, unique across calls |
| OAI41 | tool call id arrives late | name first, id in a later chunk | the id is used; first non-empty id wins |
| OAI42 | tool call with empty arguments | | arguments `{}` |
| OAI43 | tool call with invalid JSON arguments | `{"a":` | kept as received |
| OAI44 | tool call with empty name | | dropped; a `StreamError` event; not an error return |
| OAI45 | text and tool calls in one message | | both present |
| OAI46 | finish `stop` but tool calls present | | finish `tool_calls` |
| OAI47 | finish reason aliases | `end_turn`, `tool_use`, `max_tokens` | normalised |
| OAI48 | finish reason missing everywhere | | finish `""`; accepted if `[DONE]` seen |
| OAI49 | usage mapping | all fields incl. cached and reasoning tokens and `cost` | every field mapped; `CostSource` `provider` |
| OAI50 | usage absent | | zero usage; no error |
| OAI51 | `Response.Model` from stream | | stream's model name |
| OAI52 | `onEvent` nil | | no panic |
| OAI53 | very large chunk | `FaultHugeChunk` (4 MiB) | parsed, within `MaxLineBytes` |
| OAI54 | line over `MaxLineBytes` | limit 1 KiB | non-retryable error naming the limit |
| OAI55 | many tiny chunks | 5,000 one-character chunks | correct content; completes in under a second |
| OAI56 | unicode split across reads | a 4-byte rune cut across network writes | content intact |
| OAI57 | idle timeout | `FaultStall` with `IdleTimeout: 50ms` | retryable error |
| OAI58 | connect timeout | headers never sent; `ConnectTimeout: 50ms` | retryable error |
| OAI59 | no goroutine or connection leak | after each failure mode | `NoLeaks` passes |
| OAI60 | unknown extra fields everywhere | | ignored |
| OAI61 | request is not mutated | compare `req` before and after | unchanged |
| OAI62 | `Name()` and `BaseURL()` | trailing slash on config | trimmed |
| OAI63 | image message sends a content array | a user message with text and a PNG part | `content` is `[text, image_url]` with a `data:` URL |
| OAI64 | image-only message sends a content array | a user message with one image and no text | `content` is one `image_url` block |
| OAI65 | text-only content stays a string | `User("hi")` | `content` is the string `"hi"`, not an array |
| OAI66 | error object after output has started | a chunk with `error` and `finish_reason` `error` after a text delta | non-retryable `*provider.Error` with the code; the delivered text is kept |
| OAI67 | `finish_reason` `error` with no error object | text, then a chunk finishing with `error` | non-retryable `*provider.Error`; the delivered text is kept |

## Fuzz and property tests

| Name | Asserts |
|---|---|
| `FuzzStream` | arbitrary bytes as an SSE body never panic or hang; the result is a response or an error |
| `FuzzToolCallAssembly` | for any interleaving of fragments for fixed calls, the assembled calls equal the originals |

## Out of scope

- Retry and backoff (C08). OpenRouter specifics (C03). The `/responses` API (a later chunk).
- Non-streaming requests.

## Acceptance

```
go run ./scripts/accept C02
```
