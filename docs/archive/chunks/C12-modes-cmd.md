---
chunk: C12
title: "The server core, modes, and the nova command"
packages: [./mode, ./cmd/nova]
budgets: { ./mode: 2320, ./cmd/nova: 1260 }
depends: [C09, C10, C11]
---

# C12 — Modes and `cmd/nova`

## Goal

Wire everything built so far into one working program. A `Server` owns a session, an
agent, the tools, the policy, and compaction, and handles protocol commands. Print, JSON,
and RPC modes are thin transports over it, and `cmd/nova` parses flags and picks a mode.
When this chunk is done, `nova -p "…"` works end to end and any client can drive Nova over
stdio. No TUI and no extension host yet.

## Read first

- `docs/PROTOCOL.md` (all of it: every command and event is implemented here).
- `docs/ARCHITECTURE.md` section 10 (modes) and section 1 (the diagram).
- Public APIs of C03 (`auth`, `openrouter`), C04, C05–C07, C08, C09, C10, C11.
- `cmd/nova-run/main.go` (the prototype CLI). It is **deleted** by this chunk.
- `docs/TESTING.md` sections 1 and 2 (L2 scenario tests) and 3, "Protocol and modes".

## Files

| File | Purpose |
|---|---|
| `mode/server.go` | `Server`: state, command dispatch, event fan-out |
| `mode/commands.go` | one handler per protocol command |
| `mode/builtin.go` | built-in slash commands for `run_command` |
| `mode/wire.go` | build the agent, tools, provider, policy, session from `Config` |
| `mode/print.go` | print mode |
| `mode/json.go` | JSON mode |
| `mode/rpc.go` | stdio RPC loop |
| `mode/pipe.go` | in-memory client connection (used by the TUI later) |
| `mode/*_test.go` | including the L2 scenario runner tests |
| `cmd/nova/main.go` | flag parsing, subcommands, mode selection |
| `cmd/nova/main_test.go` | flag parsing and exit codes (the import-boundary check lives in `scripts/importcheck`, not here) |
| `cmd/nova-run/` | deleted |

## Interfaces

```go
// package mode
type Options struct {
    Config     *config.Config
    CWD        string
    Home       string
    Provider   provider.Provider   // nil means build OpenRouter from the config and auth
    Session    string              // path to open or create; "" with NoSession false means a new default path
    NoSession  bool                // keep the transcript in memory only
    Out        io.Writer           // for rpc/json/print
    In         io.Reader
    Clock      Clock               // injectable; default real
    Interactive bool               // whether a UI client is attached (affects permission "ask")
    Version    string
}
func New(opt Options) (*Server, error)

func (s *Server) Handle(ctx context.Context, cmd proto.Command) proto.Response
func (s *Server) Subscribe(buf int) (events <-chan proto.Event, cancel func())
func (s *Server) AnswerUI(resp proto.UIResponse)       // routes a ui_response to the waiting request
func (s *Server) SetUIHandler(h func(context.Context, proto.UIRequest) (proto.UIResponse, error))
func (s *Server) Close() error

func RunPrint(ctx context.Context, s *Server, prompt string, out, errOut io.Writer) (exitCode int)
func RunJSON(ctx context.Context, s *Server, prompt string, out io.Writer) (exitCode int)
func ServeRPC(ctx context.Context, s *Server, in io.Reader, out io.Writer) error
func NewPipe(s *Server, capabilities ...string) *Client // an in-process client; capabilities are sent in its hello
```

Exit codes: `0` run ended `done`; `1` error (provider failure, bad configuration);
`2` usage error; `3` run ended `exhausted`; `130` cancelled (SIGINT).

`cmd/nova` flags: `-p <prompt>` (or `-p -` to read it from stdin), `--mode print|json|rpc|tui`,
`--model`, `--thinking`, `--session <path>`, `--no-session`, `--continue`, `--resume`,
`--tools a,b`, `--no-tools`, `--cwd`, `--max-turns`, `--context-limit`, `--yes` (headless
permission default becomes allow for the run; prints a warning), `--version`, `--help`.
Subcommands: `login`, `logout`, `models`, `sessions`, `docs [topic]`, `version`, `completion`.
The command line is built with **cobra** (D45): one root command with the flags above, one cobra command per
subcommand, `SilenceUsage` and `SilenceErrors` set so `main` controls output, and every usage error mapped to
exit code 2. `cmd/nova` imports only cobra for this; the styled help in the full build is added in C19. (`ext` is added
in C13, `serve` and `action` in C14.)

