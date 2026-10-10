# Nova: next steps

Start every session here: read this file and `AGENTS.md`, then do the first item under **Start here**.
This file is edited by hand. Each agent updates it as its last step (see `AGENTS.md`); so do you.

## Start here

**State:** C00, C01, and C02 are done. `accept C02` and `make ci` pass. `message`, the `provider`
contract, `testkit`, and the `openaicompat` streaming provider exist.

**Next:** run **C04** (`docs/chunks/C04-tool-permission.md`): the tool contract, registry, and permission
policy. C04 and C08 are independent, one at a time. Then C05, C06, C07, C11, C09, C10, C03, C12. C04 does
not need `testkit` yet, but remember D48: any package's `_test.go` may import it.

**Everyday prompt:** *Read NEXTSTEPS.md and AGENTS.md, then do the first item under Start here.*

**Instructions from the human:** take every chunk in many small steps and commit after every green step;
never one big lump (see `AGENTS.md`, "Working rhythm").

## Status

| Chunk | Title | Status | Depends on | Date | Cost |
|---|---|---|---|---|---|
| C00 | Guards | done | — | 2026-10-09 | |
| C01 | Message, provider contract, testkit | done | C00 | 2026-10-09 | |
| C02 | openaicompat streaming provider | done | C01 | 2026-10-09 | |
| C03 | OpenRouter preset, login, model catalog | todo | C02 | | |
| C04 | Tool contract, registry, permission policy | todo | C01 | | |
| C05 | Filesystem tools | todo | C04 | | |
| C06 | Search tools | todo | C04 | | |
| C07 | The bash tool | todo | C04 | | |
| C08 | Protocol types and the session store | todo | C01 | | |
| C09 | The agent loop | todo | C01, C04, C08 | | |
| C10 | Compaction, context limit, usage ledger | todo | C08, C09 | | |
| C11 | Config, context files, skills, prompt templates, trust | todo | C04 | | |
| C12 | The server core, modes, and the nova command | todo | C09, C10, C11 | | |
| C13 | The extension host | todo | C12 | | |
| C14 | The serve mode and the GitHub Action | todo | C12 | | |
| C15 | TUI foundation | todo | C12 | | |
| C16 | TUI rendering: thinking block, tool cards, diffs, themes, splash | todo | C15 | | |
| C17 | TUI dialogs, widgets, status, extension UI bridge | todo | C15, C13 | | |
| C18 | Bundled extensions | todo | C13, C17 | | |
| C19 | Polish, first run, doctor, examples, release | todo | C16, C17, C18 | | |

Status values: `todo`, `in-progress`, `done`, `partial` (something unsatisfied, listed below), `blocked`.

## Last session

### C02 — openaicompat streaming provider — 2026-10-09 — pi — main — cost not visible

Rebuilt `provider/openaicompat` from the prototype so every path in `TESTING.md` section 3 is correct.

- `provider/openaicompat/wire.go` — wire message/request/chunk/usage/error types; options merge via `json` map.
- `provider/openaicompat/errors.go` — `httpError`, `bodyError`, `stripHTML`, and 200-stream error objects.
- `provider/openaicompat/stream.go` — `lineReader` (line numbers, `MaxLineBytes`), `assembler`, `consume`.
- `provider/openaicompat/openaicompat.go` — `Config`, `New`, `Stream`, `buildBody`, header precedence,
  `APIKeyFunc`, connect and idle timeouts.
- `provider/openaicompat/openaicompat_test.go` — OAI01–OAI62, `FuzzStream`, `FuzzToolCallAssembly`.
- `testkit/server.go` — finish reasons, `ServerTurn.Raw`, `Fault401`, OpenAI usage detail objects (C01's
  handoff asked for this).
- `scripts/importcheck`, `docs/DECISIONS.md` D48, `docs/ARCHITECTURE.md` — test-only `testkit` imports.

