---
chunk: C17
title: "TUI dialogs, widgets, status, extension UI bridge"
packages: [./tui/dialog, ./tui/widget, ./tui/trust, ./mode, ./tui/ui, ./cmd/nova]
budgets: { ./tui/dialog: 1650, ./tui/widget: 300, ./tui/trust: 150, ./mode: 1850, ./tui/ui: 1900, ./cmd/nova: 720 }
depends: [C15, C13]
---

# C17 — Dialogs, widgets, status, and the extension UI bridge

## Goal

Everything in the TUI that is not the transcript or the editor: the approval dialog
(Nova asks, unlike Pi), selection and input dialogs, the multi-question form behind the
`ask` tool, the model and session pickers, widgets and status segments published by
extensions, toasts, the first-run trust prompt, and the bridge that turns protocol
`ui_request` frames into all of these. After this chunk the TUI covers every method in
`PROTOCOL.md`.

## Read first

- `docs/PROTOCOL.md` sections "UI requests", "Fire-and-forget UI", and "Extension connections".
- `docs/TUI.md` sections "Dialogs", "Widgets and status", "Screen layout".
- `tui/ui/*`, `tui/layout/*`, `tui/styles/*` from C15 and C16; `mode/server.go`, `mode/commands.go`,
  `mode/pipe.go` from C12; `ext/host.go` from C13 (`Status`); `config/trust.go` from C11.
- `docs/TESTING.md` section 3, "TUI".

## Files

| File | Change | Purpose |
|---|---|---|
| `tui/dialog/overlay.go` | create | the dialog stack and compositing over the screen |
| `tui/dialog/confirm.go` | create | confirm and permission dialogs |
| `tui/dialog/select.go` | create | single and multi select with filter |
| `tui/dialog/input.go` | create | text input |
| `tui/dialog/form.go` | create | the multi-question form |
| `tui/dialog/models.go` | create | model picker |
| `tui/dialog/sessions.go` | create | session list and tree |
| `tui/dialog/extensions.go` | create | the `/ext` view |
| `tui/dialog/help.go` | create | key and command help |
| `tui/widget/widget.go` | create | widget slots, status segments, toasts |
| `tui/trust/trust.go` | create | the pre-start trust prompt |
| `tui/ui/bridge.go` | create | `ui_request` to dialog and widget; answers back |
| `tui/ui/*.go` | change | key bindings, slash commands, footer segments, toasts |
| `mode/commands.go`, `mode/server.go`, `mode/pipe.go` | change | the protocol additions below |
| `cmd/nova/tui.go` | change | run the trust prompt before the server starts |
| `*/*_test.go`, `tui/dialog/testdata/` | create | goldens and model tests |

## Server additions (small, in `mode/`)

```
list_models      {query?}   → data: [{id, name, provider, context_window, in_per_m, out_per_m, current}]
session.list                → data: [{id, path, name, modified, entries, current}]
get_extensions              → data: [{name, version, state, restarts, last_error}]   ([] with no host)
```

`confirm` requests made for permission `ask` set `remember: true`. A `ui_response` with `ok: true` and
`remember: "session"` appends `Rule{Tool: <tool name>, Action: Allow}` to the **session's** policy (never to
the config on disk). Existing `deny` rules still win, because the most restrictive rule always wins (C04).
`remember` is ignored for any other request.

## Interfaces

