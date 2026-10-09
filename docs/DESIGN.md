# Nova — Design

This document records the decisions behind Nova and the exploration that led to
them. It is the "why" companion to [`ARCHITECTURE.md`](ARCHITECTURE.md) (the
"how") and [`PLAN.md`](PLAN.md) (the "what/when").

## Decision log

| # | Decision | Choice | Rationale | Alternatives rejected |
|---|---|---|---|---|
| D1 | Core engine | Own, modeled on pi | Full control, no upstream churn, matches our habits; the loop is the smallest and most reusable piece | Adopt `fantasy`; adopt `sausheong/harness`; fork crush |
| D2 | v1 scope | Loop + registry + core tools + sessions + compaction + TUI | A harness that can't remember and can't be driven isn't useful as a daily coding agent | Loop-only library first |
| D3 | Tool authoring | Both Go registration and YAML/Markdown manifests | Go for logic, YAML for description/schema/agent-scoping/trace; matches her-go | Go-only; YAML-only |
| D4 | Provider | Small interface + registry; OpenRouter first over a generic OpenAI-compatible implementation | OpenRouter is what we actually use; everything else compatible is nearly free | Hard-code one client; adopt fantasy providers |
| D5 | Extensions | Compile-time in-process Go registration; MCP for external tools | Type-safe, zero runtime deps, idiomatic Go; MCP covers other languages | Go `.so` plugins; Starlark/JS scripting in v1 |
| D6 | Distribution | Separate versioned Go module (`github.com/AutumnsGrove/nova`) | One source of truth for three consumers | Vendored copy per repo (drifts) |
| D7 | First adopter | Nova standalone first | Prove the harness as a coding agent before touching production loops | Integrate ivy/her-go first |
| D8 | Name | **Nova** | Astral, short, in the family with `polaris`; only the N capitalized | Aster, Canopy, Cirrus, Loom |
| D9 | North star | A full pi-class coding harness, our own, same principles | The library and the standalone agent are the same artifact | Keep it a pure library |
| D10 | Frontend stack | `charm.land` libraries (Bubble Tea, Lipgloss, Bubbles, Glamour, Log), designed after crush | Best-in-class terminal UX, proven at scale by crush; we get the craft without writing a renderer | Hand-rolled ANSI; a web UI; a different TUI toolkit |
| D11 | Backend/frontend split | Two separate programs: a UI-free backend (library + headless CLI) and a frontend coding agent that imports it | Programs that embed an agent loop pay for nothing UI-related; the frontend can evolve fast without destabilizing the backend | One binary; UI inside the core; `tui/` as a backend subpackage |
| D12 | Repo layout | Monorepo, two Go modules (`github.com/AutumnsGrove/nova` at root, `github.com/AutumnsGrove/nova/tui` under `tui/`), tied with `go.work` | Backend consumers never see charm deps in their module graph; one repo keeps the pair in lockstep | One module with `cmd/` binaries; two repos |
| D13 | Transport | In-process library now, with an RPC-ready seam; backend also ships a headless CLI | Simplest and fastest today; a daemon/RPC mode (remote or multiple frontends) can be added later without touching the frontend | Build RPC first; library-only with no seam |

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

Nova is two programs, not one, and the split is load-bearing.

**Why two programs:**

- **Dependency isolation.** The backend is embedded by her-go, polaris, and ivy.
  If the TUI lived in the backend module, every embedder's module graph would
  carry Bubble Tea, Lipgloss, and friends even though they never render a frame.
  Two modules means a backend consumer's `go.mod` never mentions a UI library.
- **Independent cadence.** The frontend is where visual iteration happens fast;
  the backend is where the stable primitives live. Separate modules keep a TUI
  redesign from touching the loop.
- **A clean contract.** The frontend can only use the backend's exported API, so
  the API stays honest. That same constraint is what makes an RPC mode feasible
  later.
- **Two audiences.** Embedders want a library; users want an app. One artifact
  serving both tends to serve neither well.

**What lives where:**

| Concern | Backend (`nova`) | Frontend (`nova/tui`) |
|---|---|---|
| Agent loop, hooks, events | ✓ | consumes |
| Providers, tools, sessions, compaction | ✓ | consumes |
| Headless CLI, JSON/print output | ✓ | |
| Events → UI state | | ✓ |
| TUI, renderers, themes, commands | | ✓ |
| `charm.land` dependencies | | ✓ |

**The seam:** the frontend depends on a small `Session` interface (see
[`ARCHITECTURE.md`](ARCHITECTURE.md#11-the-seam-between-them)) and a stream of
serializable `Event`s — not on the loop or its internals. In-process today; the
same interface can be backed by an RPC client later.

**Layout:** a monorepo with two modules tied by `go.work`. The repo is
`github.com/AutumnsGrove/nova`. The backend module is the repo root,
`github.com/AutumnsGrove/nova`; the frontend is the nested module
`github.com/AutumnsGrove/nova/tui`, living in the `tui/` subdirectory. A
`replace`/`go.work` entry points the frontend at the local backend during
development; once tagged, it is a normal dependency.

Because the frontend is a nested module, its release tags are prefixed by the
subdirectory: `tui/v0.1.0`, not `v0.1.0`. The backend keeps unprefixed tags
(`v0.1.0`). Each module versions independently.

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

Decision: **compile-time in-process Go registration, plus MCP for external
tools.**

- Go's `plugin` package (`.so`) is toolchain- and platform-locked and cannot be
  recommended.
- An embedded scripting host (Starlark or JS via goja) is the closest analogue to
  pi's TypeScript extensions, but it adds a runtime and a large host-API surface
  before the API is stable. Deferred, not rejected.
- MCP is the ecosystem standard for out-of-process tools and gives us tools
  written in any language. It slots into the same registry.
- An in-process `Extension` interface calling `Host.RegisterTool/Provider/
  Command/On` is idiomatic, type-safe, and exactly how her-go already registers
  tools.

The `Host` API is designed so a scripting host can be layered on later without
changing extension authors' code.

## Naming

**Nova** — a star that suddenly brightens. Astral like `polaris`, short,
one word, and not overloaded. Display form is `Nova` (capital N only); the module
path is lowercase `github.com/AutumnsGrove/nova`.
