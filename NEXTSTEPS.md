# Nova: next steps

Start every session here: read this file and `AGENTS.md`, then do the first item under **Start here**.
This file is edited by hand. Each agent updates it as its last step (see `AGENTS.md`); so do you.

## Start here

**State:** C00 through C06 are done. `accept C06` and `make ci` pass. `message`, the `provider`
contract, `testkit`, `openaicompat`, the `openrouter` preset with its model catalog, `auth`
(PKCE login, key store, credential resolution), the hardened `tool` registry with JSON-schema argument
validation, the `permission` allow/ask/deny policy, the filesystem tools (`read`, `write`, `edit`,
`list`), and the search tools (`glob`, `grep`) exist.

**Next:** run **C07** (`docs/chunks/C07-tool-bash.md`): the bash tool. Then C11, C09, C10, C12. Remember
D48: any package's `_test.go` may import `testkit`.

**Everyday prompt:** *Read NEXTSTEPS.md and AGENTS.md, then do the first item under Start here.*

**Instructions from the human:** take every chunk in many small steps and commit after every green step;
never one big lump (see `AGENTS.md`, "Working rhythm").

## Status

| Chunk | Title | Status | Depends on | Date | Cost |
|---|---|---|---|---|---|
| C00 | Guards | done | — | 2026-10-09 | |
| C01 | Message, provider contract, testkit | done | C00 | 2026-10-09 | |
| C02 | openaicompat streaming provider | done | C01 | 2026-10-09 | |
| C03 | OpenRouter preset, login, model catalog | done | C02 | 2026-10-09 | |
| C04 | Tool contract, registry, permission policy | done | C01 | 2026-10-09 | |
| C05 | Filesystem tools | done | C04 | 2026-10-09 | |
| C06 | Search tools | done | C04 | 2026-10-09 | |
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

### C06 — Search tools — 2026-10-09 — pi — main — cost not visible

Built `tools/search` in small green commits: path resolution, gitignore, walker, then glob and grep.

- `tools/search/path.go` — `resolve` with the same containment rules as `tools/fs` (length/NUL checks,
  WorkDir, `Roots` via `evalExisting`, which follows symlinks in the existing part of a path).
- `tools/search/ignore.go` — `parseIgnore`/`ignoreSet`/`matchStack`. Reuses `permission.MatchPath` for an
  anchored pattern and for the base name of an unanchored one; the subset is comments, blanks, `!`,
  anchoring, a trailing `/`, `*`/`?`/`**`, classes, `\#`/`\!`, trailing spaces, BOM, and CRLF.
- `tools/search/walk.go` — DFS in `os.ReadDir` order (already bytewise), `.git` always skipped, hidden
  entries unless `Hidden`, a `.gitignore` stack per level, symlinks off by default with a real-path
  visited set for loops, `MaxDepth`, ctx between entries, and a `walkStats` unreadable-directory count.
- `tools/search/glob.go` — `glob` with `permission.MatchPath` semantics (`**/*.go`, `src/*.go`, `*.go`
  at any depth) and `permission.ValidPath` for the invalid-pattern case; sorted, capped at 1000 with an
  exact omitted count.
- `tools/search/grep.go` — `grep` with RE2/fixed/`(?i)`, context groups merged with `--` separators,
  binary sniff, 2 MiB file cap, 1 MiB line cap, 500-char display cut, invalid-UTF-8 replacement, match cap.
- Tests `WLK01`–`WLK13`, `IGN01`–`IGN22`, `GLT01`–`GLT14`, `GRP01`–`GRP32`, `FuzzParseIgnore`,
  `FuzzGlobPattern`, and `TestProperty_IGN_Subset`.

**Unsatisfied matrix rows:** none. `go run ./scripts/accept C06` passes all nine steps; `make ci` exits 0;
`go test -race ./...` is green; both fuzzers ran 5 s clean. Budgets: `tools/search` 699/700,
`permission` 297/300.

**Differed from spec:**
- The layer rule now lets `tools/*` import `permission` (human-approved 2026-10-09), so `glob`/`grep` reuse
  `MatchPath` instead of copying the engine. `scripts/importcheck` and `docs/ARCHITECTURE.md` were updated.
- `permission/glob.go` gained a one-line `ValidPath` so invalid-pattern handling (GLT07 and the invalid
  `glob` filter) has one implementation; this file is outside C06's Files table.
- `walkOptions` gained an unexported `stats *walkStats` field, because the specified `walk(...) error`
  signature has nowhere to return the unreadable-directory count (behaviour 5).
