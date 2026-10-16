---
chunk: C16
title: "TUI rendering: thinking block, tool cards, diffs, themes, splash"
packages: [./tui/render, ./tui/diff, ./tui/chat, ./tui/styles, ./tui/ui]
budgets: { ./tui/render: 740, ./tui/diff: 400, ./tui/chat: 1300, ./tui/styles: 450, ./tui/ui: 2380 }
depends: [C15]
---

# C16 — TUI rendering

## Goal

Make the transcript look and feel like Nova: a native, timed, collapsible thinking block
(D24), per-tool cards with highlighted code and real diffs, user themes, and the splash screen
drawn from the logo's cell grid. This chunk adds renderers and one item type to the C15 `chat`
package, and three new packages. The thinking block is the headline feature: it replaces what
Pi needed an extension and component patching for.

The `tui/chat` and `tui/styles` budgets were raised in C15 to cover this chunk's additions
there (`chat` 1300 lines, `styles` 450).

## Read first

- `docs/TUI.md` sections "Transcript items", "Tool renderers", "Themes", "Splash and logo".
- `docs/BRAND.md` and `assets/logo/gen/main.go` (the cell grid the splash is drawn from).
- `tui/chat/*`, `tui/list/*`, `tui/styles/*` from C15, and the `tool.Result.Details` shapes in
  C05 to C07 (`diff`, `added`, `removed`, `exit_code`, ...).
- `docs/TESTING.md` section 3, "TUI".

## Files

| File | Change | Purpose |
|---|---|---|
| `tui/chat/thinking.go` | create | the thinking item |
| `tui/chat/toolitem.go` | create | the tool card item (replaces the generic tool item from C15) |
| `tui/chat/transcript.go` | change | create thinking and tool-card items |
| `tui/render/registry.go` | create | `Renderer`, `Registry`, argument summaries |
| `tui/render/read.go`, `write.go`, `edit.go`, `bash.go`, `search.go`, `generic.go` | create | the built-in renderers |
| `tui/render/highlight.go` | create | chroma highlighting by file name |
| `tui/diff/diff.go` | create | the unified diff view |
| `tui/styles/themeyaml.go` | create | load user themes |
| `tui/splash/splash.go` | create | render the mark (replaced by C21; `mark_gen.go` is deleted there) |
| `assets/logo/gen/main.go` | change | `-go <file>` writes the cell data as Go |
| `tui/ui/*.go` | change | tick timers, `/theme`, `/display`, splash, expansion |
| `tui/*/*_test.go`, `tui/*/testdata/` | create | golden files |

## Interfaces

```go
// package render
type Call struct {
    ID, Name  string
    Args      json.RawMessage
    Running   bool
    Live      string          // output streamed so far, for a running call
    Content   string          // the result text
    IsError   bool
    Truncated bool
    Details   json.RawMessage
    Started, Ended time.Time
}
type Renderer interface {
    Header(c Call, st styles.Styles, width int) string  // exactly one line
    Body(c Call, st styles.Styles, width int, expanded bool) string // zero or more lines
}
type Registry struct{ /* ... */ }
func NewRegistry() *Registry                       // with every built-in registered
func (r *Registry) Register(tool string, rd Renderer)
func (r *Registry) Lookup(tool string) Renderer    // never nil: falls back to the generic renderer
func Summarize(name string, args json.RawMessage, max int) string // one-line argument summary
func Highlight(code, filename string, st styles.Styles, width int) []string

// package diff
type Options struct{ MaxLines int; Wrap bool; ShowNumbers bool }
func Render(unified, path string, width int, st styles.Styles, opt Options) string

// package styles (additions)
func LoadTheme(path string) (Theme, []string, error) // warnings for unknown or invalid keys
func LoadThemes(dirs []string) []Theme

// package splash
type Level int // Full, Half, Quarter, Plain
func Render(width, height int, st styles.Styles, p colorprofile.Profile, version string) (out string, lvl Level, ok bool)

// package chat (additions)
type Display struct {
    ThinkingPreviewLines int  // default 8, 1 to 50
    ExpandThinking       bool
    ExpandTools          bool
    Splash               bool // default true
}
```

## Behaviour

### Thinking block

1. A thinking item is created on `thinking_start` and belongs to the assistant message of that turn,
   shown **above** its text. A turn with no reasoning has none. Reasoning that arrives with no `thinking_start`
   (a provider quirk) creates the item on the first `thinking_delta`.
