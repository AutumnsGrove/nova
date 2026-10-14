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
| D23 | Other providers | **Superseded by D56.** OpenCode Go and others were extensions that register a provider, needing `/responses` and `/chat/completions` support later. | Not core scope. The user wants OpenRouter as the one core provider. | Bundle every provider |
| D56 | Second launch provider | **OpenCode Go** ships at launch as a core provider beside OpenRouter, built from three streaming dialects (chat completions, OpenAI Responses, Anthropic Messages) over a shared SSE transport, with its own catalog and `OPENCODE_GO_API_KEY`/config credentials. | The Go subscription is the low-cost path to the open coding models, and some of its models speak only `/responses` or `/messages`; a launch with one provider has no fallback. | Keep it a suggested extension installed later (D23); bundle every provider; OpenRouter only |

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
| D32 | Config | YAML. `~/.config/nova/config.yaml` plus project `.nova/config.yaml`, deep-merged, JSON accepted. | `gopkg.in/yaml.v3` is small and standard for Go YAML; see D46 for the dependency policy | JSON only; a bash config (Crush) |
| D33 | Context files | `AGENTS.md` and `CLAUDE.md` in the working directory and parents | Matches every other agent | A Nova-only file |

### Safety

| # | Decision | Choice | Rationale |
|---|---|---|---|
| D39 | OS sandbox | **None now.** Possibly later, for `bash` only, as a Landlock-based add-on with a "fail if unavailable" option. | Pi ships none and Crush ships none. Claude Code's is off by default and covers shell commands only. Nova's own file tools are covered by path containment and the permission policy. A container or VM is the user's choice (run Nova inside one). |
| D40 | Approvals | Core: the permission policy and the approval dialog (C04, C09, C17). Unlike Pi, Nova can ask. | Pi is full-auto; for a daily driver that is risky |
| D41 | Command classifier | **Much later**, as an optional suggested extension, never bundled and never able to grant approval, only to make a call stricter. | A classifier is probabilistic and can itself be a target of prompt injection, so it is a filter, not a boundary |
| D42 | Bundled extensions' API | An in-process Go API (`ext/inproc`, added in C18) that registers through the same path as subprocess extensions, plus access to the TUI registry in builds that include it | Bundled extensions need rich UI; the registration path stays single |

### How we build

