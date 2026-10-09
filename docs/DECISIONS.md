# Nova — Decisions

The locked decisions, with the reasoning and what was rejected. This replaces the
decision log that was in `DESIGN.md`. Entries marked **Superseded** record a
decision that was changed during the 2026-10-09 planning pass, so the history is
not lost.

The ecosystem survey and the "what we borrow from where" notes stay in
[`DESIGN.md`](DESIGN.md). They are still accurate. The decision table in that file
is not.

## What Nova is

One Go binary, `nova`, that is an agent harness. The same binary is:

- an interactive **coding agent** in the terminal (the TUI);
- a **headless** agent for scripts and CI (print and JSON modes);
- a **GitHub Action** that reviews pull requests;
- a **backend** for a website or another program (RPC over stdio, or HTTP);
- a **Go library** another program can import.

All of these are one mechanism with different front doors. See
[`ARCHITECTURE.md`](ARCHITECTURE.md).

## Decision log

### Shape of the project

| # | Decision | Choice | Rationale | Rejected |
|---|---|---|---|---|
| D1 | Core engine | Our own, modelled on Pi | Full control; the loop is the smallest, most reusable piece | Adopt `fantasy`; adopt `sausheong/harness`; fork Crush |
| D2 | Scope | Loop, registry, core tools, sessions, compaction, TUI, extensions | A harness that cannot remember or be extended is not a daily driver | Loop-only library |
| D6 | Distribution | One Go module, `github.com/AutumnsGrove/nova`, one binary | One thing to version, install, and document | Vendored copies; many modules |
| D7 | First adopter | Nova standalone, then Ivy, her-go, polaris | Prove the harness as a coding agent first | Integrate production loops first |
| D8 | Name | Nova | A star that suddenly brightens. Short, astral, family of `polaris` | Aster, Canopy, Cirrus, Loom |
| D9 | North star | A Pi-class coding harness that is our own | Library and agent are one artifact | Pure library |
| D30 | Brand | Two cell-built marks, see [`BRAND.md`](BRAND.md) | The mark depicts the real nova mechanism and renders in a terminal | Spiral galaxy (K3 alone, rejected as "not a nova") |

### Binary, modules, and modes

| # | Decision | Choice | Rationale | Rejected |
|---|---|---|---|---|
| D11 | Backend/frontend split | **Superseded.** One program with a UI-free core and a separate `tui/` package. | The split was about dependency isolation. A build tag gives the same isolation without a second module or binary. | Two programs (earlier D11) |
| D12 | Repo layout | **Superseded.** One module. `tui/` is a package the core never imports. | Simpler to version and ship. Enforced by a test, see below. | Nested module with `go.work` (earlier D12) |
| D14 | Build flavours | Default build includes the TUI. `-tags notui` produces a minimal headless binary. | The user chooses: one binary that does everything, or a small one without the charm dependencies | Always include the TUI |
| D13 | Transport | **Superseded.** Protocol first. Everything is a client of one JSONL protocol: the TUI, the GitHub Action, web backends, tests, and extensions. In-process pipe by default; stdio and HTTP for others. | One mechanism, tested once. Makes the e2e tests cheap, see [`TESTING.md`](TESTING.md). | In-process interface only (earlier D13) |
| D15 | Import boundary | A test fails the build if any non-`tui`, non-`bundled` package imports `charm.land/*` | The core must stay embeddable without UI weight | Convention only |

Import consequence worth knowing: because there is one module, an embedder's
`go.mod` and `go.sum` may still list the charm modules even though they never
compile them. That is cosmetic. Nothing UI-related is built into their binary.

### Extensions

| # | Decision | Choice | Rationale | Rejected |
|---|---|---|---|---|
| D5 | Extension runtime | **Superseded.** Subprocess extensions speaking the same JSONL protocol, plus declarative YAML tools and command hooks. | A self-extending agent needs extensions it can write and reload without recompiling `nova`. A subprocess does that in any language. | Compile-time Go only (earlier D5) |
| D16 | No embedded interpreter | No Yaegi, goja, Starlark, or wasm host | Yaegi's supported-Go-version and generics story was unverified and it is experimental. The user chose a current, supported approach. Revisit only if subprocess latency proves a problem. | Yaegi; goja; wasm |
| D17 | Two tiers | **Bundled** extensions are in-tree Go packages that use the rich in-process API and the charm libraries directly. **Third-party** extensions use the protocol and a fixed set of declarative UI components. | Defaults look native. Third-party code gets a stable, sandboxable surface that still fits the look. | One tier; third-party in-process |
| D18 | Extension UI | Declarative components over the protocol: notify, confirm, select, input, form, status segment, widget (text, list, table, progress), message renderer by template | Pi's RPC mode takes the same approach. A subprocess cannot hand over a Bubble Tea component. | Arbitrary component injection |
| D19 | Install | Git, no registry server. `nova ext install github.com/owner/repo@ref`. Pinned commit in a lockfile. Trust prompt lists requested capabilities. Discovery via the GitHub topic `nova-extension` and an `index.json` in this repo. | The project has no domain and may never have one | A central registry |
| D20 | Reload | `/reload` restarts extension processes and re-reads manifests, skills, prompts, and config | Matches Pi. Needs no recompile of `nova`. | Hot code swap |