- The walker reports directories as well as files (`walkEntry.IsDir`), so GLT05 can list directories only.
  WLK01 asserts the file order `a,b,c/d`; the full pre-order is `a,b,c,c/d`.
- `grep` `Details.files` is the number of files with at least one match, not files searched. `skipped`
  counts skipped files plus over-long lines and is surfaced in `Details`, not a visible trailer.
- `grep` sets `Truncated` when the match cap is reached, so a search with exactly `max_results` matches
  and no more still reports truncation.
- `grep` stops the walk at the cap, so `files` and `skipped` stop counting past it.
- `glob` collects every match to print an exact omitted count, so memory is proportional to the matches.
- Directory results from `glob` are plain relative paths, with no trailing `/`.

**Gotchas:**
- `tools/search` has one line of budget headroom; `permission` has three. Comments and blanks do not count,
  but new code will need something removed.
- `os.ReadDir` already sorts by name, so the walker does not re-sort.
- For an unanchored ignore pattern the matcher runs `permission.MatchPath` against `path.Base(rel)`; `*`
  never crosses `/`, which is exactly the basename rule.
- IGN07 works because an ignored directory is never descended into, so its nested `.gitignore` is never
  read; a child set only overrides a parent because `matchStack` visits deeper sets later.
- `permission.MatchPath` treats an unbalanced `[` as "matches nothing", so an invalid ignore line is
  skipped without a separate validity check.

**One command:** `go run ./scripts/accept C06`.

### C05 — Filesystem tools — 2026-10-09 — pi — main — cost not visible

Rebuilt `tools/fs` in small green commits: path resolution, atomic writes, read, diff, edit, list.

- `tools/fs/fs.go` — `resolve` (length/NUL checks, WorkDir, `Roots` containment via `evalExisting`, which
  follows symlinks in the existing part of a path) and a per-path lock shared by write and edit.
- `tools/fs/atomic.go` — `writeAtomic` (temp file 0600, fsync, rename, dir sync), `realTarget` (writes
  through a symlink), `fileMode`.
- `tools/fs/read.go` — a streaming `lineReader` that counts runes without buffering a long line, detects
  CRLF/BOM/invalid UTF-8, pages with offset/limit, caps at 2000 lines or 50 KiB, and reports `Details`.
- `tools/fs/write.go` — size cap, parent creation, mode preservation, created/overwrote, `Details`.
- `tools/fs/diff.go` — `unifiedDiff` with common prefix/suffix + LCS, hunks, and the no-newline marker.
- `tools/fs/edit.go` — all edits matched against the original, exactly once, non-overlapping, applied
  together and written atomically; CRLF fallback; BOM and mode preserved; on-disk change check.
- `tools/fs/list.go` — sorted rows, glob filter, symlink targets, `?` for unstatable entries, 500 cap.
- Tests `PTH01`–`PTH13`, `RDF01`–`RDF31`, `WRT01`–`WRT17`, `EDT01`–`EDT33`, `LST01`–`LST15`, `DIF01`–`DIF07`,
  plus `FuzzResolve`, `FuzzUnifiedDiff`, `FuzzEditApply`, and `TestEdit_EDT33_Property`.

**Unsatisfied matrix rows:** none. `go run ./scripts/accept C05` passes all nine steps; `make ci` and
`go test -race ./...` are green. Budget: `tools/fs` 994/1000.

**Differed from spec:**
- The `tools/fs` budget was raised from 900 to 1000 (human-approved 2026-10-09) in the C05 spec and
  `docs/ARCHITECTURE.md`.
- `RDF29` uses a FIFO whose writer closes after two lines to test a stream that ends before its size,
  rather than truncating a file mid-read (which is not deterministic). The reader also handles a real
  mid-read EOF without a panic.
- `read` reports `line_endings` over the lines it scanned; for a huge file stopped early it reflects that
  prefix, not the whole file.
- `RDF28` creates a 200 MiB sparse file to prove streaming; the reader continues to EOF to count remaining
  lines for the "N more lines" trailer, so a huge file is read but never buffered.
- `write` cannot carry invalid UTF-8 through JSON tool arguments (`json.Marshal` replaces bad bytes with
  U+FFFD), so `WRT16` covers CRLF and NUL byte-for-byte rather than invalid bytes.
- `read` takes `offset` as a pointer so an explicit `0` errors while an omitted offset defaults to 1;
  `limit: 0` still means the default.
- `list` shows `-` as a directory's size.

