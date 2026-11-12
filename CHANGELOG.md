# Changelog

All notable changes to Nova are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- The minimum Go is now 1.26.11, up from 1.26.6: `net/http` below that patch carries
  GO-2026-6859, a reachable crash in the HTTP/1 server that `nova serve` runs and that
  `provider/ssestream` and `auth` both reach. A consumer building with an older patch inherits the
  advisory, so `go get github.com/AutumnsGrove/nova` now requires 1.26.11 or newer. Nothing in the
  language changes; the raised floor is a patch release of the same minor (#104).
- Speech cost is now an estimate from the model's catalog price, and it is always reported: a
  synthesis is priced per character, `runes * pricePerMillion / 1e6`, with `CostSource: "catalog"`.
  A role pinned to one provider is priced from that endpoint, so a pin to Together costs what
  Together charges rather than DeepInfra's rate. The `/generation` lookup this replaces returned an
  all-null record for a `gen-tts-*` id, so speech cost was always zero and a remote voice looked
  free beside a local one (#91).
- `voice/openrouter`: the catalog tables behind the estimate are cached, so `CostLookup` costs at
  most one extra request per client rather than one per clip. `Config.CostTimeout` and `DefaultCostTimeout` are
  removed — there is no separate lookup to bound — which breaks a caller that sets either. Both
  were additive options; drop them and nothing else changes.

### Added

- The gate now reacts to more than a commit. `security.yml` runs a vulnerability scan on every pull
  request, on `main` and weekly, and it found three reachable advisories the day it was written;
  `fuzz.yml` runs all 63 fuzz targets for a timed smoke, which `docs/TESTING.md` had promised since the
  build-out and no workflow had ever done; `nightly.yml` runs the race suite twice, shuffled, and files
  an issue naming the failing test and the shuffle seed. Dependabot keeps the module and the actions
  current weekly (#104).
- `make lint` (staticcheck), `make vuln` (govulncheck), `make fuzz-short` and `make nightly` are the same
  commands the workflows run, and `make static` is the pull request gate's fast half (#104).
- The release gate runs the linter and the vulnerability scan, and executes the linux/amd64 artifacts
  `--version`, `docs` and `doctor` before publishing them, so a binary that compiles and then dies at
  startup no longer reaches a release page (#104).
- A speech role can pin the endpoint that serves it. `voice.speech.roles.<name>.provider` (and
  the same key on a fallback) is sent to `POST /audio/speech` as the `provider` object, verbatim,
  so `only`, `order`, `ignore` and the data-policy keys all work. No pin still means the provider
  picks, which is the cheapest endpoint rather than the most reliable one: an unpinned Kokoro
  synthesis hung for 45 s because the cheapest endpoint was returning 429 (#92).

### Fixed

- Nova's voice documentation claimed OpenRouter ignores `only` on the audio endpoints, so a
  provider pin could not work. It does work: `only: [Together]` answered in 0.6 s against a 45 s
  hang unpinned. `docs/VOICE.md` and D162's successor, D170, correct the record (#92).

### Removed

- `voice/openrouter.DefaultSpeechModel` and `DefaultTranscribeModel`. Nothing referenced them, and
  naming a default implied one exists when the model always comes from a role (#92).

## [0.4.1] - 2026-11-11

### Added

- Release pages now carry a body. The `release` workflow writes it from the tag range: the
  changelog section, the non-merge commits and the diffstat go to a pinned agent, whose answer is
  checked for shape and size before it is published. A failure, a timeout or an unusable answer
  falls back to the changelog section, so a release never blocks on it, and the step warns when
  that happens (#90).
- `scripts/release-notes` runs the same generation locally: `--prompt-only` prints the request,
  `--out` writes the body, and `--max-bytes` overrides the 16 KiB cap (#90).
- The release workflow takes a `dry_run` input, so the job that turns a tag into a release page can
  be rehearsed against a branch or a version that is not tagged yet: it runs in full, writes the
  body to the job summary and an artifact, and skips publishing (#90).

## [0.4.0] - 2026-11-10

Upgrade notes for library consumers (#85):

- `agent.Config` and `tool.Context` gain optional `Speak` and `Transcribe` fields, and
  `proto.Command` gains `speech` and `transcription`. All are additive: a nil field changes
  nothing, so existing callers need no edit.
- The protocol line cap rises from 16 MiB to 64 MiB in `proto`, the extension host and the SDK,
  and `serve`'s request-body default rises from 32 MiB to 64 MiB. If you run your own client with
  a line or body limit below 64 MiB, raise it: a `speak` response above about 12 MiB will
  otherwise fail. No frame shape changed.
- `agent/sub` now passes the parent's `Embed` and `Decide` into a child, so a subagent's tool
  that used to see nil for either reaches the parent's seam. If you worked around that, the
  workaround is no longer needed.
- `POST /v1/commands` on `serve` gets its own transport budget (10 minutes to write, 5 to read)
  instead of the shared 30 s / 2 min, because a voice command can take a minute and return tens
  of megabytes. Other routes keep the short budget.

### Added

- **Speech and transcription** as harness primitives (#85). `voice.Speaker` and
  `voice.Transcriber` turn text into audio and audio into text, with the
  OpenRouter adapter in `voice/openrouter`, a role table over `roles`, and
  `voice.Chunks`, `voice.Join` (which keeps one ID3 tag when joining clips) and
  `voice.WAV` for long answers and raw PCM. The seams are
  `tool.Context.Speak`/`Transcribe`, `agent.Config`, the `speak` and `transcribe`
  protocol commands, `model:speech`/`model:transcribe` extension capabilities
  with per-run caps, `sdk.Host.Speak`/`Transcribe`, and the `nova speak` and
  `nova transcribe` subcommands. There is no TUI entry point.
- `provider.Model` gains `Voices` (the roster's `supported_voices`) and the
  `SupportsSpeech` / `SupportsTranscription` predicates (#85).
- The `voice` config block: `allow_aliases`, `cost_lookup`, `speech.roles` and
  `transcribe.roles`, plus `extensions.speech_max_per_run` and
  `extensions.transcribe_max_per_run` (default 20 each) (#85).

### Changed

- The protocol line cap rises from 16 MiB to **64 MiB** in `proto`, the
  extension host and the SDK reader, and `serve`'s request-body default rises
  from 32 MiB to 64 MiB. A 40 MiB audio clip is a 53 MiB base64 line, so the old
  cap could not carry the largest clip the library allows. This is a relaxation
  for Nova's own ends, but a third-party protocol client with a limit below
  64 MiB will fail on a clip above ~12 MiB (#85).

### Fixed

- `POST /v1/commands` on `serve` no longer cuts a long command's response off at 30 seconds
  (#85).
- A subagent now reaches the parent's embedder and decider. `sub.Spawner` never
  filled `Decide`, and `Embed` was missing from the parent entirely, so a
  child's tool calling `tctx.Embed` or `tctx.Decide` saw nil (#87).

## [0.3.5] - 2026-11-07

Upgrade notes for library consumers (#53):

- `agent.Config` gains `ErrorHint func(error) string`. It is additive and optional: a nil hook
  changes nothing. When set, its result is appended on a new line to the `error` event's message,
  after the error's own text.
- If you run `mode.New` without `Options.Provider` (Nova owns the provider), the `error` event for a
  provider 401 now ends with a second line such as `The OpenRouter key was rejected: run "nova
  login" or set OPENROUTER_API_KEY`. Code that matches on the whole message should match on a
  prefix. The event's `code` and the provider's text are unchanged, and a host that supplies its own
  `Options.Provider` sees no change.

### Fixed
- A provider that rejects the key (HTTP 401) now ends the run with a sign-in hint for the provider
  serving the current model (`nova login`, or `nova login opencode-go`), after the provider's own
  message, instead of a bare `(status 401)` line. Other failures get no hint (#53).
- `nova doctor` now probes OpenRouter's authenticated `/auth/key` route when a key is configured,
  so a revoked key reports `fail` with a `nova login` fix instead of `ok`. Only a 401 counts as
  a rejected key: a 403, a 5xx, or a timeout stays a warning. Other base URLs are still probed
  at `/models`. Thanks to @mikemikimike for the first version of this (#53, #79).
- `opencode-go/deepseek-v4.1-flash` now carries `multimodal: true` in the Go
  baseline, so `AcceptsImages()` is true and `InputModalities` reads
  `[text, image]` — it takes `image_url` parts live, and the entry said
  text-only (#81).

## [0.3.4] - 2026-11-05

Nova is `v0.x`, so anything here may break callers. **Upgrade notes for library consumers:**

- `provider.Usage` gains `AllowanceUSD`, `ListCostUSD`, and `BillingSource`, and `CostSource`
  gains `"allowance"`. A Go call now reports `CostUSD` as allowance dollars instead of falling
  back to a catalog estimate: if you match on `CostSource`, handle `"allowance"` (a covered
  call is not real spend; `usage.Ledger` splits them as `Covered` vs `Real`).
- `provider.Response` gains `Provider`, the id of the backend that served the call. Routers
  stamp it; plain providers leave it empty unless they set it.
- `provider.Model` gains `ReasoningEfforts`, `ToolChoice`, `PriceTiers`, `CostMultipliers`,
  and `MonthlyLimitUSD`. All are additive; an unlisted model reads as the zero value.
- `mode/providers.Build` now returns a `provider/route` router. Behaviour is unchanged
  (single attempts, no fallback), but `Name()` is `"route"` instead of `"router"`.

### Added
- A library-consumer provider router, `provider/route`: named providers built from values,
  per-catalog-id attempt chains, `OnLimitOrRefusal`/`OnAnyError` fallback predicates with
  exactly-once spill semantics, a visible `OnFallback` event, `Resolve`/`Primary` inspection,
  a dynamic `Unknown` hook, and a `ForceProvider` kill switch. `mode/providers` builds on
  it, so the TUI/`serve` path and every consumer run the same routing code (#59).
- Allowance accounting across all three wire dialects: Go's `usage.opencode` block parses to
  `AllowanceUSD`/`ListCostUSD`/`BillingSource` with `CostSource` `"allowance"`, including an
  explicitly reported zero, and `usage.Ledger` reports covered vs real spend (#59).
- A one-shot completion seam, `provider/chat.Complete`: plain messages and tools through the
  same routing, headers, and usage reporting as a loop turn, with no agent (#59).
- A key-supplied meter fetch, `opencode.FetchMeters`, with `opencode.PeriodEnd` for the plan
  period end; `FetchUsage` keeps Nova's config-based behaviour on top of it (#59).
- Picker fields on `provider.Model` — reasoning effort tiers, `tool_choice` variants,
  prompt-size price tiers, time-based cost multipliers, and docs-only monthly allowances —
  populated for the Go baseline from the live catalog (#59).
- A Go-dialect `testkit` double: it requires `x-opencode-session`, emits
  `usage.opencode`, and serves `/api/go/status` (#59).

### Fixed
- `provider/opencodego` sends the bare wire slug even when called with a catalog id, and
  dispatches on the id first, so the endpoint table (not a heuristic) picks the dialect (#59).
- Error bodies that report `error.type` with no `error.code` (Go's `MissingSessionID` and
  `EntitlementError`) now yield a `Code` the fallback predicate can classify on (#59).

## [0.3.3] - 2026-11-05

**Install this.** `v0.3.0`, `v0.3.1` and `v0.3.2` are tagged and resolve as Go modules, but none
published binaries: their release workflows died on the way, each one stage further than the last. This
is the `v0.3.2` tree plus the two fixes the build jobs needed, and the release that publishes. The
feature list is `v0.3.0`'s, further down. Nothing here changes behaviour for a caller: the diff is
`scripts/package.sh`'s file mode and `release.yml`.

### Fixed
- The build jobs package their archives. `scripts/package.sh` was tracked without an execute bit, so
  every job died at `./scripts/package.sh: Permission denied` after the acceptance gate had passed
  (#74).
- A `workflow_dispatch` run now goes through the build matrix and stops before publishing, so the
  archive step is exercised before a tag rather than by one (#74).

## [0.3.2] - 2026-11-05

**Install this.** v0.3.0 and v0.3.1 are tagged and resolve as Go modules, but neither published
binaries: both release workflows died in the acceptance gate, which had been broken since v0.2.0. This
release is the v0.3.1 tree plus the fixes that got the gate through, and the first since v0.2.0 with a
GitHub release and downloadable archives. The feature list is v0.3.0's, below. Nothing here changes
behaviour for a caller: the diff is tests, CI configuration, and the C00 spec.

### Fixed
- A tag now publishes. The gate is a loop over every archived chunk, and three of its tests failed
  when it first reached them: `SUP12` slept 50ms of real time hoping a fake-clock timer had
  registered and failed under load (#67); `TestProperty_BSH_GroupKill` gave its command 300ms to
  start bash and write a pid file, so a loaded runner killed it first (#71); and the splash GIF
  re-render ran under `-short`, which made the whole package slow everywhere the fast loop touched it
  (#65). All three are test-only.
- The release gate can be run without tagging (`workflow_dispatch`), which is how this release was
  checked before `v0.3.2` was cut (#70).

### Changed
- CI costs a third of what it did. A branch push ran two identical pipelines because the workflow
  triggered on both `push` and `pull_request`; `push` is now limited to `main`. The splash job ran the
  GIF renderer under `-race` — seven minutes for a package that starts no goroutine — and now runs it
  plain in about 40 seconds, with `-race` kept for `make race-splash` and for every release (D159).

## [0.3.1] - 2026-11-04

**Install this, not v0.3.0.** The v0.3.0 tag exists and the Go module resolves, but its release
workflow died in the acceptance gate, so no GitHub release and no binaries were published — the same
failure that hit v0.2.0. This release is the v0.3.0 tree plus the two gate fixes, so a v0.2.0 user
should upgrade straight to `0.3.1` and read the v0.3.0 notes below. Nothing here changes behaviour for
a caller: the diff from `v0.3.0` is `scripts/accept`, its test, one `cmd/nova` test file, and the C00
spec that documents the gate.

### Fixed
- A tag publishes its binaries again. `scripts/accept` ran its test steps with no `-timeout` and
  inherited Go's ten-minute default, so the chunk that owns `tui/splash/splashgif` was killed there
  while the same package needs about eight minutes under `-race`; every chunk now states its budget,
  25m for the chunk that owns that package and 10m otherwise (#62).
- The `-short` step passes on a clean checkout. Six example-extension tests started a binary that only
  a `-short`-skipped test builds, so the gate failed at C19 on any fresh clone; they skip under `-short`
  now, as their siblings already did (#60).

## [0.3.0] - 2026-11-04

An embeddings primitive and the shared role resolver it sits on, plus a rebuilt `ask` form. Nova is
`v0.x`, so this release may break callers. **Read these before upgrading from v0.2.0:**

- `bundled/ask.Card` takes the call's question labels (`[]string`) instead of a count (#54).
- Every `ui_request` now carries an `id` (#55). The server assigns one whether or not the caller set it,
  so a client that used to answer by an empty id must echo the id it is given.
- In the `ask` form, `s` is no longer a skip shortcut; skipping is the visible `Skip this question` row,
  and the tab strip shows answered, skipped and unvisited questions (#54).

### Added
- An embeddings primitive, `embed`, with a role table on the shared resolver: `embed.Embedder` (text in,
  vectors plus cost and provenance out), OpenAI-compatible and Ollama adapters that batch and remap by
  index, an OpenRouter preset with the live embeddings roster, and a `embed.Roles` decorator that refuses
  a fallback whose dimensions disagree instead of corrupting a stored index. It reaches tools
  (`tool.Context.Embed`), the loop (`agent.Config.Embed`), print/json/rpc/serve (the `embed` command),
  extensions (`model:embed`, capped by `extensions.embed_max_per_run`), and the TUI (`/embed`). There is
  no vector store (#49).
- The role resolver is a shared package, `roles`; `decide.Roles` delegates to it unchanged, and
  `decide.Role` and `decide.Clock` are now aliases of the shared types (#48).

### Changed
- The `ask` form is a borderless, full-width sheet sized to its content instead of a large empty box: a
  tab strip that shows answered (`☑`), skipped (`⊘`), and unvisited (`☐`) questions with `✓ Submit →` at
  the end; numbered options with `1`-`9` hotkeys and a visible cursor on multi-select; descriptions on
  their own dim line, wrapped or clipped with an ellipsis; type-to-answer from anywhere on a choice
  question; a `Notes: ctrl+n` affordance; and a visible `Skip this question` row, so `s` is no longer a
  hidden shortcut (#54).

### Fixed
- Dialogs draw the styling they already asked for, and opening one no longer flattens the colours of the
  transcript behind it. The clipping that every dialog line goes through was running the line through the
  untrusted-text sanitiser, which stripped the SGR sequences along with everything else (#54).
- A UI request from an extension reaches the client with an id, so a form, select, or input dialog
  can actually answer it. Before this, `ask` opened the form and then hung when it was submitted,
  because the TUI dropped an answer for an empty id (#55).
- Capital letters and shifted symbols are typed in the ask form and in every text input dialog. The
  buffers were reading `Keystroke`, which spells a shifted letter `shift+a`, so only lowercase (and
  only pasting for anything else) worked. The model picker filter also accepts a space now (#54, #56).
- The generic text input dialog (`/todos`, `/plan`, extension `input` requests) accepts a typed
  space; it had the same single-rune filter and dropped it (#54).
- Space works in the ask form's text answer, its free-text row, and a note. The text buffers only
  accepted single-rune keystrokes, and Space arrives as `space`, so a multi-word answer could only be
  pasted (#54).
- The `ask` tool card reports the call's questions (`ask: 3 questions (Ambiguity, Explanations,
  Off-topic)`) instead of always `asked 0 question(s)`; while the tool is running, before any result
  details exist, it names them from the call's arguments (#54).

## [0.2.0] - 2026-10-27

The first release a Go program can depend on by tag: Polaris is adopting Nova as a library (#22), and this
release carries what that adoption needed (the agent loop primitives, `agent/sub`, `tool.Context.CallID`, the
embedding example, and the six gaps in #40 to #45).

Nova is `v0.x`, so a minor release may break callers. **Read these before upgrading from v0.1.0:**

- `mode.Options.Out`, `In` and `Interactive` are removed.
- No turn cap is on by default anywhere (agent runs, subagents, the GitHub Action); set `MaxTurns` /
  `max_turns` if you relied on the old default (#21).
- The GitHub Action's `--max-cost` now defaults to unlimited instead of `$1.00`, and its review no longer stops
  at a turn count (#21).
- `compaction.PruneToolOutput` is removed; compaction is the only thing that shrinks the context (D134).
- An assistant message with no content, parts or tool calls stays in the transcript but is no longer sent to
  the provider (#43).

### Added
- Six gaps found by Polaris's adoption (nova#22), each small and opt-in or additive: `provider.Error.Body`
  keeps the scrubbed, bounded raw response body of a failed request (#40); `provider.Request.ParallelToolCalls`
  sends `parallel_tool_calls` on a chat-completions request that has tools (#41);
  `openaicompat.Config.EmptyToolCallContent` (and the same field on `openrouter.Config`) sends
  `"content": ""` on a tool-call-only assistant message instead of omitting it (#42); an assistant
  message with no content, parts or tool calls stays in the transcript but is no longer sent (#43);
  `agent.Config.RequestTimeout` bounds one provider attempt (#44); and `agent.Result.Final` is the
  closing assistant message's text, with `Answer` documented as the last non-empty text of the run (#45).
- `tool.Context.CallID`: the loop now tells a tool which call it is serving, the same id the
  `tool_start`, `tool_update`, and `tool_end` events and the assistant message's `tool_calls` carry, so
  an embedding application can tag the events a tool emits itself (#38, LPI09–LPI12). It is empty when
  a tool runs outside a loop.
- **`agent/sub`**, an in-process primitive for child agent runs, so a program that embeds Nova can fan
  out sub-agents without rewriting the hard parts: a scoped `Registry.View`, fresh `tool.Context.Data`
  (`sub.Depth`), event tagging, usage folded into the parent exactly once through `Parent.AddUsage`, a
  depth cap and a per-depth concurrency cap, and cancellation inherited from the parent's context with
  no goroutine of its own. `bundled/subagent` is now a wrapper over it. A child's events reach the
  parent's stream with `run` rewritten to a child id, and the events that address a run (`run_start`,
  `run_end`, `usage`, `message_end`, and the rest) never cross, so a client's answer and totals stay
  the parent's (D149, LPY01–LPY10).
- A nested sub-agent now honours the depth cap. The extension and the primitive had separate `Depth`
  types, so every nested child looked top level and the cap was effectively off (SUB34).

- **`examples/embed/`**, a standalone program that uses Nova as a library: it builds a provider from a
  key it holds, registers two domain tools whose dependencies ride on `tool.Context.Data`, keeps its own
  history through `OnMessage`, maps `proto.Event` to its own event type, scopes tools per run with
  `Registry.View`, ends a run early from a `TurnEnd` hook, and reports usage and cost from `Result`.
  It imports the harness layer only, and `scripts/importcheck` now fails if it imports `mode`, `config`,
  `session`, `tui`, or `bundled`, so the embedding story is guarded rather than asserted (#31).
- **Live stream filtering**: `agent.Config.TextFilter` and `Config.ReasoningFilter` build a fresh
  stateful filter per provider attempt, so an app can hold the opening chunks of a pseudo-tool-call and
  then release or drop them without changing what is stored. `Hooks.StreamDelta` composes after the
  filter and can pass, rewrite, hold, or drop each chunk. Both control only live `text_delta` /
  `thinking_delta`; `Result.Answer` and the session keep the original text. A filter panic is reported
  as `extension_error` and the text passes through; the hook runs under `HookTimeout`.
- **`agent.Config.WrapUp`** gives a run that hits `MaxTurns` one final request with no tools. Nova
  appends a wrap-up instruction, makes the request, and ends normally with the answer; if the model
  still calls a tool or returns nothing, the run records a synthetic fallback and ends with
  `Exhausted=true`. The extra call's usage is in `Result.Usage`, and a `TurnEnd` hook cannot extend the
  budget. Unset keeps the old behaviour exactly.
- **Decision models**: a `decide.Decider` primitive for typed questions answered in parallel with
  calibrated probabilities, with a `decide/openrouter` client for the Decisions API and the Jev-only
  `/systemone` surface. Configure roles under `decide.roles` (model, optional fallback, per-attempt
  timeout); an alias id (`-latest` or `~...`) is refused unless `decide.allow_aliases` is set. Tools
  reach the model through `tool.Context.Decide` and hooks through `agent.Config.Decide`, mirroring
  `Complete`; the call's cost is counted through `AddUsage`.
- Decision questions cover all three documented kinds: `choice`, `noul` (a probability), and `score`
  (an ordered set of levels with an optional legend). `Instructions` may be structured, and a score
  keeps its levels in `Question.Levels`.
- Extensions can call decisions too: a `model:decide` capability, a `decide`/`decide_result` frame,
  `ext/sdk.Decide`, and `ext.Env.Decide`. `extensions.decide_max_per_run` (default 20) caps a run. A
  `decide` protocol command carries one request, and `/decide` / `/decide models` show the configured
  roles and the live roster.
- A **`web-search` example extension** showing a provider-agnostic search adapter: one `Backend`
  interface, a canned backend that needs no key, and a README recipe for Parallel, Tavily, Brave, or
  Google. It is an example, not a shipped tool — install it, read it with `nova docs example-web-search`,
  or copy it and fill in your own provider.
- **`max_tokens`** caps the output of every request, from the config file, `NOVA_MAX_TOKENS`, or
  `--max-tokens`. It applies to the agent loop and to nested tool calls that set no cap of their own.
  Without it a gateway assumes the model's maximum output, which a small balance cannot afford
  (OpenRouter rejects `claude-opus-5.5` for a key that cannot cover 65,536 tokens).
  A subagent inherits the cap, so a child run cannot spend more per call than its parent.

- `/reload` (and the `reload` command) now **re-reads the config**, not only skills, prompts and extensions. It waits
  for an active run, applies the tools, retry policy, limits and system prompt the file now holds, and keeps what was
  chosen at runtime (model, thinking level, `set_tools`) on top. A config that does not load is left as it was and
  reported as `config_error` (D137).
- `nova --resume` opens the session picker. `nova logout [provider]` removes the key of the provider you name
  (`openrouter` by default, `opencode-go`); an unknown name is an error that names it.
- `tools.bash.scrub_env` names extra environment variables to hide from commands the model runs, and
  `tools.bash.inherit_credentials: true` turns the default off. Nova's own provider keys, OAuth tokens and
  `NOVA_SERVE_TOKEN` are hidden from `bash`, command hooks, declarative extension tools and extension build steps by
  default, so a model-run `env` can no longer print them (D135).
- `run_end` carries `dropped_queue` (steering and follow-up text the run ended without consuming), and an aborted run
  or a failed turn puts those messages back in the input box instead of losing them. The restored messages leave the
  transcript so they are not shown twice.
- `nova ext install` reports an extension that is already current as "up to date" rather than an error, and
  `nova ext list` warns about a damaged lockfile on stderr.
- The crash and debug logs are capped: a log past 4 MiB moves aside to `<name>.1` and a new one starts.
- `diag` records a stack trace for a recovered panic, and `Agent.Reconfigure` lets a library caller change an idle
  agent's settings (it returns `ErrBusy` during a run).
- **Prompt caching** for models that do not cache on their own. A request to those
  models now carries an ephemeral cache marker, so an agent turn reuses the previous
  turn's prefix instead of re-billing the whole history: about 10x cheaper on
  Anthropic models. The catalog decides per model, and `agent.Config.PromptCache`
  (or `provider.Request.PromptCache`) overrides it either way. Cache writes are now
  counted (`Usage.CacheWriteTokens`, shown as `cache_write_tokens` on the wire) and
  priced at 1.25x input by the cost fallback (#28).

### Fixed
- A bundled extension's tool can be named in `tools.active`, `--tools`, or `set_tools`: the list is
  checked against both the built-in pool and the tools each enabled extension declares, so
  `--tools read,subagent` no longer fails with `unknown tool "subagent"` before the extension has
  started. A name from a disabled extension, or a typo, is still an error.
- A subagent now stays on the session's provider. The child run was given only the wire slug, so a
  session on OpenCode Go quietly sent its subagents to OpenRouter (or failed with "no API key"); the
  child now carries the parent's catalog model id, and a tool's `model` argument that names another
  catalog model re-routes it correctly.
- A truncated Responses response no longer reports zero usage: `response.incomplete` carries the
  same usage object as a completed one, and dropping it hid real spend. Both the Responses and the
  Messages dialect now also trust a cost the gateway reports (OpenRouter sends one for MiniMax on
  `message_delta`), so those calls stop falling back to the catalog estimate (#28 follow-up).

- The Messages dialect now reports the **whole** prompt in `prompt_tokens`. Anthropic's
  `input_tokens` is only the uncached remainder, so cache reads and writes are added back:
  cost estimates stopped under-billing a warm or cold cache, and the status region's `↑in`
  shows the real prompt size (#34).

**Security**

- **Terminal escape injection.** Text from files, tool arguments, tool output, extension widgets, window titles, the
  working directory, git branch names and the trust prompt is now cleaned of escape sequences, control characters,
  newlines where a single line is expected and bidirectional overrides before it is drawn (`tui/sanitize`). The
  approval prompt can no longer be made to show a command other than the one that will run, and syntax-highlighted
  and diff output no longer lets a file's escape sequences through.
- **Opening a folder cannot run code.** The status line's `git` reads switch off `core.fsmonitor`, which a hostile
  repository's own config could point at a program.
- **Prompt-injection fence.** The GitHub review action wraps pull-request text in a per-run random fence the text
  cannot close, caps comment and summary sizes, and gives its GitHub client timeouts and bounded retries.
- **Fetch hardening.** `web_fetch` refuses reserved, tunnelled and link-local address ranges, closes its connections
  and cuts text on character boundaries. `nova update` and `install.sh` bound every download, validate the version
  they are told to fetch, refuse non-regular archive members and resolve the target through symlinks.
- Deny rules can no longer be dodged by a newline: glob wildcards match newlines. `grep`, `read` and `edit` refuse
  a named pipe instead of blocking on it (D136), `edit` rejects overlapping matches, and manifests, config, skills
  and prompt templates are read as bounded regular files. A directory that contains the home directory cannot be
  trusted. Provider error messages are bounded and scrubbed.

**Correctness**

- `checksums.txt` listed `./nova_….tar.gz`, a name every install looked up without the `./`, so verification could
  not succeed. It now lists bare names, and `install.sh` and `nova update` read every common form.
- The release workflow runs with least privilege and `action.yml` passes inputs through `env`, not into shell text.
- `GET /v1/events` could hang a client: the stream is subscribed before the headers go out, so nothing is lost
  between them. Request bodies are bounded in time, expired UI requests are forgotten, and a failed read is a 400
  while an oversize body is a 413. An upload reports the type the server sniffed, not the one the client claimed.
- A steering message sent as a run ends is no longer lost, `set_tools` keeps extension tools
  registered, `reload` reports an extension that fails to rebuild, and `rpc` keeps delivering events while a slow
  command runs. The run is deregistered before `run_end`, so `Running()` is false by the time a client sees it.
- `RetryPolicy.Jitter` of zero now means the default and a negative value means none. A mid-stream provider error
  fails the turn, and a tool call with truncated arguments still encodes.
- An Anthropic stream that reports `overloaded_error`, `rate_limit_error` or `api_error` partway through is now
  retried like the same failure arriving as an HTTP status; a bad request or key still is not. The Responses
  dialect now retries a rate limit or overload reported inside its stream the way the chat dialect already did.
- An extension's `complete` cap is per run, as documented. It was counted for the whole session, so an extension
  that used its cap was refused for good.
- A JPEG tool image is converted to PNG for Kitty, which draws only PNG, instead of showing nothing.
- A pasted API key works on the first-run screen (the paste was ignored), and server text there is cleaned.
- Compaction counts replies and images, keeps tool names and cuts only on real turns.
- The `tasks` tool changes nothing when an update is rejected and no longer reports an unsaved change as done. The
  `ask` tool shows the model the "Other" text of a multi-choice answer. Sub-agent events and usage stay separate.
- An `$EDITOR` that carries arguments now runs (and its temp file is cleaned up), a stale file completion is dropped, equal-length pastes merged into one, and a quit timer that
  forked the tick chain are fixed. `!cmd` runs without the bang and shows what it printed.
- `nova docs <unknown>` now exits 1 and lists the topics on stderr, instead of exiting 0 with the list on stdout.
- `docs` search excerpts no longer split a multi-byte character.

### Changed

- The unused `mode.Options` fields `Out`, `In` and `Interactive` are gone. Library callers that set them should
  drop them.
- The `tool` package owns the head-and-tail output buffer and environment filtering that `tools/bash` and `ext`
  each carried a copy of, and a new leaf package `proc` owns process-group start, signal and exit status.
- Request-time pruning of old tool output is removed (`compaction.PruneToolOutput`): it rewrote the middle of the
  history, so every turn after it missed the prompt cache. Compaction is the only thing that shrinks the context
  (D134).
- The one-line footer is now a multi-line **status region**: cwd and git branch/changes, token counts
  (`↑in ↓out Rcache CH%`), cost, context ratio/window with `(auto)`, session and block timers, and the hint, with
  the provider/model/thinking right-aligned on the last line. Extension status segments wrap onto extra rows
  instead of being truncated at one line. The header is now just `✦ nova`, and `/settings` has a status section
  (extension rows, git, timers, model) persisted under `display.status` (#17).
- The splash banner now plays a **hero phase**: for the first half of the sequence it shows the tallest scene that
  fits — Full on a large terminal, Compact on a normal one — then settles to the five-row lockup. When the
  sequence ends it **holds the final frame** at the top until the first prompt is sent, then collapses to the
  header. The transcript and editor stay usable beneath it, so the full animation is back without taking over the
  session (#26).
- The opening splash is a top banner instead of a full-screen takeover. It plays for its full 8 second sequence
  while the header, transcript, and editor stay usable beneath it; typing and transcript events no longer dismiss
  it, and it collapses to the one-line `✦ nova` header once the first prompt is sent. `/display splash off` still
  removes it immediately, and a short screen drops the banner before the header, transcript, or editor (#16).
- The editor grows with the prompt to 10 visual rows, counting soft wraps as well as newlines, and follows the
  cursor past that; a paste, a history recall, or a resize grows it too. The old cap was 8, and a paste did not
  grow it at all (#18).
- Once the editor is taller than its 10 rows, the mouse wheel scrolls the editor window without moving the cursor;
  the next edit snaps the window back to the cursor. Over the transcript (or an editor that fits), the wheel still
  scrolls the transcript (#18).
- Tool cards are full-width panels with one cell of padding and a background tinted by state: pending while
  running, success when ok, error on failure. The panel wraps every tool, the `subagent` card included, and the
  new theme tokens are `panel`, `panelok`, `panelerr`, and `paneluser` for the user's own messages, which now get
  their own background too. `mono` and `NO_COLOR` stay background-free and are only padded (#15).
- Transcript items are closed with one blank line, so tool cards, thinking blocks, and prose no longer run together
  (#15).
- No turn cap is on by default anywhere. An agent run, a subagent, and the GitHub Action run until the
  model finishes, the user aborts, or a real error ends them; `max_turns` (and the subagent's `max_turns`)
  are optional and off unless a caller sets one. The abort path is now the runaway guard (#21).
- The GitHub Action's review no longer stops at a turn count. Its wall clock stays but is configurable:
  `--wall-clock` (and the `wall-clock` action input) defaults to 15 minutes and takes any duration, with
  `0` meaning no limit. `--max-cost` now defaults to `0` (unlimited) instead of `$1.00` (#21).
- Finished tool calls show a 6-line preview of their result (the tail for `bash`) with an expand hint, instead
  of a bare header (#5).
- Finished thinking blocks keep a tail preview of the reasoning on screen, in dim italics under a ruled
  header, instead of collapsing to a single line (#5).

### Added

- Images. Attach one to a prompt with `ctrl+v` (reads the OS clipboard), by naming its path in the
  prompt, or by picking it with `@` completion; it rides `prompt`, `steer`, and `follow_up` to the model.
  The TUI shows a pending chip above the editor and the transcript renders a received image inline on a
  Kitty terminal (off where there is no graphics protocol, and off by default in `mono`/`NO_COLOR`).
  `display.show_images` and `display.image_width_cells` tune it (#20).
- A model that cannot take images is handled rather than crashed into: a new prompt carrying one is refused
  with a `bad_request` naming the model, and an image already in the session (or from a tool) is replaced
  with a one-line omission note so the run continues. `list_models` and the state report the model's input
  modalities (#20).
- Tools can show the model an image: `read` on an image path returns a text note plus the image, a tool can
  return `Result.Parts` (and an extension a `tool_result` `parts` field), and the image is normalized and
  bounded the same way a pasted one is (#20).
- `serve` takes an image as raw bytes at `POST /v1/attachments` and answers with an id a later `prompt` can
  reference, so a browser or remote client can attach one without inlining base64 (#20).
- `/settings` (alias `/config`) opens a display-settings dialog: `up`/`down` choose a row, the arrows
  or typing edit it, `enter` saves, `esc` cancels. Changes apply live and are written to the user config.
  The first two settings are the thinking-trace height (`display.thinking_lines`, default 8) and the tool
  result height (`display.tool_lines`, default 6), both clamped to 1–50. `/display lines N` still works and
  now persists too (#14).
- An always-visible run indicator. While a run is active the footer leads with a spinner and the elapsed
  run time (`⠋ working 3s`), animated on the 250 ms tick chain and cleared the instant the run ends. It fills
  the waiting-on-the-provider gap that the per-block spinners miss, and it survives truncation on a narrow
  terminal because it is the first segment (#19).
- Crash reports: a panic now restores the terminal, prints a one-line error and the path to
  `<config dir>/nova/crash.log`, and records the version, mode, model, last operation, and a full stack. Panics in
  Nova-owned goroutines are caught into the same report instead of escaping (#11).
- `nova log` (alias `nova crash`) prints the most recent crash report and `nova log --path` prints the file; the
  TUI's `/log` opens it in a dialog and `nova doctor` shows the crash-log path and the last crash (#11).
- `--debug` and `NOVA_DEBUG=1` write routine activity to `<config dir>/nova/debug.log`, so a crash report has
  context (#11).
- Clicking a tool card or thinking block expands or collapses just that block. `ctrl+o` still expands or collapses
  every block, and `/display` sets what new blocks do (#5).
- `/quit` and `/exit` leave the TUI cleanly: they cancel open dialogs, send `shutdown`, and quit when the
  server confirms. Pressing `ctrl+c` twice does the same, and the footer shows a `ctrl+c again to quit` hint
  once the first press arms it (#10).
- `nova update --target <path>` and `NOVA_INSTALL_DIR` choose which binary `nova update` replaces, and the
  command prints the target it is using.
- Styled `--help` and `nova version` with JSON output.
- First-run flow: log in or paste a key, then pick a model.
- `nova doctor` for checking the installation.
- `nova docs` topics and ranked search, including the examples and the SDK.
- The built-in `extension-author` skill.
- Ten example extensions under `examples/extensions/`.
- A favicon variant of the logo.
- A release workflow with checksummed binaries and `install.sh`.

### Changed

- `/login` shows a provider as ready when its key comes from the config, and the OAuth sign-in dialog shows the
  loopback callback status. After a login the model picker opens only when the current model needs it, and
  `/logout` from the provider serving the current model switches to another signed-in provider's model.
- `list_providers` reports `current` and `default_model`, and counts a key from the loaded config as ready.

### Fixed

- The splash scene no longer writes `press any key`: a key does not dismiss the banner, so the prompt was wrong.
  The README GIF is regenerated from the same scene, so the two still agree (#25).
- The first-run screen offers every provider Nova can sign in to, not only OpenRouter. It is built from the
  same `providerauth` registry `/login` uses, so a new user can pick OpenCode Go (device code) and sees the
  verification URL and user code while the flow runs, or paste either provider's key (#3).
- Assistant markdown no longer carries glamour's document padding: paragraphs are flush left instead of
  indented two cells, and blocks are separated by a single blank line instead of two. The streaming tail and
  the finished block agree, so text does not shift when a paragraph closes (#15).
- `/new` was advertised and autocompleted but returned `unknown command new`; it and its new alias `/clear`
  now start a fresh session and clear the transcript, and are refused with a notice while a run is active (#12).
- The TUI transcript now scrolls: the mouse wheel and trackpad work, `home`/`end` jump to the top and bottom,
  and scrolling up while a run streams no longer snaps back to the tail (#7).
- The input editor now takes its colours from the active theme. It used fixed terminal colours, including a black
  highlight on the cursor line that looked wrong on the light theme.
- A provider that panicked mid-run crashed `nova` outright; the run now ends as an error with its `run_end`.
- The `AgentEnd` hook now runs when a run is aborted. It was skipped or raced with `run_end` because the run's own
  context was already cancelled.
- `nova serve` now gives every route except the event stream a 30 s write deadline, so a client that stops reading
  can no longer hold a handler open indefinitely.
- OpenCode Go models were routed to OpenRouter by the running server, because the request carried the wire slug
  and not the catalog id; they now reach OpenCode Go.
- A prompt was refused with an OpenRouter key message for a user signed in only to OpenCode Go; the check now
  follows the provider that serves the current model.
- OpenCode Go requests now carry the conversation id as `x-opencode-session`.

- `nova update` under `go run` replaced a temporary binary and reported success; it now refuses and says to
  pass `--target` or set `NOVA_INSTALL_DIR`.

## [0.1.0] - 2026-10-09

### Added

- The first release: the agent loop, tools, sessions, config, the TUI, the
  extension host, the bundled extensions, serve mode, and the GitHub Action.

[Unreleased]: https://github.com/AutumnsGrove/nova/compare/v0.4.1...HEAD
[0.4.1]: https://github.com/AutumnsGrove/nova/releases/tag/v0.4.1
[0.4.0]: https://github.com/AutumnsGrove/nova/releases/tag/v0.4.0
[0.3.5]: https://github.com/AutumnsGrove/nova/releases/tag/v0.3.5
[0.3.4]: https://github.com/AutumnsGrove/nova/releases/tag/v0.3.4
[0.3.3]: https://github.com/AutumnsGrove/nova/releases/tag/v0.3.3
[0.3.2]: https://github.com/AutumnsGrove/nova/releases/tag/v0.3.2
[0.3.1]: https://github.com/AutumnsGrove/nova/releases/tag/v0.3.1
[0.3.0]: https://github.com/AutumnsGrove/nova/releases/tag/v0.3.0
[0.2.0]: https://github.com/AutumnsGrove/nova/releases/tag/v0.2.0
[0.1.0]: https://github.com/AutumnsGrove/nova/releases/tag/v0.1.0
