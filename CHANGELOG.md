# Changelog

All notable changes to Nova are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
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

[Unreleased]: https://github.com/AutumnsGrove/nova/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/AutumnsGrove/nova/releases/tag/v0.1.0
