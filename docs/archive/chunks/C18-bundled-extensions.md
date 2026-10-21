---
chunk: C18
title: "Bundled extensions"
packages: [./ext, ./bundled, ./bundled/tasks, ./bundled/ask, ./bundled/plan, ./bundled/subagent, ./bundled/webfetch]
budgets: { ./ext: 3100, ./bundled: 150, ./bundled/tasks: 450, ./bundled/ask: 350, ./bundled/plan: 400, ./bundled/subagent: 500, ./bundled/webfetch: 760 }
depends: [C13, C17]
---

# C18 — Bundled extensions

## Goal

Ship the five extensions Nova includes by default: **tasks**, **ask**, **plan**, **subagent**, and
**webfetch**. Each is an in-tree Go package (D17) that registers through the same host path as
a subprocess extension, using a small in-process API (`ext.Env`, D42), and adds richer UI when the TUI is
present. Together they prove the extension surface: if a bundled one needs something a subprocess
extension cannot do, that is a gap to close in the protocol.

## Read first

- `docs/EXTENSIONS.md` sections "Bundled extensions" and "What an extension can do".
- `docs/DECISIONS.md` D17, D18, D42.
- `ext/host.go`, `ext/hooks.go`, `ext/tools.go` (C13): the registration path the in-process API joins.
- `agent/agent.go` (C09): `Hooks`, `Config`, `Result`. `tool/tool.go` (C04). `permission/policy.go`.
- `tui/render/registry.go` (C16) and `tui/dialog` and `tui/widget` (C17).
- For behaviour ideas only (not code): the `pi-tasks` and `pi-q-n-a` extensions in the
  `AutumnsGrove/pi-extensions` repository, and the `plan-mode` and `subagent` examples under
  `packages/coding-agent/examples/extensions/` in the Pi repository.
- `docs/TESTING.md` section 3, "Extensions", and "Tools: filesystem" for the style of edge cases.

## Files

| File | Change | Purpose |
|---|---|---|
| `ext/inproc.go` | create | `Bundled`, `Env`, `Host.AddBundled`, event fan-out to bundled extensions |
| `ext/inproc_test.go` | create | |
| `bundled/bundled.go` | create | `All()`, enable and disable by config |
| `bundled/tasks/*.go` | create | `tasks.go`, `state.go`, `tasks_tui.go` (`//go:build !notui`) |
| `bundled/ask/*.go` | create | `ask.go`, `format.go`, `ask_tui.go` |
| `bundled/plan/*.go` | create | `plan.go`, `plan_tui.go` |
| `bundled/subagent/*.go` | create | `subagent.go`, `subagent_tui.go` |
| `bundled/webfetch/*.go` | create | `fetch.go`, `ssrf.go`, `html.go`, `webfetch_tui.go` |
| `tui/ui/registry.go` | create | `Registry`: card renderers and command dialogs for bundled extensions |
| `mode/wire.go` | change | build the `Env`, start enabled bundled extensions, implement `Spawn` |
| `mode/server.go` | change | count `Details.nested_usage` into the ledger |
| `scripts/importcheck/main.go` | change | allow `golang.org/x/net/html` for `./bundled/webfetch` |
| `examples/extensions/` | change | add `todo-like-sdk` (a subprocess port of `tasks`) as the equivalence test |
| `*/*_test.go` | create | |

`golang.org/x/net/html` is this chunk's dependency (D44); the standard library has no HTML parser. It is
in the importcheck table for `./bundled/webfetch` only.

## Interfaces

