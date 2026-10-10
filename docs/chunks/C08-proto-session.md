---
chunk: C08
title: "Protocol types and the session store"
packages: [./proto, ./session]
budgets: { ./proto: 600, ./session: 900 }
depends: [C01]
---

# C08 — `proto` and `session`

## Goal

Two data-heavy packages that everything else builds on. `proto` defines the wire types
and JSONL framing for the single protocol ([`PROTOCOL.md`](../PROTOCOL.md)). `session`
stores a conversation as an append-only JSONL tree with branches, custom entries, and
compaction, and rebuilds the model-facing context from it. Both must survive bad input,
truncated files, and concurrent writers.

## Read first

- `docs/PROTOCOL.md` (all of it: it is the contract for `proto`).
- `docs/ARCHITECTURE.md` section 5 (Sessions).
- `message/message.go`, `provider/provider.go` as changed in C01.
- `docs/TESTING.md` section 3, "Sessions and compaction" and "Protocol and modes".

## Files

| File | Purpose |
|---|---|
| `proto/frame.go` | frame types: `Command`, `Response`, `Event`, `UIRequest`, `UIResponse`, `Hello`, `Error` |
| `proto/codec.go` | `Reader`, `Writer`, `Decode` |
| `proto/codes.go` | error codes, event and command name constants |
| `proto/*_test.go` | |
| `session/entry.go` | `Entry` and its JSON form |
| `session/store.go` | the file-backed store |
| `session/tree.go` | branch, children, navigate |
| `session/context.go` | model-facing context reconstruction |
| `session/list.go` | listing and forking sessions |
| `session/lock_unix.go` | `//go:build !windows` advisory file lock |
| `session/lock_other.go` | no-op lock |
| `session/*_test.go` | |

## Interfaces

### proto

```go
const Version = 1

type Error struct {
    Code    string `json:"code"`
    Message string `json:"message"`
}

type Command struct {
    Type string `json:"type"`           // the command name, for example "prompt"
    ID   string `json:"id,omitempty"`
    // Fields below are used by specific commands; all optional on the wire.
    Text        string          `json:"text,omitempty"`
    Model       string          `json:"model,omitempty"`
    Level       string          `json:"level,omitempty"`
    Tokens      *int            `json:"tokens,omitempty"`
    Active      []string        `json:"active,omitempty"`
    Instructions string         `json:"instructions,omitempty"`
    Session     string          `json:"session,omitempty"`
    Entry       string          `json:"entry,omitempty"`
    Name        string          `json:"name,omitempty"`
    Args        string          `json:"args,omitempty"`
    Command     string          `json:"command,omitempty"`
    Queue       bool            `json:"queue,omitempty"`
    Attachments []Attachment    `json:"attachments,omitempty"`
}
type Attachment struct{ Kind, Name, Path, Text string }

type Response struct {
    Type    string          `json:"type"` // "response"
    ID      string          `json:"id,omitempty"`
    Command string          `json:"command"`
    OK      bool            `json:"ok"`
    Data    json.RawMessage `json:"data,omitempty"`
    Error   *Error          `json:"error,omitempty"`
}

type Event struct { /* the single struct holding every event's optional fields; see below */ }
type UIRequest struct{ /* type "ui_request", id, method, fields, timeout_ms, default */ }
type UIResponse struct{ /* type "ui_response", id, ok, ids, text, answers, cancelled */ }
type Hello struct{ /* type "hello", version, client or server, capabilities, session */ }

// Frame is a decoded line: exactly one of the pointers is set.
type Frame struct {
    Command    *Command
    Response   *Response
    Event      *Event
    UIRequest  *UIRequest
    UIResponse *UIResponse
    Hello      *Hello
}

func Decode(line []byte) (Frame, error)   // ErrBadFrame, or *UnknownTypeError{Type, ID}

type Writer struct{ /* mutex-protected */ }
func NewWriter(w io.Writer) *Writer
func (w *Writer) Write(v any) error        // marshals, appends "\n", one write call

type Reader struct{ /* ... */ }
func NewReader(r io.Reader, maxLine int) *Reader   // maxLine 0 means 16 MiB
func (r *Reader) Next() (line []byte, err error)   // io.EOF at a clean end; ErrLineTooLong
```

