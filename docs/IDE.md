# Nova — IDE

Status: **proposal.** Nothing here is built. This document scopes a graphical front door for Nova, the
protocol additions it needs, and a milestone plan in the style of [`PLAN.md`](PLAN.md). Decisions below are
numbered `P1`, `P2`, … (proposed). Accepted ones move into [`DECISIONS.md`](DECISIONS.md) with real `D` numbers.

The goal, in one line: **an IDE with the same principles as Nova and pi — minimal by design, extensible by
default — where the agent loop is the centre and the editor is a lens on what the agent did.**

## 1. Why this is not "pi-gui for Nova"

Several projects already wrap pi in a desktop shell (pi-gui, two Pi Desktop apps, Zenbu, and editor bridges for
VS Code, Zed and Neovim). Most are an agent cockpit around a CLI: parallel threads, a diff review rail, PR
buttons. Their GUI extension points are usually separate from the agent's own extension system, so an extension
written for the agent does not appear in the GUI unless the GUI's authors add it.

Nova starts from a better position:

- The TUI is already **one client of one JSONL protocol** ([`PROTOCOL.md`](PROTOCOL.md)). `nova serve` already
  exposes that protocol over HTTP and SSE. An IDE is a second client, not a rewrite.
- A client declares what UI it can answer in `hello`, and the server never sends a request the client did not
  declare. That is the seam for rich UI that degrades gracefully.
- The core is UI-free and a check enforces it (`make ci`, import rules). The IDE can live beside it without
  weakening that rule.
- Extensions are subprocesses in any language, registered through one host registry.

What is missing is the **extension UI tier**. Third-party extensions get a closed set: `notify`, `confirm`,
`select`, `input`, `form`, `status`, `widget` (`text`, `list`, `table`, `progress`). That is right for a TUI and
too little for an IDE. This document is mostly about closing that gap without breaking "minimal".

## 2. Principles

Nova's principles ([`PLAN.md` §4](PLAN.md)) all hold. The IDE adds these:

1. **The agent is the centre; the editor is a lens.** The unit of work is a turn and its change set, not a file.
   Navigation is the session tree (fork, navigate, rewind), not tabs.
2. **The host owns very little.** A window, a buffer and workspace model, an event bus, and the protocol client.
   The file tree, git panel, language-server client, terminal, search, and review UI are extensions.
3. **One extension, many clients.** An extension declares *what* it contributes (a panel, a decoration, a
   command) and the client decides *how* to show it. The same extension runs in the TUI, the IDE, and a plain
   script.
4. **Declarative first, webview last.** Rich UI is a data description rendered by the client. A sandboxed
   webview is an escape hatch for the cases a description cannot express, and it is GUI-only.
5. **Everything has a budget**, in lines like the rest of Nova. The IDE client has a line budget and so does each
   bundled panel.
6. **No second protocol.** Anything the IDE can do, a script can do. New capabilities are protocol frames, tested
   with the same fake-server rig as everything else.

## 3. Shape

```
                      ┌───────────────────────────────┐
                      │  nova-ide  (client, web)      │   ← new
                      │  shell: browser first, then   │
                      │  Wails/Tauri wrapper          │
                      └──────────────┬────────────────┘
                                     │ JSONL protocol over HTTP + SSE
   ┌─────────┐  ┌─────────┐          │
   │  TUI    │  │ rpc/json│   ┌──────▼────────┐        ┌───────────────────────┐
   └────┬────┘  └────┬────┘   │ nova serve    │◀──────▶│ extension processes   │
        └────────────┴───────▶│ (mode server) │        │ (panels, LSP, git, …) │
                              └───────────────┘        └───────────────────────┘
```

The server and extension host are what exist today, plus the additions in §5. The IDE is a client that lives
outside the core tree (see P1).

## 4. The reimagined surface

Not a VS Code clone. The default layout is built around the loop:

| Surface | What it is | Provided by |
|---|---|---|
| **Conversation** | The transcript, tool cards, thinking blocks, queued steering. | Core client |
| **Change set** | The files a turn touched, as diffs, with accept/revert per hunk and per turn. | Bundled extension |
| **Timeline** | The session tree as the primary navigator: rewind to any turn, fork, compare branches. A turn is a checkpoint. | Core client, backed by `session.tree`/`navigate`/`fork` |
| **Buffer view** | A code editor opened *from* a change, a tool result, or a search hit. Not a tab strip first. | Core client |
| **Panels** | Everything else: files, search, git, terminal, tasks, diagnostics, usage. | Extensions |

Checkpoints deserve a note: the session already has fork and navigate for *conversation* state. Rewinding the
*workspace* needs a snapshot per turn (a git worktree or stash ref, taken by an extension and recorded with
`append_entry`). Without it, "rewind" would silently leave files in the wrong state, so the Timeline must not be
enabled until that extension exists (milestone I3).