```go
// package dialog
type Result struct {
    Done      bool
    Cancelled bool
    Value     any // see each dialog
}
type Dialog interface {
    ID() string                       // the request id, or "" for local dialogs
    HandleKey(k tea.KeyPressMsg) Result
    Size(maxW, maxH int) (w, h int)
    View(w, h int, st styles.Styles) string  // exactly h lines of width w
    Tick(now time.Time) bool          // for countdowns; reports whether the view changed
}
type Overlay struct{ /* ... */ }
func NewOverlay() *Overlay
func (o *Overlay) Push(d Dialog)
func (o *Overlay) Pop() Dialog
func (o *Overlay) Top() Dialog
func (o *Overlay) Len() int
func (o *Overlay) HandleKey(k tea.KeyPressMsg) (Result, Dialog)  // dialog that produced the result
func (o *Overlay) Compose(base string, w, h int, st styles.Styles) string

func NewConfirm(req proto.UIRequest) Dialog          // Value: proto.UIResponse
func NewPermission(req proto.UIRequest, expanded bool) Dialog
func NewSelect(req proto.UIRequest) Dialog           // Value: []string (ids)
func NewInput(req proto.UIRequest) Dialog            // Value: string
func NewForm(req proto.UIRequest) Dialog             // Value: []Answer
type Answer struct{ ID string; IDs []string; Text string; Note string; Skipped bool }
func NewModelPicker(models []ModelInfo, current string) Dialog   // Value: string (model id)
func NewSessionList(items []SessionInfo) Dialog                  // Value: SessionChoice{Path; Fork bool}
func NewTree(root []TreeNode, current string) Dialog             // Value: string (entry id)
type TreeNode struct{ ID, Type, Preview string; Children []TreeNode } // decoded from the `session.tree` data
func NewExtensions(items []ExtInfo) Dialog                       // Value: "reload" or ""
func NewHelp(keys []KeyHelp, cmds []string) Dialog

// package widget
type Content struct {
    Kind     string // text | list | table | progress
    Lines    []string
    Items    []Item
    Columns  []string
    Rows     [][]string
    Label    string
    Value    float64
}
type Item struct{ Text, State, Detail string } // State: todo | doing | done
type Store struct{ /* ... */ }
func NewStore() *Store
func (s *Store) SetWidget(key, placement string, c *Content)    // nil clears
func (s *Store) SetStatus(key string, segs []Segment)           // nil clears
func (s *Store) Notify(text, level string, now time.Time)
func (s *Store) Lines(placement string, width, maxLines int, st styles.Styles) []string
func (s *Store) Footer(width int, st styles.Styles) string
func (s *Store) Toasts(now time.Time, width int, st styles.Styles) []string
func (s *Store) Height(placement string, width, maxLines int) int
type Segment struct{ Text, Style string } // Style: muted | accent | warn | error | success | ""

// package trust
type Decision int // Trust, Once, Quit
type Found struct{ Config bool; Extensions []string; Hooks int; ContextFiles []string }
func Prompt(dir string, f Found, in io.Reader, out io.Writer) (Decision, error)
```

## Behaviour

### Dialog stack

1. Dialogs form a stack. The top one has focus. A new `ui_request` is **queued** when a dialog is open and shown
   when the previous closes (FIFO); a `ui_request` that arrives while a permission dialog is open does not
   pre-empt it. `esc` cancels the top dialog (answering `ok:false` or empty). A dialog opened while a run is
   streaming never interrupts the stream; the transcript keeps updating underneath.
2. `Compose` draws dialogs centred over the base screen with a border, a title, and a one-line key hint; it
   limits each dialog to 80% of the screen width (at least 30 columns) and 80% of the height, and clips content that
   does not fit (with a scroll indicator). A screen too small for the minimum (30 by 8) shows a one-line
   fallback prompt (`allow? y/n`) for confirm dialogs and cancels others with a notice.
3. When the server emits `ui_expired {id}` for a queued or open dialog, that dialog closes without answering
   and a notice says it timed out.

### Confirm and permission

4. `NewConfirm` shows title, message, and `Yes`/`No` with the request's `default` pre-selected; `y`, `n`,
   `enter`, `esc`, `left`/`right`, and `tab` work. If `timeout_ms` is set, a countdown is shown and the dialog
   closes itself with the default when it reaches zero (the server will also have resolved it).
5. `NewPermission` (a confirm with `remember: true`) shows: the tool name and its argument summary (using
   `render.Summarize`), the reason text that names the matched rule, and these choices: **Allow once** (`y`),
   **Allow for this session** (`s`), **Deny** (`n`, `esc`). `tab` toggles an expanded view of the full
   arguments (pretty JSON, capped at 30 lines; long strings wrapped). The default is **Deny** unless the request
   says otherwise; `enter` activates the highlighted choice. The answer carries `remember: "session"` only for
   the second choice.
6. Arguments and messages shown in any dialog are untrusted: control sequences are stripped and widths are
   measured with `ansi.StringWidth`.

### Select, input, form

7. `NewSelect`: a vertical list with `up`/`down`/`pgup`/`pgdn`, a type-to-filter box (substring, case-insensitive),
   `enter` to choose (single) or `space` to toggle and `enter` to confirm (multi). An empty filter result shows
   `no matches`. `Value` is the chosen ids in option order. Options may have a description shown dim on the right.
8. `NewInput`: a single-line input with placeholder and default; `enter` submits; `esc` cancels. A paste with
   newlines is joined with spaces.