`Event` fields (all `omitempty`): `Type`, `Seq int64`, `TS time.Time`, `Run string`,
`Turn *int`, `Role`, `Text`, `Call *ToolCall`, `CallID`, `Result *ToolResult`,
`Message *message.Message`, `Usage *provider.Usage`, `Total *provider.Usage`, `Reason`,
`Attempt`, `Max`, `DelayMS`, `Error *Error`, `Entry`, `Replaced []string`, `TokensBefore`,
`TokensAfter`, `Steering`, `FollowUp` (pointers where zero is meaningful), `Ms`,
`Tokens *int`, `Extension`, `Hook`, `Session`, `Leaf`, `State json.RawMessage`,
`Decision`. `ToolCall` is `{ID, Name, Arguments}` and `ToolResult` is
`{Content, IsError, Truncated, Details json.RawMessage}`. Names match PROTOCOL.md exactly.

### session

```go
type Entry struct {
    Type   string    `json:"type"`   // message | model_change | compaction | branch_summary | custom | custom_message | label | session_info | (unknown)
    ID     string    `json:"id"`
    Parent string    `json:"parent"` // "" for a root entry
    TS     time.Time `json:"ts"`

    Message      *message.Message `json:"message,omitempty"`
    Usage        *provider.Usage  `json:"usage,omitempty"`
    Model        string           `json:"model,omitempty"`
    Summary      string           `json:"summary,omitempty"`
    FirstKept    string           `json:"first_kept,omitempty"`
    TokensBefore int              `json:"tokens_before,omitempty"`
    From         string           `json:"from,omitempty"`
    Kind         string           `json:"kind,omitempty"`
    Data         json.RawMessage  `json:"data,omitempty"`
    Keep         bool             `json:"keep,omitempty"`
    Content      string           `json:"content,omitempty"`
    Display      bool             `json:"display,omitempty"`
    InContext    bool             `json:"in_context,omitempty"`
    Target       string           `json:"target,omitempty"`
    Label        string           `json:"label,omitempty"`
    Name         string           `json:"name,omitempty"`
    Raw          json.RawMessage  `json:"-"` // set for an unknown type
}

type Options struct {
    Sync bool           // fsync after every append
    Now  func() time.Time
    ID   func() string  // id generator; default random
}
type Header struct{ Version int; ID, CWD string; Created time.Time; ParentSession string }
type Warning struct{ Line int; Err error }

func Open(path string, opt Options) (*Store, error) // creates the file and header if missing
func (s *Store) Header() Header
func (s *Store) Warnings() []Warning
func (s *Store) Append(e Entry) (Entry, error)       // assigns ID and TS; Parent defaults to the leaf; moves the leaf
func (s *Store) AppendOn(parent string, e Entry) (Entry, error) // explicit parent, moves the leaf
func (s *Store) Leaf() string
func (s *Store) SetLeaf(id string) error
func (s *Store) Get(id string) (Entry, bool)
func (s *Store) Entries() []Entry                    // file order
func (s *Store) Branch(leaf string) ([]Entry, error) // root to leaf; "" means the current leaf
func (s *Store) Children(id string) []string         // "" means the roots
func (s *Store) Messages(leaf string) ([]message.Message, error) // the model-facing context
func (s *Store) Close() error

func List(dir string) ([]Info, error)                // Info: Path, ID, Name, CWD, Created, Modified, Entries
func Fork(src *Store, from string, dst string, opt Options) (*Store, error)
func DefaultDir(cwd string) string                   // ~/.local/share/nova/sessions/<hash of cwd>
```

## Behaviour

### proto: framing

1. `Writer.Write` marshals with `encoding/json`, appends exactly one `\n`, and issues a
   **single** `Write` call so concurrent writers never interleave bytes. Output contains no
   raw newline, U+2028, or U+2029 (the standard encoder escapes them).
2. `Reader.Next` splits on `\n` only, strips one trailing `\r`, skips empty lines, and
   returns `ErrLineTooLong` (and then stays failed) if a line exceeds `maxLine`. A final line
   without a trailing newline is returned if non-empty. It never allocates more than
   `maxLine`+one read buffer, even for a hostile stream that never sends `\n`.
