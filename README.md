<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo/nova-mark-dark.svg">
    <img alt="Nova" src="assets/logo/nova-mark-light.svg" width="280">
  </picture>
</p>

# Nova

> A minimal, pi-and-crush-inspired agent harness in Go — built to be embedded in
> Go applications (her-go, polaris, ivy) or run standalone as a CLI/TUI.

**Status: planning.** The design is documented and all twenty implementation chunks
are specified in full, each with a test matrix. No product code is being written yet; the repository also holds an early
M0 prototype at the repository root (see [`docs/SPIKE.md`](docs/SPIKE.md)). The prototype is
exploratory and its API is not frozen.

## What Nova is

**One Go binary, `nova`, that is an agent harness.** The same binary is:

- an interactive **coding agent** in the terminal (a Bubble Tea TUI, modelled on
  [crush](https://github.com/charmbracelet/crush));
- a **headless** agent for scripts and CI (`nova -p "…"`, `--mode json`);
- a **GitHub Action** that reviews pull requests (`nova action review`);
- a **backend** for a website or another program (`--mode rpc` on stdio, `nova serve` over HTTP);
- a **Go library** another program can import.

Every one of those is a front door onto a single JSONL protocol
([`docs/PROTOCOL.md`](docs/PROTOCOL.md)), so they share one implementation and one set of
tests. The core stays small and grows by **extension**, in the spirit of
[pi](https://github.com/earendil-works/pi): an extension is any program that speaks the protocol,
installs from git, and can be written by Nova itself and loaded with `/reload`
([`docs/EXTENSIONS.md`](docs/EXTENSIONS.md)).

Build it two ways: `go build ./cmd/nova` for everything, or `-tags notui` for a minimal headless
binary with no UI dependencies.

## Layout

One module, `github.com/AutumnsGrove/nova`. The core never imports the UI; a check enforces it.
See [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) for the full tree and line budgets.

```
message/ provider/ auth/ tool/ tools/ permission/ proto/ agent/ session/
compaction/ usage/ config/ mode/ ext/ testkit/      the backend (no UI dependencies)
bundled/                                           in-tree extensions (use charm.land)
tui/                                               the terminal UI (charm.land)
cmd/nova/  scripts/  assets/  docs/
```

## North star

One day, a **full coding harness on par with pi** — our own, same principles. It
is reached in two pieces: the **backend** grows into a complete primitive layer
(loop, tools, sessions, compaction, extensions), and the **frontend** grows into
a polished daily coding agent on the charm.land stack. Any Go program can embed
the backend; only the frontend pays for the UI.

## Why

her-go, polaris, and ivy each implement (or are about to implement) the same
agentic substrate: an LLM tool-calling loop, a tool registry, tool descriptions,
session/context management, and streaming. her-go already extracted a shared
`agent_engine`; polaris has a large bespoke research loop; ivy's "Ask Ivy" loop
is planned but unbuilt. Nova is that substrate, factored out once, so a new Go
program can add an agent loop by importing a package instead of recreating the
wheel.

## Documents

| Document | Contents |
|---|---|
| [`docs/PLAN.md`](docs/PLAN.md) | Vision, goals, principles, and the roadmap. |
| [`docs/DECISIONS.md`](docs/DECISIONS.md) | Every locked decision, with rationale and what was rejected. |
| [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) | Layout, layers, core types, the loop, sessions, modes. |
| [`docs/PROTOCOL.md`](docs/PROTOCOL.md) | The one JSONL protocol every front door speaks. |
| [`docs/EXTENSIONS.md`](docs/EXTENSIONS.md) | Extension tiers, manifest, install over git, bundled and suggested extensions. |
| [`docs/TUI.md`](docs/TUI.md) | The terminal UI: stack, layout, components, themes, splash. |
| [`docs/TESTING.md`](docs/TESTING.md) | Test layers, the failure-mode catalog, the matrix rule, definition of done. |
| [`docs/IMPLEMENTATION.md`](docs/IMPLEMENTATION.md) | The chunk plan and how agents build it. |
| [`docs/chunks/`](docs/chunks/) | One spec per chunk (C00 to C19), each with a test matrix. |
| [`AGENTS.md`](AGENTS.md) | Instructions for the agents that implement the chunks. |
| [`docs/BRAND.md`](docs/BRAND.md) | The two logo marks, the palette, and how to regenerate them. |
| [`docs/DESIGN.md`](docs/DESIGN.md) | The ecosystem survey and what we borrow from where. |
| [`docs/INTEGRATION.md`](docs/INTEGRATION.md) | How her-go, polaris, and ivy embed Nova. |
| [`docs/SPIKE.md`](docs/SPIKE.md) | What the M0 prototype proves, and its known limitations. |
| [`docs/BRAND.md`](docs/BRAND.md) | The two logo marks, the palette, and how to regenerate them. |

## Name

**Nova** — a star that suddenly brightens. Astral, short, and in the same family
as `polaris`, `her-go`, and `ivy`. The module path is
`github.com/AutumnsGrove/nova`.

## Inspirations

Two projects shape Nova:

- **[pi](https://github.com/earendil-works/pi)** — the *architecture*. A small
  agent loop, hooks at every stage, a tool/provider extension model, a session
  tree, and a minimal core that grows rich through extension.
- **[crush](https://github.com/charmbracelet/crush)** — the *experience*. A
  gorgeous, fast, keyboard-driven terminal agent built on the `charm.land`
  libraries. Nova uses the same stack for its frontend so the result feels as
  good as it works.

## Lineage

Nova borrows deliberately:

- from **pi** — the agent loop and hooks, the tool/provider extension model, the
  session tree, and the minimal-core philosophy;
- from **crush / charm.land** — the terminal design language and the frontend
  stack (Bubble Tea, Lipgloss, Bubbles, Glamour, Log);
- from **her-go** — the shared loop with lifecycle hooks, YAML tool manifests,
  continuation windows, and the OpenAI-compatible client shape;
- from **polaris** — concurrent tool dispatch with panic isolation, pseudo
  tool-call recovery, and research nudges as hooks;
- from **ivy** — the provider-as-chokepoint idea (its `llm.Gate` enforces
  policy, caps, and ledger as a provider decorator).
