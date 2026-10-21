# Nova — Extensions

Nova ships small and grows by extension. The goal, taken from Pi, is that you can
tell Nova "I want X" and Nova can read its own documentation, write the extension,
and load it, without hand-holding. That shapes everything below: a small API, a
format that is easy to write correctly, and documentation that fits in an agent's
context.

Protocol details are in [`PROTOCOL.md`](PROTOCOL.md). The reasons are in
[`DECISIONS.md`](DECISIONS.md) (D5, D16 to D20).

## Two tiers

| | Bundled | Third-party |
|---|---|---|
| Written in | Go, inside this repo (`bundled/`) | any language |
| Runs | in the `nova` process | as a subprocess |
| Talks via | the Go `Host` API | the JSONL protocol |
| UI | charm.land directly, full native look | the fixed declarative components |
| Installed by | being compiled in | `nova ext install` |
| Removed with | the `notui` build tag, or config | `nova ext remove` |

Both tiers register the same things through the same host registry, so a bundled
extension is the reference for how a third-party one should behave. Anything a
bundled extension does through the in-process API but a subprocess cannot do is a
gap in the protocol, to be filed and closed, not a permanent privilege.

## What an extension can do

| Capability | In a manifest | In the protocol |
|---|---|---|
| Add a model-callable tool | `tools` | `register.tools`, handle `tool_call` |
| Add a slash command | `commands` | `register.commands`, handle `command` |
| Observe or change the loop | `hooks` | `register.hooks`, answer `hook` |
| Add a CLI flag | `flags` | `register.flags` |
| Show something to the user | `ui:*` | `ui_request` (see below) |
| Keep state in the session | `session:append_entry` | `append_entry` |
| Call the model | `model:complete` | `complete` |
| Add a model provider | `providers` | `register.providers` |
| Add an MCP server | `mcp` | `register.mcp` |

### UI available to third-party extensions

Fixed, so the result always fits the look of the TUI and also works in other
clients: `notify`, `confirm`, `select`, `input`, `form`, `status` segments, and
`widget` (text, list, table, progress). Tool results and extension messages can
name a **renderer template**: a small, safe template over the `details` JSON
(`{{.path}} {{.added}} lines added`) so a tool's card looks right without any code
in the TUI.

A third-party extension cannot inject arbitrary Bubble Tea components. A bundled
one can.

## Layout of an extension

```
my-ext/
  extension.yaml      the manifest (required)
  README.md           what it does; the agent reads this
  main.go  (or index.js, main.py, a binary, a script)
  skills/             optional SKILL.md directories
  prompts/            optional prompt templates
```

### The manifest

```yaml
name: tasks                    # lowercase, a-z 0-9 and hyphen
version: 0.1.0
description: A todo list the model can edit, with a panel above the editor.
nova: ">=0.1"                  # compatible Nova versions

run: ["./bin/tasks"]           # how to start it. Any language.
build: ["go", "build", "-o", "bin/tasks", "."]   # optional; run at install and reload
restart: { max: 3 }

capabilities:                  # what it asks for; the user approves at install
  - tools
  - commands
  - hooks:turn_end
  - ui:widget
  - session:append_entry

# Declarative tools need no process at all. See below.
tools:
  - name: todo
    description: Manage the task list.
    parameters: { type: object, properties: { action: { type: string } }, required: [action] }
```

`nova ext check ./my-ext` validates the manifest and starts the process to confirm it
registers cleanly.

### Declarative tools

Many tools are a description plus a command. These need no protocol code.

```yaml
tools:
  - name: word_count
    description: Count words in a file.
    parameters:
      type: object
      properties: { path: { type: string, description: "File to count" } }
      required: [path]
    exec: ["wc", "-w", "{{.path}}"]
    read_only: true
    timeout_ms: 5000
    render: "{{.path}}: {{.output}}"
```