**Unsatisfied matrix rows:** none. `go run ./scripts/accept C02` passes all nine steps; `make ci` exits 0.

**Differed from spec:**
- C02's tests use `testkit.Server`, but the layer rule lets `provider` import only `message`. The human chose
  to allow test-only `testkit` imports; recorded as D48 and covered by a new importcheck test.
- The idle timeout is enforced by a timer that cancels the request context, not by a reader goroutine (and
  not by `ResponseController`, which is server-only). Same behaviour, fewer lines.
- C02's Files table omits `testkit/server.go`; it was extended, as C01's handoff asked.

**Gotchas:**
- `net/http`'s server does not always notice a client close while a handler with an unread request body is
  blocked. The header-less and stalling servers in the tests need a release channel, or `httptest.Server.Close`
  wedges and the test times out in cleanup.
- A cut-off SSE line without a newline is `errTruncatedLine` (retryable), not malformed JSON; `FaultCutMidEvent`
  depends on that distinction.
- `testkit`'s fault bodies (`FaultMalformedJSON`, `FaultHugeChunk`) return before `writeTurn`, so they carry no
  finish reason.
- `openaicompat` is at 597 code lines against the 600 budget; comments do not count, but new code will need
  something removed.

**One command:** `go run ./scripts/accept C02`.

### C01 — Message, provider contract, testkit — 2026-10-09 — pi — main — cost not visible

Finished `message` and the `provider` contract, and built all of `testkit`.

- `message/message.go`, `message/validate.go` — added `Reasoning`/`Name`; `Validate` pairs tool calls with results.
- `provider/options.go` — `MergeOptions` and `NormalizeFinish`.
- `provider/error.go` — typed `Error`; `Classify`/`parseRetryAfter` (seconds and HTTP dates, capped at 5m).
- `provider/provider.go` — `Request.Stop/ReasoningEffort/ProviderOptions`, `Usage.ReasoningTokens/CostSource`,
  `StreamUsage`, `StreamEvent.Usage`.
- `testkit/clock.go` — `Clock`, `RealClock`, `FakeClock` with `Advance`/`BlockUntil`.
- `testkit/provider.go` — scripted in-process `Provider`.
- `testkit/server.go` — SSE `Server` with the nine faults.
- `testkit/fs.go`, `golden.go`, `leaks.go` — `TempTree`/`Symlink`/`WriteBinary`, `Golden`, `NoLeaks`.

**Unsatisfied matrix rows:** none. `go run ./scripts/accept C01` passes all nine steps.

**Differed from spec:**
- `NoLeaks` is tested through its core `checkLeaks(t leakTB, baseline)` with a small fake, because `testing.TB` has an
  unexported method and no test can implement it. `NoLeaks` is a thin wrapper that snapshots and registers the cleanup.

**Gotchas:**
- `testkit.Server`'s usage chunk marshals `provider.Usage` directly, so `reasoning_tokens` is top-level, not under
  `completion_tokens_details`. C02 should adjust `server.go` if its usage mapping expects the OpenAI detail objects.
- Go 1.26's `t.TempDir()` returns a new numbered subdirectory on each call, not one shared directory.
- `Classify` must cap the seconds value before multiplying by `time.Second`: `Atoi` accepts numbers large enough to
  overflow a `time.Duration` (found by `FuzzClassify`; it returns a negative otherwise).

**One command:** `go run ./scripts/accept C01`.

### C00 — Guards — 2026-10-09 — pi — main — cost not visible in this session

Built the four guard programs, the shared spec parser, the Makefile, and the CI workflow.

- `scripts/spec/spec.go` — parses YAML front matter and `## Matrix` tables; `Load`, `Find`.
- `scripts/matrixcheck/main.go` — every matrix row needs a test, and no test may cite an unknown ID.
- `scripts/linecount/main.go` — non-test code lines (via `go/scanner`) against each package budget.
- `scripts/importcheck/main.go` — layer rules, UI boundary, D46 allow-list, and a working `-tags notui` build.
- `scripts/accept/main.go` — the nine definition-of-done steps in order, with `-from step` to resume.
- `Makefile`, `.github/workflows/ci.yml`.