```go
// package ext (in-process API)
type Bundled interface {
    Name() string
    Init(env *Env) error
}
type Env struct {
    CWD     string
    Mode    string
    Config  *config.Config
    Log     func(level, msg string)
    Session SessionAPI
    UI      UIBridge
    Ledger  *usage.Ledger
    Extra   map[string]any            // "tui" -> *ui.Registry when the TUI is present
    // registration
    Tool     func(t tool.Tool) error
    Command  func(c InProcCommand) error
    Hooks    func(h agent.Hooks)       // composed with every other extension's hooks, in load order
    Events   func(fn func(proto.Event)) // observe every event
    // services
    Complete func(ctx context.Context, req provider.Request) (*provider.Response, error)
    Spawn    func(ctx context.Context, r SpawnRequest) (*SpawnResult, error)
    HasUI    func(capability string) bool // whether the attached client declared it
}
type InProcCommand struct {
    Name, Description string
    Run func(ctx context.Context, args string) (output string, err error)
}
type SpawnRequest struct {
    System   string
    Task     string
    Tools    []string      // names; resolved against the parent registry
    Model    string        // "" means the parent's model
    MaxTurns int
    Depth    int           // the caller's depth; the child runs at Depth+1
    OnEvent  func(proto.Event)
}
type SpawnResult struct {
    Answer string
    Turns  int
    Usage  provider.Usage
    Reason string            // done | exhausted | aborted | error
    Tools  []string          // names of the tools it called, in first-use order
}
func (h *Host) AddBundled(b Bundled) error

// package bundled
func All() []ext.Bundled                           // tasks, ask, plan, subagent, webfetch
func Enabled(cfg *config.Config) []ext.Bundled     // minus config extensions.disable

// package ui (additions)
type Registry struct{ /* ... */ }
func (r *Registry) Renderer(tool string, rd render.Renderer)
func (r *Registry) Dialog(command string, build func(args string, st any) dialog.Dialog)
```

### Tool schemas

`todo`: `{action: create|update|delete|list|clear, id?, title?, status?: todo|doing|done|blocked, detail?,
tasks?: [{title, detail?}]}`. `ask`: `{questions: [{id, prompt, kind: single|multi|text, options?: [{id, label,
description?, preview?}], allow_other?: bool}]}`. `plan_submit`: `{plan: string}`.
`subagent`: `{task: string, tools?: [string], model?: string, max_turns?: integer}`.
`web_fetch`: `{url: string, max_chars?: integer, raw?: bool}`.

## Behaviour

### In-process API

1. `AddBundled` calls `Init` once per start and again after every reload. A bundled extension's tools,
   commands, and hooks are registered through the same registry and hook composition as subprocess extensions;
   names clash exactly as in C13 (a bundled tool name is reserved, so a third-party extension cannot take it).
2. A panic in `Init` or in any registered function is recovered and reported as an `extension_error`; the
   extension is disabled until reload, and nothing else is affected. A bundled extension that returns an error
   from `Init` is reported and skipped.
3. `Events` callbacks run on the server's event goroutine in order and must not block: each has a 100 ms budget
   after which it is skipped for that event and an `extension_error` is reported at most once per minute.
4. `Env.HasUI(cap)` reflects the client's `hello`. With no client (print mode, the Action) every capability is false.
5. `Config.Extensions.Disable` names (`tasks`, `ask`, `plan`, `subagent`, `webfetch`) remove an extension entirely,
   including its tools and commands.
6. In a `-tags notui` build the non-TUI parts of every bundled extension still work through the protocol's UI
   methods; only the `*_tui.go` files (card renderers, command dialogs) are absent.

### tasks

7. `todo` actions: `create` (one `title`, or up to 50 in `tasks`), `update` (`id`, plus `title`, `status`, or `detail`),
   `delete` (`id`), `list`, `clear`. IDs are `t1`, `t2`, … from a counter that never reuses a number within a
   session branch. A task has `id`, `title` (1 to 200 characters after trimming), `status` (default `todo`), `detail` (up to
   2,000 characters). At most 200 tasks.
8. Every call returns the **whole current list** as text (`t3 [doing] Fix the retry`), so the model always sees the
   state. Errors (unknown id, bad status, empty title, over a limit, unknown action) are error results naming the problem.
   More than one `doing` task is allowed; the result adds a one-line reminder to keep one in progress at a time.
9. **State** is stored as a `tasks.state` custom entry (`keep: true`) holding `{v:1, next:N, tasks:[…]}` after every change
   (a full snapshot, not a diff). On start, reload, resume, and whenever the active branch changes (`session_changed`),
   the state is rebuilt from the **last snapshot on the active branch**; none means an empty list. Compaction never loses
   it (`keep`).