The command is run directly, **without a shell**, with arguments substituted from the
parsed JSON. A value is never interpolated into a shell string. This is the safe
default and the reason declarative tools are preferred for simple cases.
The command itself (the first element of `exec`) is fixed when the manifest loads
and may not contain a placeholder, so a model-supplied value can choose arguments
but never the program.

### Command hooks

For a rule that is just a script, use a command hook, in config or a manifest:

```yaml
hooks:
  - on: tool_call
    match: { tool: bash }
    run: ["./scripts/check-bash.sh"]     # JSON event on stdin, JSON decision on stdout
```

The script reads the event on stdin and may print `{"block":true,"reason":"…"}`.
This is the same idea as Crush's and Claude Code's hooks.

## Writing a subprocess extension

Any program that speaks the protocol works. For Go there is a small SDK that handles
framing, correlation, deadlines, and panics:

```go
package main

import "github.com/AutumnsGrove/nova/ext/sdk"

func main() {
    sdk.Run(func(h *sdk.Host) error {
        h.Tool(sdk.Tool{
            Name:        "hello",
            Description: "Greets someone.",
            Params:      sdk.Object(sdk.String("name", "Who to greet", sdk.Required)),
            ReadOnly:    true,
        }, func(c *sdk.Call) (sdk.Result, error) {
            return sdk.Text("Hello, " + c.String("name") + "!"), nil
        })

        h.Command("hello", "Say hello", func(c *sdk.Command) error {
            return c.UI.Notify("Hello from an extension")
        })

        // Every hook has the same shape: decode the event you care about with
        // e.Decode(&v), and return nil to leave the loop alone or a *sdk.HookDecision.
        h.On("turn_end", func(e *sdk.Hook) (*sdk.HookDecision, error) { return nil, nil })
        return nil
    })
}
```

Extensions should follow these rules, which the SDK makes easy and `nova ext check`
partly verifies:

- **Do nothing at startup except register.** Start long-lived work from
  `session_start` or from the command or tool that needs it.
- **Release resources on `session_shutdown`**, and make that idempotent.
- **Keep state in the session** with `append_entry`, so `/reload` and resume rebuild
  it. Do not keep it only in memory.
- **Truncate large tool output** and say where the full output is.
- **Write errors as results**, not crashes. A tool returns `is_error: true` with a
  message the model can act on.

## Install, update, remove

```
nova ext install github.com/AutumnsGrove/nova-extensions@v0.1.0
nova ext install ./path/to/ext            # a local directory, linked, not copied
nova ext list
nova ext update [name]
nova ext remove name
nova ext check ./path/to/ext
```

- **No registry server.** `install` takes a git URL (`https://` or ssh, never plain
  `http://`; GitHub shorthand allowed) and an optional tag, branch, or commit. It clones to `~/.config/nova/extensions/<name>/`.
- **A repository may hold several extensions.** Each subdirectory with an
  `extension.yaml` is one.
- **Pinned.** The resolved commit SHA goes in `~/.config/nova/extensions.lock`.
  `update` moves the pin and shows the diff of the manifest and requested
  capabilities first.
- **Trust prompt.** Install shows the name, source, commit, `run` and `build`
  commands, and capabilities, and asks to continue. Headless installs need
  `--yes`.
- **Build.** If `build` is set it runs at install and on `update`. A failed build
  aborts the install and leaves nothing behind.
- **Discovery.** `nova ext search <terms>` lists GitHub repositories with the topic
  `nova-extension`, and the entries of `index.json` in this repository. The index
  is a plain curated list, not a gate: any git URL installs.
- **Project extensions** live in `.nova/extensions/` and load only in a trusted
  project (see [`ARCHITECTURE.md`](ARCHITECTURE.md) section 9).

### What "capabilities" do and do not mean

Capabilities limit what an extension may ask the **host** to do: register tools,
call the model, write session entries, show UI. The host enforces that.

They do **not** sandbox the process. An extension runs with the user's operating
system permissions and can read files and use the network regardless of its manifest.
The `network`, `fs`, and `exec` capabilities exist so a manifest can say what it
intends and the install prompt can show it. Treat installing an extension like
installing any program. For stronger isolation, run Nova in a container.