3. A reader reads continuously from a slow source and handles a frame split across any
   number of reads, and several frames in one read.

### proto: decoding

4. `Decode` reads `type` first. `hello`, `response`, `ui_request`, `ui_response` map to their
   structs. The **event** names listed in PROTOCOL.md map to `Event`. Any other string is a
   **command**, so a new command needs no change to `Decode`.
5. Invalid JSON, a JSON value that is not an object, or a missing or non-string `type` is
   `ErrBadFrame` (wrapping the cause). `*UnknownTypeError` is returned for a `type` that is
   an empty string or contains whitespace or control characters; it carries `Type` and `ID`
   so the caller can answer with an `error` response.
6. Unknown fields are ignored. A field of the wrong JSON type is `ErrBadFrame` naming the field.
7. Round trip: every frame struct marshals and decodes back equal.

### session: file

8. The first line is a header `{"type":"session","v":1,"id":…,"cwd":…,"created":…,"parent_session":…}`.
   A file with no header, or an unsupported `v`, is an error from `Open`.
9. Every other line is an entry. `Append` writes one line and flushes it before returning.
   Entries are never rewritten or deleted.
10. If the file does not end with `\n` when opened (an interrupted write), `Open` writes one
    before the first append so the next entry starts on its own line.
11. **Loading is forgiving.** A line that is not valid JSON, or is JSON of the wrong shape,
    is skipped and reported in `Warnings` with its line number. A truncated final line is a
    warning, not an error. An entry with an unknown `type` is kept (with `Raw`) in memory,
    never dropped from the file, and ignored for context. A duplicate `id` keeps the first and
    warns. An entry whose `parent` does not exist becomes a root and warns. A parent cycle is
    broken at the entry that closes it, with a warning.
12. `Open` sets the leaf to the **last entry in the file** that is not `label` or
    `session_info`, unless a later `SetLeaf` was recorded (see 14).
13. IDs are `e_` plus 8 hex digits from `crypto/rand` (or `Options.ID`); `Append` retries on a
    collision.
14. `SetLeaf` does not write to the file. `Append` always attaches to the current leaf, so
    navigating and then appending creates a branch. (A reopened session resumes at the last
    appended entry; callers persist an explicit leaf with a `session_info` entry if they need it.)

### session: concurrency

15. `Store` is safe for concurrent use. Appends are serialised; reads see a consistent state.
16. On Unix, `Append` takes an exclusive advisory lock (`flock`) on the file for the write, so
    two processes appending to one session do not interleave lines. A process that sees the
    file grew since its last read re-reads the new lines before assigning a parent.
17. `Close` is idempotent. Appending after `Close` is an error.

### session: tree and context

18. `Branch(leaf)` returns entries from the root to `leaf`. An unknown leaf is an error.
19. `Children("")` returns the root ids; `Children(id)` returns children in file order.
20. `Messages(leaf)` builds the model context from `Branch(leaf)`:
    a. Find the **last** `compaction` entry on the path.
    b. If there is one, start with a user-role message whose content is the summary, wrapped
       as `<summary>\n…\n</summary>`; then include `message` entries from the entry whose id is
       `FirstKept` onward, **skipping** any before it; if `FirstKept` is not on the path, include
       nothing before the compaction entry.
    c. If there is none, include all `message` entries.
    d. After a compaction, include `message` entries that follow it.
    e. A `custom_message` with `InContext` is included as a user message; without it, it is
       display-only and excluded. `custom`, `label`, `session_info`, `model_change`,
       `branch_summary` (see f) and unknown entries are excluded.
    f. A `branch_summary` entry is included as a user message `<branch_summary>\n…\n</branch_summary>`.
21. The result must pass `message.Validate`. If the stored data does not (for example a tool
    result whose call was dropped), `Messages` repairs it by removing orphaned tool results and
    adding a placeholder result `(result unavailable)` for any call that has none, and reports a
    warning.
22. A `custom` entry with `Keep: true` that lies before the compaction cut remains visible to
    `Entries()` and to `Branch()` (so extensions can replay it); it never enters the context.

### session: list and fork

23. `List` reads only the header and the last `session_info` name of each `*.jsonl` file,
    sorted by modified time descending, and skips (with no error) files it cannot parse.
