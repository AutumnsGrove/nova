# Nova — TUI

The terminal UI is a client of the protocol ([`PROTOCOL.md`](PROTOCOL.md)). It lives
in the `tui/` package, is excluded by `-tags notui`, and is the only part of the
core tree allowed to import `charm.land/*` (D15). Its experience is modelled on
[Crush](https://github.com/charmbracelet/crush): calm, information-dense, keyboard-first.

## Stack

Versions are those Crush uses today. They are pinned in `go.mod`; chunk C15 confirms
they resolve before anything else is written.

| Library | Version | Role |
|---|---|---|
| `charm.land/bubbletea/v2` | v2.1.0 | event loop |
| `charm.land/lipgloss/v2` | v2.0.6 | styling |
| `charm.land/bubbles/v2` | v2.2.1 | textarea, viewport, spinner, key bindings |
| `charm.land/glamour/v2` | v2.0.1 | markdown |
| `charm.land/log/v2` | v2.0.1 | logging to a file (never stdout) |
| `charm.land/fang/v2` | v2.0.1 | styled CLI help and errors, through `tui/cli` (D45) |
| `github.com/charmbracelet/ultraviolet` | pinned | screen buffer and layout rectangles |
| `github.com/charmbracelet/x/ansi` | v0.11.9 | all ANSI-aware string work |
| `github.com/alecthomas/chroma/v2` | v2.27.0 | syntax highlighting |
| `github.com/charmbracelet/x/exp/golden` | v0.1.0 | golden-file comparison (tests) |
| `charm.land/catwalk` | v0.52.49 | snapshot testing of components (tests) |

## Rules of the code

Taken from Crush's `internal/ui/AGENTS.md`, because they work.

- **One Bubble Tea model.** The top-level `UI` is the only `tea.Model`. Sub-components
  (chat list, editor wrapper, dialogs, widgets) are plain structs with imperative
  methods. They do not have their own `Update`.
- **No I/O and no expensive work in `Update`.** Use a `tea.Cmd`.
- **Never mutate model state inside a command.** A command returns a message; `Update`
  applies it.
- **Never touch ANSI strings by byte.** Use `ansi.Cut`, `ansi.StringWidth`,
  `ansi.Strip`, `ansi.Truncate`.
- **Rendering is a pure function of state.** Every item renders from its data and a
  width. This is what makes golden tests possible.
- **Items cache their render** and invalidate on change or width change.
- **Themes are data.** A theme is a palette of tokens that builds every style. No
  component hardcodes a colour.

## Screen layout

```
┌──────────────────────────────────────────────────────────────┐
│ ✦ nova   deepseek/deepseek-v4.1   ~/code/project   main      │  header
├──────────────────────────────────────────────────────────────┤
│                                                              │
│  you   add a retry to the fetch call                         │
│                                                              │
│  ▸ Thinking for 12s · ~3.4k tokens             click to expand  │  thinking block
│    …the call is in client.go and has no retry…               │
│                                                              │
│  ● read  client.go                                  0.1s     │  tool card
│  ● edit  client.go   +9 −2                          0.0s     │
│    ┌ diff ───────────────────────────────────────────┐       │
│    │ - resp, err := http.Get(u)                      │       │
│    │ + resp, err := retry(ctx, func() ...            │       │
│    └─────────────────────────────────────────────────┘       │
│                                                              │
│  nova  I added exponential backoff…                          │  assistant text
│                                                              │
├──────────────────────────────────────────────────────────────┤
│ ☐ 1 of 3 tasks  · fix retry ▸ write test ▸ update docs       │  widgets (extensions)
├──────────────────────────────────────────────────────────────┤
│ > _                                                          │  editor
├──────────────────────────────────────────────────────────────┤
│ ctx ▮▮▮▮▯▯▯▯ 42% · $0.031 · plan:off · 2 queued    ///  ext… │  footer
└──────────────────────────────────────────────────────────────┘
```

Regions are rectangles computed from the terminal size by one `layout()` function.
Widgets appear above the editor, below it, or in a sidebar that shows only on wide
terminals. A narrow terminal drops the sidebar, then the header detail, then the
footer detail, in that order.

## Transcript items

| Item | Notes |
|---|---|
| User message | a full-width panel with its own tint (`paneluser`), so it stands out from the assistant; plain text, wrapped; shows attached files |
| Assistant text | **incremental markdown**: re-render only the changed tail; code blocks highlighted; flush left with a single blank line between blocks |
| **Thinking block** | native (D24). A ruled header (`── ▸ Thought for 12s · ~3.4k tokens ──── click to expand`), the last 8 lines of reasoning in dim italics, and a closing rule, both while streaming (live timer) and once finished, so thinking is never hidden. Click the block to expand just it; `ctrl+o` expands or collapses every block; the preview height is a setting. |
| Tool card | a full-width panel tinted by state (pending, success, error) with one cell of padding on each side, holding a header (status dot, name, key argument, duration) and a compact result box: the first 6 lines of the result (the last 6 for `bash`) with a muted `… N more lines (click to expand)` line. A call with no output stays header only. Click the card to expand just it; `ctrl+o` expands or collapses every card. |
| Compaction marker | a rule with `compacted 38k → 6k tokens` |
| Error | red, with the retry count when retrying |
| Extension message | rendered by the extension's template, or by a bundled renderer |

Each item is closed with one blank line, so neighbouring cards and blocks have room
to breathe instead of running together.

### Tool renderers

A registry maps tool name to a renderer. A renderer takes the call, the result, the
width, and an `expanded` flag, and returns a string. Defaults:

| Tool | Collapsed | Expanded |
|---|---|---|
| `read` | path and line range | first lines, highlighted |
| `write` | path and size | the content, highlighted |
| `edit` | path and `+N −M` | a unified diff, highlighted |
| `bash` | the command and exit status | command, then output (tail-biased, capped) |
| `glob`, `grep` | pattern and match count | the matches |
| anything else | name and a one-line argument summary | the arguments and result as text |

A tool the TUI has never heard of still renders through the default, so a new
extension tool works without any TUI change. Third-party tools can supply a template
over their `details` JSON for a nicer card.

Every card, whatever its renderer, is wrapped in a full-width panel with one cell
of padding on each side and a background tinted by state: pending while running,
success when ok, error on failure. That includes a `subagent` card, which shows its
child's tool activity as progress lines inside the same panel. Under `mono` or
`NO_COLOR` the panel carries no background and the card is only padded.

## Input

- Multi-line editor (`bubbles/v2/textarea`). `enter` sends, `shift+enter` or `alt+enter`
  newline, `ctrl+g` opens `$EDITOR`. It grows to 10 visual rows (soft wraps count too) and
  follows the cursor past that. Its prompt, text, placeholder, and cursor use the active theme.
- `/` opens command completion: built-ins, prompt templates, and extension commands.
- `@` completes file paths.
- `!cmd` runs a shell command and shows the output; `!!cmd` runs it without adding it
  to the model's context.
- Pasted text over a size limit is attached as a block, not inserted raw.
- Input history on `up` and `down`.

### Keys (defaults, all rebindable in config)

| Key | Action |
|---|---|
| `enter` | send. While a run is active, queue as a **steering** message |
| `alt+enter` | queue as a **follow-up** |
| `esc` | abort the run; in a dialog, close it |
| `ctrl+c` | clear the editor; twice quits (the footer shows `ctrl+c again to quit` once armed) |
| `ctrl+o` | expand or collapse **every** tool and thinking block |
| click | expand or collapse the block under the pointer |
| `ctrl+l` | model picker |
| `ctrl+t` | cycle thinking level |
| `ctrl+r` | session list |
| `pgup` / `pgdn` | scroll the transcript |
| `home` / `end` | jump to the top / bottom of the transcript |
| `tab` | complete |

Mouse wheel and trackpad scroll the transcript. Over a multi-line editor that
overflows its rows, the wheel scrolls the editor window instead, without moving
the cursor; the next edit snaps the window back to the cursor. Scrolling the
transcript up stops auto-follow and keeps the viewport anchored while new content
streams in; scrolling back to the bottom resumes it.

### Slash commands

`/model`, `/thinking`, `/context [tokens]`, `/compact`, `/new` (or `/clear`), `/resume`, `/fork`,
`/tree`, `/name`, `/cost`, `/theme`, `/settings` (or `/config`), `/reload`, `/ext`, `/todos`, `/plan`,
`/login`, `/logout`, `/help`, `/quit` (or `/exit`). Extensions add more. Each is also a protocol command, so
a headless client has the same set. `/new` and `/clear` start a fresh session file and clear the
transcript; they are refused while a run is active. `login`, `login_submit`, `login_cancel`, `logout`, and
`list_providers` are the protocol commands for sign-in ([`PROTOCOL.md`](PROTOCOL.md)); the local TUI runs
the sign-in flows itself and asks the server for each provider's status.

`/settings` (alias `/config`) opens a dialog over the display settings. `up`/`down`
choose a row, the arrows or `+`/`-` step it, typing a number sets it, `enter` saves,
and `esc` cancels. Each change applies live — tool cards and thinking blocks
re-render at the new height — and is written to `display.thinking_lines`,
`display.tool_lines`, `display.show_images` (draw a received image inline, on by
default where the terminal supports it), and `display.image_width_cells` (the
preferred inline width, 20..200 cells, default 60) in the user config, so it
survives a restart. `/display lines N`
is the shortcut for the thinking height and persists the same way. Adding a setting
is one row in the dialog's table plus one case in `applySettings`.

## Dialogs

A stack of overlays. Each is a struct with `Draw` and a key handler.

| Dialog | Used for |
|---|---|
| Permission | the `ask` outcome of the permission policy: shows the tool, its arguments, and the matched rule; allow once, allow for the session, deny |
| Select / input / confirm | `ui_request` methods from extensions |
| Sign-in | `/login` and `/logout`: a provider picker (readiness comes from the server, so a key in the config counts), a method picker, a masked key input, and the OAuth/device flow dialog. The OAuth dialog shows the callback status (waiting on the loopback address, then received) and keeps a pasted-redirect fallback for remote machines. After a login the model picker opens only if the current model has no signed-in provider; after a logout from the provider serving the current model, the model switches to another signed-in provider's default, or the notice says to run `/login` |
| **Form** | the `ask` tool: tabs per question, single and multi select, free text, a note per answer, skip, a review screen |
| Model picker | filterable list from the catalog, with context window and price |
| Session list and tree | resume, fork, navigate branches |

A dialog opened while a run is streaming does not interrupt the stream; the transcript
keeps updating underneath.

## Widgets and status

Extensions (bundled or third-party) publish to named slots through `widget` and
`status`. The TUI owns layout; an extension only chooses a placement and content. The
footer shows status segments left to right with a muted `///` between them. While a
run is active the footer leads with the run indicator: a spinner and the elapsed run
time (`⠋ working 3s`). It sits first so it survives truncation on a narrow terminal,
animates on the 250 ms tick chain, and clears the instant `run_end` arrives. It is the
global counterpart to the per-block spinners in the transcript, visible whatever the
transcript is scrolled to.

## Themes

A theme is a YAML palette that builds every style (the same idea as Crush's
`quickStyle`). The default palette comes from the brand, so the product and the logo
match ([`BRAND.md`](BRAND.md)).

```yaml
name: nova-dark
colors:
  spark: "#62c6ff"     # primary accent, links, focus
  ink:   "#f2f6fb"     # text
  core:  "#fff4cf"     # highlights, the active item
  warm:  "#ff9a5c"     # warnings, the companion accent
  bg:    "#0b0f16"
  muted: "#8b98ad"
  success: "#7ee0a1"
  error:   "#ff6b6b"
  panel:    "#1b2230"   # tool-card panel while running
  panelok:  "#112c1d"   # tool-card panel when ok
  panelerr: "#30161a"   # tool-card panel on error
  paneluser: "#122c44"  # the user's own messages
```

Shipped themes: `nova-dark`, `nova-light`, and `mono` (no colour, for dumb terminals and
`NO_COLOR`). The colour profile is detected and degraded (`colorprofile`).

## Splash and logo

The first screen of a new session is a short animation drawn on the character grid
rather than a downscale of the mark: a slash-filled `nova` wordmark slides in on the
left while a star on the right ignites and throws off a shell of gas that cools from
blue to orange, over a field of twinkling stars. It is specified in
[`archive/chunks/C21-splash-sequence.md`](archive/chunks/C21-splash-sequence.md); the design reference
is [`mockups/splash/`](mockups/splash/README.md).

The tier is the largest of Full (96 by 21), Compact (64 by 13), Thin (46 by 10), and
Lockup (40 by 5) that fits the transcript area. A terminal below 256 colours, the
`mono` theme, and an area under 40 columns fall back to the static Plain tier: `nova`
and `press any key`, centred. After the 8 second sequence nothing fades; the word keeps
shimmering, the stars keep twinkling, and the companion keeps orbiting until the first
keypress or event dismisses the splash. The UI drives it with a 66 ms tick that shares
the thinking block's tick chain, so only one timer is ever pending. The header uses the
**minimal mark** as a one-line glyph, `✦`.

The README banner is the same `splash.Cells` render rasterised to an animated GIF, so
it cannot drift from the terminal. Regenerate it with `go run ./tui/splash/splashgif`
whenever a change to `tui/splash` moves pixels.

## Modes the TUI degrades to

The same binary runs print and JSON modes with no TUI. In the TUI, `NO_COLOR` selects
`mono`, a terminal under 60 columns uses a single-column layout, and a non-terminal
stdin refuses to start the TUI and says to use `-p`.

## Bundled extension UIs

| Extension | UI it adds |
|---|---|
| `tasks` | a checklist widget above the editor (current task highlighted) and a full `/todos` view |
| `ask` | the form dialog |
| `plan` | a footer segment and a banner while planning, an approve/revise dialog |
| `subagent` | a nested, collapsible card with live progress and the sub-run's cost |
| `webfetch` | a card with the URL, status, and size |

## Performance budget

- A streaming token must reach the screen in one frame. Rendering work is bounded by
  re-rendering only the changed item.
- A transcript of 10,000 items scrolls without re-rendering items that did not change.
- `Update` does no I/O. Markdown rendering for finished items happens once and is cached.
- A resize re-renders only visible items.

These are tested with benchmarks and a golden test at three widths (see
[`TESTING.md`](TESTING.md), the TUI section).

## How the TUI is tested

- **Model tests**: send `tea.Msg` values to `Update`, assert on the model.
- **Golden views**: render `View()` at widths 40, 80, and 120 for each component and
  state, compare with `testdata/*.golden`. `catwalk` and `x/exp/golden` provide the
  harness.
- **Event replay**: feed a recorded protocol event stream (from a scenario) into the
  TUI and compare the final view. This ties the TUI to the real backend behaviour
  without running a terminal.
- **PTY smoke** (CI only): start, send a prompt against the fake server, quit.
