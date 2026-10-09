# Nova — Implementation

How the build runs. The plan is the one used for Ivy: plan every chunk in advance,
start an agent per chunk, let it work, and review the result once. The difference
is that the edge cases are decided in the spec first (D35), so the first pass is
the correct pass.

The implementing agent is DeepSeek V4.1 (D36). It is capable and cheap, and it
follows a precise spec well. It will not invent edge cases it was not given, so the
specs do not leave that to it.

## The chunks

Each chunk is one spec file in [`chunks/`](chunks/). Each is small enough for one
agent session and has a fixed set of files, signatures, a test matrix, and an
acceptance command.

| Chunk | Title | Depends on | Budgets (non-test lines) | Spec |
|---|---|---|---|---|
| **C00** | Guards: matrixcheck, linecount, importcheck, accept, Makefile, CI | | scripts only | [C00](chunks/C00-guards.md) |
| **C01** | `message`, provider contract, `testkit` (fake provider, SSE server, clock) | C00 | 250 + 500 + 1200 | [C01](chunks/C01-foundations.md) |
| **C02** | `openaicompat` streaming provider | C01 | 600 | [C02](chunks/C02-openaicompat.md) |
| **C03** | `openrouter` preset, `auth` (OAuth login), model catalog | C02 | 300 + 500 | [C03](chunks/C03-openrouter-auth.md) |
| **C04** | Tool contract, registry, schema validation, permission policy | C01 | 400 + 300 | [C04](chunks/C04-tool-permission.md) |
| **C05** | `fs` tools: read, write, edit, list | C04 | 900 | [C05](chunks/C05-tools-fs.md) |
| **C06** | `search` tools: glob, grep, gitignore | C04 | 700 | [C06](chunks/C06-tools-search.md) |
| **C07** | `bash` tool | C04 | 400 | [C07](chunks/C07-tool-bash.md) |
| **C08** | `proto` (frames, framing) and `session` (JSONL tree store) | C01 | 600 + 900 | [C08](chunks/C08-proto-session.md) |
| **C09** | The agent loop: hooks, queues, retry, permissions, events | C01, C04, C08 | 900 | [C09](chunks/C09-agent-loop.md) |
| **C10** | Compaction, context limit, usage ledger | C08, C09 | 600 + 200 | [C10](chunks/C10-compaction-usage.md) |
| **C11** | `config`: settings, trust, context files, skills, prompt templates | C04 | 900 | [C11](chunks/C11-config-resources.md) |
| **C12** | Server core, print / json / rpc modes, `cmd/nova` | C09, C10, C11 | 1200 + 300 | [C12](chunks/C12-modes-cmd.md) |
| **C13** | Extension host: manifest, supervision, hooks, declarative tools, install, reload, SDK | C12 | 1500 + 400 | [C13](chunks/C13-extension-host.md) |
| **C14** | `serve` mode and the GitHub Action | C12 | 400 + 700 | [C14](chunks/C14-serve-action.md) |
| **C15** | TUI foundation: model, layout, editor, transcript, streaming markdown | C12 | 3000 | to write |
| **C16** | TUI: thinking block, tool cards, diffs, themes, splash | C15 | 2500 | to write |
| **C17** | TUI: dialogs, widgets, status, extension UI bridge, trust prompt, `/ext` | C15, C13 | 2000 | to write |
| **C18** | Bundled extensions: tasks, ask, plan, subagent, webfetch | C13, C17 | 2500 | to write |
| **C19** | Polish: fang help, first run, docs command, examples, release workflow, favicon | all | 800 | to write |

C00 to C14 are the backend and are specified in full, each with a test matrix. C15 to C19
are scoped here and are specified in full once the backend lands, because their specs
depend on the exact events and commands that C09 and C12 produce. The first TUI spec
(C15) starts by confirming the pinned charm.land versions in `TUI.md` resolve.

### Order and parallelism

```
C00 ─▶ C01 ─┬─▶ C02 ─▶ C03
            ├─▶ C04 ─┬─▶ C05
            │        ├─▶ C06      (C05, C06, C07 are independent: run together)
            │        ├─▶ C07
            │        └─▶ C11
            └─▶ C08 ─▶ C09 (needs C04) ─▶ C10 ─▶ C12 (needs C11) ─┬─▶ C13 ─┐
                                                                    ├─▶ C14  ├─▶ C17 ─▶ C18 ─▶ C19
                                                                    └─▶ C15 ─▶ C16 ─┘
```

Safe to run at the same time: {C02, C04, C08}, then {C05, C06, C07, C11} alongside C09 once
C04 and C08 are merged, then {C13, C14, C15}. Chunks that touch the same package must not
run together. C03 can run any time after C02.

