# Nova — Protocol

One JSONL protocol connects every front door to the session: the TUI, `--mode rpc`,
`nova serve`, tests, and extension processes. This is the single most important
interface in Nova, so it is specified exactly. The Go types live in `proto/`.

Version: **1**. Every connection starts with a `hello` exchange that names the
version. Unknown fields are ignored. A line that is not a valid frame gets a failed
`response` with `bad_request`, and an unknown frame type or command gets one with
`unsupported`; neither is fatal.

## Framing

- UTF-8, one JSON object per line, terminated by `\n`. A reader also accepts `\r\n`, skips empty
  lines, and takes a final line that has no terminator.
- Split records on `\n` only. Do not use a reader that also splits on U+2028 or
  U+2029, which are legal inside JSON strings.
- A line may be up to 16 MiB. A longer line is a protocol error that closes the
  connection (a line of exactly 16 MiB, newline excluded, is accepted).
- Stdout carries only protocol frames. Logs go to stderr.
- Writers honour backpressure; readers read continuously.

## Frame families

Every frame has a `type`. Frames that expect an answer carry a client-chosen string
`id`. The answer repeats it.

| Direction (client → server) | Frame | Purpose |
|---|---|---|
| → | **command** | do something (`prompt`, `abort`, `set_model`, ...) |
| ← | **response** | the outcome of one command, `ok` or `error` |
| ← | **event** | something happened; no `id` |
| ← | **ui_request** | the server needs the user (confirm, select, ...) |
| → | **ui_response** | the user's answer to a `ui_request` |

Command handling is asynchronous. A successful `prompt` response means the prompt
was **accepted**, not that the run finished. Watch events for that.

Over stdio (`--mode rpc`), commands run one at a time in the order they arrive, and
events are written independently of them, so a slow command (a `reload` waiting for
the run to end, a long `bash`) never stalls the event stream. `abort` is the one
exception: it is handled as soon as it is read, so it can end the run that a queued
command is waiting for. On end of input the queued commands finish and answer, then
any run is aborted. A client that stops reading is dropped once it falls 1024 events
behind.

### Hello

```json
{"type":"hello","version":1,"client":"nova-tui","capabilities":["ui.confirm","ui.select","ui.form","ui.widget"]}
{"type":"hello","version":1,"server":"nova 0.1.0","session":"s_01H…","modes":["tui"],"capabilities":["steer","fork","compact"]}
```

The client's `capabilities` list tells the server which UI requests it can answer.
The server never sends a `ui_request` the client did not declare. In print mode the
list is empty and permission `ask` resolves to the headless default.

## Commands

All commands may carry `id`. Responses are `{"type":"response","id":…,"command":…,"ok":true,"data":…}`
or `{…,"ok":false,"error":{"code":…,"message":…}}`.