| # | Decision | Choice | Rationale |
|---|---|---|---|
| D34 | Testing | No browser. Layers: unit, scripted fake-provider scenarios against the real binary, provider cassettes, golden TUI views, a few PTY smoke tests. See [`TESTING.md`](TESTING.md). | Playwright slowed the development machine on the last project |
| D35 | Edge cases | Written into the spec as a test matrix, at plan time, by the larger model. The implementing agent fills the matrix, it does not invent it. | On Ivy the first pass covered only the happy path and needed a second review |
| D36 | Implementing agent | DeepSeek V4.1. Specs are therefore explicit: file lists, signatures, matrices, and acceptance commands. | The user's preferred model. Their Pi extensions were built with it for about $2.50 in total. |
| D37 | Size | A line budget per package, enforced in CI | The Pi lesson: an agent can only extend what fits in its context |
| D38 | Go version | The version in `go.mod`, currently 1.26 | Crush uses 1.27. Move when needed. |
| D44 | HTML parsing | `golang.org/x/net/html` in `bundled/webfetch` | The standard library has no HTML parser | A hand-written parser |
| D45 | CLI framework | `github.com/spf13/cobra` in `cmd/nova`; `charm.land/fang/v2` for styled help and errors in the full build, through a build-tagged `tui/cli` package so a `notui` build has plain cobra output | Subcommands, flags, completion, and help for free; fang is what Crush uses | Hand-rolled `flag` parsing |
| D46 | Dependency policy | Dependencies are welcome when they remove code or risk. The UI boundary stays: no UI library outside `tui/`, `tui/cli`, and `bundled/`. Every module is listed, with the packages allowed to import it, in `scripts/importcheck`, and each addition gets a line in this file. Prefer the standard library for trivial things. Pin versions. `CGO_ENABLED=0` builds must keep working. | Earlier "standard library plus yaml only" was a means to keep the core small, not a goal. Initial table: `yaml.v3` (core), `cobra` (`cmd/nova`), `fang` and the charm stack (`tui`, `tui/cli`, `bundled`), `x/net/html` (`bundled/webfetch`). Pre-approved candidates, to use when they save code: `bmatcuk/doublestar/v4` for glob matching (C04, C06) and `mvdan.cc/sh/v3` for parsing shell commands in permission rules. | A fixed one-dependency rule |
| D47 | Session handoff | One hand-edited `NEXTSTEPS.md`. Every session starts there; every agent updates it as its last step. Chunks run one at a time, so no tooling is needed. | Simple, and the habit is the same one that worked on Ivy | A generated file with per-chunk notes and a script (rejected: needless complexity for sequential work) |
| D48 | Test-support imports | Any package's `_test.go` may import `testkit`, even when the layer table does not list it; production code still may not. `scripts/importcheck` allows the test-only import and still fails a non-test one. | `testkit` is test support (TESTING.md says every test uses it), while the layer table describes production dependencies. C02's `openaicompat` tests are the first to need it. | Listing `testkit` in every layer's allowed set, or copying SSE helpers into each package |
| D49 | Glob engine reuse | `tools/*` may import `permission`, and `permission.ValidPath` is exported, so `glob` and `grep` share one path-glob engine (`permission.MatchPath`). `permission` never imports `tools`, so the graph stays acyclic. | One implementation of `**`/`*`/`?`/class matching instead of copying the engine into `tools/search` | Duplicating the matcher in `tools/search` |
| D50 | `grep` result summary | `Details.files` is the number of files with at least one match; skipped files and over-long lines both count in `Details.skipped`. | A display summary the model reads, not a contract; matches what a person expects from "matches across files" | Counting every file searched |
| D51 | Bash group lifecycle | The bash tool always force-kills its process group at the end of a call, including a clean exit, so a background child that redirected its output cannot outlive the call. | BSH24/25: a redirected child holds no pipe, so the pipe grace cannot detect it | Detecting background children only via the pipe |
| D52 | Bash environment | `buildEnv` appends the fixed variables and extras to `os.Environ()` and relies on `exec.Cmd`'s duplicate-key rule (last value wins). | Fewer lines and no map; `exec` documents that the last duplicate key wins | Rebuilding the environment in a map and sorting |
| D53 | Session entry parent field | Session entries use `parent` (not `parentId`) as the JSON field; the `ARCHITECTURE.md`/`TESTING.md` prose was corrected to match. | The C08 interface is the wire contract; `parent` is shorter and is what the store already writes | `parentId` (the older prose) |
| D54 | Session write durability | Session writes use `O_APPEND` under an advisory `flock`, and the leaf is re-derived under the lock before a parent is assigned. | Two `Open` handles otherwise write at their own offsets and clobber each other (found by SES19) | Seeking to the end before each write |
| D55 | Session warning channel | `Store.Warnings()` is the single warning channel; it carries load warnings and the context repairs from `Messages`. | `Messages` returns `([]Message, error)` with no warning slot, and one channel is easier to surface | A separate `ContextWarnings()` accessor |
| D57 | Syntax highlighting | `github.com/alecthomas/chroma/v2` is allowed in `tui/` and `bundled/`. One helper package, `tui/highlight`, wraps the lexer and the `terminal256` formatter so `tui/diff` and `tui/render` share it. | The TUI spec pins chroma; a single wrapper avoids a `render` ↔ `diff` import cycle. | Duplicating chroma setup, or a package per consumer |
| D58 | Theme files | `gopkg.in/yaml.v3` may be imported by `tui/` and `bundled/` as well as the core, because `tui/styles` reads user theme YAML. | C16 loads themes from `*.yaml`; the core already depends on yaml, so the UI boundary does not change. | A JSON-only theme format |
| D59 | PTY smoke tests | The L5 pseudo-terminal tests import `golang.org/x/sys/unix` (already an indirect dependency) to allocate a pty on Linux and macOS. Test-only, in `tui/ui`. | `TESTING.md` says the standard library only, but a pty cannot be allocated without a syscall wrapper; x/sys is the smallest already-present option. | `creack/pty` (a new module), or `script(1)` (a subprocess) |
| D60 | `tui/cli` takes cobra | `github.com/spf13/cobra` is allowed in `tui/cli` as well as `cmd/nova`, because the fang wrapper's `Execute` takes the `*cobra.Command` root. The package is behind `!notui`, so the core stays free of both cobra and fang in a notui build. | C19's `tui/cli.Execute(root *cobra.Command)` is the seam between the command tree and fang; cobra is already a dependency and the alternative would move fang's import into `cmd/nova`. | Passing `cmd/nova` into `tui/cli` by an interface |
| D61 | Bundled host conventions | `Env.Extra["tools"]` is the parent `*tool.Registry` and `Env.Extra["plan"]` starts plan mode (the `--plan` flag). These are host conventions passed to bundled extensions, not protocol. | C18 needs a bundled extension to reach the parent registry and the mode flag without widening the protocol. | A new protocol field for each |
| D62 | Bundled tools are always offered | A bundled tool is offered whenever its extension is enabled; `extensions.disable` and `--no-tools` are the off switches, while `tools.active` does not filter them. | A bundled tool is a core capability, not an opt-in tool list entry; filtering it by `tools.active` surprised users. | Filtering bundled tools with `tools.active` |
| D63 | No empty widget on a fresh start | A bundled extension must not publish an empty widget at startup; an empty widget is only sent to clear a previous one. | An extra publish shifts the serve event ring and broke `SSE12`. | Publishing an empty widget to establish a key |
| D64 | First-run is standalone; `EntryKind` | The first-run flow is a standalone Bubble Tea program in `tui/firstrun` run by `cmd/nova` before `mode.Build`, not a sub-model inside `tui/ui`; and `ext/sdk.EntryKind` declares a session entry kind at register time so the host replays it. | Keeps `tui/ui` (at its budget) from growing, makes the flow testable without a terminal, and lets a subprocess extension rebuild state on reload. | A first-run sub-model inside `tui/ui`; relying on `AppendEntry` before register |
| D65 | Built-in skills load last | The `extension-author` skill is embedded in `bundled/skills` and appended by `config.LoadSkillsWithBuiltins` from `mode/wire.go`. | `config` may not import `bundled/`, and a built-in must lose to a user or project skill of the same name. | Importing `bundled` from `config` |
| D66 | `tui/cli` wraps fang output | `tui/cli` hard-wraps fang's styled output to the terminal width with `ansi.Hardwrap`, because fang styles but does not wrap the command and flag groups. | `--help` must never overflow the terminal, at any width, in the full build. | Accepting fang's unwrapped groups |
| D67 | OpenCode Go login and key | `nova login opencode-go` runs the Console OAuth device grant, then provisions a per-machine service-account key. The key is stored in `~/.config/nova/opencode-go.json` (mode 0600), not `config.yaml`. | The Console device flow is how the pi extension signs in; CFW10 refuses a secret-named config key, so a dedicated store is the only place the key can live. | Writing `provider.opencode_go.api_key` via `config.SetUser` |
| D68 | OpenCode Go catalog | The Go catalog is a code-defined baseline plus a live overlay from `GET /zen/go/v1/models`, enriched by models.dev and cached 12h in `~/.config/nova/opencode-go-models.json`; an unknown id infers its dialect and defaults to chat. | The Go list changes often; fetching it avoids a code change per model, and the baseline keeps the binary useful offline. | A code-only catalog; fetching every start |
| D69 | Usage footer is TUI-driven | The OpenCode Go usage footer is refreshed by the TUI: it sends `get_usage` on init and every 5 minutes and sets its own `opencode-go` status segment. The server returns `rows` and `footer` and stays stateless. | The server has no session-start hook of its own and a background fetcher would leak goroutines in tests; the TUI already owns the footer and the clock seam. | The server pushing `ui.status` on a timer |
| D70 | Extension re-registration is in place | `ext.Host` replaces a tool the instance already owns in place, rather than clearing its registrations first. | The clear-then-register window let a reader see no tools; bundled startup changed the timing enough to make HST07 fail deterministically. | Clearing first |
| D43 | Protocol additions for the TUI | `list_models`, `session.list`, `get_extensions`, and a `remember` field on confirm answers | The model picker, session list, `/ext` view, and "allow for this session" need them; added in C17 |

## Superseded decisions at a glance

| Old | New |
|---|---|
| D5 compile-time Go extensions | D5 subprocess extensions, D17 two tiers |
| D11 two programs | D11 one program, UI isolated by package and build tag |
| D12 two modules and `go.work` | D12 one module |
| D13 in-process seam, RPC later | D13 protocol first |
| D23 OpenCode Go as a later extension | D56 OpenCode Go as a core launch provider |

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
