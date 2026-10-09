# Nova

> A minimal, pi-inspired agent harness in Go — built to be embedded in Go
> applications (her-go, polaris, ivy) or run standalone as a CLI/TUI.

**Status: planning.** No library code is being written yet. The repository
contains the plan and design documents, plus an early M0 prototype at the
repository root that was built to validate the loop and provider design (see
[`docs/SPIKE.md`](docs/SPIKE.md)). The prototype is exploratory; its API is not
frozen.

## What Nova is

Nova is a reusable agentic-loop library for Go. It provides:

- a small **agent loop** (`request → tool calls → results → repeat`) with hooks;
- a **tool registry** you extend without modifying the core, using both Go code
  and declarative YAML/Markdown manifests;
- a **pluggable provider layer** with OpenRouter as the first provider and a
  generic OpenAI-compatible implementation underneath it;
- built-in **core tools** (`read`, `write`, `edit`, `list`, `glob`, `grep`,
  `bash`);
- **sessions** (a JSONL message tree) and **compaction**;
- a **CLI/TUI** frontend, and an in-process extension API.

The design is modeled on [pi](https://github.com/earendil-works/pi): minimal to
start, easy to extend, and useful out of the box.

## North star

One day, a **full coding harness on par with pi** — our own, same principles. The
library and the standalone agent are the same thing viewed from two sides: the
package other Go programs import is the package that powers Nova's own terminal
coding agent.

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
| [`docs/PLAN.md`](docs/PLAN.md) | Vision, principles, milestones M0–M5, open questions. |
| [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) | Package layout, core interfaces, data flow, concurrency, error model. |
| [`docs/DESIGN.md`](docs/DESIGN.md) | Decision log, the ecosystem survey, and what we borrow from where. |
| [`docs/INTEGRATION.md`](docs/INTEGRATION.md) | How her-go, polaris, and ivy adopt Nova. |
| [`docs/SPIKE.md`](docs/SPIKE.md) | What the M0 prototype proves, and its known limitations. |

## Name

**Nova** — a star that suddenly brightens. Astral, short, and in the same family
as `polaris`, `her-go`, and `ivy`. The module path is
`github.com/AutumnsGrove/nova`.

## Lineage

Nova borrows deliberately:

- from **pi** — the agent loop and hooks, the tool/provider extension model, the
  session tree, and the minimal-core philosophy;
- from **her-go** — the shared loop with lifecycle hooks, YAML tool manifests,
  continuation windows, and the OpenAI-compatible client shape;
- from **polaris** — concurrent tool dispatch with panic isolation, pseudo
  tool-call recovery, and research nudges as hooks;
- from **ivy** — the provider-as-chokepoint idea (its `llm.Gate` enforces
  policy, caps, and ledger as a provider decorator).
