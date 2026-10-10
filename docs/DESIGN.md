# Nova — Design

This document records the decisions behind Nova and the exploration that led to
them. It is the "why" companion to [`ARCHITECTURE.md`](ARCHITECTURE.md) (the
"how") and [`PLAN.md`](PLAN.md) (the "what/when").

## Decision log

Moved to [`DECISIONS.md`](DECISIONS.md), which also records what was superseded on
2026-10-09 (the two-program split, the two-module layout, compile-time-only extensions).

## Ecosystem survey

We evaluated embedding an existing Go harness instead of writing our own. All of
the following are *libraries* (except crush), so "adopt" was genuinely on the
table.

| Project | What it is | Strengths | Why we didn't adopt |
|---|---|---|---|
| [pi](https://github.com/earendil-works/pi) | TypeScript coding agent + `pi-agent-core` + `pi-ai` | The design we're modeling: small loop, hooks, session tree, extensions, great UX | Not Go; a Node process |
| [crush](https://github.com/charmbracelet/crush) | Full Go TUI coding agent | Battle-tested, polished, rich tools, best-in-class terminal UX | An *application*: SQLite, LSP, TUI, pub/sub — not an embeddable core. We adopt its **design language and frontend stack**, not its code |
| [charm.land/fantasy](https://pkg.go.dev/charm.land/fantasy) | The agent/provider library behind crush | Real-world hardened, good provider coverage, streaming callbacks | Only the LLM/loop layer — no tools, sessions, or compaction; we'd build the whole harness on top anyway, inheriting its types |
| [sausheong/harness](https://github.com/sausheong/harness) | Reusable Go agentic platform | Closest to pi out of the box: registry, file/bash tools, sessions, compaction, sub-agents, MCP, 6 providers, MIT | Young (v0.6.x, ~12 stars), API still shifting; adopting means rewriting our tools and LLM clients to its interfaces |
| [looprig/harness](https://pkg.go.dev/github.com/looprig/harness) | Typed, durable agent runtime | Sophisticated: actors, journal, gates, restore, events | Pre-v1, heavy multi-module ecosystem; overkill for our scope |
| [lox/agent-harness](https://github.com/lox/agent-harness) | Minimal `Run()`-style library | Very small | No core tools, no compaction, no session store; still requires building the harness |
| [jgabor/go-agent](https://github.com/jgabor/go-agent) | Embeddable single-import harness | Clean facade, policies, sessions | Young; OpenAI-compatible only; sub-agents explicitly out of core |
| [goframe/agent](https://pkg.go.dev/github.com/sevigo/goframe/agent) | Think-act-observe loop | Middleware, governance | Heavier framework; tighter coupling |
| [hax](https://github.com/OleksandrChekhovskyi/hax) | C coding agent | Tiny, fast | Intentionally *not* a plugin/library framework; not Go |

**Verdict:** none of them is simultaneously (a) embeddable, (b) feature-complete
out of the box, (c) mature, and (d) shaped like our habits. `fantasy` is the most
battle-tested engine but gives us no harness; `sausheong/harness` is the most
complete but youngest and would force a rewrite of our tool and client layers.
Building our own is the smaller, more certain path — and the north star (our own
pi-class coding agent) requires owning the core regardless.

Revisit this if `sausheong/harness` matures: its feature set is the closest match
and could replace parts of our provider/session layers later.

## What we borrow from where

### From pi

- The **agent loop** shape: request → stream → tool calls → results → repeat.
- **Hooks at every stage**: `beforeToolCall`, `afterToolCall`, `finishTurn`,
  `prepareRequest`, `transformContext`.
- The **tool registration model**: name, description, JSON Schema, execution,
  plus `exposure` and MCP-style annotations.
- The **provider registry** and `models.json`-style compatible-endpoint config.
- The **session tree** (JSONL entries with parent links) and compaction that
  inserts a summary entry without destroying history.
- The **minimal-core philosophy**: small to start, rich when extended.

### From crush and charm.land

- The **terminal design language**: a calm, information-dense transcript; clear
  tool-call cards; streaming that feels alive; restrained color; consistent
  spacing; keyboard-first navigation.
- The **frontend stack**: `charm.land/bubbletea/v2` (event loop and rendering),
  `charm.land/lipgloss/v2` (layout and style), `charm.land/bubbles/v2`
  (editor, viewport, list, spinner), `charm.land/glamour/v2` (markdown),
  `charm.land/log/v2` (logging). Later: `charm.land/fang` for CLI help and
  `charm.land/catwalk` for golden tests, matching crush's tooling.
- The **quality bar**: polish and performance are features, not afterthoughts.

Nova does **not** take crush's architecture (SQLite/SQLC/LSP/pub-sub). It takes
crush's *taste* and the libraries that express it, applied to a much smaller core.

### From her-go

- The **shared loop with lifecycle hooks** (`agent_engine.EngineConfig`:
  `OnLoopStart`, `OnDone`, `OnLoopExit`, `PreTool`, `PostToolResult`, `PostTool`,
  `ContinuationMsg`).
- **YAML tool manifests** (`tools/<name>/tool.yaml`) with
  description/hot/category/parameters/trace, and the loader that compiles them to
  provider tool definitions.
- **Continuation windows**: max iterations per window, then a new window seeded
  with a progress summary. Pi doesn't have this; it's genuinely useful for
  long-running agents.
- The **OpenAI-compatible client** behavior: fallback model, streaming, cached
  token accounting, cost.

### From polaris

- **Concurrent tool dispatch** that preserves result order and recovers per-call
  panics.
- **Pseudo tool-call recovery**: models that emit tool-call syntax as text get a
  real dispatch and a corrective message.
- **Research nudges** as loop-level hooks rather than core logic.
- The idea of a **catalog of tool descriptions** separate from the tool code.

### From ivy

- **Provider as the single chokepoint** for policy, caps, vetting, and ledger.
  Nova leaves the loop ignorant of this; ivy wraps the provider.
- **Feature gating by name** (`llm.Gate`'s `features` table) as a pattern for
  what must be impossible to forget.
- **Read-only tools with hard caps** as the default posture for a first agent.

## Backend/frontend split

**Superseded.** This section argued for two programs and two Go modules. The decision changed:
Nova is one binary and one module; the UI lives in the `tui/` package, is excluded by
`-tags notui`, and a check stops the core importing it. See [`DECISIONS.md`](DECISIONS.md)
(D11, D12, D14, D15). The dependency-isolation goal is kept; only the mechanism changed.

## Provider design

Why OpenRouter first, on top of a generic OpenAI-compatible implementation:

1. OpenRouter is what we actually use. It aggregates everything and speaks
   OpenAI chat-completions, so one implementation covers the majority of models.
2. The generic `openaicompat` provider also covers Ollama, LM Studio, vLLM,
   LiteLLM, and most self-hosted endpoints — the "compatible endpoint" tail is
   free.
3. A tiny `Provider` interface means a bespoke wire protocol (Anthropic Messages,
   Bedrock, a private gateway) is one file, not a refactor.
4. Provider **decorators** give us policy/cap/ledger insertion (ivy) and
   observability (logging, traces) without the loop knowing.

Model metadata (stable id, wire slug, context window, pricing) is **code-defined**
(the Polaris pattern), so a stored choice keeps resolving even when a provider
renames a slug, and cost accounting has a fallback when a response reports none.

## Extension mechanism

**Superseded.** This section chose compile-time Go registration plus MCP. The decision changed:
extensions are subprocesses speaking the same JSONL protocol as every other client, plus
declarative YAML tools and command hooks; bundled extensions are in-tree Go. Embedded
interpreters (Yaegi, goja, wasm) were rejected. See [`EXTENSIONS.md`](EXTENSIONS.md) and
[`DECISIONS.md`](DECISIONS.md) (D5, D16 to D20).

## Naming

**Nova** — a star that suddenly brightens. Astral like `polaris`, short,
one word, and not overloaded. Display form is `Nova` (capital N only); the module
path is lowercase `github.com/AutumnsGrove/nova`.
