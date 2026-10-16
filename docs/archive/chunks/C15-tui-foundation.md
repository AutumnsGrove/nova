---
chunk: C15
title: "TUI foundation"
packages: [./tui/layout, ./tui/list, ./tui/styles, ./tui/client, ./tui/chat, ./tui/ui]
budgets: { ./tui/layout: 150, ./tui/list: 350, ./tui/styles: 450, ./tui/client: 200, ./tui/chat: 1300, ./tui/ui: 2420 }
depends: [C12]
---

# C15 — TUI foundation

## Goal

A working terminal UI that can hold a conversation: a transcript that streams, an editor,
slash-command and file completion, steering and abort, a footer with context and cost, and
a clean layout from 40 to 200 columns. It is a client of the protocol only. This chunk lays the
architecture that C16 (rich rendering) and C17 (dialogs, widgets) build on, so the structure
and the test seams matter more than the polish.

## Read first

- `docs/TUI.md` (all of it) and `docs/PROTOCOL.md` (commands, events, UI requests).
- `docs/BRAND.md` (the palette tokens).
- `mode/pipe.go` and `mode/server.go` from C12: the in-process client this UI talks to.
- Crush's `internal/ui/AGENTS.md` rules, restated in `TUI.md` ("Rules of the code").
- `docs/TESTING.md` section 1 (L4 and L5) and 3, "TUI".

## First step: confirm the dependencies

Before writing any code, `go get` the versions pinned in `TUI.md` (bubbletea v2.1.0, lipgloss
v2.0.6, bubbles v2.2.1, glamour v2.0.1, log v2.0.1, ultraviolet, x/ansi v0.11.9, x/term, chroma
v2.27.0, x/exp/golden v0.1.0, colorprofile). Run `go build ./...` and `go build -tags notui
./...` and `go run ./scripts/importcheck`. If any version does not resolve or the import
boundary breaks, **stop** and write the problem under `Open questions` in `NEXTSTEPS.md`. Do not substitute versions
silently. All tui files carry `//go:build !notui`.

## Files

| Package | File | Purpose |
|---|---|---|
| `tui/layout` | `layout.go` | pure rectangle computation |
| `tui/list` | `list.go`, `item.go` | virtualised list with cached item renders |
| `tui/styles` | `theme.go`, `styles.go`, `profile.go` | palette tokens, built styles, colour-profile degrade |
| `tui/client` | `client.go` | adapts `mode.Client` to Bubble Tea messages |
| `tui/chat` | `transcript.go` | folds protocol events and stored messages into items |
| `tui/chat` | `items.go` | user, assistant, error, compaction, and generic-tool items |
| `tui/chat` | `markdown.go` | incremental markdown renderer |
| `tui/ui` | `model.go` | the single `tea.Model` |
| `tui/ui` | `update.go`, `keys.go`, `view.go` | message handling, key map, drawing |
| `tui/ui` | `editor.go`, `complete.go`, `history.go` | the input area |
| `tui/ui` | `run.go` | `Run`: start the program against a server |
| `tui/*/*_test.go`, `tui/ui/testdata/` | | model tests, golden views, replay fixtures |
| `cmd/nova/tui.go` | | start the TUI when stdin and stdout are terminals |

## Interfaces

