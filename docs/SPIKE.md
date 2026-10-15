# Nova: the M0 spike (historical)

Before the real plan was written, a small backend-only prototype was built to de-risk three interfaces: the
agent loop, the provider, and the tool. It is **finished and superseded**. This file records what it proved
and where each idea lives now. It is not a description of how Nova works today; for that, read
[`ARCHITECTURE.md`](ARCHITECTURE.md).

## What it proved

- **The loop works.** Request, tool calls, results, repeat, with sequential and parallel dispatch, result order
  preserved, and a panic in one call contained to that call.
- **The provider boundary is right.** `provider.Provider` is a single `Stream` method. An OpenAI-compatible
  provider needed only the standard library to parse text, reasoning, tool-call deltas, usage, and OpenRouter's
  `cost` field.
- **Tools are easy to add.** `tool.New(Info, Func)` plus a registry; the core tools are ordinary tools, not
  special cases.
- **Hooks can absorb domain behaviour** without changes to the core.
- **A scripted fake provider** makes the loop deterministic to test.

## Where it went

Everything the spike listed as missing has since been built. The interfaces it proposed became the real ones.

| The spike lacked | Today |
|---|---|
| Sessions, compaction, steering queues | `session/` (JSONL tree), `compaction/`, and the steering and follow-up queues in `agent/` |
| Search tools, context files | `tools/search` (`glob`, `grep`); context files, skills, and prompt templates in `config/` |
| A TUI | `tui/` (Bubble Tea, Lipgloss, Bubbles, Glamour) |
| Extensions | `ext/` (the host, subprocess extensions, `ext/install`) and `bundled/` |
| Permissions | `permission/` (allow, ask, deny) |
| Other front doors | `mode/`: `print`, `json`, `rpc`, `serve`, and the GitHub `action` |
| Other providers | `provider/responses`, `provider/messages`, and the `openrouter` and `opencodego` presets, all on `provider/ssestream` |

The hook set grew and was renamed. Today it is `BeforeProviderRequest`, `TransformContext`, `BeforeTurn`,
`MessageEnd`, `ToolCall`, `ToolResult`, `TurnEnd`, and `AgentEnd` (see `agent.Hooks`).

## Limits that no longer apply

The spike carried text-only messages, no non-streaming fallback, no token accounting, no persistence, and a
line REPL instead of a TUI. The first of these still holds: message content is text only, and image and
document parts are deferred (`DECISIONS.md`). The rest were fixed.

## What is left of it

`cmd/nova-run` (about 170 lines) is still in the tree. It is a throwaway headless runner from the spike and is
not part of the product; `cmd/nova` is. It still builds. The old plan said the CLI chunk would delete it, and
that did not happen, so removing it is open work, not something this file promises.

The chunk specs in `docs/archive/chunks/` list the prototype defects that the test matrices were written to
prevent (panic recovery only in parallel mode, `bash` leaving child processes running, and others).