**Gotchas:**
- `gofmt` expands one-line `if`/`for` bodies; line budgets are code lines (comments and blanks do not count).
- The read `lineReader` has a fast path for ASCII runs so a 200 MiB line is scanned in 64 KiB chunks
  without a per-byte syscall; `renderRead` still counts to EOF for the trailer.
- `editWriteHook` is a package var set by `EDT22`/`EDT26` to simulate another writer and a failing rename;
  tests restore it. Tests that lower `maxFileBytes`/`maxDiffLines` restore them too.
- Symlink containment is checked on the deepest existing ancestor, so a write to a new file under a
  symlinked parent that leaves a root is refused (`PTH11`).

**One command:** `go run ./scripts/accept C05`.

### C04 — Tool contract, registry, permission policy — 2026-10-09 — pi — main — cost not visible

Hardened the tool contract and added the permission policy, in small green commits: schema validator,
registry/Execute, globs, then policy.

- `tool/schema.go` — `ValidateArgs`, a JSON Schema subset validator. Keywords: `type`, `properties`,
  `required`, `additionalProperties`, `enum`, `items`, `minimum`/`maximum`, `minLength`/`maxLength`,
  `minItems`/`maxItems`; malformed keywords are ignored and log one debug line per call.
- `tool/tool.go` — `Result.Details`/`Truncated`, `Update`, and `Context.Roots`/`SessionID`/`Emit`/
  `Complete` with a nil-safe `Progress`.
- `tool/registry.go` — `Execute` validates before running, treats empty/null args as `{}`, rejects a
  non-object, returns `cancelled` on a cancelled context, recovers panics to `internal error in <tool>`, and
  lists up to ten known tools for an unknown one. Added `Replace`, `Unregister`, `View`, and `Info`.
- `permission/glob.go` — `MatchPath` (with `**`) and `MatchString`.
- `permission/shell.go` — `HasShellControlOperator` for the allow-rule guard.
- `permission/policy.go` — `Policy`, `Rule`, `Call`, `Decision`, `Validate`, and `Decide` with
  annotation defaults, most-restrictive-wins, path globs, and the headless resolution.
- Tests `SCH01`–`SCH20`, `REG01`–`REG22`, `GLB01`–`GLB14`, `PRM01`–`PRM30`, plus `FuzzValidateArgs`,
  `FuzzMatchPath`, `FuzzMatchString`, `FuzzDecide`, and `TestProperty_PRM_MostRestrictive`.

**Unsatisfied matrix rows:** none. `go run ./scripts/accept C04` passes all nine steps; `make ci` and
`go test -race ./...` are green. Budgets: `tool` 516/550, `permission` 298/300.

**Differed from spec:**
- The `tool` budget was raised from 400 to 550 (human-approved 2026-10-09) in the C04 spec and
  `docs/ARCHITECTURE.md`; the SCH matrix plus the hardened registry need 516 lines.
- "First problem in schema order": a Go `map[string]any` has no order, so `properties` and unknown keys are
  walked in sorted-key order, and the check order is type, enum, required, properties,
  additionalProperties, then type-specific bounds.
- An unknown type name (not just a non-string `type`) is treated as malformed: ignored and logged.
- Annotation default is `allow` only for a read-only, non-destructive, non-open-world tool; the spec said
  "read-only and not open-world", and `!Destructive` only makes a contradictory tool stricter.
- `additionalProperties` with a non-bool value is ignored; if it is the only malformed keyword, no debug
  line is written, because the log is once per call for the first malformed keyword.
- `Context.Progress` also guards a nil receiver, not only a nil `Emit`.

**Gotchas:**
- `gofmt` expands one-line `if`/`for` bodies, so trimming lines is mostly about fewer functions, not denser
  syntax. `permission` has only two lines of headroom; `tool` has thirty-four.
- Line budgets are code lines only (comments and blanks do not count). `make ci` skips chunks whose
  Status is not `done`, so run `accept C04` while working on it.
- The glob to regexp translation keeps `**/` as `(?:.*/)?`, so `src/**/x.go` matches `src/x.go`; a single
  `*` becomes `[^/]*` in path mode.

**One command:** `go run ./scripts/accept C04`.

### C03 — OpenRouter preset, login, model catalog — 2026-10-09 — pi — main — cost not visible

Built the OpenRouter preset, its typed routing and model catalog, and all of `auth` (PKCE, login, store,
resolution). Built in small green commits: routing, preset, catalog, PKCE, store, resolve, login.