9. `NewForm` (the `ask` tool): one tab per question plus a final **Review** tab. Per question: single select, multi
   select, or free text, each with an always-present `Other (type something)` row when `allow_other` is true. `n` adds
   or edits a **note** on the current answer. `s` skips the question. `tab`/`shift+tab` or `left`/`right` move between
   tabs. The Review tab lists each question with its answer, note, or `skipped`; `enter` there submits; `esc` cancels
   the whole form (answering with every question skipped and `cancelled: true`). Unanswered questions are allowed
   (partial submit); nothing forces an answer. Answers keep question order; notes are returned verbatim.
10. A question with an option `preview` shows it in a side panel when the terminal is at least 100 columns wide,
    and below the list otherwise.
11. Limits (enforced by the `ask` tool in C18, defended here): at most 8 questions and 8 options each; extra are
    ignored. A question with no options and `kind: single` becomes free text.

### Pickers and views

12. **Model picker** (`ctrl+l` or `/model` without an argument): requests `list_models`, shows the catalog with context
    window and price per million tokens, the current model marked, filter-as-you-type; `enter` sends `set_model`.
13. **Session list** (`ctrl+r` or `/resume`): requests `session.list`, newest first with name, age, and entry count;
    `enter` resumes (`session.resume`), `ctrl+f` forks the highlighted one. Not available while a run is active (a notice).
14. **Tree** (`/tree`): shows `session.tree` as an indented tree with the active path highlighted and message previews;
    `enter` sends `session.navigate`; `/fork` forks from the highlighted entry.
15. **Extensions view** (`/ext`): lists `get_extensions` with state (`running`, `failed`, `disabled`), restart count, and the last
    error; `r` sends `reload` and refreshes. Failed extensions are shown in the error colour.
16. **Help** (`/help`, `?` with an empty editor): key bindings and slash commands, scrollable.
17. `ctrl+t` cycles thinking through `off, low, medium, high` with `set_thinking` and shows a toast. `/cost` shows a dialog
    from `get_state.usage` and the cost command's text.

### Widgets, status, toasts

18. `widget` frames set or clear a named widget at `above_editor`, `below_editor`, or `sidebar`. Widgets in one placement stack in the
    order of their first appearance. Total height per placement is at most 8 lines; extra lines are cut with `… N more`.
    `sidebar` widgets appear only when the layout has a sidebar, otherwise they are drawn under the editor.
19. Content kinds: `text` (wrapped lines), `list` (items with `☐` todo, `◐` doing, `☑` done, a dim detail, the
    `doing` item highlighted), `table` (columns sized to fit, truncated with `…`, header bold), `progress` (a label and a bar with
    the percentage). Every kind is clipped to the width and stripped of control sequences.
20. `status` segments are shown in the footer between the built-in segments, in key order, separated by a muted `///`. Clearing a key
    removes it. Segment styles map to theme tokens. If the footer is too narrow, extension segments are dropped before built-in ones.
21. `notify` shows a toast above the footer: info for 4 seconds, warn and error for 8, at most 3 at once (oldest dropped), identical
    text deduplicated. Toasts expire by the clock; they never take focus.
22. `title` sets the terminal title. `set_editor_text` replaces the editor text (only when the editor is empty or the user has not
    typed in the last 2 seconds; otherwise a notice offers to apply it).
23. A `widget`, `status`, `notify`, `title`, or `set_editor_text` frame the client did not declare in `hello` is never sent; if one arrives
    anyway it is ignored. `hello` declares: `ui.confirm ui.select ui.input ui.form ui.notify ui.status ui.widget ui.title ui.set_editor_text`.

### Bridge

24. The bridge maps `UIRequestMsg` methods to dialogs and answers with `AnswerUI`. An unknown method answers `unsupported`
    immediately. A dialog's result is converted to the protocol answer (`ok`, `ids`, `text`, `answers`, `cancelled`, `remember`). The
    request id is preserved exactly.
25. The bridge handles one answer per request: a second answer for the same id is dropped. Closing the program with dialogs open answers
    them all as cancelled.

### Trust prompt

26. Before the server starts, if the working directory (or its project root) contains `.nova/` content or context files and is
    **not** trusted, and stdin and stdout are terminals, `trust.Prompt` shows what would load (config, extension names, hook count,
    context files) and three choices: **Trust this folder**, **Continue without** (once), **Quit**. Choosing trust writes to the trust store
    (a too-broad directory shows `that folder is too broad to trust` and does not trust it). Non-terminal runs skip the prompt and stay
    untrusted. Nothing from the project is read or executed before the answer.