### Provider and auth

| # | Decision | Choice | Rationale | Rejected |
|---|---|---|---|---|
| D4 | Provider | OpenRouter first, built on a generic OpenAI-compatible provider | OpenRouter is what is actually used and it reaches everything else | Many providers in core |
| D21 | Login | `nova login` runs OpenRouter's OAuth PKCE flow and stores the key. `OPENROUTER_API_KEY` also works. Nova should work when launched through Ori. | Ori logs in to OpenRouter by OAuth PKCE and bills to the OpenRouter account. Nova does the same natively. | Key paste only |
| D22 | Provider options | A `ProviderOptions` map on every request, passed through to the wire body. OpenRouter routing (`order`, `only`, `ignore`, `allow_fallbacks`, `sort`, `quantizations`, `max_price`, ...) is a typed helper. A `before_provider_request` hook can mutate the payload. | Replaces the `provider-pinning` extension. Keeps prompt caches warm. | Extension only |
| D23 | Other providers | OpenCode Go and others are extensions that register a provider. They need `/responses` and `/chat/completions` support in the OpenAI-compatible layer, which comes later. | Not core scope. The user wants OpenRouter as the one core provider. | Bundle every provider |

### Primitives that were extensions in Pi

| # | Decision | Choice | Rationale |
|---|---|---|---|
| D24 | Thinking | Native. Reasoning streams into a collapsible block that shows duration and reasoning tokens. Expand with a key. | Pi had no render hook for this, so it was an extension that patched components. Building from scratch removes the problem. |
| D25 | Context limit | Native per-session `context_limit`. It drives compaction and the footer. | Pi needed a derived-model hack in `models.json`. |
| D26 | Usage ledger | Every assistant message carries a `Usage` record with tokens, cached tokens, reasoning tokens, and cost. A running total is part of session state. | Makes a cost tracker trivial and makes cost caps possible. |
| D27 | Custom session entries | Extensions append typed entries to the session tree. They replay on resume and survive compaction when marked `keep`. | How `pi-tasks` stays correct across `/reload`. |
| D28 | Permission policy | Core, declarative: allow/ask/deny rules by tool name and path glob, with a non-interactive default. Replaces `permission-gate`, `protected-paths`, and `confirm-destructive`. | Headless modes have no one to ask, so the rule must be decided up front. |
| D29 | Nested model calls | `ctx.Complete()` for extensions. Usage counts toward the session. | `parallel`'s page summariser needs it. |

### Data and config

| # | Decision | Choice | Rationale | Rejected |
|---|---|---|---|---|
| D31 | Sessions | JSONL tree, one file per session, Pi-style entries with `id` and `parentId` | Simple, diffable, no database, easy to test | SQLite (Crush) |
| D32 | Config | YAML. `~/.config/nova/config.yaml` plus project `.nova/config.yaml`, deep-merged, JSON accepted. | `gopkg.in/yaml.v3` is the only non-stdlib dependency in the core | JSON only; a bash config (Crush) |
| D33 | Context files | `AGENTS.md` and `CLAUDE.md` in the working directory and parents | Matches every other agent | A Nova-only file |

### How we build

| # | Decision | Choice | Rationale |
|---|---|---|---|
| D34 | Testing | No browser. Layers: unit, scripted fake-provider scenarios against the real binary, provider cassettes, golden TUI views, a few PTY smoke tests. See [`TESTING.md`](TESTING.md). | Playwright slowed the development machine on the last project |
| D35 | Edge cases | Written into the spec as a test matrix, at plan time, by the larger model. The implementing agent fills the matrix, it does not invent it. | On Ivy the first pass covered only the happy path and needed a second review |
| D36 | Implementing agent | DeepSeek V4.1. Specs are therefore explicit: file lists, signatures, matrices, and acceptance commands. | The user's preferred model. Their Pi extensions were built with it for about $2.50 in total. |
| D37 | Size | A line budget per package, enforced in CI | The Pi lesson: an agent can only extend what fits in its context |
| D38 | Go version | The version in `go.mod`, currently 1.26 | Crush uses 1.27. Move when needed. |

## Superseded decisions at a glance

| Old | New |
|---|---|
| D5 compile-time Go extensions | D5 subprocess extensions, D17 two tiers |
| D11 two programs | D11 one program, UI isolated by package and build tag |
| D12 two modules and `go.work` | D12 one module |
| D13 in-process seam, RPC later | D13 protocol first |

## Open items

These need an answer during implementation, not before.

1. **Ori compatibility.** Ori launches other agents with credentials and a model
   chosen. How it passes them (environment variables, config file) has not been
   verified for Nova. Chunk C03 starts by reading Ori's harness documentation.
2. **OpenRouter OAuth specifics.** The PKCE endpoints, scopes, and key lifetime are
   to be read from OpenRouter's current documentation in chunk C03, not assumed.
3. **Web fetch.** Bundled `webfetch` is keyless (plain HTTP and HTML to markdown).
   The `parallel` extension (needs a key and has a quota) is suggested, not bundled.
4. **Image and document content parts.** The message type is text-only. Content parts
   are deferred but the session format reserves room for them.