2. **Streaming state.** It shows a header `▸ Thinking for 4s` (seconds from the clock, updated by a 250 ms tick that
   runs only while some block is streaming) followed by the **last `ThinkingPreviewLines` wrapped lines** of the
   text in the muted style, with a leading `…` line when earlier lines are hidden. The item's `Version` changes only
   when the displayed second or the preview text changes, so a quiet block does not cause re-renders.
3. **Finished state.** On `thinking_end` the item freezes its duration from `ms`. Collapsed, it is one line:
   `▸ Thought for 12s · 3.4k tokens` with `ctrl+o to expand` right-aligned when it fits. Tokens come from, in order,
   `thinking_end.tokens`, the turn's `message_end.usage.reasoning_tokens` (the item updates when that arrives), or an
   estimate (`~` prefix) of characters divided by 4. Durations format as `850ms`, `4s`, `1m 05s`; tokens as `640`,
   `3.4k`, `15k`, `1.2M`.
4. **Expanded** (`Display.ExpandThinking` or the global `ctrl+o` flag): header `▾ Thought for 20s · 15k tokens`, then
   the **full** text, markdown-rendered in the muted style, wrapped to the width.
5. A block whose text is empty or only whitespace renders nothing at all. Thinking text is treated as untrusted
   output: control sequences are stripped (as in C15 rule 17).
6. `retry` discards the in-progress thinking item with the rest of the turn's partial output.
7. `Display.ThinkingPreviewLines` is clamped to `[1, 50]`.

### Tool cards

8. A card is a header line plus an optional body. The header: a status dot (a spinner frame while running, `●` in the
   success colour when ok, in the error colour when `IsError`), the tool name in bold, the summarised arguments, and the
   right-aligned duration (`0.1s`, `12s`) when finished or the running time when running. The header is always one
   line, truncated with `…` to the width; the duration is dropped first when space is short.
9. `Summarize` picks the argument that identifies the call: `path`, `file_path`, `command` (first line only), `pattern`,
   `url`, `query`, `name`; else the first string argument; else the compact JSON. It strips control characters, collapses
   whitespace, and truncates to `max` cells with `…`. Paths shorter than the width are shown in full; longer ones keep the
   file name and elide the middle (`…/deep/dir/file.go`).
10. A body is shown when the card is expanded, **or** when the call is running (live output tail, up to 5 lines) **or**
    when the result is an error (the first 12 lines of the message). Otherwise only the header is shown.
11. **Registry.** `Lookup` returns the registered renderer for the tool name, else the generic one. A renderer that panics
    is caught: the card falls back to the generic rendering and a muted note `(renderer error)` is added; the
    transcript never crashes.
12. **`read`**: header `read <path> [a–b]` using `Details.start/end`; expanded body is the content with a dim line-number
    gutter, syntax highlighted by file extension, capped at 200 lines (`… N more lines`).
13. **`write`**: header `write <path> (created|overwrote, N bytes)`; expanded body is the written content (from the
    arguments) highlighted, capped at 200 lines.
14. **`edit`**: header `edit <path> +A −R` from `Details.added/removed`; the body (expanded **or** while the result is
    fresh, see 15) is the diff view of `Details.diff`. If `diff_omitted`, show the replaced and new text from the arguments
    trimmed to 10 lines each.
15. A freshly finished `edit` card shows its diff **collapsed to the first 12 diff lines** with `… N more lines` until
    expanded; this lets the user see a change without pressing a key. Other tools show nothing extra until expanded.
16. **`bash`**: header `$ <first line of command>` (with `…` if multi-line) and `exit N` in the error colour when non-zero.
    Running: the last 5 lines of live output. Expanded: the full command (wrapped), then the output with the tail kept, then
    the exit status line. Output control sequences are stripped; carriage-return progress was already collapsed by the tool.
17. **`glob`, `grep`, `list`**: header with the pattern or path and the count (`12 matches`, `3 files`) from `Details`
    when present, else counted from the content; expanded body is the lines, with the match text highlighted for `grep`.
18. **Generic**: header is the name and the argument summary; expanded body is the pretty-printed arguments, then the result
    text. Truncated results show `(output truncated)`.