## 5. Protocol additions

All additive. Version stays `1`; unknown fields and frames are already ignored, and a client that does not
declare a capability never sees its frames. Exact wire shapes are to be written in `PROTOCOL.md` test-first; the
sketches below fix the *vocabulary*.

### 5.1 View contributions (`ui.view`)

A new fire-and-forget UI method next to `widget`, for placements a TUI cannot offer. An extension contributes a
**view tree**; the client renders it natively.

```json
{"type":"ui_request","method":"view","key":"git",
 "placement":"sidebar|panel|editor_overlay|statusbar",
 "title":"Git",
 "content":{"kind":"tree","nodes":[{"id":"…","label":"…","icon":"…","badge":"…","actions":["stage","open"],"children":[…]}]}}
```

- `content.kind` is a closed set that **extends** the widget set: `text`, `list`, `table`, `progress` (as today)
  plus `tree`, `diff`, `markdown`, `form`, `tabs`, `split`. Anything the TUI cannot show falls back to the
  nearest widget kind (a `tree` becomes an indented `list`, a `diff` becomes `text`).
- A node or control names an **action id**. When the user triggers it the client sends
  `{"type":"view_action","key":"git","action":"stage","node":"…"}` to the owning extension. State lives in the
  extension, the client holds none.
- Updates replace the whole tree for a `key` (simple, cacheable). A patch form can be added if measurement
  shows it is needed, not before.

### 5.2 Workspace events (`workspace.*`)

Today an extension sees the loop (hooks) but not the user's editing. A new capability family, declared in
`hello` and in the manifest:

| Direction | Frame | Purpose |
|---|---|---|
| client → host | `workspace.opened {root, folders}` | what the user has open |
| client → host | `workspace.selection {path, ranges}` | cursor and selection |
| client → host | `workspace.buffer {path, version, dirty}` | unsaved state |
| host ↔ ext | `workspace.changed {paths, source: user\|agent\|external}` | file-change events, with who made them |
| ext → host | `workspace.diagnostics {path, items}` | language-server and linter output |
| ext → host | `workspace.decorate {path, ranges, kind}` | gutter marks, inline hints, agent-edit highlights |

This is also what lets the *agent* know what the user is looking at, which is the part editor bridges for pi
(such as live file tracking) bolt on today.

### 5.3 Webview escape hatch (`ui.webview`)

An extension may ask for a sandboxed surface it fully controls: `{"method":"webview","key":"…","entry":"ui/index.html"}`.
The client serves the extension's `ui/` directory from an opaque origin, in an iframe with no ambient authority,
and bridges a small `postMessage` API to the extension's `view_action` channel. GUI only; the TUI never sees it.
The extension's manifest declares `ui:webview` and the user approves it at install, like any capability.

### 5.4 Multiple sessions

`nova serve` is one trusted agent behind a bearer token today. An IDE with parallel agents needs more. Two
options, in order of preference:

1. **One process per session.** A supervisor (a bundled extension or a small `nova ide` subcommand) starts a
   `serve` per worktree and the client holds several connections. No change to the server's model.
2. **Multiplexed `serve`.** Sessions addressed by id on one listener. A real change to a core invariant.

P3 chooses option 1 for the first version.

## 6. Bundled extensions the IDE needs

Each is an ordinary extension and follows the two-tier rule: bundled ones may use the in-process API, but any
capability they need that a subprocess cannot reach is a protocol gap to close.

| Extension | Contributes |
|---|---|
| `files` | file tree view, open-file requests, create/rename/delete (through permission rules) |
| `changes` | the change-set diff view per turn, accept/revert, stage |
| `checkpoint` | per-turn workspace snapshot and restore; records refs with `append_entry` |
| `git` | status, branches, worktrees, commit and PR actions |
| `lsp` | language-server client: diagnostics, definitions, hover, and **tools for the model** (`diagnostics`, `references`) |
| `terminal` | PTY panel; its output is also available to `bash`-class tools |
| `search` | text and (later) semantic search panel |

`lsp` is the one with the largest payoff for the agent: today the model finds errors by running builds. Giving it
`diagnostics` and `references` as tools makes the editor's intelligence the agent's too.

## 7. Choices

| | Choice | Why | Rejected |
|---|---|---|---|
| Shell | A web client against `nova serve`, wrapped later by Wails or Tauri | `serve` already exists, so the first milestone needs no shell; a Go host fits Wails; both keep the binary small | Electron (heavy, fights "minimal"); a native toolkit (a second UI stack to maintain) |
| Editor widget | CodeMirror 6 | modular and small; Monaco is a large fixed bundle | Monaco; building an editor |
| Fork | None | a VS Code or Theia fork inherits a heavy core and an extension model that is not Nova's | Forking VS Code, Theia, or Zed |
| Zed / ACP | Not the base; maybe an adapter later | an ACP adapter would let Zed drive Nova without us building a shell, but the extension surface would be Zed's | Making Zed the IDE |