- `provider/openrouter/routing.go` — `Routing`, `Price`, `Options()` rendering the documented `provider`
  object (`order`, `only`, `ignore`, `allow_fallbacks`, `sort`, `quantizations`, `require_parameters`,
  `data_collection`, `zdr`, `max_price`, `preferred_min_throughput`, `preferred_max_latency`).
- `provider/openrouter/openrouter.go` — `Config`, `New`; the attribution headers and key function.
- `provider/openrouter/catalog.go` — `Catalog`, `RegisterModels` (called from `init`), `DefaultModel`.
- `auth/pkce.go` — `NewVerifier`, `NewState`, `Challenge` (S256, RFC 7636 vector).
- `auth/store.go` — `Store`, `NewFileStore` (0600 file, 0700 dir, atomic rename), `ErrNoKey`,
  `*PermissionWarning`; an injectable `rename` so STO04 can force a failed rename.
- `auth/resolve.go` — `Resolve` (config, env, store) and `EnvAPIKey`.
- `auth/login.go` — `Login`, `LoginOptions`, the single-use loopback listener, the callback page, and the
  code exchange (`POST` JSON `code`/`code_verifier`/`code_challenge_method` → `{key}`).
- Tests `ORT01`–`ORT12`, `PKC01`–`PKC04`, `LOG01`–`LOG17`, `STO01`–`STO09`, `RES01`–`RES06`, plus
  `FuzzRoutingOptions` and `FuzzCallbackQuery`.
- `cmd/nova-run/main.go` — updated to the new `Config` form (see differed below).

**Unsatisfied matrix rows:** none. `go run ./scripts/accept C03` passes all nine steps; `make ci` and
`go test -short -race ./...` are green. Budgets: `provider/openrouter` 163/300, `auth` 399/500.

**Differed from spec:**
- The spec's model list has no `deepseek/deepseek-v4.1` slug; OpenRouter's list only has
  `deepseek/deepseek-v4.1-flash` (checked 2026-10-09). Catalog entry: stable id `deepseek/deepseek-v4.1`,
  wire slug `deepseek/deepseek-v4.1-flash`. `DefaultModel` is that stable id.
- Behaviour 9's "(wrapped with the key)" is implemented as "returned alongside the key": `Load` returns
  `(key, *PermissionWarning)`, and the warning names only path and mode, because STO08 forbids the key in
  any error.
- `cmd/nova-run/main.go` is not in the spec's Files table but had to change: `New(Config)` replaces the
  prototype's `New(apiKey string, opts ...Option)`. The prototype `Option` helpers and `Register` were
  dropped; C12 should call `New` directly (it already plans to use `auth.Resolve`).
- `Login` does not quote the callback's `error` parameter; it reports a fixed "authorization was denied",
  so no untrusted text can reach the page.

**Gotchas:**
- The callback page is flushed before `Login` closes the listener; without the flush the client sees EOF
  before the page. `srv.Close()` after `flow.result` is deliberate (the whole flow is one request).
- Fuzzing `httptest.NewRequest` with a raw query panics on a space; set `req.URL.RawQuery = raw` after
  building the request. Two seed files now live in `auth/testdata/fuzz/FuzzCallbackQuery`.
- `provider/openrouter` registers the catalog from `init`, so importing the package (including from tests)
  populates `provider.Models`. `RegisterModels` is idempotent and re-registers.
- OpenRouter authorization codes are single-use and expire after ten minutes; the default login timeout is
  five minutes, below that. The key's own lifetime is undocumented and no refresh token is documented.

**One command:** `go run ./scripts/accept C03`.

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

- C05 budgets: `./tools/fs: 900` is too small for the full behaviour (994 lines). Applied: raised to 1000
  in the C05 spec and `docs/ARCHITECTURE.md` (human-approved 2026-10-09).
- C05 Matrix RDF29: "file shrinks while reading" is not deterministically reproducible; the test uses a
  FIFO that ends early instead, and the reader is written to tolerate a real mid-read EOF.
- C05 Behaviour 12: `line_endings` is detected over the scanned lines, not the whole file, so a huge read
  stopped at the cap reports the prefix. Later drafts should say so.
- C05 Matrix WRT16: invalid UTF-8 cannot cross JSON tool arguments; only CRLF and NUL are byte-for-byte
  testable through the tool interface.
- C04 budgets: `./tool: 400` is too small for the SCH matrix plus the hardened registry (516 lines).
  Applied: raised to 550 in the C04 spec and `docs/ARCHITECTURE.md` (human-approved 2026-10-09).