```go
// package layout
type Rect struct{ X, Y, W, H int }
type Inputs struct {
    Width, Height int
    EditorLines   int // desired height of the editor, 1 to 8
    AboveLines    int // widgets above the editor
    BelowLines    int
    WantSidebar   bool
}
type Regions struct{ Header, Main, Above, Editor, Below, Footer, Sidebar Rect }
func Compute(in Inputs) Regions

// package list
type Item interface {
    ID() string
    Version() int                 // increments whenever the item's content changes
    Render(width int) string      // pure; no I/O
}
type List struct{ /* ... */ }
func New() *List
func (l *List) SetItems(items []Item)
func (l *List) Append(it Item)
func (l *List) Touch(id string)               // the item changed (its Version changed)
func (l *List) Len() int
func (l *List) Resize(width, height int)
func (l *List) View() string                  // exactly Height lines
func (l *List) ScrollBy(lines int)
func (l *List) ScrollToBottom()
func (l *List) Following() bool               // auto-scroll with new content
func (l *List) SetFollowing(bool)
func (l *List) RenderCount() int              // item renders since creation, for tests and benchmarks

// package styles
type Theme struct {
    Name   string
    Colors map[string]string // spark, ink, core, warm, bg, muted, success, error
}
func Dark() Theme; func Light() Theme; func Mono() Theme
type Styles struct{ /* lipgloss styles for every region and item kind */ }
func Build(t Theme, p colorprofile.Profile) Styles
func DetectProfile(env func(string) string, out io.Writer) colorprofile.Profile

// package client
type Client interface {
    Send(cmd proto.Command) tea.Cmd           // result: ResponseMsg
    Next() tea.Cmd                            // result: EventMsg | UIRequestMsg | ClosedMsg; the model re-issues it
    AnswerUI(proto.UIResponse) tea.Cmd
}
type ResponseMsg struct{ proto.Response }
type EventMsg struct{ proto.Event }
type UIRequestMsg struct{ proto.UIRequest }
type ClosedMsg struct{}
func FromPipe(c *mode.Client) Client

// package chat
type Transcript struct{ /* ... */ }
func NewTranscript(width int, r Renderer, st styles.Styles) *Transcript
func (t *Transcript) Items() []list.Item
func (t *Transcript) Apply(e proto.Event) (changed []string)       // item ids touched or added
func (t *Transcript) Load(msgs []message.Message)                    // restore a stored conversation
func (t *Transcript) SetWidth(w int)
type Renderer interface{ Markdown(src string, width int) string }    // glamour in production, a fake in tests
func NewGlamourRenderer(st styles.Styles) Renderer

// package ui
type Clock interface{ Now() time.Time; After(d time.Duration) tea.Cmd }
type Options struct {
    Client  client.Client
    Theme   styles.Theme
    Clock   Clock
    CWD     string
    Version string
    Width, Height int      // initial size, used by tests
    Profile colorprofile.Profile
    Renderer chat.Renderer
}
func New(opt Options) *Model          // implements tea.Model
type RunOptions struct{ Theme string; Version string; NoColor bool }
func Run(ctx context.Context, srv *mode.Server, opt RunOptions) error
```

## Behaviour

### Layout

1. `Compute` is pure and deterministic. The header is 1 line when `Height >= 12`, else 0. The footer
   is 1 line. The editor height is `EditorLines` clamped to `[1, 8]`, and is further limited so the
   main area keeps at least 3 lines. `Above` and `Below` are each clamped to 8 lines and to what
   fits. Main takes all remaining height. The sidebar exists only when `WantSidebar` and
   `Width >= 120`, is 32 columns, and is carved from the right of Main.
2. A terminal under 8 lines drops widgets, header, and sidebar in that order. A width under 20 is
   clamped to 20 for layout so no rectangle has negative size. Rectangles never overlap and always
   lie inside the screen.

### List

3. `List` shows the last lines by default (`Following`). Scrolling up turns following off;
   `ScrollToBottom` or reaching the bottom turns it on. New content while not following does not move
   the view.
4. An item's render is cached by `(ID, width, Version)`. `Touch(id)` or a changed `Version`
   invalidates only that item. `Resize` to a new width invalidates every cache lazily (items are
   re-rendered only if they become visible).
5. `View` returns exactly `Height` lines, each at most `Width` cells wide, padded with blanks. It
   renders only items intersecting the viewport. Height of an item is the number of lines of its
   render at the current width.
6. Scrolling is clamped to the content. `ScrollBy` with a huge value is safe. An empty list renders
   blank lines.

### Styles

7. `Build` derives every style from the theme's tokens; no style literal contains a colour.
   `Dark` uses the brand palette from `BRAND.md` (`spark #62c6ff`, `ink #f2f6fb`, `core #fff4cf`,
   `warm #ff9a5c`, background `#0b0f16`); `Light` uses the light palette; `Mono` uses no colour at
   all (bold, dim, and reverse only).
8. `DetectProfile` returns `NoColor` when `NO_COLOR` is set to any non-empty value, or the output is
   not a terminal; otherwise the detected profile (truecolor, 256, 16). `Build` degrades colours to the
   profile and, for `NoColor` or `Mono`, never emits a colour escape.

### Client and messages

9. `Send` runs the command on the pipe and returns a `ResponseMsg`; `Next` blocks for the next event or
   UI request and returns it; when the pipe closes it returns `ClosedMsg`. The model calls `Next` again
   after each message it receives, so a stream of events is a chain of commands. No state is shared
   with the server other than these messages.
10. The model never calls the server directly from `Update`; it returns `tea.Cmd`s.

### Transcript