## Behaviour

### Server

1. `New` builds, from `Options`: the provider (OpenRouter with the key from `auth.Resolve`, unless
   `Options.Provider` is set), the tool registry (`read write edit list glob grep bash`, filtered by
   `Config.Tools.Active` when set), the permission policy, the session store, the compaction
   settings, and the agent. A missing API key is **not** an error from `New`; the first `prompt`
   returns an error response with code `provider_error` and the message
   `no API key: run "nova login" or set OPENROUTER_API_KEY`.
2. The system prompt is `config.BuildSystemPrompt` over the base prompt in `mode/prompt.md`
   (embedded; short and generic), context files, skills, tools, and `Config.SystemPrompt`.
3. `Handle` implements every command in `PROTOCOL.md`. Unknown commands return
   `{ok:false, error:{code:"unsupported"}}`; malformed arguments return `bad_request` naming the
   field. Handlers never panic the server: a panic becomes `internal` and an `error` event.
4. **Run control.** `prompt` returns `ok` as soon as the run is accepted and then runs in its own
   goroutine; the response is sent before `run_start`. While a run is active, `prompt` returns
   `busy` unless `queue` is true, in which case it is a follow-up. `steer` and `follow_up` map to the
   agent's methods. When idle, `steer` returns `bad_request` (message `not running`) and `follow_up`
   is treated as a `prompt`. `abort` calls the agent's `Abort` and returns the queued messages in `data`.
5. **Events.** Every agent event, plus server events (`session_changed`, `state_changed`,
   `compaction`, `queue_update`), goes to every subscriber in order with a per-connection `seq`.
   A subscriber that does not drain its channel (buffer full) is **dropped** with a final `error`
   event; it never blocks the agent or other subscribers.
6. **Persistence.** The agent's `OnMessage` appends a `message` entry (with usage for assistant
   messages) to the session store synchronously. A store error ends the run with `error`. With
   `NoSession`, an in-memory store is used and nothing is written.
7. **Compaction.** `BeforeTurn` calls `compaction.ShouldCompact` and, if true, `compaction.Run`; the
   result replaces the working transcript with `store.Messages` and emits a `compaction` event.
   A failure emits `error` (code `internal`, `compaction failed: …`) and the run continues
   uncompacted. `compact` runs it on demand and returns `ErrNothingToCompact` as `bad_request`.
8. **State.** `get_state` returns the model, thinking level, context limit, session id and path,
   leaf, whether a run is active, queue sizes, the estimated context tokens, the effective
   limit, the usage totals, and the cwd. `set_model`, `set_thinking`, `set_context_limit`, and
   `set_tools` update the state, append a `model_change` entry where relevant, and emit
   `state_changed`. `set_model` with an unknown id is `bad_request`; an id not in the catalog but
   well-formed is accepted (the provider will say if it is wrong).
9. **Sessions.** `session.new`, `session.resume {id|path}`, `session.fork {entry}`, `session.tree`,
   `session.navigate {entry}`, `session.name` are implemented over `session`. They are refused with
   `busy` while a run is active. `--continue` resumes the most recent session for the cwd;
   `--resume` is the same command for the TUI picker (in non-interactive modes it is an error).
10. `reload` re-reads config, skills, and templates and rebuilds the system prompt (extensions are
    added in C13). It waits for an active run to finish; the response says what was reloaded.
11. `bash` runs a user shell command with the `bash` tool code, records it in the session as a
    `custom_message` (display, not in context) unless the command was `!!`, and returns the output.
12. `get_commands` lists built-ins and prompt templates. `run_command` runs a built-in
    (`model`, `thinking`, `context`, `compact`, `new`, `clear`, `name`, `cost`, `help`) or expands a prompt
    template into a prompt. `new` and `clear` are aliases for `session.new`, so a slash command clears the
    conversation. Built-ins return their text in `data`.
13. **Permissions and UI.** With a policy and a UI handler set, `ask` calls the handler as a
    `confirm` request; without one, `ask` resolves per `Headless`. `AnswerUI` routes responses by
    id; a response for an unknown or expired id is ignored with a log line. A `ui_request` with
    `timeout_ms` resolves to its default on expiry and emits a `ui_expired {id}` event so a UI can close
    the dialog.
14. `shutdown` aborts any run, flushes, closes the store, and returns `ok` before the connection ends.
    `Close` is idempotent.

### Print and JSON modes

