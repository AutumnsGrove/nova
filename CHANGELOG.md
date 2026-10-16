# Changelog

All notable changes to Nova are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

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