| Command | Fields | Notes |
|---|---|---|
| `prompt` | `text`, `attachments?`, `queue?` | starts a run. If one is running: error `busy`, unless `queue` is set. The response is sent once the run exists, so an `abort` sent right after it always cancels that run |
| `steer` | `text`, `attachments?` | queue a steering message for the current run |
| `follow_up` | `text`, `attachments?` | queue a message to run after the current work |
| `abort` | | cancel the current run; queued messages come back in the response |
| `get_state` | | model, session id, run state, context usage, queue sizes |
| `set_model` | `model` | |
| `set_thinking` | `level` | `off`, `low`, `medium`, `high` |
| `set_context_limit` | `tokens` | `0` clears it |
| `set_tools` | `active: []string` | replace the active built-in tools. An unknown or repeated name is `bad_request` and changes nothing |
| `compact` | `instructions?` | run compaction now |
| `session.new` | | start a fresh session file; answers `{id, path}`. Refused with `busy` during a run; forgets the previous conversation's token counts |
| `session.resume` | `session` | open an existing session by id or path; answers `{id, path}`. `busy` during a run; forgets the token counts |
| `session.fork` | `entry?` | copy the branch ending at `entry` (default: the active leaf) into a new session. `busy` during a run; forgets the token counts |
| `session.tree` | | entries as a tree |
| `session.navigate` | `entry` | move the active leaf (`busy` during a run) |
| `session.name` | `name` | |
| `reload` | | restart extensions; re-read config, skills, prompts |
| `get_messages` | `entry?` | the messages of the branch ending at `entry` (default: the active leaf); without a session store, the in-memory transcript |
| `get_commands` | | slash commands, including extension and prompt-template ones |
| `list_models` | `text?` | the model catalog: id, name, provider, context window, prices; `text` keeps the models whose id or name contains it |
| `get_usage` | | the OpenCode Go usage meters: `{meters, rows, footer}`, or the same with an `error` message when the host is not signed in |
| `session.list` | | sessions for the working directory: id, name, modified, entry count. A directory that does not exist yet is an empty list; one that cannot be read is `internal` |
| `get_extensions` | | each extension's name, version, state, restarts, last error |
| `run_command` | `name`, `args` | execute a slash command; `quit` and `exit` are aliases for `shutdown`, and `new` and `clear` start a fresh session (the `session.new` command) |
| `bash` | `command` | run a user shell command and record it in the session |
| `shutdown` | | graceful close |
| `list_providers` | | every provider with `id`, `name`, `methods`, `ready`, and `source` (`config`, `env`, `store`, or empty), `current` (it serves the current model), and `default_model` (what to switch to after signing in). `ready` counts a key from the loaded config |
| `login` | `name`, `args?`, `text?` | sign the server host in. `args` is the method (`api_key`, `subscription`, `device`) and may be omitted when the provider has one. An `api_key` login takes the key in `text`, saves it, and answers `{provider, saved}`. An interactive one answers `{provider, method, url, code, prompt}` and finishes with one `login_end` event. A second login for the same provider is `busy` |
| `login_submit` | `name`, `text` | give an open sign-in a pasted redirect URL (the loopback callback cannot reach a remote server). `not_found` when none is open; `bad_request` leaves it open for a retry |
| `login_cancel` | `name` | abandon an open sign-in. No `login_end` follows |
| `logout` | `name` | delete the stored credential; answers `{provider, remaining}`, the source that still signs the host in |

The server never opens a browser for `login`: its host is usually headless, so the client opens `url`
where the user is. The credential is never echoed in a response or an event.

### Attachments

An `attachments` entry is `{id?, kind?, name?, mime_type?, data?, text?, path?}`. `kind` is `image` or `text`;
empty with an `image/*` mime type means `image`. A `text` attachment's `text` is appended to the prompt. An
`image` carries either `data` (base64 bytes) or the `id` of an upload stored through `POST /v1/attachments`,
never both. `path` is reserved for a local transport and is refused today: a server must not read a path a
remote client names. Images are normalized before they reach the model (see [`IMAGE.md`](IMAGE.md)).

## Events

Events carry `seq` (a per-connection counter) and `ts`, including the ones the server
raises itself (`state_changed`, `session_changed`, `compaction`, `error`). Order within a run is exact.

