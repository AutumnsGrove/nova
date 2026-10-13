# Nova extension SDK

`github.com/AutumnsGrove/nova/ext/sdk` is the Go SDK for a subprocess extension.
It handles the JSONL framing, request correlation, deadlines, and panic recovery,
so an extension only describes its tools, commands, and hooks.

## Minimal extension

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
            return sdk.Text("Hello, %s!", c.String("name")), nil
        })
        return nil
    })
}
```

## What the host registers

- `h.Tool(sdk.Tool{...}, fn)` — a model-callable tool. `sdk.Object`, `sdk.String`,
  `sdk.Int`, `sdk.Bool`, and `sdk.Number` build the JSON schema. `sdk.Required`
  marks a field required.
- `h.Command(name, description, fn)` — a slash command. The handler gets a
  `*sdk.Command` with `Write` and `UI`.
- `h.On(hook, fn)` — observe or change the loop. Return a `*sdk.HookDecision`.
- `h.Flag(name, description, def)` — add a CLI flag.
- `h.AppendEntry(kind, data, keep)` — append a session entry; `keep` entries are
  replayed to the extension on reload.
- `h.Entries(kind)` — the replayed entries of one kind.
- `h.Complete(ctx, req)` — ask the model to complete a request.

## UI

`c.UI` (and `h.UI` from a command) shows fixed components that fit the TUI:
`Notify`, `Status`, `Widget`, `Confirm`, `Select`, and `Input`. The host refuses
a UI call whose capability the manifest did not declare.

## Rules

- Register only at startup; start work from a tool, command, or hook.
- Keep state in session entries, not memory, so reload and resume rebuild it.
- Truncate large tool output and say where the full output lives.
- Return errors as results (`is_error: true`), never panic.