**Unsatisfied matrix rows:** none. `go run ./scripts/accept C00` passes all nine steps; `make ci` exits 0.

**Differed from spec:**
- C00 front matter omitted `./scripts/spec`; added it to `packages`/`budgets` (human-approved).
- `matrixcheck-all`/`linecount-all` guard specs whose chunk is `done` in this file, not "whose packages exist".
  The M0 prototype already occupies `./provider/openaicompat`, `./tools/fs`, `./tools/bash`, and `./agent`, so the
  package proxy marked unfinished chunks as ready and made `ci` red (human-approved change).
- The MCK rows name IDs like `X01`, which do not match the documented `^[A-Z]{2,4}[0-9]{2,3}$` shape; the tests use
  valid two-letter IDs (`AA01`, `XX01`) instead.

**Gotchas:**
- `accept` and `importcheck` shell out to `go`, so their tests put stub `go`/`gofmt` first on the real process `PATH`
  with `t.Setenv`; setting PATH only in `run`'s env does not affect `exec.Command` lookup.
- The third-party rules match on import-path prefix, so `go list -deps` without `-test` is enough: `TestImports` is
  populated on the named packages, and `golang.org/x/net/html` resolves as a package path, not a module path.
- `gofmt -l` exits 0 even when it prints files; `accept` treats non-empty output as a failure.

**One command:** `go run ./scripts/accept C00`.

## Spec corrections to apply

- C02 Files table: it says `provider/openaicompat/*_test.go` "uses testkit.Server", but the layer rule
  (`provider` may only import `message`) forbade that. Resolved by D48 (test-only `testkit` imports),
  human-approved 2026-10-09.
- C02 Files table: it omits `testkit/server.go`, which must carry `finish_reason`, `ServerTurn.Raw`, and the
  OpenAI usage detail objects for the matrix to be testable; later drafts should list it.
- C02 Behaviour 12: "naming the line number" is implemented as the physical SSE line (blank lines count).
  Say so explicitly if a data-line ordinal was intended instead.
- C00: `packages`/`budgets` omitted `./scripts/spec`, where the SPC01–SPC14 tests live, so
  `accept C00` could not run or count them. Applied: added `./scripts/spec` with budget 250 (human-approved
  2026-10-09).
- C00 Makefile section: "whose packages exist" is not a usable skip rule while the M0 prototype occupies future
  package paths. Applied: `matrixcheck-all`/`linecount-all` guard only chunks whose Status is `done`
  (human-approved 2026-10-09).
- C00 Matrix: the MCK example IDs `X01`/`X02`/`X03`/`X99` are one letter and so do not match the documented
  `^[A-Z]{2,4}[0-9]{2,3}$` ID shape; later drafts should use two-to-four letter IDs.
- C01 Matrix: TKL01/TKL02 say "run inside a sub-test harness", but `testing.TB` cannot be implemented outside
  `testing`, so `NoLeaks` is tested through its core `checkLeaks`. Later drafts should say "test the check core".

## Decisions to record

- The `ci` guard loops (`matrixcheck-all`, `linecount-all`) guard only chunks marked `done` in `NEXTSTEPS.md`,
  replacing C00's "whose packages exist" rule, because the M0 prototype already occupies several future package
  paths. Belongs in `docs/DECISIONS.md`.

## Open questions for the human

- C03 needs OpenRouter's OAuth PKCE details and Ori's behaviour read from their current documentation (the specs say so).
- Multi-user `serve` is assumed out of scope (one trusted agent behind a bearer token).

## Cost

Total: $0.00 recorded; the C00, C01, and C02 session costs were not visible.