15. `RunPrint` sends one `prompt`, waits for `run_end`, writes the **final assistant text** to
    `out` followed by a newline, and returns the exit code. Progress (tool names, errors, and with
    `--verbose` thinking) goes to `errOut` only. Nothing but the answer is ever written to `out`.
16. `RunJSON` writes **every event** as one JSONL line to `out` until `run_end`, nothing else, then
    returns the exit code. No `hello` frame is written in JSON mode.
17. In both, SIGINT (via the passed `ctx`) aborts the run, waits for `run_end`, and returns 130.
    Permission `ask` resolves to `Headless` (default deny). A tool error does not change the exit
    code; only the run's final reason does.
18. `-p -` reads the prompt from stdin until EOF. If stdin is not a terminal and `-p` has an
    argument, the stdin text is appended to the prompt inside a fenced block. An empty prompt is a
    usage error (exit 2).

### RPC mode

19. `ServeRPC` writes a `hello` frame first (`server: "nova <version>"`, session id, capabilities),
    expects the client's `hello`, then reads commands with `proto.Reader` and writes responses and
    events with `proto.Writer`. If the client's `hello` is missing the loop still starts (some
    clients skip it) and treats the client as having no UI capabilities.
20. A malformed line yields an `error` response with `bad_request` and the loop continues. A line
    over the size limit yields one `error` and closes the connection. An unknown frame `type` is a
    command and gets `unsupported`.
21. Commands are handled concurrently with an active run (so `abort` and `steer` work) but
    **responses to commands that mutate state are ordered**: handlers for such commands run one at a
    time in arrival order.
22. When the input reaches EOF the server aborts any run, closes the session, and returns nil. A
    client that disconnects mid-run cancels the run (no orphaned work).
23. Stdout carries only frames. Logs and panics' stack traces go to stderr.

### `cmd/nova`

24. `nova` with no flags starts the TUI when stdin and stdout are terminals (C15; until then it
    prints usage and exits 2). `nova -p …` runs print mode. `--mode rpc` and `--mode json` as above.
25. `nova login` runs `auth.Login` (printing the URL, opening a browser when `--no-browser` is not
    given) and stores the key; `nova logout` deletes it; `nova models` lists the catalog;
    `nova sessions` lists sessions for the cwd; `nova docs [topic]` prints embedded documentation
    (`docs/*.md` embedded with `//go:embed`); `nova version` prints the version.
26. A usage error (unknown flag, conflicting modes) prints a one-line message and the usage to
    stderr and exits 2. `--help` prints usage to stdout and exits 0.
27. `--cwd` changes the working directory for the run; a nonexistent directory is a usage error.
28. `NO_COLOR` and non-terminal stderr disable any colour in messages written by this package.

## Matrix

L2 rows build the real `nova` binary (once, in `TestMain`) and drive it against
`testkit.Server`. Provider URL and key are injected by flags or environment set by the test
(`NOVA_OPENROUTER_BASE_URL`, `OPENROUTER_API_KEY`).