## Reload

`/reload` (or the `reload` command):

1. stops every extension process (`shutdown`, then kill after a grace period);
2. re-reads config, skills, prompt templates, and manifests (see below for what a config reload changes);
3. rebuilds extensions whose source changed, if they declare `build`;
4. starts the processes and replays each one's `keep` entries so it can rebuild state;
5. emits `state_changed` and re-lists commands and tools.

A config reload applies the permissions, the active tools and the `bash` settings, the
compaction, retry and turn limits, the display, the system prompt extras, and the model,
thinking level and context limit. Anything you set while running (`/model`,
`set_thinking`, `set_context_limit`, `set_tools`) stays on top of the file. A config that
does not load, or names a tool that does not exist, changes nothing and is reported in
`config_error`. The `extensions.*` settings and the config the bundled extensions read at
start need a restart.

A reload during a run waits for the run to end. A reload that fails for one
extension, including a failed rebuild, leaves the others running and reports the
failure; a failed rebuild is tried again on the next reload.

## Letting Nova write its own extensions

This is a first-class use, so it is designed for.

- `nova docs [topic]` prints the embedded documentation. The binary includes
  `EXTENSIONS.md`, `PROTOCOL.md`, and the example extensions, so no network is needed.
- A bundled skill, `extension-author`, tells the model the workflow: read the docs,
  copy the nearest example, write `extension.yaml`, run `nova ext check`, then
  `/reload`.
- The authoring context is deliberately small: the protocol, this document, the SDK
  signatures, and about ten short examples fit in well under 40,000 tokens.
- `examples/extensions/` holds one small example per capability: a declarative tool,
  a command hook, a Go tool, a status segment, a widget, a form, a hook that blocks, a
  provider. Each is under 100 lines and has a test.

## Bundled extensions

In-tree Go packages under `bundled/`. Each is on by default and can be turned off in
config (`extensions.disable: [plan]`).

| Name | What it does | Inspired by |
|---|---|---|
| `tasks` | A `todo` tool (create, update, delete, list) with a live panel above the editor and a `/todos` view. State replays from the session. | `pi-tasks` |
| `ask` | A model-facing `ask` tool: a multi-question form with single and multi select, free text, per-question notes, optional previews, and skip. | `pi-q-n-a` |
| `plan` | Plan mode: the model plans with read-only tools, the user approves, then it executes. A status indicator shows the mode. | Pi's `plan-mode` example |
| `subagent` | A `subagent` tool that runs a fresh agent loop with its own context and a restricted tool set, and returns its summary. | Pi's `subagent` example |
| `webfetch` | A keyless `web_fetch` tool: HTTP GET, HTML to markdown, size caps, redirect and host limits. | `parallel`'s `web_fetch`, without the key |

Native features that were extensions in Pi are in the core, not here: the thinking
block, the context limit, the cost and usage status region, OpenRouter provider routing,
and the permission policy.

OpenCode Go is no longer a suggested extension. D56 makes it a core launch provider
beside OpenRouter, with its own catalog and its Responses and Messages dialects (C20).

## Suggested, not bundled

Official or recommended extensions, installed on request.

| Name | Why not bundled |
|---|---|
| `semantic-search` | Heavy: tree-sitter chunking, SQLite with vector search, an embedding service. Genuinely useful, so it is the first recommended install. |
| `parallel` | Needs an API key and has a quota. A better `web_search`. Search is not built into the core. |
| `git-checkpoint` | Opinionated about how you use git. |
| `cost-tracker` | Reads the usage ledger to attribute cost to a task. The ledger is core; the reporting is optional. |

## Bundled extension specs

Each bundled extension has a chunk spec with a test matrix (see
[`archive/chunks/C18-bundled-extensions.md`](archive/chunks/C18-bundled-extensions.md)). Their behaviour is defined by those
specs, not by this table.