19. A result with `Truncated` shows a muted `(truncated)` marker in the header.
20. Cards never exceed the width, never contain raw control sequences, and render identically whether the call is new or
    restored from a stored session (`Load`), apart from the live tail and the timer.

### Diff view

21. `diff.Render` parses a unified diff (file headers optional; `@@` hunk headers; `+`, `-`, space, and
    `\ No newline at end of file` lines). Output has an optional two-column line-number gutter (old, new), added lines on the
    success background, removed lines on the error background (when colours are available; with no colour, `+` and `-`
    markers are used), context lines plain, hunk headers muted, and syntax highlighting chosen by `path` layered over the
    background.
22. Long lines wrap at the width with a continuation marker `↪` in the gutter when `Wrap`; otherwise they are truncated
    with `…`. Tabs are expanded to 4 spaces. CRLF is normalised. Wide characters and combining marks are measured by
    `ansi.StringWidth`.
23. Output is capped at `MaxLines` (default 60) with `… N more lines`. A diff with no hunks renders `(no changes)`.
    Malformed diff text is rendered as plain preformatted text, never an error. Width under 20 renders without the gutter.
24. Added and removed counts in the header of `edit` come from `Details`, not from re-counting.

### Themes

25. `LoadTheme(path)` reads a YAML file with `name` and `colors` (keys `spark, ink, core, warm, bg, muted, success, error`).
    Values are `#rgb` or `#rrggbb`. Missing keys fall back to the dark theme with a warning; unknown keys and invalid
    colours are warnings (the key falls back); a file that is not YAML, or has no name, is an error. `LoadThemes` loads
    every `*.yaml` in the given directories, ignoring errors but returning warnings, built-ins first; a user theme with a
    built-in's name replaces it.