11. `Apply` folds events into items: `message_start` (assistant) creates an assistant item; `text_delta`
    appends to it; `tool_call` creates a **generic tool item** (name and a one-line argument summary) in
    the order calls arrive; `tool_start` marks it running; `tool_update` appends to its live output;
    `tool_end` finishes it with the result and error flag; `message_end` finalises the assistant item;
    `compaction` adds a marker; `error` adds an error item; `retry` discards the in-progress assistant
    item of that turn and adds a muted "retrying (n/m)" line; `run_end` with reason `aborted` or `error`
    adds a closing line. Events for an unknown call id are ignored. Events never reorder existing items.
12. `Load(msgs)` rebuilds items from stored messages: user, assistant (text, then its tool calls), and tool
    results attached to their calls. A tool result whose call is missing is shown as a standalone note.
13. `thinking_*` events are accepted and stored on the assistant item (C16 renders them); in this chunk
    they render as nothing.
14. `SetWidth` changes the width used for all renders; it does not recreate items.
15. The user's own prompt appears in the transcript **when the `prompt` response is ok** (an optimistic
    echo is not used), and shows its attachments. Steering and follow-up messages appear marked
    `queued` until they are consumed, then normal.

### Markdown

16. `markdown` renders incrementally. The text is split into blocks at blank lines **outside code
    fences**; a fence that is not yet closed makes the rest of the text one open block. Closed blocks
    are rendered once per width through `Renderer.Markdown` and cached. The open (tail) block is rendered
    with a cheap path (wrap to width, keep bold, code, and fence styling) and is replaced by the full render
    when it closes or at `message_end`. The number of `Renderer.Markdown` calls for a message is therefore
    proportional to the number of blocks, not the number of deltas. The renderer drops glamour's document
    margin and the blank lines it prefixes and suffixes every block with, so a block is flush left and the
    join leaves exactly one blank line between blocks; the cheap tail and the full render then agree, and
    text does not shift when the tail closes.
17. Rendering never exceeds the width in cells (measured with `ansi.StringWidth`), including for wide
    characters, combining marks, and long unbreakable words (which are hard-wrapped). ANSI sequences in the
    source text are stripped before wrapping, so tool or model output cannot move the cursor or change
    colours.

### Model and keys

18. `ui.Model` is the only `tea.Model`. Sub-structures expose methods, not `Update`. `Update` does no I/O.
19. `Init` issues `get_state`, `get_commands`, `get_messages`, and the first `Next`.
20. `WindowSizeMsg` recomputes the layout and resizes the list and the editor.
21. **Editor.** A multi-line `bubbles/textarea`. `enter` submits; `shift+enter` inserts a newline;
    `ctrl+g` opens `$EDITOR` through `tea.ExecProcess` and replaces the text on return. The editor grows to at most 8 lines. Pasted text over 8,000 characters is
    attached as a block, shown as `[pasted 12,400 chars]`, and sent in full.
22. **Submit rules.** Trim the text; ignore empty. If a run is **not** active: `/name args` →
    `run_command`; `!cmd` → `bash`; `!!cmd` → `bash` flagged not-in-context; anything else → `prompt`. If a
    run **is** active: `enter` sends `steer`; `alt+enter` sends `follow_up`; `/` and `!` forms behave as when
    idle. An unknown slash command shows a notice and leaves the text in the editor.
23. `esc` aborts an active run (`abort`; the returned queued messages are put back into the editor, one per
    line); with no run it closes completion. `ctrl+c`: clears a non-empty editor; else aborts an active run;
    else arms quit, and a second `ctrl+c` within 2 seconds (clock) quits; any other key disarms.
24. `pgup`/`pgdn` scroll the transcript by a page; `ctrl+o` toggles a global `expanded` flag (rendered in C16;
    here it only flips state and is shown in the footer help); `ctrl+l`, `ctrl+t`, `ctrl+r` are recognised and
    do nothing until C16/C17 (they show a "not available yet" notice only in tests).
25. **History.** `up` at the first line of the editor and `down` at the last move through submitted inputs
    (in memory, up to 500, duplicates of the last entry skipped). Editing a recalled entry does not change the
    stored one.
26. **Completion.** Typing `/` at the start opens a list of commands from `get_commands`, filtered by prefix and
    then by substring; `@` opens path completion for the current directory level using `os.ReadDir` run as a
    `tea.Cmd`, directories first and suffixed `/`, hidden files only after a `.`. `tab` or `enter` accepts the
    highlighted entry; `esc` closes; `up`/`down` move. Completion never blocks input.

### Footer and header