### Server additions

27. `list_models` returns the catalog filtered by a case-insensitive substring `query` on id and name, sorted by provider then id, with
    `current` marking the active model. `session.list` returns the sessions of the working directory newest first with `current` set.
    `get_extensions` returns the host's status, or `[]`.
28. The permission `confirm` sets `remember: true`. A `remember: "session"` answer adds one allow rule for that tool to the session policy
    and affects later calls in the **same server only**.

## Matrix

| ID | Case | Input / setup | Expected |
|---|---|---|---|
| DLG01 | stack order | push A then B | B has focus; closing B returns focus to A |
| DLG02 | queued requests | two `ui_request`s | second shown after the first closes |
| DLG03 | esc cancels the top dialog | | answer `ok:false` or empty |
| DLG04 | dialog while streaming | events arrive | transcript keeps updating |
| DLG05 | centred and bordered | | box centred; title and hint present |
| DLG06 | size limits | tiny and huge terminals | at most 80%; at least 30 wide; clipped with an indicator |
| DLG07 | too-small screen fallback | 20 by 6 | one-line `allow? y/n` for confirm; others cancelled with a notice |
| DLG08 | `ui_expired` closes the dialog | | closed; notice |
| DLG09 | closing the program answers all as cancelled | | each answered once |
| DLG10 | `Compose` output size | | exactly the screen size; no line wider than `w` |
| CNF01 | yes and no keys | `y`, `n`, `enter`, arrows | each works; default pre-selected |
| CNF02 | countdown | `timeout_ms` 5000, fake clock | counts down; closes with the default at zero |
| CNF03 | answer shape | | `{ok}` |
| PRD01 | shows tool, summary, reason | | all three |
| PRD02 | three choices | | once, session, deny |
| PRD03 | default is deny | | highlighted |
| PRD04 | `s` answers with `remember: "session"` | | `ok:true, remember:"session"` |
| PRD05 | `y` answers without `remember` | | `ok:true` |
| PRD06 | `n` and `esc` deny | | `ok:false` |
| PRD07 | `tab` expands arguments | | pretty JSON, capped at 30 lines |
| PRD08 | hostile arguments | control sequences and 1 MiB strings | stripped; capped; widths correct |
| PRD09 | no `remember` offered | request without it | no session choice shown |
| SEL01 | single select | | `enter` returns one id |
| SEL02 | multi select | `space` toggles | ids in option order |
| SEL03 | filter | type text | substring, case-insensitive |
| SEL04 | no matches | | `no matches`; `enter` does nothing |
| SEL05 | descriptions | | dim, right side |
| SEL06 | many options | 500 | scrolls; responsive |
| SEL07 | `esc` | | cancelled |
| INP01 | submit | | text returned |
| INP02 | placeholder and default | | shown; default returned if untouched |
| INP03 | paste with newlines | | joined with spaces |
| INP04 | `esc` | | cancelled |
| INP05 | wide characters | | cursor and widths correct |
| FRD01 | tabs per question plus Review | 3 questions | 4 tabs |
| FRD02 | single select answer | | one id |
| FRD03 | multi select answer | | several ids |
| FRD04 | free text | | text |
| FRD05 | `Other` row | | free text entry; answer marked other |
| FRD06 | note on an answer | `n` | note returned verbatim, including newlines and Unicode |
| FRD07 | skip | `s` | `Skipped` true |
| FRD08 | partial submit | some unanswered | allowed; the unanswered are skipped |
| FRD09 | review lists everything | | answers, notes, `skipped` |
| FRD10 | `esc` cancels the form | | `cancelled:true`, every question skipped |
| FRD11 | navigation | `tab`, `shift+tab`, arrows | moves between tabs; wraps correctly |
| FRD12 | preview panel | wide and narrow | side panel; below the list |
| FRD13 | limits | 12 questions, 12 options | 8 and 8 shown |
| FRD14 | `single` with no options | | becomes free text |
| FRD15 | answers in question order | | order kept |
| FRD16 | long prompt and option text | | wrapped; no overflow |
| FRD17 | golden at three widths | | matches |
| MPK01 | list and current marker | | catalog shown; current marked |
| MPK02 | filter | | works |
| MPK03 | choose | `enter` | `set_model` sent |
| MPK04 | prices and windows formatted | | `128k`, `$0.27/M` style |
| MPK05 | empty catalog | | message |
| SSL01 | list newest first | | order |
| SSL02 | resume | `enter` | `session.resume` |
| SSL03 | fork | `ctrl+f` | fork choice |
| SSL04 | not while running | | notice |
| SSL05 | empty list | | message |
| TRV01 | tree shows the active path | | highlighted |
| TRV02 | navigate | `enter` | `session.navigate` |
| TRV03 | deep and wide trees | 1,000 entries | responsive; scrolls |
| EXV01 | list with states | running, failed, disabled | each styled |
| EXV02 | `r` reloads | | `reload` sent; list refreshed |
| EXV03 | no extensions | | message |
| HLP01 | help lists keys and commands | | complete; scrolls |
| KYA01 | `ctrl+l`, `ctrl+r`, `ctrl+t` are live | | open picker, list, cycle |
| KYA02 | `ctrl+t` cycles | four presses | off, low, medium, high, off |
| KYA03 | `?` with an empty editor | | opens help; with text, types `?` |
| WDG01 | set a text widget | above | shown above the editor |
| WDG02 | clear a widget | `nil` | removed |
| WDG03 | order by first appearance | A, B, then update A | A stays first |
| WDG04 | height cap | 30 lines | 8 lines; `… 22 more` |
| WDG05 | list kind | todo, doing, done | glyphs; doing highlighted |
| WDG06 | table kind | | columns fit; truncated with `…` |
| WDG07 | progress kind | 0.4 | bar and `40%` |
| WDG08 | clipped to width; control sequences stripped | | yes |
| WDG09 | sidebar widget with no sidebar | | drawn under the editor |
| WDG10 | placements independent | | each has its own cap |
| WDG11 | `Height` matches `Lines` | | equal |
| WDG12 | unknown kind | | ignored with a notice |
| STS01 | segments in key order with `///` | | as stated |
| STS02 | clear a key | | removed |
| STS03 | style mapping | | theme tokens |
| STS04 | narrow footer drops extension segments first | | built-ins kept |
| TST01 | info and warn lifetimes | fake clock | 4 s; 8 s |
| TST02 | at most 3 | 5 toasts | newest 3 |
| TST03 | duplicate text | | one toast |
| TST04 | never takes focus | | keys unaffected |
| UXB01 | each method maps | confirm, select, input, form | right dialog |
| UXB02 | unknown method | | `unsupported` at once |
| UXB03 | answer preserves the request id | | exact |
| UXB04 | one answer per id | double answer | second dropped |
| UXB05 | fire-and-forget methods | notify, status, widget, title, set_editor_text | applied; no answer |
| UXB06 | `set_editor_text` while typing | typed within 2 s | not applied; notice |
| UXB07 | undeclared method frame | | ignored |
| UXB08 | hello declares the capabilities | | the documented list |
| UXB09 | end to end with a real server | extension asks a form; user answers | the extension receives the answers |
| TRP01 | untrusted project with content | | prompt shown with what would load |
| TRP02 | trust | | store updated; project loads |
| TRP03 | continue without | | not trusted; project skipped |
| TRP04 | quit | | exit without starting |
| TRP05 | too broad | home directory | refused with the message |
| TRP06 | non-terminal | | no prompt; untrusted |
| TRP07 | nothing read or run before the answer | project with a hook and an extension | neither started |
| TRP08 | already trusted or nothing to load | | no prompt |
| SVA01 | `list_models` | | catalog entries; `current` set |
| SVA02 | `list_models` query | | filtered |
| SVA03 | `session.list` | two sessions | newest first; `current` set |
| SVA04 | `get_extensions` with and without a host | | status list; `[]` |
| SVA05 | `remember: "session"` | permission ask answered | a session allow rule is added for the tool |
| SVA06 | the session rule is not persisted | restart | gone |
| SVA07 | a deny rule still wins | config denies the tool | still denied |
| SVA08 | `remember` ignored for other requests | | no rule added |
| SVA09 | `remember` with `ok:false` | | no rule added |

## Fuzz and property tests

| Name | Asserts |
|---|---|
| `FuzzDialogKeys` | arbitrary key sequences on any dialog never panic and never produce a result with an unknown id |
| `FuzzWidgetContent` | arbitrary content never panics; every output line is within the width |
| `TestProperty_DLG_ComposeBounds` | for random sizes and stacks, `Compose` returns exactly the screen size |

## Out of scope

- Renderers and dialogs specific to bundled extensions (C18 provides them using these interfaces).
- Mouse interaction in dialogs. Persisting the theme or the chosen model beyond the session.

## Acceptance

```
go run ./scripts/accept C17
```