| ID | Case | Input / setup | Expected |
|---|---|---|---|
| SRV01 | `New` without a key | | no error; first prompt returns `provider_error` with the login hint |
| SRV02 | `prompt` accepted then runs | scripted text turn | response `ok` before `run_start`; events then `run_end(done)` |
| SRV03 | `prompt` while busy | | `busy` |
| SRV04 | `prompt` with `queue` while busy | | becomes a follow-up |
| SRV05 | `steer` during a run | slow tool | injected after the turn |
| SRV06 | `steer` and `follow_up` when idle | | `follow_up` runs as a prompt; `steer` is `bad_request` |
| SRV07 | `abort` | `Hang` turn | `run_end(aborted)`; queued messages in the response |
| SRV08 | `abort` when idle | | ok, empty |
| SRV09 | unknown command | | `unsupported` |
| SRV10 | malformed arguments | `set_model` without `model` | `bad_request` naming the field |
| SRV11 | handler panic | injected panic | `internal`; server still serves |
| SRV12 | events reach every subscriber in order | two subscribers | identical sequences; `seq` increasing |
| SRV13 | slow subscriber is dropped | never drains | gets an `error`; others unaffected; agent not slowed |
| SRV14 | messages persisted | one run | session file has the user, assistant, tool entries in order |
| SRV15 | persistence error | read-only file | run ends `error` |
| SRV16 | `NoSession` | | no file created |
| SRV17 | compaction triggers | small `context_limit`, long history | `compaction` event; context replaced; run continues |
| SRV18 | compaction failure | summariser errors | `error` event; run continues |
| SRV19 | `compact` command, nothing to compact | | `bad_request` |
| SRV20 | `get_state` fields | | all listed fields present |
| SRV21 | `set_model`, `set_thinking`, `set_context_limit` | | `state_changed`; `model_change` entry for the model |
| SRV22 | `set_model` unknown shape | `""` | `bad_request` |
| SRV23 | `set_tools` | | the next request offers only those tools |
| SRV24 | session new, resume, fork, tree, navigate, name | | each works and emits `session_changed` |
| SRV25 | session commands while running | | `busy` |
| SRV26 | navigate then prompt makes a branch | | new entry's parent is the navigated entry |
| SRV27 | `reload` | edit a skill file | new system prompt on the next request |
| SRV28 | `reload` during a run | | waits for `run_end` |
| SRV29 | `bash` command recorded | | `custom_message` entry, not in the next context |
| SRV30 | `!!` command not recorded in context | | not included in the next request |
| SRV31 | `get_commands` | a template exists | built-ins and the template listed |
| SRV32 | `run_command` built-in and template | | text returned; template becomes a prompt |
| SRV33 | permission ask with a UI handler | handler says yes / no | tool runs / is blocked |
| SRV34 | permission ask with no handler | | `Headless` applies |
| SRV35 | `ui_request` timeout | | resolves to the default |
| SRV36 | `ui_response` for an unknown id | | ignored |
| SRV43 | `ui_request` expiry | `timeout_ms` elapses | resolves to the default; `ui_expired` event with the id |
| SRV44 | pipe capabilities | `NewPipe(s, "ui.confirm")` | the server sends only methods the client declared |
| SRV37 | `shutdown` mid-run | | run aborted; store closed; response ok |
| SRV38 | `Close` twice | | nil |
| SRV39 | provider error surfaces | server returns 401 | `provider_error`; `run_end(error)` |
| SRV40 | retry visible | server returns 500 then ok | `retry` event; run `done` |
| SRV41 | usage events | | totals match the scripted usage |
| SRV42 | thinking events | reasoning in the script | `thinking_*` with a duration |
| SRV45 | `run_command quit` and `exit` | | aliases for `shutdown`; `get_commands` lists them |
| SRV46 | `run_command new` and `clear` | a session with one turn | each opens a fresh session (path changes) and emits `session_changed`; the old messages are gone; `get_commands` lists both; refused with `busy` while a run is active |
| SRV47 | `set_model` moves the compaction window | switch from an unknown model to one with a catalog window | `get_state` `effective_limit` becomes that window |
| SRV48 | server-raised events carry `ts` | `set_thinking`, watch `state_changed` | the event has a non-zero `ts` |
| SRV49 | `get_messages` without a session store | `NoSession`, one run | the in-memory user and assistant messages come back |
| SRV50 | a session switch forgets the last response's usage | a run reporting a near-full prompt, then `session.new` or `session.navigate`, then a prompt | no `error` event (no spurious compaction) |
| SRV51 | concurrent session commands | `session.new` beside `get_messages`, `get_state`, `session.tree`, `set_model` | race-free under `-race` |
| SRV52 | `set_tools` with an unknown or repeated name | `["bash","nope"]`, `["read","read"]` | `bad_request`; the registry is unchanged |
| SRV53 | `session.list` when the directory cannot be read | a file where the directory tree should be | `internal`; a missing directory is still an empty list |
| SRV54 | fire-and-forget UI requests keep their order | several `Notify` calls to a pipe client | every request arrives, in order |
| SRV55 | a client that never reads | more `Notify` calls than the queue holds | `Notify` never blocks |
| PRI01 | print mode answer | scripted "42" | stdout is `42\n`; exit 0 |
| PRI02 | stdout has only the answer | tool calls in the run | tool progress only on stderr |
| PRI03 | `-p -` reads stdin | | prompt from stdin |
| PRI04 | stdin appended in a fenced block | `-p x` and piped input | both in the prompt |
| PRI05 | empty prompt | | exit 2 |
| PRI06 | exit code on provider failure | 401 | exit 1; message on stderr |
| PRI07 | exit code on exhaustion | `--max-turns 1`, model keeps calling tools | exit 3 |
| PRI08 | SIGINT | send SIGINT mid-run | exit 130; run ended `aborted` |
| PRI09 | tool error does not change the exit code | a failing tool, then a final answer | exit 0 |
| PRI10 | permission ask in print mode | destructive tool | denied (headless) unless `--yes` |
| PRI11 | `--yes` | | allowed; warning on stderr |
| JSN01 | every event as JSONL | | each line decodes to an event; first is `run_start`, last `run_end` |
| JSN02 | no other output | | stdout has only event lines |
| JSN03 | exit codes match print mode | | same |
| JSN04 | no `hello` frame | | absent |
| RPC01 | hello first | | first frame is the server hello |
| RPC02 | client skips hello | | still works |
| RPC03 | full prompt round trip | | response then events |
| RPC04 | malformed line | `not json` | `bad_request`; loop continues |
| RPC05 | line over the limit | 17 MiB | one `error`; connection closed |
| RPC06 | unknown type | | `unsupported` |
| RPC07 | `abort` during a run | | works while the run is active |
| RPC08 | state-mutating commands are ordered | two `set_model` back to back | applied in order |
| RPC09 | EOF on stdin | | run aborted; process exits 0 |
| RPC10 | client disconnect mid-run | close stdin | run cancelled; no leaked goroutine or child process |
| RPC11 | stdout contains only frames | `-race`; panic in a handler | stack trace on stderr only |
| RPC12 | correlation ids | two concurrent commands with ids | each response carries its id |
| RPC13 | backpressure | client reads slowly | no deadlock; server not killed |
| RPC14 | large event | 1 MiB tool result | delivered intact |
| RPC15 | a slow command does not stall events | `reload` queued while a run emits more events than the subscriber buffer holds | every event is delivered, no `subscriber too slow` error, the reload then answers `ok` |
| RPC16 | `abort` overtakes a blocked command | `reload` waiting on a hung run, then `abort` | the abort answers at once, the run ends `aborted`, the reload then answers `ok` |
| RPC17 | no goroutine outlives `ServeRPC` | cancelled context with unread input | `testkit.NoLeaks` passes |
| RPC18 | server hello capabilities | | `steer`, `fork`, `compact` |
| RPC19 | no frame after return | a UI request after `ServeRPC` returned | refused; nothing written |
| CLI01 | `--help` | | usage on stdout; exit 0 |
| CLI02 | unknown flag | | message and usage on stderr; exit 2 |
| CLI03 | conflicting modes | `-p x --mode rpc` | exit 2 |
| CLI04 | `--version` | | version string; exit 0 |
| CLI05 | `--cwd` missing | | exit 2 |
| CLI06 | `nova login` | fake OAuth server | key stored (0600); success message |
| CLI07 | `nova logout` | | key removed; second call is fine |
| CLI08 | `nova models` | | lists the catalog |
| CLI09 | `nova sessions` | two sessions | listed, newest first |
| CLI10 | `nova docs` and `nova docs extensions` | | embedded text printed; unknown topic lists topics |
| CLI11 | no mode and no terminal | | usage; exit 2 |
| CLI12 | `NO_COLOR` | | no ANSI in any message |
| CLI13 | `--tools` and `--no-tools` | | the registry matches |
| CLI14 | `--continue` | existing session | resumes it |
| CLI15 | `--session` path | | created or opened there |
| CLI16 | `nova completion bash`, `zsh`, `fish` | | a valid completion script on stdout; exit 0 |
| CLI17 | cobra errors map to exit 2 | unknown flag, missing argument, unknown subcommand | message on stderr; exit 2; no stack |
| CLI18 | flags after subcommands and `--` | `nova docs -- --help` | handled as cobra documents |
| FUL01 | the whole stack | scripted: read a file, edit it, answer | file changed; events as expected; session file valid |
| FUL02 | tool permission denied by config | policy denies `write` | tool blocked; file unchanged; model sees the error |
| FUL03 | compaction in a real run | small limit | compacted; the next request contains the summary |
| FUL04 | resume after restart | run, exit, `--continue`, run | second request contains the first run's messages |
| FUL05 | untrusted project | `.nova/config.yaml` present, not trusted | ignored; warning on stderr |

## Fuzz and property tests

| Name | Asserts |
|---|---|
| `FuzzRPCLoop` | arbitrary bytes on stdin never panic or hang; stdout always contains only valid frames |
| `TestProperty_SRV_EventOrder` | for random command sequences, `seq` is strictly increasing and every run has exactly one `run_start` and `run_end` |

## Out of scope

- The extension host, `nova ext`, and extension commands (C13). `serve` and `action` (C14).
- The TUI (C15 onward). A first-run trust prompt UI: in headless modes an untrusted project is
  simply untrusted; the TUI asks.

## Acceptance

```
go run ./scripts/accept C12
```