```
run_start        { run }
turn_start       { run, turn }
message_start    { run, turn, role }
text_delta       { run, turn, text }
thinking_start   { run, turn }
thinking_delta   { run, turn, text }
thinking_end     { run, turn, ms, tokens }          // tokens may be null
tool_call        { run, turn, call: {id, name, arguments} }
tool_start       { run, call_id }
tool_update      { run, call_id, text }             // streamed progress
tool_end         { run, call_id, result: {content, is_error, truncated, details?, parts?} }
permission       { run, call_id, decision, reason }
message_end      { run, turn, message, usage }
turn_end         { run, turn, usage }
usage            { run, usage, total }              // running totals
retry            { run, attempt, max, delay_ms, error: {code, message} }   // discard the in-progress message of the current turn
compaction       { entry, tokens_before, tokens_after }   // entry is the new compaction entry; the wire type also has a reserved `replaced` list that the server does not fill
queue_update     { run, steering, follow_up }       // sizes now; sent on queueing and when the loop takes a message off
run_end          { run, reason, turn, usage, dropped_queue? }   // turn is the number of turns the run took; reason: done | exhausted | aborted | error; exhausted only when a bound was set; dropped_queue {steering?: [text], follow_up?: [text]} holds queued messages the run ended without consuming (absent when none), which a client should put back in the input box
error            { run?, error: {code, message} }
extension_error  { extension?, hook?, error: {code, message} }
ui_expired       { id }                              // a ui_request timed out; close its dialog
session_changed  { session, leaf }
state_changed    { state, reason? }                 // state: model, thinking level, context limit; reason "resync" on an HTTP stream whose Last-Event-ID can no longer be replayed
login_end        { id, error?: {code, message} }    // an interactive sign-in finished; id is the provider id
```

`usage` is `{prompt_tokens, completion_tokens, reasoning_tokens, total_tokens, cached_tokens, cost_usd,
cost_source}`; zero `reasoning_tokens`, `cached_tokens`, `cost_usd`, and an empty `cost_source` are omitted.
`cost_usd` is the provider's number when it reports one, otherwise computed from the
model catalog; `cost_source` (`provider` or `catalog`) says which.

A run always ends with exactly one `run_end`, even on error or abort.

## UI requests

When the server needs the user it sends a `ui_request` and waits for the matching
`ui_response`. If the request has `timeout_ms` and nothing arrives, the server
resolves it with the stated `default`. A client that cannot answer a method says so in
`hello`, and the server will not send it.

| Method | Fields | Answer |
|---|---|---|
| `confirm` | `title`, `message`, `default?`, `timeout_ms?`, `remember?: bool`, `tool?`, `args?` | `{ok: bool, remember?: "session"}` |
| `select` | `title`, `options: [{id,label,description?,preview?}]`, `multi?` | `{ids: []}` |
| `input` | `title`, `placeholder?`, `text?` (the initial value) | `{text}` |
| `form` | `title`, `questions: [...]`, `allow_skip` | `{answers: [...]}` (the `ask` primitive) |