- C04 Behaviour 12: "first problem found, in schema order" cannot mean map order, because `properties` is a
  Go map. Implemented as sorted-key order over `properties` and unknown keys, with a fixed check order
  (type, enum, required, properties, additionalProperties, then type-specific bounds).
- C04 Behaviour 10/14: an unknown `type` name (not just a non-string `type`) is treated as malformed and
  logged like the other malformed keywords; later drafts should say so.
- C03 Behaviour 3 / Matrix ORT12: there is no OpenRouter slug `deepseek/deepseek-v4.1`; the model list has
  only `deepseek/deepseek-v4.1-flash` (fetched 2026-10-09). Implemented as stable id
  `deepseek/deepseek-v4.1` with wire slug `deepseek/deepseek-v4.1-flash`; later drafts should print the
  confirmed slug and make the stable-id/slug split explicit.
- C03 Behaviour 9: "returns a non-fatal `*PermissionWarning` (wrapped with the key)" contradicts STO08 if
  read as "the warning contains the key". Implemented as "returned alongside the key": `Load` yields
  `(key, *PermissionWarning)`, and the warning holds only path and mode.
- C03 Files table: it omits `cmd/nova-run/main.go`, the one call site the `New(Config)` signature change
  forces; later drafts should list it (or say the prototype `Option` helpers are removed).
- C03 Behaviour 5: confirmation done. OpenRouter supports `state` (returned on the callback after
  authorization), so the flow always sends it; the spec's "bind to the single-use listener" fallback is
  not used. The auth URL is `https://openrouter.ai/auth`, the exchange is `POST
  https://openrouter.ai/api/v1/auth/keys` with `code`, `code_verifier`, `code_challenge_method`, and the
  response key is in `key`.
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
- C06 Read first / layer rules: `tools/search` reuses `permission.MatchPath`, but `scripts/importcheck` let
  `tools` import only `message`, `provider`, `tool`. Applied: `tools` may import `permission`
  (human-approved 2026-10-09); ARCHITECTURE updated. Later drafts should name the allowance.
- C06 Files table: it omits `permission/glob.go`, which needed a small exported `ValidPath` for GLT07 and the
  invalid `glob` filter; later drafts should list it or say validity is reused.
- C06 interfaces: `walk(...) error` has nowhere to return the unreadable-directory count; implemented with an
  unexported `walkOptions.stats`. Later drafts should name the channel.
- C06 Behaviour 19: "counted in a skipped note" is read as the `Details.skipped` field, not a visible trailer;
  later drafts should say which.
- C06 Behaviour 19: `grep` `Details.files` is not defined (searched files vs files with matches); implemented
  as files with matches. Later drafts should say.

## Decisions to record

- The `ci` guard loops (`matrixcheck-all`, `linecount-all`) guard only chunks marked `done` in `NEXTSTEPS.md`,
  replacing C00's "whose packages exist" rule, because the M0 prototype already occupies several future package
  paths. Belongs in `docs/DECISIONS.md`.
- `auth.Resolve` treats a `*PermissionWarning` as non-fatal: a stored key returns even when the file is more
  permissive than 0600, and the warning is left to direct `Store.Load` callers. Belongs in `docs/DECISIONS.md`.
- `permission.Policy.Decide` reports the first rule with the winning action and walks map keys in sorted order,
  so the decision and its `Reason` are deterministic across runs. Belongs in `docs/DECISIONS.md`.
- `tools/*` may import `permission` so the path-glob engine has one implementation (C06); `permission` never
  imports `tools`, so the graph stays acyclic. Belongs in `docs/DECISIONS.md`.
- `grep` summarizes `files` as files-with-matches and folds skipped lines into `Details.skipped`; both are
  display summaries, not part of the tool contract. Belongs in `docs/DECISIONS.md`.

## Open questions for the human

- OpenRouter's key lifetime is undocumented and its PKCE flow documents no refresh token. Nova stores the key
  until `nova logout`; confirm whether a refresh flow is needed before C12.
- Ori's harness documentation does not document environment variables for credentials or model handoff, so
  reading Ori's environment is out of scope (as C03 allowed).
- Multi-user `serve` is assumed out of scope (one trusted agent behind a bearer token).
- C06: is `grep` truncation expected to be exact (true only when a further match exists), or is "the cap was
  reached" acceptable? Currently the latter.

## Cost

Total: $0.00 recorded; the C00 through C06 session costs were not visible.