10. **UI.** A `tasks` widget above the editor (kind `list`; `doing` highlighted) shows while any task exists; when every
    task is `done` it collapses to `☑ N of N tasks`. A footer status segment shows `☐ k of N`. `/todos` opens the full list
    (TUI dialog) or prints it (other clients); `/todos clear` empties the list. The widget and status are cleared when the
    list is empty.
11. Tool annotations: `todo` is not read-only (it mutates state) but is not destructive; it is not parallel.
12. With no UI client the tool still works; only the widget calls are skipped.

### ask

13. `ask` presents a form (`ui_request` method `form`) and returns the answers. Limits: at most 8 questions, 8 options each;
    ids must be unique and non-empty; a `single` or `multi` question needs at least 2 options (otherwise it becomes `text`);
    prompts up to 500 characters, labels up to 100. Violations are error results naming the problem.
14. The result is text, one block per question, in order:

    ```
    Q1 [scope] Which scope should the change cover?
    Answer: Only the client package
    Note: keep the public API stable
    Q2 [tests] Add tests?
    Answer: skipped
    ```

    `Other` answers read `Answer: Other: <text>`. Multi answers are comma separated labels. Notes are included verbatim and
    on their own line. The tool's description tells the model that **notes are authoritative** and that a skipped question
    means "no preference".
15. If the client lacks `ui.form`, or `HasUI` is false, the result is an error: `No interactive user is available. Make a
    reasonable assumption, state it, and continue.` If the user cancels the form, the result is `The user dismissed the form
    without answering. Ask in plain text or proceed with a stated assumption.` (not an error).
16. `ask` has no timeout. It is read-only and not parallel. Its card shows the questions and the answers.

### plan

17. `/plan` toggles plan mode (also `--plan` on the command line, and the protocol command `run_command plan`). The state is
    stored as a `plan.mode` custom entry (`keep: true`) and rebuilt on resume and branch changes. A footer segment shows
    `plan` while on.
18. In plan mode, a `tool_call` hook **blocks every tool that is not read-only**, with the reason `plan mode: only read-only
    tools are allowed until the plan is approved`; `plan_submit` and `ask` are allowed. A `BeforeProviderRequest` hook appends
    plan-mode instructions to the system message (investigate, write a concrete step-by-step plan, call `plan_submit`, do not
    modify anything).
19. `plan_submit {plan}` (1 to 20,000 characters) opens a form with three choices: **Approve and execute**, **Revise** (with
    a note), **Cancel plan**. Results: approve → plan mode turns off and the result says `The user approved the plan. Leave plan
    mode and carry it out.`; revise → the result includes the note and `Revise the plan and call plan_submit again.`; cancel →
    plan mode turns off and the result says `The user cancelled the plan. Stop and ask what they would like to do.`
20. With no UI client, `plan_submit` records the plan, leaves plan mode on, returns `Plan recorded. No approval is possible here;
    present the plan as your final answer.`, and a `TurnEnd` hook ends the run after that turn.
21. `plan_submit` outside plan mode is an error result (`not in plan mode`). Toggling off during a run takes effect on the next
    tool call.

### subagent

22. `subagent` runs a fresh agent loop through `Env.Spawn` with its own transcript, a system prompt saying it is a sub-agent
    that must finish the task and report concisely, and a **restricted tool set**: the requested `tools` that exist in the parent
    registry, defaulting to the read-only tools (`read`, `list`, `glob`, `grep`, `web_fetch`). Unknown names are an error result.
    `subagent` itself is **not** available to the child (maximum depth 1; `Config` may raise it, to at most 3). `max_turns`
    is optional, with **no default and no cap**: unset means the child runs until it finishes, and a value the caller passes is
    used as-is.
23. The child inherits the parent's permission policy and approval path; an `ask` from the child reaches the user with the reason
    prefixed `[subagent]`. The parent's `tool_call` and `tool_result` hooks apply to the child's calls (so a block hook guards
    sub-agents too); its other hooks do not.
24. At most **4 sub-agents run at once** at each nesting depth (a shared limit would deadlock a parent that holds a slot while its child waits for one); extra calls wait. The call honours cancellation: abort stops the child
    and returns a result for the call.