## 8. Proposed decisions

| | Decision | Notes |
|---|---|---|
| P1 | The IDE client lives outside the core packages, in its own directory and Go module (`ide/`), never imported by the core | Keeps the "core never imports the UI" check intact; the web assets are built separately and are not part of `go build ./cmd/nova` |
| P2 | View contributions extend the widget model rather than replacing it | One vocabulary; every view kind has a TUI fallback so no extension is GUI-only by accident |
| P3 | Parallel sessions are one `serve` process each in v1 | Avoids changing the one-agent-per-server invariant; revisit if process cost matters |
| P4 | Workspace snapshots are an extension, not core | Core stays ignorant of git; the Timeline is gated on the extension being present |
| P5 | Webview is GUI-only, capability-gated, and opaque-origin | It is the least safe surface, so it is last, opt-in, and sandboxed |
| P6 | The IDE gets a line budget like every other package | Set at the first milestone, with the same "raise the budget in a decision" rule |

## 9. Risks and open questions

1. **Extensions are not sandboxed.** They run with the user's permissions ([`PLAN.md` §3](PLAN.md)). Once an
   extension can contribute UI to a window with the user's files open, the install-time capability prompt carries
   more weight. Open: should `ui:webview` and `workspace.*` be separately approved?
2. **Declarative views may not be expressive enough.** The webview exists for this, but if most useful panels end
   up there, the declarative tier has failed and we have rebuilt VS Code's extension model. Track the ratio.
3. **Latency and state.** Whole-tree replacement per `key` is simple but may be wasteful for large trees (a file
   tree of a big repo). Measure before adding patches.
4. **Remote and auth.** A browser client over `serve` means a token in a browser. Loopback-only by default; a
   non-loopback listener already requires a token. Open: how the Wails/Tauri wrapper stores it.
5. **Where does the agent's view of "what the user has open" stop?** Sending selections to the model is a privacy
   and context-cost decision. Default off; an extension opts in per turn.
6. **Compatibility with pi extensions.** Nova's extension protocol is not pi's TypeScript API. Whether a bridge is
   worth building is out of scope here.
7. **Unverified assumptions.** This proposal was written from the docs, without reading `mode/serve` or the host
   code. Before milestone I0, confirm that the SSE stream and `ui_request` routing tolerate several concurrent
   clients (a TUI and a browser on one session) as the design assumes.

## 10. Roadmap

Same shape as `PLAN.md`: milestones made of small, test-first chunks, each with a test matrix.

| Milestone | Result | Notes |
|---|---|---|
| **I0 Read-only client** | A browser page against `nova serve` shows the conversation, tool cards and thinking, and can send a prompt and abort. No new protocol. | Proves the seam; settles the §9.7 concurrency question |
| **I1 Views** | `ui.view` and `view_action` land in the protocol, the TUI renders the fallbacks, and the client renders trees, diffs and tabs. A `tasks`-style bundled extension is ported as the reference. | The first real protocol change |
| **I2 Workspace** | `workspace.*` frames and a buffer view (CodeMirror 6). `files` and `changes` extensions. | The IDE becomes usable for review |
| **I3 Timeline** | `checkpoint` extension and the Timeline view: rewind, fork, compare. | Gated on I2 |
| **I4 Intelligence** | `lsp` extension with model-facing tools; `terminal`, `git`, `search`. | The agent gains the editor's senses |
| **I5 Many sessions** | The supervisor, a session switcher, and parallel worktrees. | Per P3 |
| **I6 Webview and shell** | `ui.webview`, then the Wails/Tauri wrapper and signed builds. | Last and optional |

Exit criteria: I0 is "the existing protocol is enough for a read-only GUI". I2 is the first point the IDE is
comparable to a pi desktop shell. I4 is where it stops being only a shell and starts doing something a wrapped
CLI cannot.

## 11. First issues to file

1. Spike: connect two clients (the TUI and a browser) to one `nova serve` session and record what breaks (§9.7).
2. Protocol: write the `view` method and `view_action` frame in `PROTOCOL.md` with a test matrix, including the
   TUI fallback table (P2).
3. Protocol: write the `workspace.*` family and capability names, including the `source` field.
4. Design: pick the budget and the directory layout for `ide/` (P1, P6).
5. Extension: a `checkpoint` design note covering what it snapshots, and what rewind does to uncommitted edits.
