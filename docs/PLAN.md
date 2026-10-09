# Nova — Plan

The roadmap. Reasons are in [`DECISIONS.md`](DECISIONS.md), structure in
[`ARCHITECTURE.md`](ARCHITECTURE.md), and the build process in
[`IMPLEMENTATION.md`](IMPLEMENTATION.md).

Status: **planning.** The design is documented; all twenty chunks (C00 to C19) are specified in full.
An M0 prototype exists at the repository root ([`SPIKE.md`](SPIKE.md)) and is the starting
point for several chunks.

## 1. Vision

One Go binary that is an agent harness, usable as a terminal coding agent, a headless
script and CI tool, a GitHub Action that reviews pull requests, a backend for a website or
another program, and an importable Go library. All of them are front doors onto one JSONL
protocol, so there is one implementation and one set of tests.

A Go program embeds it with one import:

```go
a := agent.New(agent.Config{
    Provider: openrouter.New(openrouter.Config{APIKeyFunc: keys}),
    Model:    "deepseek/deepseek-v4.1",
    Tools:    tools.Core(),
})
res, err := a.Run(ctx, nil, message.User("summarize this repo"))
```

A user extends it the way Pi is extended: tell Nova what you want, and it can read its own
documentation, write an extension, and load it with `/reload`.

## 2. Goals

- **A small core that grows by extension.** The core stays near 11,000 lines; the agent core
  near Pi's 2,500. Documentation is written so an agent can read all it needs in under
  40,000 tokens.
- **Strong primitives.** The loop, tools, sessions, compaction, permissions, and the protocol
  are small, exact, and tested hardest.
- **Flexible deployment.** TUI, print, JSON, RPC, HTTP, Action, library: one mechanism.
- **Extensions in any language**, installed from git with no registry server.
- **Native versions of what Pi needed extensions for**: a thinking block, a context limit, a
  usage ledger, provider routing options, session entries, permission rules.
- **Beautiful by default.** A crush-class TUI on the `charm.land` stack.
- **Correct on the first pass.** Edge cases are specified in advance as test matrices and
  enforced by a script (D35).
- **Cheap to test.** No browser; the real binary is driven over stdio against a fake provider.

## 3. Non-goals (first version)

- A web UI or a hosted multi-tenant service. (`serve` is one trusted agent behind a bearer token.)
- Every provider. OpenRouter is the core provider; others are extensions.
- A workflow or DAG engine.
- Sandboxing extensions. They run with the user's permissions; run Nova in a container for isolation.
- An embedded scripting runtime (Yaegi, goja, wasm): decided against, D16.

## 4. Principles

1. **The loop is small; behaviour is hooks and extensions.**
2. **The transcript is the wire.** Messages map onto the OpenAI chat format.
3. **One protocol.** Anything a UI can do, a script can do.
4. **Failures are results, not crashes.** Tool errors go to the model; panics are recovered;
   provider errors end one run, not the process; a bad extension never takes down the loop.
5. **Add, don't modify.** New tools, providers, and commands are registered from outside.
6. **Spend is never lost, and every run ends cleanly.** One `run_start`, one `run_end`.
7. **The core is UI-free**, enforced by a check, and has one non-stdlib dependency (`yaml.v3`).
8. **Everything has a budget**, in lines, in time, and in context.
9. **Specs carry the edge cases.** An agent fills in a matrix; it does not invent one.

## 5. Roadmap

Each milestone is a set of chunks from [`IMPLEMENTATION.md`](IMPLEMENTATION.md).

| Milestone | Chunks | Result |
|---|---|---|
| **M1 Foundations** | C00, C01 | the guards run; the fake provider and server exist |
| **M2 Providers and tools** | C02, C03, C04, C05, C06, C07 | a tested OpenRouter provider and the core tools |
| **M3 The engine** | C08, C09, C10, C11 | protocol, sessions, the loop, compaction, config |
| **M4 It runs** | C12 | `nova -p "…"` and `--mode rpc` work end to end |
| **M5 Extensible** | C13, C14 | the extension host, `nova serve`, the GitHub Action |
| **M6 The terminal** | C15, C16, C17 | the TUI: thinking block, tool cards, dialogs, widgets |
| **M7 Batteries** | C18, C19 | bundled extensions, polish, release |
| **M8 Adoption** | | Ivy, then her-go, then polaris embed the library ([`INTEGRATION.md`](INTEGRATION.md)) |

Exit criteria: M4 is "the backend is complete and tested before any UI exists". M6 is the
first point Nova is comparable to Pi as a daily coding agent. M8 is "two production projects
run on Nova with no bespoke loop code".

## 6. Cross-cutting

- **Security.** Permission policy; path containment (`Roots`); extension env scrubbing; no secrets
  in logs; the Action is read-only and treats pull-request content as untrusted.
- **Cost.** A usage ledger from day one; cost caps; provider cost when reported, catalog otherwise.
- **Caching.** A stable system prefix; provider-specific hints belong to the provider; routing options
  keep OpenRouter prompt caches warm.
- **Testing.** See [`TESTING.md`](TESTING.md).

## 7. Open questions

1. **Ori and OpenRouter OAuth specifics** are read from their documentation in C03, not assumed.
2. **Multi-user `serve`.** Out of scope now. Needed only if a website wants one Nova serving many users.
3. **Web fetch and search.** Bundled `webfetch` is keyless; search is the suggested `parallel` extension.
4. **Image and document content parts.** Deferred; the session format reserves room.
5. **MCP client.** The manifest accepts the `mcp` capability; the client is a later chunk.
6. **A `bash` sandbox.** Possibly later, Landlock-based, `bash` only (D39). A command classifier is
   later still (D41). Neither is on the roadmap.
