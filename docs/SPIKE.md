# Nova — M0 Spike

An early prototype of the Nova **backend** spine was built at the repository root
to de-risk the loop, provider, and tool interfaces before writing the real plan.
It is **exploratory**: its API is not frozen, and it does not implement the full
target architecture. Treat it as executable documentation.

The prototype is backend-only. The `cmd/nova-run` in it is a throwaway line REPL, not
the real frontend, which will live in a separate `nova-tui` module (M3).

## What is present

```
message/                 canonical transcript types
provider/                Provider interface, registry, model catalog
  openaicompat/          OpenAI chat completions + SSE + tool calls
  openrouter/            OpenRouter preset
tool/                    Tool interface, registry, annotations
tools/fs/                read, write, edit, list
tools/bash/              bash (timeout, output cap)
agent/                   loop, Config, hooks, typed events
cmd/nova-run/                throwaway headless CLI (single prompt or REPL)
```

(No frontend module is present yet; the real coding agent lands in M3 as a
separate `nova-tui` module.)

~2,300 lines including tests. `go vet ./...`, `go build ./...`, and
`go test ./...` are clean.

## What it proves

- **The loop works.** `agent.Run` performs request → tool calls → results →
  repeat, with `Sequential` and `Parallel` dispatch, order preservation, and
  per-call panic recovery.
- **The provider boundary is right.** `provider.Provider` is a single `Stream`
  method; `openaicompat` implements it with only the standard library and parses
  text, reasoning, tool-call deltas, usage, and OpenRouter's `cost` field.
- **Tools are easy to add.** `tool.New(Info, Func)` plus a registry; the core
  tools are ordinary tools, not special cases.
- **Hooks absorb domain behavior.** `BeforeToolCall`, `AfterToolCall`,
  `FinishTurn`, `TransformContext`, and `OnEvent` are enough to express the
  behaviors her-go and polaris need, without core changes.
- **A CLI is cheap.** `cmd/nova-run` wires OpenRouter + core tools into a working
  coding agent in ~200 lines.
- **It is testable.** A scriptable fake provider drives deterministic loop tests.

## Try it

```sh
cd nova
export OPENROUTER_API_KEY=sk-or-...
go run ./cmd/nova-run -p "what's in this directory?"
go test ./...
```

## Deltas from the target architecture

The prototype is intentionally narrower than [`ARCHITECTURE.md`](ARCHITECTURE.md):

| Area | Prototype | Target |
|---|---|---|
| YAML manifests | none | `tool.yaml` loader (M1) |
| Tool annotations | present on `Info` | surfaced to a permission hook and dispatch policy |
| Search tools | none | `glob`, `grep` (M1) |
| Sessions | none | JSONL tree + `session.Manager` (M2) |
| Compaction | none | summarize-and-splice (M2) |
| Context files | none | project instructions in the system prompt (M1) |
| TUI | line-oriented REPL | `charm.land` TUI (Bubble Tea, Lipgloss, Bubbles, Glamour) (M3) |
| Extensions | none | `Extension` + `Host`, MCP (M4) |
| Message content | text only | images/content parts (M4) |
| Steering queues | none | pi-style steer/follow-up (M2) |
| Model catalog | exists, barely used | drives model picker and cost fallback |
| Per-run tool filtering | none | `Config.FilterTools` (M1) |

## Known limitations

- The message model carries text only; images and rich content parts are not
  representable.
- `openaicompat` assumes streaming is available; there is no non-streaming
  fallback.
- No token estimation or context accounting, so no compaction trigger.
- The CLI keeps a single in-memory history; nothing persists between processes.
- The CLI is a plain line REPL, not the crafted `charm.land` TUI planned for M3;
  the real frontend will follow crush's design language.
- Provider behavior is only exercised against a fake SSE server (and optionally
  a live OpenRouter call), not against a compatibility matrix.

## What to keep

The interfaces in the prototype are the ones proposed in
[`ARCHITECTURE.md`](ARCHITECTURE.md): the `Provider`/`Tool`/`agent.Config`
shapes, the OpenAI-shaped transcript, and the hook set. When the real build
starts, each becomes the basis for its milestone package rather than a throwaway.