24. `Fork(src, from, dst)` writes a new file whose header names `ParentSession`, and copies the
    branch ending at `from` (entries keep their ids and parents). The new store's leaf is the
    last copied entry. `from` unknown is an error; `dst` existing is an error.
25. `DefaultDir` is stable for a given absolute cwd and differs between cwds; it never
    contains the raw path (a hash is used), and is created with mode 0700 by `Open`.

## Matrix

| ID | Case | Input / setup | Expected |
|---|---|---|---|
| FRM01 | write ends with one newline | any frame | one `\n` at the end, none inside |
| FRM02 | U+2028 and U+2029 in text | | escaped; no raw separator |
| FRM03 | concurrent writers | 20 goroutines, 100 frames each | every line is valid JSON; none interleaved; `-race` clean |
| FRM04 | reader splits on `\n` only | line containing U+2028 | one line |
| FRM05 | CRLF line endings | | `\r` stripped |
| FRM06 | empty lines skipped | | skipped |
| FRM07 | two frames in one read | | two lines |
| FRM08 | one frame in many reads | 1 byte at a time | one line |
| FRM09 | final line without newline | | returned |
| FRM10 | line at the limit and one over | limit 100 | ok; `ErrLineTooLong` |
| FRM11 | stays failed after too long | | later `Next` returns the same error |
| FRM12 | hostile stream with no newline | 1 GiB of `a` | returns the error after `maxLine`; memory bounded |
| FRM13 | clean EOF | | `io.EOF` |
| FRM14 | reader error mid-line | source returns an error | the error is returned |
| PRT01 | decode a command | `{"type":"prompt","id":"1","text":"hi"}` | `Command` with fields |
| PRT02 | unknown command name decodes as a command | `{"type":"frobnicate"}` | `Command` |
| PRT03 | decode each event type | every name in PROTOCOL.md | `Event` with the right fields |
| PRT04 | decode response, hello, ui_request, ui_response | | typed |
| PRT05 | invalid JSON | `{` | `ErrBadFrame` |
| PRT06 | not an object | `[1]`, `"x"`, `5`, `null` | `ErrBadFrame` |
| PRT07 | missing `type` | `{}` | `ErrBadFrame` |
| PRT08 | `type` not a string | `{"type":5}` | `ErrBadFrame` |
| PRT09 | empty or odd `type` | `""`, `"a b"`, `"a\n"` | `*UnknownTypeError` carrying the id |
| PRT10 | unknown fields ignored | | no error |
| PRT11 | wrong field type | `{"type":"prompt","text":5}` | `ErrBadFrame` naming `text` |
| PRT12 | round trip every struct | | equal |
| PRT13 | `Event` omits empty fields | `run_start` | only `type`, `seq`, `ts`, `run` |
| PRT14 | `Turn` zero is preserved | `turn_start` turn 0 | `"turn":0` present |
| PRT15 | `Usage` embeds with reasoning tokens | | present |
| PRT16 | large frame (15 MiB) | | decodes |
| PRT17 | `Response` ok and error shapes | | `data` or `error`, not both |
| PRT18 | `Details` raw JSON preserved byte-for-byte | | identical |
| SES01 | create a new session | missing path | file with a valid header |
| SES02 | open an existing session | | entries loaded; leaf is the last entry |
| SES03 | append assigns id and ts | | non-empty; ts from `Options.Now` |
| SES04 | append parent defaults to the leaf | | `Parent` is the previous leaf |
| SES05 | `AppendOn` explicit parent | | branch created |
| SES06 | ids are unique | 10,000 appends | no duplicates |
| SES07 | id collision retry | generator returns a repeated id once | second id used |
| SES08 | file has no header | | error from `Open` |
| SES09 | unsupported version | `v: 99` | error |
| SES10 | corrupt line in the middle | | skipped; warning with the line number; other entries load |
| SES11 | truncated final line | | warning; later append starts on a fresh line |
| SES12 | file missing its final newline | valid last entry | `Open` adds `\n`; append works; both entries load |
| SES13 | valid JSON of the wrong shape | `{"type":"message","message":5}` | skipped with a warning |
| SES14 | unknown entry type | `{"type":"future","id":"x"}` | kept with `Raw`; excluded from context; **still in the file** after more appends |
| SES15 | duplicate id | | first kept; warning |
| SES16 | missing parent | | becomes a root; warning |
| SES17 | parent cycle | `a->b->a` | broken; warning; no hang |
| SES18 | concurrent appends in one process | 20 goroutines | all entries present; file lines valid; `-race` clean |
| SES19 | two processes append | two `Store`s on one file | lines never interleave; both see the other's entries after re-read |
| SES20 | append after `Close` | | error |
| SES21 | `Close` twice | | nil |
| SES22 | `Sync` option | | file synced (no assertion beyond not failing); works with and without |
| SES23 | entries are never rewritten | append many; compare the file prefix | byte-identical |
| SES24 | very large entry | 10 MiB message | stored and reloaded |
| SES25 | unicode and escapes in content | | round trip |
| SES26 | `Header` fields | | id, cwd, created |
| SES27 | empty file (0 bytes) | | treated as missing: header written |
| TRE01 | branch root to leaf | chain of 5 | 5 entries in order |
| TRE02 | branch of a middle entry | | prefix |
| TRE03 | unknown leaf | | error |
| TRE04 | children in file order | | as stated |
| TRE05 | roots via `Children("")` | two roots | both |
| TRE06 | navigate then append makes a branch | `SetLeaf(c2)`, append | new entry's parent is `c2`; old leaf still reachable |
| TRE07 | `SetLeaf` unknown id | | error |
| TRE08 | `SetLeaf` does not write | | file unchanged |
| TRE09 | active branch is always a path | property over random operations | `Branch(leaf)` parents chain to a root |
| CTX01 | simple context | user, assistant | two messages |
| CTX02 | tool pairs preserved | assistant with calls and results | all present, in order |
| CTX03 | compaction replaces older messages | `m1..m6`, compaction `FirstKept=m4` | summary message, then `m4..m6` |
| CTX04 | messages after the compaction included | | yes |
| CTX05 | last compaction wins | two compactions | only the second applies |
| CTX06 | `FirstKept` not on the path | | summary only, plus later messages |
| CTX07 | summary message shape | | user role; `<summary>` wrapper |
| CTX08 | `custom_message` with `InContext` | | included as a user message |
| CTX09 | `custom_message` without `InContext` | | excluded |
| CTX10 | custom, label, session_info, model_change, unknown excluded | | excluded |
| CTX11 | `branch_summary` included | | user message with the wrapper |
| CTX12 | orphaned tool result repaired | result whose call is missing | removed; warning |
| CTX13 | call without a result repaired | | placeholder `(result unavailable)`; warning |
| CTX14 | context result always passes `message.Validate` | property over random stored data | true |
| CTX15 | `keep` custom entry before the cut | | in `Branch`, not in context |
| CTX16 | context on a side branch | | only that branch's messages |
| CTX17 | empty session | | empty slice, not an error |
| SLS01 | list sessions | 3 files | sorted by modified time, newest first |
| SLS02 | unparseable file skipped | | not in the list; no error |
| SLS03 | name from the last `session_info` | | shown |
| SLS04 | `DefaultDir` stable and cwd-specific | | same input same output; different cwds differ; no raw path |
| SLS05 | directory created 0700 | | verified |
| FRK01 | fork copies the branch | | ids and parents preserved |
| FRK02 | fork header names the parent session | | `ParentSession` set |
| FRK03 | fork leaf | | last copied entry |
| FRK04 | fork unknown entry | | error; no file created |
| FRK05 | fork destination exists | | error; nothing overwritten |
| FRK06 | fork excludes other branches | | only the chosen path |

## Fuzz and property tests

| Name | Asserts |
|---|---|
| `FuzzDecode` | never panics; the result is a frame or an error |
| `FuzzReader` | for any chunking of any bytes: never panics; memory bounded; the same lines regardless of chunking |
| `FuzzOpen` | opening arbitrary file contents never panics; never loses a valid entry that precedes a corrupt one |
| `TestProperty_SES_AppendOnly` | after any sequence of operations the file's earlier bytes are unchanged |

## Out of scope

- Compaction itself and token estimation (C10). Config paths beyond `DefaultDir` (C11).
- Any HTTP or stdio transport (C12 and C14).

## Acceptance

```
go run ./scripts/accept C08
```