25. Progress: while running, the tool emits `tool_update` lines `▸ <tool> <summary>` for each child tool call and a final line with
    the turn count. Reasoning text is not forwarded.
26. The result `Content` is the child's final answer, cut at 20,000 characters (`Truncated`). A child that ends `exhausted` returns its last
    text with a note; one that errors returns an error result. `Details` is `{turns, tools, duration_ms, usage, reason}` and
    `nested_usage` (the child's usage). The tool also reports that usage through `Context.AddUsage`, so the run total and the footer include it, and `Spawner` adds it to the ledger, even when the child failed or was aborted.
27. The tool is not read-only and not parallel by annotation, but the loop treats separate `subagent` calls in one message as
    **parallel** (they declare `Parallel: true`, bounded by rule 24).

### webfetch

28. `web_fetch {url, max_chars?, raw?}` performs an HTTP GET. Schemes `http` and `https` only. `max_chars` defaults to 50,000 and is
    capped at 200,000.
29. **Network safety (SSRF).** The destination host is resolved **at dial time** and every resolved address is checked: loopback,
    private (RFC 1918 and unique-local), link-local (including `169.254.169.254` cloud metadata), unspecified, multicast, CGNAT
    (100.64.0.0/10), the other reserved, documentation, and benchmarking ranges, and IPv4-mapped, NAT64, and 6to4 forms of
    these (judged by the IPv4 address inside) are refused with an error result `blocked address`. The connection is made to
    the checked IP, so DNS rebinding between check and connect cannot change the target. The same check applies to **every redirect**.
    A URL with an IP literal is checked the same way. `Config` may allow specific hosts (`webfetch.allow_hosts`) or all private
    addresses (`webfetch.allow_private: true`); both default off.
30. At most 5 redirects, each re-validated; redirects to other schemes are refused. The total time limit is 20 seconds, the connect
    timeout 5 seconds. No cookies, no `Authorization` forwarded across hosts, no proxy from the environment (`Proxy: nil`),
    a fixed `User-Agent: nova-webfetch`. A URL with embedded credentials is rejected.
31. The body is read through a **5 MiB cap** (streamed; more than that is an error `response too large` unless `raw` is false and the
    first 5 MiB are enough, in which case it is truncated with a note). Compressed responses are decoded with the cap applied to the
    decoded size (so a zip-bomb cannot expand past it).
32. **Content handling.** HTML is converted to markdown by an in-package converter over `golang.org/x/net/html`: script, style, nav-like
    boilerplate (`nav`, `footer`, `aside` when a `main` or `article` exists), and comments are dropped; headings, paragraphs, lists
    (nested), links (`[text](url)` with relative URLs resolved; `javascript:` and `data:` targets are reduced to their text), emphasis, inline
    and fenced code (line breaks and indentation inside `pre` are kept), block quotes, and simple tables are kept.
    `text/plain`, `text/markdown`, and `application/json` (pretty-printed) are returned as text. Other content types return an error
    (`unsupported content type: image/png`). `raw: true` returns the body text unconverted (for text types).
33. Charset: from the header or a `meta` tag; UTF-8, and Latin-1 and Windows-1252 as fallbacks; invalid sequences become U+FFFD.
34. The output is cut to `max_chars` at a line boundary with `... (truncated; N characters omitted)`. The result starts with a
    one-line header `<final URL> (<status>, <content type>, <bytes> bytes)`. A non-2xx status returns that header plus up to 2,000
    characters of the body, as an error result for 4xx and 5xx.
35. `web_fetch` is `ReadOnly`, `OpenWorld`, and `Parallel`. Its card shows the URL, status, and size.

## Matrix

| ID | Case | Input / setup | Expected |
|---|---|---|---|
| IPC01 | register a tool, command, hooks | | present in the registry and the composition |
| IPC02 | name clash with a third-party tool | subprocess extension tries `todo` | rejected; the bundled one wins |
| IPC03 | panic in `Init` | | extension disabled; others unaffected; `extension_error` |
| IPC04 | panic in a tool, command, hook | | error result or skipped; reported |
| IPC05 | `Init` error | | skipped; reported |
| IPC06 | `Init` again after reload | | called once per start |
| IPC07 | `Events` order | | events in sequence |
| IPC08 | slow `Events` callback | sleeps 500 ms | skipped past 100 ms; reported at most once per minute; loop unaffected |
| IPC09 | `HasUI` reflects hello | with and without a client | true and false |
| IPC10 | `Disable` removes it | `extensions.disable: [tasks]` | no tool, command, or widget |
| IPC11 | hooks compose with subprocess extensions | | load order respected |
| IPC12 | `-tags notui` build | | the logic works; `*_tui.go` absent; `go vet` clean |
| BND01 | `All()` lists five | | the five names |
| BND02 | `Enabled` honours config | | filtered |
| TSK01 | create one task | | `t1`; list returned |
| TSK02 | create many | 50 | `t1` to `t50` |
| TSK03 | more than 50 in one call | 51 | error; nothing created |
| TSK04 | the 201st task | | error |
| TSK05 | update status, title, detail | | applied |
| TSK06 | unknown id | | error naming it |
| TSK07 | bad status | | error |
| TSK08 | empty and whitespace title | | error |
| TSK09 | title over 200 characters, detail over 2,000 | | error |
| TSK10 | delete | | removed; ids not reused |
| TSK11 | ids never reused after delete | create after delete | next number |
| TSK12 | list and clear | | text list; empty after clear |
| TSK13 | unknown action | | error listing the actions |
| TSK14 | every call returns the whole list | | yes |
| TSK15 | two `doing` tasks | | allowed; reminder line |
| TSK16 | snapshot entry written | after each change | `tasks.state` with `keep:true` |
| TSK17 | state rebuilt on resume | | same list |
| TSK18 | state rebuilt on reload | | same list |
| TSK19 | branch navigation | navigate to an older entry | list matches that branch's last snapshot |
| TSK20 | state after compaction | | intact |
| TSK21 | no snapshot on the branch | | empty |
| TSK22 | concurrent calls | 20 goroutines, `-race` | consistent; no lost updates |
| TSK23 | widget and status | | list widget above the editor; `☐ k of N` segment |
| TSK24 | all done collapses | | `☑ N of N tasks` |
| TSK25 | empty list clears widget and status | | cleared |
| TSK26 | `/todos` and `/todos clear` | | list shown; cleared |
| TSK27 | no UI client | print mode | works; no widget calls |
| TSK28 | unicode titles | | stored and shown intact |
| TSK29 | equivalence with the subprocess port | same call script against both | identical results and state |
| TSK30 | rejected update changes nothing | `update` with a valid title and a bad status; `Apply` on a shared state | the task is unchanged and `Apply` never modifies its input |
| TSK31 | unsaved change is rolled back | the session cannot append the snapshot | an error naming the failure; the list and next id are unchanged |
| ASK01 | one single-select question | | form request sent; answer in the result text |
| ASK02 | multi and text questions | | labels joined; text shown |
| ASK03 | other answer | | `Other: …` |
| ASK04 | note included verbatim | newlines and Unicode | on its own line |
| ASK05 | skipped question | | `Answer: skipped` |
| ASK06 | cancelled form | | the dismissed text; not an error |
| ASK07 | no `ui.form` capability | | error with the assumption advice |
| ASK08 | print mode | | same error |
| ASK09 | limits | 9 questions, 9 options | error |
| ASK10 | duplicate or empty ids | | error |
| ASK11 | single with one option | | becomes text |
| ASK12 | long prompt and labels | | error over the limits |
| ASK13 | description tells the model notes are authoritative | | present |
| ASK14 | answers in question order | | yes |
| ASK15 | no timeout | wait 10 minutes (fake clock) | still waiting |
| ASK16 | abort while waiting | | returns a cancelled result; the dialog closes |
| ASK17 | Other text on a multi-choice question | a `multi` answer with `value` only, with `values` and `value`, and with ids only | the text is shown as `Other: ...` after any chosen labels; never an empty answer |
| PLN01 | `/plan` toggles | | on and off; footer segment |
| PLN02 | state persisted | | rebuilt on resume |
| PLN03 | non-read-only tool blocked | `write` | blocked with the plan-mode reason |
| PLN04 | read-only tools allowed | `read`, `grep` | run |
| PLN05 | `ask` and `plan_submit` allowed | | run |
| PLN06 | system message gains instructions | | appended |
| PLN07 | approve | | mode off; approval text |
| PLN08 | revise with a note | | note in the result; mode stays on |
| PLN09 | cancel | | mode off; cancel text |
| PLN10 | no UI | | recorded text; run ends after the turn; mode stays on |
| PLN11 | `plan_submit` outside plan mode | | error |
| PLN12 | plan size limits | 0 and 20,001 characters | error |
| PLN13 | toggle off mid-run | | next tool call is allowed |
| PLN14 | `--plan` flag | print mode | starts in plan mode; the plan is the answer |
| PLN15 | blocked call counts as a result | | the model sees the block reason |
| SUB01 | basic run | scripted child answers | result is the answer |
| SUB02 | default tools | none requested | read-only set only |
| SUB03 | requested tools | `[read, bash]` | both available (subject to policy) |
| SUB04 | unknown tool name | | error result |
| SUB05 | `subagent` not offered to the child | | absent |
| SUB06 | depth limit | config depth 2 | one nested level allowed; a third refused |
| SUB07 | `max_turns` optional, no default or cap | none, 500 | 0; 500 passed straight through |
| SUB08 | child hits max turns | | answer with an exhausted note |
| SUB09 | child provider error | | error result |
| SUB10 | policy inherited | child calls a denied tool | denied |
| SUB11 | approval path | child needs approval | the user is asked; reason prefixed `[subagent]` |
| SUB12 | parent hooks guard the child | block hook on `bash` | child's `bash` blocked |
| SUB13 | other parent hooks not applied | a `TurnEnd` hook | not called for the child |
| SUB14 | concurrency cap | 10 parallel subagent calls | at most 4 run at once |
| SUB15 | cancel | abort mid-run | child stopped; result returned; no goroutine leak |
| SUB16 | progress lines | child uses 3 tools | three `tool_update` lines and a final turn count |
| SUB17 | reasoning not forwarded | | absent |
| SUB18 | result cap | 100 KiB answer | cut at 20,000; `Truncated` |
| SUB19 | `nested_usage` | | added to the ledger; footer cost increases |
| SUB20 | `Details` | | turns, tools, duration, usage, reason |
| SUB21 | model override | `model` set | child request uses it; unknown model gives the provider's error |
| SUB22 | panic in the child | | error result; parent continues |
| SUB23 | no UI | print mode | works |
| SUB24 | card renders | | nested summary with cost |
| SUB25 | child lifecycle events stay off the host stream | child run | only permission and extension_error events reach the host; the caller's observer sees all |
| SUB26 | one progress line per child tool | `tool_call` plus `tool_start` | one `▸ name` line each |
| SUB27 | failed child still counts its usage | provider error on turn 2 | ledger and `AddUsage` include turn 1 |
| SUB28 | nested call under `max_concurrent: 1` | child calls `subagent` | runs; does not wait for its parent's slot |
| SUB29 | result cap on a character boundary | multi-byte answer | valid UTF-8 |
| SUB30 | spawner depth cap | configured depth 10 | refused at 3 |
| SUB31 | the parent's `max_tokens` reaches the child | `Spawner.MaxTokens` 24 | the child request carries 24 |
| WFT01 | fetch an HTML page | local test server | markdown with header line |
| WFT02 | plain text, markdown, JSON | | returned; JSON pretty-printed |
| WFT03 | unsupported content type | `image/png` | error |
| WFT04 | `raw` | | unconverted text |
| WFT05 | non-http scheme | `file:///etc/passwd`, `ftp://` | error |
| WFT06 | credentials in URL | `http://u:p@h/` | rejected |
| WFT07 | loopback blocked | `http://127.0.0.1:port` | `blocked address` |
| WFT08 | private ranges blocked | 10.0.0.1, 192.168.1.1, 172.16.0.1, fc00::1 | blocked |
| WFT09 | metadata address blocked | 169.254.169.254 | blocked |
| WFT10 | CGNAT, unspecified, multicast, link-local v6 | | blocked |
| WFT11 | IPv4-mapped IPv6 loopback | `::ffff:127.0.0.1` | blocked |
| WFT12 | hostname resolving to a private address | fake resolver | blocked |
| WFT13 | DNS rebinding | first lookup public, second private | the connection goes to the first checked IP only |
| WFT14 | redirect to a blocked address | 302 to `http://127.0.0.1` | blocked |
| WFT15 | redirect chain over 5 | | error |
| WFT16 | redirect to another scheme | | refused |
| WFT17 | `allow_private` and `allow_hosts` | | permitted when set |
| WFT18 | timeout | server stalls | error within the limit (fake clock) |
| WFT19 | response over 5 MiB | | handled per rule 31 |
| WFT20 | gzip bomb | 10 MB decoded from a tiny body | cap applied to decoded size |
| WFT21 | non-2xx | 404, 500 | header plus up to 2,000 characters; error result |
| WFT22 | `max_chars` | 1,000 | cut at a line boundary with the note |
| WFT23 | `max_chars` above the cap | 10 million | 200,000 |
| WFT24 | charset handling | UTF-8, Latin-1, Windows-1252, bad bytes | decoded; U+FFFD for invalid |
| WFT25 | HTML scripts and styles dropped | | absent |
| WFT26 | boilerplate dropped when `main` exists | | `nav` and `footer` gone |
| WFT27 | headings, lists, links, code, tables | | markdown equivalents; relative URLs resolved |
| WFT28 | deeply nested HTML | depth 10,000 | no stack overflow |
| WFT29 | malformed HTML | | converts without error |
| WFT30 | huge page | 5 MiB of HTML | converts within 2 seconds |
| WFT31 | no cookies and no auth forwarding | redirect across hosts | not forwarded |
| WFT32 | environment proxy ignored | `HTTP_PROXY` set | not used |
| WFT33 | `User-Agent` | | fixed value |
| WFT34 | annotations | | read-only, open-world, parallel |
| WFT35 | cancel mid-request | | prompt return; connection closed |
| WFT36 | concurrent fetches | 20, `-race` | independent results |
| WFT37 | URL with spaces, Unicode, fragment, port | | handled |
| WFT38 | empty URL, relative URL, huge URL | | errors |
| WFT39 | `pre` blocks keep their lines | code with tabs and newlines | fenced block identical to the source |
| WFT40 | blank elements between blocks | six empty `div`s | no leading spaces on the next block |
| WFT41 | blank lines made of spaces | `br` separated by spaces | at most one blank line |
| WFT42 | inert link targets | `JavaScript:`, `vbscript:`, `data:` | text and alt kept, target dropped |
| WFT43 | cuts count characters | multi-byte text, odd limits | valid UTF-8; character counts exact |
| WFT44 | connection closed after a fetch | kept-alive server | the connection is closed |
| WFT45 | caller cancel is not a timeout | cancel mid-request | `cancelled`, not `timed out` |
| WFT46 | redirect target validated like the URL | redirect to `http://u:p@host/` | credentials refused; not echoed |
| WFT47 | other reserved ranges | `0/8`, `240/4`, documentation, NAT64, 6to4, Teredo | blocked; tunnelled forms follow the inner address |
| WFT48 | allow-list matching | trailing dots, leading-dot suffix | normalised; dot boundary only |

## Fuzz and property tests

| Name | Asserts |
|---|---|
| `FuzzHTMLToMarkdown` | arbitrary bytes never panic or hang; output is valid UTF-8 |
| `FuzzBlockedAddress` | for any IP, the checker's answer matches an independent reference classification |
| `FuzzTaskOps` | any sequence of task actions keeps ids unique, counts within limits, and the snapshot decodable |
| `FuzzAskFormat` | any answers format without panic and with every question present |
| `TestProperty_TSK_SnapshotRebuild` | after any operations and any branch navigation, rebuilt state equals the last snapshot on the active branch |

## Out of scope

- Search tools for the web (`parallel` is a suggested extension). A browser or JavaScript execution.
- Persisting sub-agent transcripts as sessions. Nested depth beyond 3.
- The subprocess examples other than the `tasks` equivalence port.

## Acceptance

```
go run ./scripts/accept C18
go build -tags notui ./...
```
