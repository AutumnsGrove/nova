---
chunk: C13
title: "The extension host"
packages: [./ext, ./ext/sdk]
budgets: { ./ext: 1700, ./ext/sdk: 400 }
depends: [C12]
---

# C13 — The extension host

## Goal

Run third-party extensions as subprocesses speaking the Nova protocol: load manifests,
supervise processes, register their tools, commands, and hooks with the server, forward
their UI requests and session entries, and install, update, check, and reload them from git.
Also ship the small Go SDK for writing one. This chunk touches security (it runs other
people's programs), so its tests are reviewed by the larger model before merge.

## Read first

- `docs/EXTENSIONS.md` (all of it) and `docs/PROTOCOL.md` section "Extension connections".
- `docs/DECISIONS.md` D5, D16 to D20.
- `mode/server.go` (C12): the host plugs into its tool registry, hooks, UI handler, and session.
- `agent/agent.go` (C09): `Hooks` and how multiple extensions' hooks must compose.
- `tool/tool.go` and `permission/policy.go` (C04).
- `docs/TESTING.md` section 3, "Extensions".

## Files

| File | Purpose |
|---|---|
| `ext/manifest.go` | parse and validate `extension.yaml` |
| `ext/proc.go` | spawn, supervise, restart, kill an extension process |
| `ext/conn.go` | one framed connection: request correlation, deadlines |
| `ext/host.go` | the `Host`: load, register, route, shut down |
| `ext/tools.go` | adapt extension tools and declarative tools to `tool.Tool` |
| `ext/declarative.go` | `exec` tools: argument templating without a shell |
| `ext/hooks.go` | compose hooks from many extensions; command hooks |
| `ext/install.go` | install, update, remove, list, check |
| `ext/lock.go` | the lockfile |
| `ext/git.go` | the git operations (calls the `git` binary) |
| `ext/reload.go` | the reload sequence |
| `ext/sdk/sdk.go` | the Go SDK |
| `ext/sdk/doc.go` | package documentation with a complete example |
| `ext/*_test.go`, `ext/sdk/*_test.go` | uses a test extension binary built from `ext/testdata/` |
| `mode/*.go` | small changes: add the host, `nova ext` wiring in `cmd/nova` |
| `cmd/nova/ext.go` | the `nova ext` subcommands |
| `examples/extensions/` | the first examples: `hello` (declarative), `block-rm` (command hook), `hello-go` (SDK) |

## Interfaces

```go
// package ext
type Manifest struct {
    Name         string             `yaml:"name"`
    Version      string             `yaml:"version"`
    Description  string             `yaml:"description"`
    Nova         string             `yaml:"nova"`      // version constraint, e.g. ">=0.1"
    Run          []string           `yaml:"run"`
    Build        []string           `yaml:"build"`
    Restart      struct{ Max int }  `yaml:"restart"`
    Capabilities []string           `yaml:"capabilities"`
    Tools        []DeclTool         `yaml:"tools"`
    Hooks        []HookSpec         `yaml:"hooks"`
    Flags        []FlagSpec         `yaml:"flags"`
    Dir          string             `yaml:"-"` // absolute directory, set by the loader
}
type DeclTool struct {
    Name, Description string
    Parameters        map[string]any
    Exec              []string
    ReadOnly          bool `yaml:"read_only"`
    TimeoutMS         int  `yaml:"timeout_ms"`
    Render            string
}
type HookSpec struct {
    On    string
    Match map[string]string
    Run   []string
}
func LoadManifest(dir string) (*Manifest, error)
func (m *Manifest) Validate() error

type Host struct{ /* ... */ }
type HostOptions struct {
    Dirs        []string           // extension directories (user, then project if trusted)
    Disabled    []string
    Registry    *tool.Registry
    Session     SessionAPI         // AppendEntry, Entries(kind) for replay
    UI          UIBridge
    Complete    func(ctx context.Context, req provider.Request) (*provider.Response, error)
    NovaVersion string
    CWD         string
    Mode        string             // "tui" | "print" | "json" | "rpc" | "serve"
    Env         func() []string    // the base environment for children; the host removes secrets (see 8)
    Clock       Clock
    Log         func(level, msg string)
}
type SessionAPI interface {
    AppendEntry(kind string, data json.RawMessage, keep bool) error
    Entries(kind string) []json.RawMessage
}
type UIBridge interface {
    Request(ctx context.Context, req proto.UIRequest) (proto.UIResponse, error)
    Notify(req proto.UIRequest) // fire-and-forget methods
}
func NewHost(opt HostOptions) *Host
func (h *Host) Start(ctx context.Context) []error // starts every extension; errors are per extension
func (h *Host) Reload(ctx context.Context) []error
func (h *Host) Hooks() agent.Hooks               // the composed hooks to give the agent
func (h *Host) Commands() []Command              // extension slash commands
func (h *Host) RunCommand(ctx context.Context, name, args string) (string, error)
func (h *Host) Status() []Status                 // name, state, restarts, last error
func (h *Host) Shutdown(ctx context.Context) error

// install
type Source struct{ URL, Ref string } // Ref is a tag, branch, or commit
func ParseSource(s string) (Source, error)
type Installer struct{ /* ... */ }
type InstallOptions struct {
    Root     string                                   // ~/.config/nova/extensions
    LockPath string
    Confirm  func(Plan) bool                          // the trust prompt; nil means refuse
    Git      GitRunner
    Run      CommandRunner                            // for build steps
    Timeout  time.Duration                            // per git or build step; default 5 minutes
}
type Plan struct{ Name, Source, Commit string; Run, Build, Capabilities []string; Replaces string }
func (i *Installer) Install(ctx context.Context, src Source) (*Manifest, error)
func (i *Installer) Update(ctx context.Context, name string) (*Manifest, error)
func (i *Installer) Remove(name string) error
func (i *Installer) List() ([]Installed, error)
func Check(ctx context.Context, dir string, opt CheckOptions) []error
```

SDK (`ext/sdk`): `Run(func(*Host) error)`, `Host.Tool`, `Host.Command`, `Host.On`,
`Host.UI`, `Host.AppendEntry`, `Host.Complete`, `Host.Entries`, `Object/String/Int/Bool` schema
helpers, `Text/Error` result helpers, deadlines passed as `context.Context`, panics recovered
into error results, and a read loop that exits cleanly on `shutdown` or stdin EOF.

## Behaviour

### Manifest

1. `name` matches `^[a-z0-9]+(-[a-z0-9]+)*$`, at most 64 characters. `version` is semver
   (`1.2.3`, optional `v`, optional prerelease). `nova` is a constraint (`>=0.1`, `^0.2`, `0.1.x`;
   an empty constraint means any). `description` is required, at most 500 characters.
2. `run` is required unless the manifest declares only `tools` with `exec` and/or `hooks` with
   `run`. `run[0]` must be either an absolute path, a bare command name found on `PATH`, or a path
   **relative to the extension directory** that stays inside it after cleaning (a `..` that escapes
   is rejected).
3. Capabilities are from the set `tools`, `commands`, `hooks:<hook>` (any hook name from
   ARCHITECTURE section 4), `ui:notify|confirm|select|input|form|status|widget`,
   `session:append_entry`, `session:read`, `model:complete`, `providers`, `mcp`, `network`, `fs`,
   `exec`. An unknown capability is a validation error. `network`, `fs`, and `exec` are
   declarations shown at install; they are not enforced (see EXTENSIONS.md).
4. Declarative tools: names unique within the manifest and not equal to a built-in; `parameters`
   must be an object schema; `exec[0]` is subject to the same rule as `run[0]`; `timeout_ms`
   defaults to 30,000 and is capped at 600,000.
5. Unknown manifest keys are a validation **warning**, not an error. A manifest over 256 KiB is
   rejected.

### Supervision

6. The host starts each enabled extension with `Run`, in the extension directory, with stdin and
   stdout as the protocol connection and stderr forwarded to `Log` (line by line, prefixed with the
   name). Each starts in its own process group so `Shutdown` can kill descendants.
7. Startup handshake: the host sends `init`; the extension must send `register` within
   **10 seconds**. A timeout, a crash, or invalid output fails that extension (an `extension_error`
   event; its registrations are removed) and does not affect others.
8. **Environment.** Children inherit the host's environment **minus** `OPENROUTER_API_KEY` and any
   variable named in config `extensions.scrub_env` (default also scrubs names ending `_API_KEY`,
   `_TOKEN`, `_SECRET`). They receive `NOVA_EXTENSION=<name>`, `NOVA_VERSION`, `NOVA_CWD`,
   `NOVA_MODE`, `NOVA_EXTENSION_DIR`. An extension that needs a secret reads its own, or the user
   lists it explicitly in config `extensions.pass_env`.
9. **Restart.** An extension that exits unexpectedly is restarted up to `restart.max` (default 3)
   times with a 1 second, 2 second, 4 second backoff. The counter resets after it has run
   for 60 seconds. After the limit it is `disabled` until reload; its tools are unregistered and
   one `extension_error` is emitted. A clean `shutdown` exit is not a crash.
10. `Shutdown` sends `shutdown`, waits up to 2 seconds, then `SIGTERM` to the group, then
    `SIGKILL` after 2 more seconds. No process may survive `Shutdown` (verified by test).
11. stdout lines that are not valid frames are logged as warnings and ignored, never fatal; a
    frame over 16 MiB closes that connection and counts as a crash.

### Registration and routing

12. `register` may be sent once at startup and again later (re-registration replaces the
    extension's previous registrations). It may declare tools, commands, hooks, flags, providers.
    A tool whose name is already taken by a built-in or another extension is rejected with a
    `register` error naming the conflict; the extension's other registrations proceed.
13. A registered tool becomes a `tool.Tool` whose `Run` sends `tool_call` with a deadline
    (the tool's `timeout_ms`, default 120 seconds) and waits for `tool_result`. A timeout, a
    crash, or a context cancel produces an error result (`extension <name> did not answer in <d>`,
    `extension <name> exited`, `cancelled`) and never a hang. A late answer after a timeout is
    discarded. Tool names are exposed unchanged; the tool's annotations come from `register`.
14. **Capability checks.** A frame that needs a capability the manifest does not declare (or
    the user did not approve) gets an `error` reply with `permission_denied` and is not acted on.
15. `ui_request` frames are forwarded to the `UIBridge` (answered or, for fire-and-forget methods,
    sent). A client that did not declare the method gets `unsupported` back to the extension.
16. `SessionAPI.Entries(kind)` returns the entries of that kind **on the active branch**, root to leaf.
    `append_entry` writes a `custom` entry (`Kind`, `Data`, `Keep`) to the session. On `init` (start
    and reload) the host sends the extension its prior entries of the kinds it declared, in order,
    as `replay` frames before it may register tools, so it can rebuild state.
17. `complete` calls `HostOptions.Complete` with the extension's request; usage is counted by the
    caller. A per-extension cap (config `extensions.complete_max_per_run`, default 20) is enforced;
    beyond it the call fails with a clear error.
18. `log` frames go to `Log`. `flags` register CLI flags readable by the extension through `init`.

### Hooks

19. Hooks from all extensions are composed in **load order** (user extensions before project ones,
    each directory alphabetical). Composition rules: `ToolCall` — the first handler that blocks
    wins; argument rewrites chain, each handler seeing the previous one's output; `ToolResult` —
    results chain; `TransformContext` — chain; `BeforeProviderRequest` — all run, in order;
    `MessageEnd` — chain; `TurnEnd` — `End` if any says end, `Continue` if any says continue,
    `Inject` concatenated in order; `AgentEnd` and observers — all run.
20. Each hook call to an extension has the `deadline_ms` it was sent. A timeout, an extension that
    is down, or an invalid result is skipped (that extension contributes nothing for that call)
    and reported once per hook per minute as `extension_error`. A slow extension never delays the
    loop beyond its deadline.
21. **Command hooks** (`hooks:` with `run`) execute `run` (no shell) with the event as JSON on stdin and a
    10 second default timeout. A zero exit with empty stdout is "no opinion"; stdout JSON
    `{"block":true,"reason":"…"}` blocks (for `tool_call`); `{"arguments":{…}}` rewrites arguments; a
    non-zero exit or malformed stdout is "no opinion" plus a warning. `match` limits which tool
    calls trigger it using `permission.MatchString` over string arguments and the tool name.

### Declarative tools

22. An `exec` tool runs `exec` **directly (no shell)**. Each element of `exec` is a template; the
    placeholder `{{.name}}` is replaced by the JSON argument `name` rendered as a **single argv
    element** (strings as is, numbers and booleans formatted, arrays and objects as compact JSON). A
    placeholder is never split on whitespace, never re-parsed, and never interpreted by a shell.
    A missing required argument was already rejected by schema validation; a missing optional one
    renders as the empty string and the **element is dropped** if it consists solely of that
    placeholder.
23. An argument value beginning with `-` is passed through unchanged (the tool author is
    responsible for `--`); the host does not guess.
24. Output (stdout and stderr merged) is capped like the bash tool (first 20%, last 80% of
    50 KiB). Non-zero exit is an error result with the exit code. A timeout kills the group.
    `render` is a template over `{output, exit_code, <args>}` stored in `Details.render`.

### Install

25. `ParseSource` accepts: `github.com/owner/repo[@ref]`, `owner/repo[@ref]` (GitHub shorthand),
    `https://host/path.git[@ref]`, `git@host:owner/repo.git[@ref]`, and a local directory path
    (`./x`, `/x`, `~/x`; installed as a link, not a copy). It rejects `file://`, `ext::` and any
    other transport, URLs with credentials, and any source whose repository or ref begins with `-`.
    A ref may be a tag, branch, or 7–40 hex commit.
26. `Install` clones into a **temporary directory** inside `Root`, checks out the ref, resolves the
    commit SHA, finds extensions (the repository root if it has `extension.yaml`, else every
    immediate subdirectory that does), validates every manifest, builds the plan, and asks
    `Confirm(plan)`. Only if confirmed does it run `build`, then **rename** the directory into
    `Root/<name>`, then write the lockfile. Any failure or a `false` answer removes the temporary
    directory and leaves `Root` and the lockfile untouched.
27. The `Plan` shows the name, source, commit SHA, `run`, `build`, capabilities, and, for an update,
    the previous commit and a diff of capabilities. `Confirm == nil` refuses.
28. Git operations use the `git` binary with `GIT_TERMINAL_PROMPT=0`, `GIT_ASKPASS=true`, a
    timeout, `--depth 1` where the ref allows it, and `--` before positional arguments. Hooks in the
    cloned repository are never executed (`core.hooksPath=/dev/null`).
29. A name collision with a different source is an error unless it is the same source (then it is an
    update). A name equal to a built-in tool or a bundled extension is rejected.
30. **Lockfile** `~/.config/nova/extensions.lock` (YAML): per extension, name, source, ref, resolved
    commit, installed time, manifest hash. Written atomically. A lockfile entry whose directory is
    missing, or whose directory's HEAD differs from the lock, makes `List` report `drifted`;
    `Start` still loads it but warns.
31. `Update(name)` fetches the ref again, builds a new plan, asks `Confirm` (showing capability
    changes), and replaces atomically: the old directory is renamed aside, the new one moved in, and
    the old one removed only after the lockfile is written. A failure restores the old one.
32. `Remove(name)` deletes the directory and the lock entry; removing something not installed is
    an error. A linked (local) extension removes only the link.
33. `Check(dir)` validates the manifest, runs `build` if set (with a timeout), starts the process,
    waits for `register`, verifies every registered tool has a valid schema and no name conflicts,
    runs a no-op hook round trip if hooks were registered, shuts it down, and returns every problem
    found (not just the first). It never installs anything.

### Reload

34. `Reload` waits for an active run to end (the server calls it between runs), then: shuts down all
    extensions, re-reads manifests, rebuilds those whose source changed (by manifest hash and
    directory mtime), starts them, replays entries, re-registers tools, and returns per-extension
    errors. A failure in one leaves the others running. The tool registry afterwards contains
    exactly the registrations of the running extensions (no stale tools).

### SDK

35. `sdk.Run` reads `init`, calls the user's function (which registers), sends `register`, then
    dispatches `tool_call`, `command`, and `hook` frames to handlers, each in its own goroutine with
    a `context.Context` that carries the host's deadline. A handler panic becomes an error result. A
    handler that returns an error becomes `is_error: true` with the message. `sdk.Run` returns when
    it receives `shutdown` or stdin closes.

## Matrix

A test extension (`ext/testdata/echoext`) is a tiny Go program built once in `TestMain`. It
is scripted by environment variables to misbehave: crash on start, never register, register
twice, answer slowly, answer twice, print garbage to stdout, exit after N calls, fork a child, etc.

| ID | Case | Input / setup | Expected |
|---|---|---|---|
| MAN01 | valid manifest | | parsed; `Dir` absolute |
| MAN02 | missing name, bad name, too long | | errors |
| MAN03 | bad version | `1.x` | error |
| MAN04 | version constraint forms | `>=0.1`, `^0.2`, `0.1.x`, empty | parsed; matching correct for sample versions |
| MAN05 | description missing or over 500 | | error |
| MAN06 | `run` escapes the directory | `../x`, `a/../../x` | error |
| MAN07 | `run` absolute, bare name, relative inside | | accepted |
| MAN08 | no `run`, declarative-only | tools with `exec` | accepted |
| MAN09 | no `run`, no declarative content | | error |
| MAN10 | unknown capability | | error naming it |
| MAN11 | known capabilities | each in the list | accepted |
| MAN12 | declarative tool name conflicts with a built-in | `read` | error |
| MAN13 | duplicate tool names | | error |
| MAN14 | parameters not an object schema | | error |
| MAN15 | `timeout_ms` defaults and cap | | 30,000; 600,000 |
| MAN16 | unknown manifest key | | warning |
| MAN17 | manifest over 256 KiB | | error |
| MAN18 | malformed YAML | | error with line |
| SUP01 | start, init, register | | tools appear in the registry |
| SUP02 | never registers | | failed after 10 s (fake clock); registrations removed; others unaffected |
| SUP03 | crashes on start | | error reported; others unaffected |
| SUP04 | invalid output before register | | failed; no panic |
| SUP05 | stderr forwarded | | log lines with the extension name |
| SUP06 | environment scrubbed | host has `OPENROUTER_API_KEY`, `FOO_API_KEY`, `X_TOKEN` | child does not see them |
| SUP07 | `pass_env` allows a variable | | child sees it |
| SUP08 | `NOVA_*` variables set | | present |
| SUP09 | crash mid-run is restarted | exits after 1 call | restarted; tools re-registered |
| SUP10 | restart limit | always crashes | stops after `restart.max`; `disabled`; one `extension_error`; tools unregistered |
| SUP11 | restart counter resets after 60 s healthy | fake clock | resets |
| SUP12 | backoff delays | | 1 s, 2 s, 4 s |
| SUP13 | clean shutdown | | exits; not counted as a crash |
| SUP14 | shutdown kills descendants | extension forks `sleep` | no process survives |
| SUP15 | extension ignores shutdown | | killed after the grace |
| SUP16 | garbage on stdout | | logged; ignored |
| SUP17 | frame over 16 MiB | | connection closed; counts as a crash |
| SUP18 | no goroutine leak after all of the above | | `NoLeaks` |
| HST01 | tool call round trip | | result returned |
| HST02 | tool call timeout | extension sleeps | error result at the deadline; late answer discarded |
| HST03 | extension crashes during a tool call | | error result `exited`; loop continues |
| HST04 | cancel during a tool call | | error result `cancelled` promptly |
| HST05 | name conflict with a built-in | | register error; other registrations proceed |
| HST06 | name conflict with another extension | | first wins; second's tool rejected |
| HST07 | re-register replaces old registrations | | old tools gone, new present |
| HST08 | capability not declared | `ui_request` without `ui:*` | `permission_denied` |
| HST09 | capability declared | | works |
| HST10 | UI request forwarded and answered | | answer reaches the extension |
| HST11 | UI method client cannot answer | | `unsupported` back to the extension |
| HST12 | fire-and-forget UI | `notify` | delivered |
| HST13 | `append_entry` stored as `custom` | | entry in the session with kind, data, keep |
| HST14 | replay on init | prior entries | `replay` frames in order before registration |
| HST15 | replay after reload | | same |
| HST16 | replay only the kinds the extension declared | | others not sent |
| HST17 | `complete` call | | result and usage returned |
| HST18 | `complete` cap | 21st call | error |
| HST19 | extension commands | | listed; `RunCommand` round trip |
| HST20 | command to a crashed extension | | error message |
| HST21 | duplicate response id / unknown id | | ignored; logged |
| HST22 | two tools called concurrently | | both answered; correct correlation |
| HST23 | flags passed in `init` | | present |
| HST24 | `Status` | | state, restarts, last error |
| HK01 | block: first handler wins | two extensions both block | first reason used |
| HK02 | rewrite chains | A rewrites, B sees the rewrite | B's input is A's output |
| HK03 | block after a rewrite | | block wins |
| HK04 | `ToolResult` chain | | applied in order |
| HK05 | `TurnEnd` merge | End, Continue, Inject from different extensions | merged per the rules |
| HK06 | hook deadline | slow extension | skipped; `extension_error` once per minute |
| HK07 | extension down | | skipped silently apart from the status |
| HK08 | invalid hook result | | skipped |
| HK09 | load order | user then project, alphabetical | as stated |
| HK10 | command hook blocks | script prints a block | tool blocked with the reason |
| HK11 | command hook rewrites | | arguments replaced |
| HK12 | command hook no opinion | empty stdout; non-zero exit; garbage | no effect; warning for the last two |
| HK13 | command hook timeout | | killed; no opinion |
| HK14 | `match` filters | | only matching calls trigger |
| HK15 | command hook receives the event JSON | | exact fields |
| HK16 | command hook has no shell | arguments containing `;` | not interpreted |
| DCL01 | simple exec tool | `wc -w {{.path}}` | output returned |
| DCL02 | argument with space, quote, `;`, `$(…)`, newline | | one argv element, verbatim |
| DCL03 | argument starting with `-` | | passed as is |
| DCL04 | missing optional argument | | element dropped or empty as specified |
| DCL05 | number, bool, array, object arguments | | rendered as specified |
| DCL06 | non-zero exit | | error result with the code |
| DCL07 | timeout | | killed; group killed; error result |
| DCL08 | output cap | 200 KiB | head and tail kept |
| DCL09 | `render` stored in `Details` | | present |
| DCL10 | command not found | | error result |
| DCL11 | working directory | | the session cwd |
| DCL12 | `read_only` annotation honoured | | `ReadOnly` true in `Info` |
| INS01 | parse GitHub forms | `github.com/a/b@v1`, `a/b`, `https://…`, `git@…`, local path | each parsed |
| INS02 | rejected sources | `file://`, `ext::sh`, credentials in the URL, leading `-` | errors |
| INS03 | ref forms | tag, branch, 7- and 40-char commit | accepted; others rejected |
| INS04 | install from a local git fixture | | extension present under `Root`; lockfile entry |
| INS05 | repository with several extensions | | each subdirectory installed |
| INS06 | no manifest | | error; nothing installed |
| INS07 | invalid manifest | | error; nothing installed |
| INS08 | build step runs | | artifact exists |
| INS09 | build fails | | error; **nothing** left behind (no directory, no lock entry) |
| INS10 | confirm refused | | nothing left behind |
| INS11 | `Confirm` nil | | refused |
| INS12 | plan contents | | name, source, commit, run, build, capabilities |
| INS13 | name collision, different source | | error |
| INS14 | name equals a built-in or bundled extension | | error |
| INS15 | clone timeout | | error; cleaned up |
| INS16 | git hooks in the clone not executed | repository with a `post-checkout` hook | marker file not created |
| INS17 | git never prompts | | `GIT_TERMINAL_PROMPT=0` set |
| INS18 | commit pinned | | resolved SHA in the lock |
| INS19 | local path installs as a link | | no copy; removal removes the link only |
| INS20 | cancel mid-install | | cleaned up |
| INS21 | update to a new commit | | new plan with the capability diff; replaced atomically |
| INS22 | update failure restores the old version | build fails | old directory and lock intact |
| INS23 | update with nothing new | | reports up to date |
| INS24 | remove | | directory and lock entry gone; not installed is an error |
| INS25 | `List` drift | edit the directory's HEAD | `drifted` |
| INS26 | lockfile atomic write | simulate a failed rename | old lock intact |
| INS27 | corrupt lockfile | | treated as empty with a warning; rewritten on the next install |
| INS28 | concurrent installs of different extensions | `-race` | both succeed; lockfile has both |
| INS29 | concurrent installs of the same extension | | one succeeds; the other errors cleanly |
| CHK01 | healthy extension | | no problems |
| CHK02 | several problems | bad schema and a name conflict | both reported |
| CHK03 | never registers | | reported after the timeout |
| CHK04 | build failure | | reported |
| CHK05 | check does not install | | `Root` unchanged |
| RLD01 | reload restarts everything | | new processes; tools present |
| RLD02 | reload picks up a changed manifest | | new tools |
| RLD03 | reload rebuilds changed source | | build ran |
| RLD04 | one extension broken on reload | | others running; error reported |
| RLD05 | no stale tools after reload | extension removed a tool | gone |
| RLD06 | reload during a run | | waits for the run to end |
| RLD07 | replay after reload | | state rebuilt |
| SDK01 | `Run` handshake | | registers and serves |
| SDK02 | tool handler | | result returned |
| SDK03 | handler returns an error | | `is_error` result |
| SDK04 | handler panics | | error result; process survives |
| SDK05 | deadline in the context | | `ctx.Deadline` matches `deadline_ms` |
| SDK06 | hook handler | | decision returned |
| SDK07 | UI helpers | `Notify`, `Confirm` | frames sent; answers returned |
| SDK08 | `AppendEntry` and `Entries` | | round trip |
| SDK09 | `Complete` | | round trip |
| SDK10 | clean exit on `shutdown` and on stdin EOF | | returns |
| SDK11 | schema helpers produce valid JSON Schema | `Object(String(...))` | accepted by `tool.ValidateArgs` |
| SDK12 | concurrent calls | 20 | independent; `-race` clean |
| EXA01 | each shipped example passes `Check` and its own test | `hello`, `block-rm`, `hello-go` | pass |

## Fuzz and property tests

| Name | Asserts |
|---|---|
| `FuzzManifest` | arbitrary YAML never panics; the result is a manifest or an error |
| `FuzzParseSource` | never panics; any accepted source passes the rules in 25 |
| `FuzzArgTemplate` | rendering any JSON arguments yields argv elements that, joined, never change the element count unexpectedly (one element per template element, or dropped) |
| `FuzzHostFrames` | arbitrary frames from an extension never panic the host |
| `TestProperty_HK_Compose` | composing hooks in any partition of the same handlers (A+B)+C equals A+(B+C) for chain-type hooks |

## Out of scope

- UI for the trust prompt and `/ext` (C17). Bundled extensions (C18). MCP servers
  (`mcp` capability is validated here but the client is a later chunk). A registry or index
  service. Sandboxing the process.

## Acceptance

```
go run ./scripts/accept C13
```