26. `/theme` lists themes; `/theme <name>` switches immediately: styles are rebuilt and every item's `Version` is bumped so
    all caches refresh. An unknown name shows a notice. The choice lasts for the process (persisting it is the config's job).
27. `/display thinking expand|collapse`, `/display tools expand|collapse`, `/display lines <n>`, and `/display splash
    on|off` change `Display`; invalid values show a notice. `ctrl+o` flips both expand flags together.

### Splash

28. `go generate` runs the logo generator's `-go` mode, which writes `mark_gen.go`: the full mark's cells as
    `{I, J int8; O uint8; Role uint8}` plus the grid half-width. The file is committed and a test fails if it is out of
    date with the generator.
29. `Render` picks the **largest level that fits** with a 2-column and 4-row margin: `Full` (one cell per half-block
    column, 97 columns by 49 rows), `Half` (2 by 2 blocks averaged, 49 by 25), `Quarter` (4 by 4, 25 by 13), else `Plain`
    (a small ASCII star, 7 rows). Each terminal row holds two cell rows using `▀`, `▄`, `█`, and space.
30. Colour comes from the cell's role (`spark`, `ink`, `core`, `warm`) blended toward the background by its opacity. With a
    profile below 256 colours, or `NoColor`, the plain ASCII star is used, with no colour.
31. Below the `Plain` size (width under 24 or height under 14), `ok` is false and nothing is drawn.
32. Under the mark: `nova`, the version, and `press any key`. The splash is shown only when the transcript is empty, and
    disappears on the first key, the first event, or when `Display.Splash` is false. It is re-rendered on resize.
33. `Render` is deterministic and never wider than `width` or taller than `height`.

## Matrix

| ID | Case | Input / setup | Expected |
|---|---|---|---|
| THK01 | created on `thinking_start` | | item above the assistant text |
| THK02 | created on a delta with no start | | item created |
| THK03 | no reasoning, no item | text-only turn | none |
| THK04 | streaming header and timer | fake clock at 4 s | `Thinking for 4s` |
| THK05 | tick runs only while streaming | no streaming blocks | no tick scheduled |
| THK06 | preview is the last N wrapped lines | 30 lines, N 8 | last 8, leading `…` |
| THK07 | preview shorter than N | 3 lines | all 3, no `…` |
| THK08 | `Version` stable when nothing changes | two ticks within one second | no re-render |
| THK09 | finished collapsed label | `ms` 12000, tokens 3400 | `Thought for 12s · 3.4k tokens` |
| THK10 | tokens from `message_end` later | `thinking_end.tokens` null | updated when usage arrives |
| THK11 | tokens estimated | no data | `~` prefix |
| THK12 | duration formats | 850 ms, 4 s, 65 s | `850ms`, `4s`, `1m 05s` |
| THK13 | token formats | 640, 3400, 15000, 1200000 | `640`, `3.4k`, `15k`, `1.2M` |
| THK14 | hint right-aligned when it fits | wide | present; absent at width 40 |
| THK15 | expanded shows full text | | complete text, muted markdown |
| THK16 | expand toggles | `ctrl+o` | collapses and expands all blocks |
| THK17 | empty and whitespace text | | renders nothing |
| THK18 | control sequences stripped | `\x1b[2J` in the text | absent |
| THK19 | `retry` discards the block | | removed |
| THK20 | preview lines clamped | 0 and 99 | 1 and 50 |
| THK21 | tool-call-only turn | reasoning then a call | block shown, then the card |
| THK22 | two turns each with thinking | | two independent blocks |
| THK23 | restored session has no thinking events | `Load` | none (reasoning is not rendered from stored messages unless `Reasoning` is set) |
| THK24 | stored `Reasoning` | message with `Reasoning` | collapsed block, no duration |
| CRD01 | header is one line | long args | single line, truncated with `…` |
| CRD02 | status dot colours | running, ok, error | spinner, success, error |
| CRD03 | spinner animates | fake clock | frame advances |
| CRD04 | duration shown and dropped first | narrow | dropped before the arguments |
| CRD05 | no result, no body (was "body hidden by default"; changed by #5, see TCP01) | finished call with no result | header only |
| CRD06 | body shown when running | | live tail, 5 lines |
| CRD07 | body shown on error | | first 12 lines of the message |
| CRD08 | expanded shows the body | | body present |
| CRD09 | unknown tool uses the generic renderer | | generic card |
| CRD10 | renderer panics | injected | generic fallback plus `(renderer error)`; no crash |
| CRD11 | width never exceeded | widths 20 to 200, wide characters | true |
| CRD12 | no control sequences | tool output containing escapes | stripped |
| CRD13 | restored card matches live card | `Load` vs events | same output (ignoring timer) |
| CRD14 | truncated marker | `Truncated` | shown |
| ARG01 | picks `path` | | path shown |
| ARG02 | picks `command` first line | multi-line | first line |
| ARG03 | picks `pattern`, `url`, `query` | | each |
| ARG04 | falls back to the first string, then JSON | | as stated |
| ARG05 | long path elision | deep path | `…/dir/file.go` |
| ARG06 | control characters and whitespace | | cleaned |
| ARG07 | truncation to `max` | | ends with `…`; width respected |
| ARG08 | invalid JSON | | no panic; empty or raw text |
| RRD01 | read header with a range | `start`, `end` | `[a–b]` |
| RRD02 | read body highlighted with a gutter | | numbers dim; code coloured |
| RRD03 | 500-line file | | 200 lines and `… 300 more lines` |
| RRD04 | unknown extension | | plain text |
| RRD05 | binary or error result | | error body |
| RWR01 | write header | created and overwrote | text per case |
| RWR02 | write body | | highlighted content, capped |
| RED01 | edit header counts | `added` 9, `removed` 2 | `+9 −2` |
| RED02 | fresh edit shows a collapsed diff | | first 12 lines and `… N more` |
| RED03 | expanded shows the whole diff | | complete |
| RED04 | `diff_omitted` | | old and new text from the arguments, 10 lines each |
| RED05 | edit error | | error body, no diff |
| RBS01 | bash header | `ls -la` | `$ ls -la` |
| RBS02 | multi-line command | | first line and `…` |
| RBS03 | non-zero exit | | `exit 2` in the error colour |
| RBS04 | running tail | 20 lines streamed | last 5 |
| RBS05 | expanded | | command, output, status |
| RBS06 | output with escapes and `\r` | | clean |
| RBS07 | no output | | `(no output)` |
| RSR01 | grep header and count | | pattern and `12 matches` |
| RSR02 | count from content when `Details` is missing | | counted |
| RSR03 | match highlighting | expanded grep | matches styled |
| RSR04 | glob and list | | counts and lines |
| RGN01 | generic body | expanded | pretty arguments then result |
| RGN02 | invalid JSON arguments | | raw text, no panic |
| DFV01 | add, remove, context styling | | colours or `+` `-` markers |
| DFV02 | gutter numbers | | old and new numbers correct across hunks |
| DFV03 | hunk headers muted | | present |
| DFV04 | long lines wrap with `↪` | | wrapped within width |
| DFV05 | truncate without wrap | `Wrap` false | `…` |
| DFV06 | tabs and CRLF | | normalised |
| DFV07 | wide characters | | widths correct |
| DFV08 | `MaxLines` | 200-line diff, max 60 | cut with `… N more lines` |
| DFV09 | no hunks | | `(no changes)` |
| DFV10 | malformed diff | arbitrary text | plain rendering, no error |
| DFV11 | `\ No newline at end of file` | | shown muted |
| DFV12 | width under 20 | | no gutter; no panic |
| DFV13 | no colour profile | | markers only; no escapes |
| DFV14 | syntax highlight by extension | `.go` | tokens coloured over the background |
| DFV15 | rendering is deterministic | twice | identical |
| THM01 | load a valid theme | | colours set |
| THM02 | missing keys | | fall back to dark; warning |
| THM03 | invalid colour | `blue`, `#12` | warning; fallback |
| THM04 | unknown key | | warning |
| THM05 | not YAML, no name | | error |
| THM06 | `LoadThemes` precedence | user theme named `nova-dark` | replaces the built-in |
| THM07 | `/theme` lists | | names |
| THM08 | `/theme x` switches | | styles rebuilt; all items refreshed |
| THM09 | unknown theme | | notice |
| THM10 | mono theme | | no colour escapes anywhere in a full transcript |
| THM11 | the editor follows the theme | dark theme, then `/theme nova-light` | prompt, text, placeholder, cursor and blurred text take the theme colours; the cursor line has no background |
| THM12 | the editor in the mono theme | mono theme, ASCII profile | no editor style carries a colour |
| DSP01 | `/display thinking expand` | | blocks expand |
| DSP02 | `/display lines 12` | | preview 12 lines |
| DSP03 | `/display lines 0` | | notice; unchanged |
| DSP04 | `/display splash off` | | splash hidden |
| DSP05 | `ctrl+o` flips both flags | | both |
| SPL12 | dismissed by a key and by the first event | | gone; not shown again |
| SPL13 | not shown when the transcript is not empty | | hidden |
| SPL14 | re-renders on resize | | new level |
| GLR01 | a full turn with thinking, tools, and an edit diff at 40, 80, 120 | golden | matches |
| GLR02 | thinking streaming, finished, expanded | golden | matches |
| GLR03 | every built-in card collapsed and expanded | golden | matches |
| GLR04 | each theme | golden | matches |
| PRR01 | transcript with 2,000 tool cards scrolls | benchmark | under 5 ms per frame |
| PRR02 | streaming thinking of 100 KiB | benchmark | under 1 ms per delta |

## Retired rows

C21 replaced the cell-grid splash with a sequence drawn on the character grid. Rows **SPL01 to SPL11**
below are retired by C21 (SQN01 to SQN06, SQN30 to SQN32, and SQN40 cover their intent), and their tests
and the `./tui/splash` budget were removed here. SPL12 to SPL14 stay and must still pass.

| ID | Case | Retired by |
|---|---|---|
| SPL01 | picks the largest level that fits | C21 |
| SPL02 | margin respected | C21 |
| SPL03 | too small | C21 |
| SPL04 | never exceeds the bounds | C21 |
| SPL05 | half-blocks pair rows | C21 |
| SPL06 | colour by role with opacity blending | C21 |
| SPL07 | 16-colour and `NoColor` | C21 |
| SPL08 | text under the mark | C21 |
| SPL09 | deterministic | C21 |
| SPL10 | golden at four sizes | C21 |
| SPL11 | generated file is current | C21 |

## Fuzz and property tests

| Name | Asserts |
|---|---|
| `FuzzDiffRender` | any text and any width: never panics; no line wider than the width |
| `FuzzSummarize` | never panics; result width at most `max` |
| `FuzzThinkingFormat` | duration and token formatters never panic and never return an empty string |
| `TestProperty_CRD_NoOverflow` | for random calls, every rendered line is within the width and contains no control sequences |

## Out of scope

- Dialogs, widgets, and the extension UI bridge (C17). Renderers for bundled extensions (C18 registers
  them through `Registry.Register`). Image output. Mouse text selection.

## Acceptance

```
go run ./scripts/accept C16
```