A client may also answer any request with `{cancelled: true}`, which the asker treats as the user dismissing it.
A method name may carry a `ui.` prefix (`ui.confirm`, the spelling the server's own permission confirm uses);
clients accept both spellings, and the capability names in `hello` always carry it.

A permission `confirm` sets `remember: true` to offer "allow for this session", and `tool` and `args` so the
dialog can show which tool asks and a summary of its arguments. If the answer has
`remember: "session"` and `ok: true`, the server adds a session-scoped allow rule for that tool
name (never broader, and never for a `deny` decision).

`form` questions are `{id, prompt, kind: single|multi|text, options?, notes?: bool, allow_other?: bool}`.
`allow_other` defaults to true. Every question may be skipped. Notes attach to an answer and are returned
verbatim. An answer is `{id, value?, values?, notes?, skipped?}`: `value` for `single` and `text`, `values`
for `multi`.

### Fire-and-forget UI

These need no answer. A client may ignore any it cannot show.

| Method | Fields |
|---|---|
| `notify` | `text`, `level: info|warn|error` |
| `status` | `key`, `segments: [{text, style?}]` or `null` to clear |
| `widget` | `key`, `placement: above_editor|below_editor|sidebar`, `content` or `null` |
| `title` | `text` |
| `set_editor_text` | `text` |

`widget.content` is one of a closed set, so a client can render it natively:

```json
{"kind":"text","lines":["…"]}
{"kind":"list","items":[{"text":"…","state":"todo|doing|done","detail":"…"}]}
{"kind":"table","columns":["…"],"rows":[["…"]]}
{"kind":"progress","label":"…","value":0.4}
```

This is the same set the TUI gives third-party extensions (see
[`EXTENSIONS.md`](EXTENSIONS.md)). Bundled extensions can do more because they run in
process.

## Extension connections

An extension process is a client of the host with extra rights. It speaks the same
framing, with the roles of some frames reversed.

```
host → ext   {"type":"init","version":1,"extension":"tasks","session":"…","cwd":"…","mode":"tui","capabilities":[…]}
ext  → host  {"type":"register","tools":[…],"commands":[…],"hooks":["tool_call","turn_end"],"flags":[…],"providers":[…]}
host → ext   {"type":"hook","id":"h1","hook":"tool_call","event":{…},"deadline_ms":2000}
ext  → host  {"type":"hook_result","id":"h1","result":{"block":true,"reason":"…"}}
host → ext   {"type":"tool_call","id":"t1","name":"todo","arguments":{…},"deadline_ms":120000}
ext  → host  {"type":"tool_result","id":"t1","content":"…","is_error":false,"details":{…},"parts":[{"type":"image","mime_type":"image/png","data":"…"}]}
host → ext   {"type":"command","id":"c1","name":"todos","args":"…"}
ext  → host  {"type":"ui_request",…}   // same UI methods as above
ext  → host  {"type":"append_entry","kind":"todo.state","data":{…},"keep":true}
ext  → host  {"type":"complete","id":"m1","request":{…}}   // nested model call
host → ext   {"type":"complete_result","id":"m1","message":{…},"usage":{…}}
ext  → host  {"type":"log","level":"info","text":"…"}
host → ext   {"type":"shutdown"}
```

Rules:

- The host enforces a **deadline** on every `hook` and `tool_call`. A timeout is an
  error result for that call and an `extension_error` event, never a hang.
- An extension may only use capabilities it declared in its manifest and the user
  approved at install. Anything else is rejected with `permission_denied`.
- A refused request is answered with an `error` frame that carries the request's
  `id` (`{"type":"error","id":"m1","error":{"code":"permission_denied",…}}`), so the
  call waiting for it fails at once. Frames that are not requests get an `error`
  frame without an id.
- A `command` is answered with `{"type":"command_result","id":"c1","content":"…","is_error":false}`;
  `is_error: true` makes the command fail with `content` as the message.
- `register` is checked: a tool name matches `[A-Za-z0-9_-]{1,64}`, `parameters`
  must be an object schema, and an extension may register at most 128 tools and
  128 commands. A tool that fails a check is refused with `bad_request` and the
  extension's other registrations proceed.
- A `ui_request` that needs an answer and a `complete` are served on their own, so
  a slow answer does not delay the extension's other frames. At most eight such
  requests per extension may be in flight; more are refused with `busy`.
- `append_entry` data is stored in the session and replayed to the extension on
  `init` after a reload, in order, so it can rebuild its state.
- If the extension process exits, the host unregisters its tools, emits
  `extension_error`, and restarts it according to its policy (default: up to three
  times, then disabled until `/reload`).
- Extension stdout is protocol only. Its stderr is captured to the Nova log.

## Errors

Error codes are stable strings: `bad_request`, `busy`, `not_found`,
`permission_denied`, `unsupported`, `timeout`, `provider_error`, `internal`.

## HTTP transport

`nova serve` maps the protocol onto HTTP without changing the frames:

- `POST /v1/commands` takes one command frame and returns its response frame.
- `GET /v1/events` is a server-sent-event stream; each event frame is one `data:` line.
- `POST /v1/ui_response` answers a `ui_request`.
- `GET /v1/state` is the same as `get_state`.
- `POST /v1/attachments` stores one image's raw bytes (the `Content-Type` is the
  mime type, an optional `?name=` is the display name) and returns
  `{id, name, mime_type, size_bytes}`. A later command references the id in
  `attachments`. The store lives until the server closes.

Authentication is a bearer token set with `--token` or `NOVA_SERVE_TOKEN`. The
server refuses to listen on a non-loopback address without one. Multi-user session
management is out of scope for the first version: one server is one trusted agent.
