<p align="center">
  <img alt="Nova: a slash-lettered nova wordmark on the left and a star igniting on the right, over a field of stars" src="assets/splash/nova-splash.gif" width="672">
</p>

# Nova

> A minimal, pi-and-crush-inspired agent harness in Go — built to be embedded in
> Go applications (her-go, polaris, ivy) or run standalone as a CLI/TUI.

**Status: iterating.** The initial build-out is finished: the agent loop, the core tools, sessions,
compaction, config, the JSONL protocol, every mode (TUI, print, JSON, RPC, serve, the GitHub Action), the
extension host and bundled extensions, two launch providers (OpenRouter and OpenCode Go), in-app login, release
packaging, and `nova update`. Work now is making it better: bugs, TUI and UX polish, provider work, and hardening
the framework. It is tracked as [GitHub issues](https://github.com/AutumnsGrove/nova/issues) and done
test-first (see [Working on Nova](#working-on-nova)).

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
message/ media/ clipboard/ diag/ provider/ auth/ tool/ tools/
permission/ proto/ agent/ session/ compaction/ usage/ config/ mode/ ext/
update/ doctor/ testkit/                                        the backend (no UI dependencies)
bundled/                                                        in-tree extensions (use charm.land)
tui/                                                            the terminal UI (charm.land)
cmd/nova/  scripts/  assets/  examples/  docs/
```

## Working on Nova

Work arrives as issues and is done **test-first**: write the failing test, make it pass, then run the gate.

```
make ci            # fmt, vet, build (full and -tags notui), race tests, import rules, solved-area guards
make test          # the fast loop: go test -short
```

[`AGENTS.md`](AGENTS.md) is the guide for anyone, human or agent, changing the code: how a change goes, the
test-driven rules, the repo map, the hard rules, and the definition of done. [`CONTRIBUTING.md`](CONTRIBUTING.md)
is the short version.

## When something goes wrong

A crash never exits silently. On a panic Nova restores the terminal, prints a
one-line error, and writes a report — the version, mode, model, last operation,
and a full stack — to `<config dir>/nova/crash.log` (on macOS and Linux that is
`~/.config/nova/crash.log`). The path is printed with the error, `nova doctor`
shows it, and `nova log` prints the most recent report (`nova log --path` prints
the file). In the TUI, `/log` opens it in a dialog.

To record routine activity for a crash you are trying to reproduce, run with
`--debug` or set `NOVA_DEBUG=1`; traces go to `<config dir>/nova/debug.log`.

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
| [`docs/IDE.md`](docs/IDE.md) | Proposal: a graphical IDE front door, the protocol additions it needs, and a milestone plan. |
| [`docs/IMAGE.md`](docs/IMAGE.md) | Images: the content part, where it is legal on the wire, normalization and bounds, every front door, inline TUI rendering. |
| [`docs/TESTING.md`](docs/TESTING.md) | Test layers, the failure-mode catalog, the matrix rule, definition of done. |
| [`AGENTS.md`](AGENTS.md) | The guide for working on Nova: workflow, test-driven rules, repo map, hard rules, definition of done. |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | The short version for contributors. |
| [`CHANGELOG.md`](CHANGELOG.md) | What changed, release by release. |
| [`docs/archive/`](docs/archive/) | The finished build-out: the chunk specs (C00 to C23), each with a test matrix that still guards its area, the build plan, and the hand-off log. |
| [`docs/BRAND.md`](docs/BRAND.md) | The two logo marks, the palette, and how to regenerate them. |
| [`docs/DESIGN.md`](docs/DESIGN.md) | The ecosystem survey and what we borrow from where. |
| [`docs/INTEGRATION.md`](docs/INTEGRATION.md) | How her-go, polaris, and ivy embed Nova. |
| [`docs/SPIKE.md`](docs/SPIKE.md) | Historical: what the early M0 prototype proved, and where its ideas live now. |

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