A reasonable first week: C00, C01, then C02, C04, and C08 in parallel, then C05, C06, C07,
C11, and C09 in parallel, then C10 and C03, then C12. That produces a working
`nova -p "..."` with the whole backend tested, before any TUI work begins.

## What an agent receives

One prompt per chunk, built from this template. The agent reads only the files it
names, not the whole repository.

```
You are implementing chunk <CNN> of Nova, a Go agent harness.

Read these, in this order, and nothing else unless a spec says to:
  1. docs/chunks/<CNN>-<name>.md        (your spec: follow it exactly)
  2. docs/TESTING.md sections 3 to 6    (the failure catalog and definition of done)
  3. The files listed under "Read first" in your spec.

Rules:
  - Write the tests first, from the matrix. Run them. Watch them fail. Then implement.
  - Every matrix row has a test whose name contains the row ID. You may add rows. You
    may not delete or weaken rows.
  - Do not change files outside the list in your spec. If you must, stop and say why.
  - Match the surrounding code's style. No new dependencies unless the spec allows them.
  - Run `scripts/accept <CNN>` and make it pass before you finish.
  - When done, write docs/chunks/<CNN>.done.md: what you built, and any matrix row or
    acceptance item you could NOT satisfy, with the reason. An empty list is fine.
```

## The human's job

1. Start an agent on a chunk.
2. Come back; read `<CNN>.done.md`.
3. Run `scripts/accept <CNN>`. If it passes and the `.done.md` list is empty, merge.
4. If not, send the failing output back to the same agent.

That is the whole review for a chunk. A second pass by a larger model is an exception,
used when a `.done.md` lists a gap or when a chunk touches security. Chunks flagged for review of
their **tests**: C09 (the loop), C08 (session), C13 (extension host), C14 (serve and action),
and C04 (permissions).

## Why this should work

| Past failure (Ivy) | Mechanism here |
|---|---|
| Only the happy path was tested | The matrix lists the edge cases; `matrixcheck` fails if one has no test |
| Gaps were found later by a bigger model | That model writes the matrix, now, in the spec |
| The agent decided its own scope | A fixed file list and a "stop and say why" rule |
| "Done" meant "I think so" | `scripts/accept` is the definition of done |
| Silent gaps | The `.done.md` must list every unmet row |

## What the guards cannot catch

Be honest about the limits. `matrixcheck` proves a test exists with a row's ID; it
does not prove the test is good. Mitigations: tests are written first and must fail
before the code exists; the matrix gives the exact expected result; and the larger
model reviews the **tests** of the highest-risk chunks (C09 loop, C08 session, C13
extension host, C14 serve and action, and anything touching permissions) rather than the implementation.

A matrix is also only as complete as the person who wrote it. When a bug is found, the
fix adds a row to the matrix and the catalog in `TESTING.md`, so the same class of
bug is covered in every later chunk.

## Conventions

- **Branches:** one per chunk, `chunk/cNN-name`. Merge to the working branch when
  accepted.
- **Commits:** semantic (`feat:`, `fix:`, `test:`, `docs:`, `refactor:`), one line.
- **Comments:** full sentences, wrapped near 78 columns. Explain why, not what.
- **Errors:** wrap with `fmt.Errorf("…: %w", err)`. Return errors; panic only for
  programmer mistakes at startup wiring.
- **Context:** first parameter of anything that blocks.
- **Dependencies:** the core allows `gopkg.in/yaml.v3` and nothing else outside the
  standard library. `tui/` and `bundled/` allow the charm stack. Anything new needs a
  decision recorded in `DECISIONS.md`.
- **Style:** `gofumpt` if available, otherwise `gofmt`.
- **Prototype code:** the M0 code at the repository root is the starting point for
  C01, C02, C04, C05, C07, and C09. Each of those specs says what to keep and what to
  change. Do not rewrite what a spec says to keep.

## Cost tracking

Development cost is tracked per chunk from the provider's own usage, in the same way
the `pi-extensions` repository does it (a table in the repository README). Each
`.done.md` records the cost of its run. The total is expected to be small; a chunk that
costs much more than its neighbours is a sign the spec was unclear.

## Spec template

Every chunk spec has these sections, in this order. See
[`chunks/TEMPLATE.md`](chunks/TEMPLATE.md).

1. **Goal**: two sentences.
2. **Read first**: the exact files and doc sections.
3. **Files**: every file to create or change, with a one-line purpose.
4. **Interfaces**: the exact Go signatures, as code.
5. **Behaviour**: rules in prose, numbered.
6. **Matrix**: the test table with IDs. The part that matters most.
7. **Fuzz and property tests**: named targets and what they assert.
8. **Out of scope**: what not to build, to stop scope drift.
9. **Acceptance**: the exact commands.