27. The footer shows, left to right: a context bar and percentage (`estimated tokens / effective limit`, or
    `ctx n/a` when the limit is unknown), the session cost, queued counts (`2 queued`), and a hint for the
    most relevant key. It truncates by dropping segments from the right, never wrapping. Values come from
    `get_state` and the `usage` and `queue_update` events.
28. The header shows the minimal mark glyph `✦`, `nova`, the model, the cwd (shortened with `~`), and the
    git branch if known (from `get_state`). It is dropped when the layout says so.

### Run

29. `Run` refuses to start (returning an error with the text `nova: the interactive UI needs a terminal; use -p
    or --mode json`) when stdin or stdout is not a terminal. It builds the pipe client, starts the program with
    alt-screen and mouse wheel enabled, and on exit returns the program's error. A panic in `Update` or `View`
    is recovered, the terminal is restored, and the panic is printed to stderr and written to the log file.
30. Logging goes to a file (`<state dir>/nova/tui.log`), never to stdout or stderr while the program runs.
31. `cmd/nova` starts the TUI when no mode flag is given and both streams are terminals; `--mode tui` forces it.

## Matrix

| ID | Case | Input / setup | Expected |
|---|---|---|---|
| LYT01 | typical 80 by 24 | | header 1, footer 1, editor 1, main fills the rest; rectangles tile the screen |
| LYT02 | no overlap, all inside | property over random sizes 1..300 and inputs | true |
| LYT03 | header dropped when short | height 10 | header height 0 |
| LYT04 | editor grows to 8 and no further | `EditorLines` 20 | 8 |
| LYT05 | main keeps at least 3 lines | tall editor and widgets on a short screen | main at least 3 where the screen allows |
| LYT06 | widgets clamp to 8 each | 50 | 8 |
| LYT07 | sidebar only when wide | width 119 and 120 | absent; present (32 wide) |
| LYT08 | tiny terminal | height 5, width 10 | no negative sizes; no panic |
| LYT09 | order of dropping | shrink from 24 lines | widgets, then header, then sidebar |
| LYT10 | deterministic | same inputs | same result |
| VLS01 | follows new content | append while at the bottom | view shows the newest lines |
| VLS02 | scrolling up stops following | `ScrollBy(-3)`, append | view unchanged; `Following` false |
| VLS03 | scrolling to the bottom resumes | | `Following` true |
| VLS04 | `View` has exactly Height lines | many sizes | true; none wider than Width |
| VLS05 | only visible items rendered | 10,000 items, height 30 | `RenderCount` under 100 on the first view |
| VLS06 | render cache | view twice | second view adds no renders |
| VLS07 | `Touch` invalidates one item | | exactly one re-render |
| VLS08 | version change invalidates | | one re-render |
| VLS09 | resize re-renders lazily | change width | only visible items re-rendered |
| VLS10 | scroll clamps | huge positive and negative | within bounds; no panic |
| VLS11 | empty list | | blank lines |
| VLS12 | item taller than the viewport | | scrollable through |
| VLS13 | items with zero lines | | skipped without error |
| VLS14 | benchmark: append to 10,000 items | | under 1 ms per append (benchmark, not a hard test) |
| VLS15 | `Sync` preserves the scroll position | scrolled up; new item appended | view unchanged; not following |
| VLS16 | `Sync` keeps following | at the tail; new item | tail shown; following |
| VLS17 | `Sync` clamps when items shrink | scrolled into a removed item | valid view; no panic |
| VLS18 | `Sync` reuses cached renders | same items | no re-render |
| VLS19 | `ItemAt` maps a viewport line to its item | | the item owning that line |
| STY01 | dark palette | `Build(Dark(), TrueColor)` | brand colours used |
| STY02 | no colour literals outside themes | code inspection test (grep) | none in `styles.go` |
| STY03 | mono emits no colour | render a sample | no colour escapes |
| STY04 | `NO_COLOR` | env set | `NoColor` profile |
| STY05 | non-terminal output | | `NoColor` |
| STY06 | 256 and 16 colour degradation | | closest colours; no truecolor escapes |
| STY07 | light theme contrast | text on background | contrast ratio at least 4.5 for body text |
| STY08 | panel tokens | `panel`, `panelok`, `panelerr`, `paneluser` | each panel style has a distinct background in Dark; none under Mono |
| CLT01 | `Send` yields a response | | `ResponseMsg` |
| CLT02 | `Next` yields events in order | | order preserved |
| CLT03 | pipe closed | | `ClosedMsg` |
| CLT04 | UI request delivered and answered | | `UIRequestMsg`; `AnswerUI` resolves it |
| TRN01 | text turn | message_start, deltas, message_end | one assistant item with the full text |
| TRN02 | tool turn | tool_call, tool_start, update, end | generic tool item with output and status |
| TRN03 | several tool calls keep order | | items in call order |
| TRN04 | tool error | `is_error` | marked as an error |
| TRN05 | unknown call id | `tool_end` for no call | ignored |
| TRN06 | `retry` discards partial text | text delta, then `retry` | partial assistant item removed; retry line present |
| TRN07 | compaction marker | | marker item |
| TRN08 | error event | | error item |
| TRN09 | `run_end` aborted | | closing line |
| TRN10 | events never reorder items | property over random valid streams | order stable |
| TRN11 | `Load` stored messages | user, assistant with calls, results | equivalent items |
| TRN12 | `Load` orphan tool result | | note item |
| TRN13 | thinking events stored | | accepted; nothing rendered |
| TRN14 | user prompt appears on ok response | | item added after the response, not before |
| TRN15 | queued messages marked then normal | steer then consumed | `queued` then normal |
| TRN16 | replay a recorded stream | fixtures in `testdata/events/*.jsonl` | final transcript equals the golden |
| TRN17 | `SetWidth` | | items not recreated; renders at the new width |
| MDK01 | paragraph and heading | | rendered through the renderer |
| MDK02 | blocks split at blank lines | | one render per closed block |
| MDK03 | open code fence | no closing fence | the rest is one open block |
| MDK04 | fence closes | | block closes and is rendered once |
| MDK05 | render count | 1,000 single-character deltas into 3 paragraphs | renderer called about 3 times, not 1,000 |
| MDK06 | tail uses the cheap path | | the open block is not sent to the renderer |
| MDK07 | width change | | closed blocks re-render once per new width |
| MDK08 | never wider than the width | wide characters, long words, URLs | every line within width |
| MDK09 | ANSI stripped | text containing `\x1b[2J` and OSC sequences | no control sequences in output |
| MDK10 | combining marks and emoji | | width computed correctly |
| MDK11 | tabs | | expanded; no misalignment |
| MDK12 | empty text; text of only whitespace | | no panic |
| MDK13 | very long message | 1 MiB | completes in under 1 s |
| MDK14 | list, quote, table, inline code | | pass through the renderer unchanged |
| MDK15 | one blank line between blocks | heading, paragraphs, list, code | no `\n\n\n`; blocks are flush, not indented by glamour's document margin |
| EDI01 | enter submits a prompt | idle | `prompt` sent |
| EDI02 | empty or whitespace ignored | | nothing sent |
| EDI03 | `/model x` | | `run_command` |
| EDI04 | `!ls` and `!!ls` | | `bash`, the second flagged not-in-context |
| EDI05 | enter while running steers | | `steer` |
| EDI06 | alt+enter while running | | `follow_up` |
| EDI07 | alt+enter while idle | | sends a `prompt`, the same as `enter` |
| EDI08 | shift+enter | | newline in the editor |
| EDI09 | unknown slash command | | notice; text kept |
| EDI10 | editor height grows to 8 | 20 lines | height 8; scrolls inside |
| EDI11 | large paste | 20,000 characters | shown as `[pasted …]`; full text sent |
| EDI12 | `ctrl+g` | `$EDITOR` set to a script | text replaced on return |
| EDI13 | `ctrl+g` with no `$EDITOR` | | notice |
| EDI14 | `/quit` and `/exit` | idle | `shutdown` sent; the editor cleared |
| KEY01 | esc aborts a run | | `abort`; queued messages returned to the editor |
| KEY02 | esc with no run | | closes completion only |
| KEY03 | ctrl+c clears a non-empty editor | | cleared |
| KEY04 | ctrl+c aborts a run | empty editor | `abort` |
| KEY05 | ctrl+c twice quits | idle; fake clock within 2 s | `shutdown` sent; quits on the response |
| KEY06 | second ctrl+c after 3 s | | does not quit; re-arms |
| KEY07 | other key disarms quit | | not armed |
| KEY08 | pgup and pgdn | | transcript scrolls; following toggles |
| KEY09 | ctrl+o | | `expanded` flips; footer hint updated |
| KEY10 | up and down history | | recalls inputs; editing does not change history |
| KEY11 | history bounds and duplicates | 600 inputs; same input twice | 500 kept; duplicate skipped |
| KEY12 | up inside a multi-line editor | cursor not on the first line | moves the cursor, not history |
| KEY13 | unavailable keys | `ctrl+l` | no panic |
| KEY14 | quit armed shows a hint | first `ctrl+c` | footer says `ctrl+c again to quit` |
| SCR01 | mouse wheel scrolls the transcript | wheel up then down | following off; view moves |
| SCR02 | scrolling up while streaming stays put | `pgup`, then an event | view unchanged |
| SCR03 | home and end | | top stops following; end resumes |
| CLK01 | click a block expands it | left click on the main region | the block toggles |
| CLK02 | click outside the transcript | | nothing changes |
| CPL01 | `/` opens commands | | list from `get_commands` |
| CPL02 | filter by prefix then substring | | ordered |
| CPL03 | tab accepts | | inserted |
| CPL04 | esc closes | | closed; text kept |
| CPL05 | `@` path completion | | directory entries, dirs first with `/` |
| CPL06 | hidden files | `@.` | shown only after the dot |
| CPL07 | unreadable directory | | no completion; no panic |
| CPL08 | `/` in the middle of text | | does not open commands |
| UIM01 | `Init` commands | | `get_state`, `get_commands`, `get_messages`, `Next` issued |
| UIM02 | `Update` does no I/O | static check plus a fake client that fails the test if called from `Update` | passes |
| UIM03 | window resize | | layout recomputed; list and editor resized |
| UIM04 | event chain | event received | `Next` issued again |
| UIM05 | `ClosedMsg` | | UI quits with a notice |
| UIM06 | footer values | state, `usage`, `queue_update` | context bar, cost, queued counts shown |
| UIM07 | footer truncation | width 40 | segments dropped from the right; one line |
| UIM08 | header | | glyph, model, cwd with `~` |
| UIM09 | run state | `run_start`/`run_end` | editor hints change; steer mode on |
| UIM10 | prompt response error | `busy`, `provider_error` | error shown; text restored to the editor |
| UIM11 | `session_changed` | | transcript reloaded via `get_messages` |
| UIM12 | `state_changed` | | footer and header update |
| UIM13 | panic in `View` | injected | recovered; terminal restored |
| UIM14 | `shutdown` response | `/quit`; response arrives | dialogs cancelled; `tea.Quit` returned |
| RUN01 | refuses without a terminal | piped stdin | the documented error |
| RUN02 | `NO_COLOR` | | mono theme and no colour escapes in the view |
| RUN03 | crash log used | | a report is written to `crash.log` and its path is printed |
| RUN04 | `cmd/nova` mode selection | terminal and no flags; `-p` set | TUI; print mode |
| GLD01 | empty session at 40, 80, 120 | golden | matches |
| GLD02 | a text turn at three widths | golden | matches |
| GLD03 | a tool turn at three widths | golden | matches |
| GLD04 | streaming mid-message | golden | matches |
| GLD05 | completion popup | golden | matches |
| GLD06 | footer at three widths | golden | matches |
| GLD07 | `NO_COLOR` view | golden | no colour escapes |
| GLD08 | recorded replay | the `testdata/events` fixtures | golden |
| PRF01 | 10,000-item transcript scrolls | benchmark | under 5 ms per frame |
| PRF02 | token stream | 1,000 deltas | under 1 ms per delta of render work |
| PTY01 | the TUI starts and quits | a pseudo-terminal; skipped unless `NOVA_PTY=1` | starts; `ctrl+c` twice exits 0 |
| PTY02 | a prompt round trip | against `testkit.Server` | the reply appears on screen |
| PTY03 | `/quit` exits cleanly | a pseudo-terminal; skipped unless `NOVA_PTY=1` | exits 0 |

## Fuzz and property tests

| Name | Asserts |
|---|---|
| `FuzzMarkdownIncremental` | for any text split into any deltas, the final output equals the output for the whole text at once |
| `TestProperty_LYT_Tiles` | rectangles never overlap and always lie inside the screen |
| `FuzzTranscriptApply` | arbitrary event sequences never panic and never reorder existing items |

## Out of scope

- The thinking block, tool-specific cards, diffs, custom themes, and the splash (C16).
- Dialogs, widgets, status segments, the model and session pickers, the trust prompt, and the extension
  UI bridge (C17). Mouse selection. Image display.

## Acceptance

```
go run ./scripts/accept C15
NOVA_PTY=1 go test ./tui/ui -run PTY     # the pseudo-terminal tests; skipped otherwise
```
