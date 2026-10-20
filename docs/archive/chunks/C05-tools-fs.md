---
chunk: C05
title: "Filesystem tools"
packages: [./tools/fs]
budgets: { ./tools/fs: 1060 }
depends: [C04]
---

# C05 — Filesystem tools: `read`, `write`, `edit`, `list`

## Goal

Make the four filesystem tools correct on every path the model can throw at them:
paging, line endings, binary and oversized files, atomic writes, symlinks, path
containment, and exact-text edits that cannot corrupt a file. The prototype handles the
happy path; several of its behaviours are wrong at the edges (noted below).

## Read first

- `tools/fs/*.go` and `tools/fs/fs_test.go` (the prototype).
- `tool/tool.go` as changed in C04 (`Result.Details`, `Context.Roots`).
- `docs/TESTING.md` section 3, "Tools: filesystem".

Known prototype defects this chunk fixes:

- `read` splits on `\n`, so a file ending in a newline reports a phantom extra line; an
  offset past the end silently returns "(empty file)"; it reads the whole file into memory;
  CRLF `\r` characters leak into output; invalid UTF-8 is not handled.
- `write` is not atomic, resets file permissions to 0644, and does not handle symlinks.
- `edit` applies edits one after another, so a later `oldText` can match text introduced by
  an earlier edit, and a failure part-way leaves earlier edits unwritten only by luck.
  It does not preserve line endings or a BOM.
- `list` has no cap and does not show symlinks.
- Nothing enforces `Context.Roots`.

## Files

| File | Change | Purpose |
|---|---|---|
| `tools/fs/fs.go` | change | `resolve` with containment and symlink handling |
| `tools/fs/read.go` | change | |
| `tools/fs/write.go` | change | |
| `tools/fs/edit.go` | change | |
| `tools/fs/list.go` | change | |
| `tools/fs/diff.go` | create | a small unified-diff generator |
| `tools/fs/atomic.go` | create | atomic write preserving mode |
| `tools/fs/*_test.go` | create or change | |

Tool names stay `read`, `write`, `edit`, `list`. Edit keeps the argument names `path`,
`edits[].oldText`, `edits[].newText`.

## Interfaces

```go
func Read() tool.Tool
func Write() tool.Tool
func Edit() tool.Tool
func List() tool.Tool

// internal
func resolve(tctx *tool.Context, path string) (abs string, err error)
func writeAtomic(path string, data []byte, perm os.FileMode) error
func unifiedDiff(path, before, after string, context int) (diff string, added, removed int, ok bool)
```

Constants (named in the code, overridable only by tests):

```
maxReadLines   = 2000        maxReadBytes   = 51200   (50 KiB of output)
maxLineChars   = 2000        maxFileBytes   = 10 MiB  (write and edit refuse larger content)
maxListEntries = 500         binarySniff    = 8000 bytes
diffMaxLines   = 5000
```

## Behaviour

### Paths (`resolve`)

1. An empty path, a path containing NUL, or a path of more than 4,096 bytes is an error
   (`path is required`, `invalid path`, `path too long`).
2. A relative path resolves against `Context.WorkDir`, or the process directory if empty.
   Result is absolute and cleaned.
3. If `Context.Roots` is non-empty, the **fully resolved** path (after following symlinks in
   the existing part of the path) must be inside one of the roots, else the tool returns an
   error result `path is outside the allowed directories`. A symlink inside a root that
   points outside is therefore refused.
4. For a path that does not exist yet (a `write` target), containment is checked on the
   deepest existing ancestor.

### `read`

5. Arguments: `path`, `offset` (1-indexed, default 1), `limit` (default 2000, max 2000).
   `offset` < 1 is an error; non-integer is rejected by schema validation.
6. A directory returns an error result `<path> is a directory; use list`.
7. A file with a NUL byte in the first 8,000 bytes returns an error result
   `<path> looks like a binary file (<n> bytes)`.
8. Read **streaming**, never the whole file. Count lines by `\n`; a final line with no
   trailing newline counts as a line; a trailing newline does not create an extra empty line.
   Strip a trailing `\r` from each displayed line. Strip a leading UTF-8 BOM from line 1.
9. Output lines are `N<TAB>text`. A line longer than 2,000 characters is cut and ends with
   `… (line truncated, 4821 chars)`. Output stops at 2,000 lines or 50 KiB, whichever comes
   first; then `Truncated` is true and a trailer says
   `... (<k> more lines; continue with offset=<n>)`. If the total is unknown (streaming
   stopped), the trailer says `more lines remain`.
10. `offset` beyond the last line is an error result
    `offset <n> is beyond the end of the file (<m> lines)`. An empty file returns
    `(empty file)`.
11. If the returned text contains invalid UTF-8, replace invalid bytes with U+FFFD and append
    `(some bytes were not valid UTF-8)`.
12. `Details` is `{"path":…, "start":n, "end":m, "total":t|null, "line_endings":"lf|crlf|mixed",
    "bom":bool}`. `Details` is not part of `Content`.
13. `read` is `ReadOnly` and `Parallel`.

### `write`

14. Arguments: `path`, `content`. `content` over `maxFileBytes` is an error result.
15. Create missing parent directories (mode 0755). Writing to an existing directory is an
    error result `<path> is a directory`.
16. Write **atomically**: write a temp file in the same directory, `fsync`, then rename over
    the target. A failed write leaves the original unchanged and removes the temp file.
17. Preserve the permissions of an existing file. A new file gets 0644.
18. If the target is a symlink to a file, write to the file it points to (the link stays).
    A dangling symlink is an error result.
19. The result is `wrote <n> bytes to <path> (created|overwrote)`. `Details` is
    `{"path":…, "created":bool, "bytes":n}`.
20. `write` is `Destructive`; it is not `Parallel`. Concurrent calls to the **same path**
    are serialised (a per-path mutex shared with `edit`).

### `edit`

21. Arguments: `path`, `edits` (non-empty array of `{oldText, newText}`).
22. Read the file (refuse over `maxFileBytes`). Detect the line ending (`crlf` if the file
    contains `\r\n`, else `lf`) and a BOM.
23. **All edits are matched against the original content**, not against the result of earlier
    edits. For each edit, `oldText` must occur **exactly once** in the original, and the
    matched ranges of different edits must **not overlap**. An empty `oldText`, a missing
    match, a duplicate match, or an overlap is an error result naming the edit number
    (`edit 2: …`), and **nothing is written**.
24. Matching tries the exact text first. If the file is CRLF and `oldText` contains only
    `\n`, retry after converting `oldText` and `newText` to `\r\n`. Matching is otherwise
    exact: no whitespace or case folding.
25. Apply all edits at once (from the highest offset down), write atomically with the
    original permissions, line ending, and BOM preserved. An edit whose result is identical
    to the original is an error result `no changes: …` (so the model notices).
26. If the file changed on disk between the read and the write (compare size and mtime), the
    edit fails with an error result asking the model to re-read the file.
27. The result is `applied <n> edit(s) to <path>`. `Details` is
    `{"path":…, "added":a, "removed":r, "diff":"<unified diff, 3 lines of context>"}`. If the
    file has more than `diffMaxLines` lines, `Details` has `"diff_omitted":true` and no diff.
28. `edit` is `Destructive`; not `Parallel`; shares the per-path mutex with `write`.

### `list`

29. Arguments: `path` (default `.`), `pattern` (a glob over entry names; invalid pattern is an
    error result). A non-directory path is an error result.
30. Output rows `kind  size  mtime  name`, sorted by name (bytewise). Directories end in `/`.
    A symlink shows `name -> target`. Hidden entries are included.
31. At most 500 entries; beyond that `Truncated` is true and the last line says how many were
    omitted. An empty result is `(no entries)`.
32. An entry whose `Info` fails (a race, a dangling link) is shown with `?` for size and time
    instead of being dropped.
33. `list` is `ReadOnly` and `Parallel`.

## Matrix

| ID | Case | Input / setup | Expected |
|---|---|---|---|
| PTH01 | relative path resolves against WorkDir | `a.txt`, WorkDir `/w` | `/w/a.txt` |
| PTH02 | absolute path accepted | | cleaned absolute |
| PTH03 | empty path | `""` | error `path is required` |
| PTH04 | NUL in path | | error `invalid path` |
| PTH05 | path over 4 KiB | | error `path too long` |
| PTH06 | `..` segments cleaned | `a/../b` | `b` under WorkDir |
| PTH07 | `Roots` set, path inside | | allowed |
| PTH08 | `Roots` set, path outside | `../x` | error result `outside the allowed directories` |
| PTH09 | `Roots` set, symlink inside pointing outside | | refused |
| PTH10 | `Roots` set, new file in a missing subdirectory of a root | | allowed |
| PTH11 | `Roots` set, new file under a symlinked parent that leaves the root | | refused |
| PTH12 | WorkDir empty | | uses the process directory |
| PTH13 | spaces and Unicode in path | `dir é/ファイル.txt` | works end to end |
| PTH14 | `Roots` is the filesystem root `/` | | everything is allowed |
| PTH15 | `Roots` set, sibling directory sharing the root name as a prefix | `/w/work-secrets` vs root `/w/work` | refused; `..foo` inside the root is allowed |
| RDF01 | read a normal file | 3 lines | `1\ta`, `2\tb`, `3\tc`; no trailer |
| RDF02 | trailing newline creates no extra line | `"a\nb\n"` | 2 lines |
| RDF03 | no trailing newline | `"a\nb"` | 2 lines |
| RDF04 | empty file | | `(empty file)` |
| RDF05 | file of only a newline | `"\n"` | 1 empty line |
| RDF06 | offset | 10 lines, offset 4 | starts at line 4 |
| RDF07 | limit | limit 3 | 3 lines and a continue trailer with `offset=` |
| RDF08 | offset beyond the end | 3 lines, offset 10 | error result naming 3 lines |
| RDF09 | offset exactly one past the end | 3 lines, offset 4 | error |
| RDF10 | offset zero or negative | | error |
| RDF11 | limit zero | | default limit used |
| RDF12 | limit above the max | 5000 | capped at 2000 |
| RDF13 | 3,000-line file, defaults | | 2,000 lines, `Truncated`, trailer says 1000 more |
| RDF14 | output over 50 KiB | 200 lines of 500 chars | stops by bytes; `Truncated`; trailer |
| RDF15 | very long line | 10,000 characters | cut at 2,000 with the marker and the real length |
| RDF16 | CRLF file | `"a\r\nb\r\n"` | lines without `\r`; `line_endings: crlf` |
| RDF17 | mixed endings | | `mixed` |
| RDF18 | BOM | `﻿a` | no BOM in line 1; `bom: true` |
| RDF19 | binary file | NULs at byte 10 | binary error with size |
| RDF20 | NUL after the sniff window | NUL at byte 9,000 | treated as text |
| RDF21 | invalid UTF-8 | `\xff\xfe` | replaced; note appended |
| RDF22 | directory | | `is a directory; use list` |
| RDF23 | missing file | | error result with the OS message |
| RDF24 | permission denied | file 0000 (skip as root) | error result |
| RDF25 | symlink to a file | | content of the target |
| RDF26 | dangling symlink | | error result |
| RDF27 | symlink loop | | error result, no hang |
| RDF28 | huge file read is streaming | 200 MiB sparse file, `limit 10` | returns quickly; memory stays small |
| RDF29 | file shrinks while reading | | no panic; returns what was read |
| RDF30 | `Details` content | | path, start, end, total, line endings, bom |
| RDF31 | tool annotations | | `ReadOnly` and `Parallel` true |
| RDF32 | an image path returns an image part | a PNG file | one `image/png` part and a read-image note |
| RDF33 | a text path has no parts | a `.txt` file | no parts, text as before |
| WRT01 | create a new file | | file exists; message says `created` |
| WRT02 | overwrite | | content replaced; says `overwrote` |
| WRT03 | creates parent directories | `a/b/c.txt` | exists; dirs 0755 |
| WRT04 | empty content | `""` | empty file created |
| WRT05 | content over 10 MiB | | error result; nothing written |
| WRT06 | target is a directory | | error result |
| WRT07 | preserves mode | existing 0755 file | still 0755 |
| WRT08 | new file mode | | 0644 |
| WRT09 | atomic on failure | make rename fail (read-only dir) | original content intact; no temp files left |
| WRT10 | no temp file left after success | | directory contains only the target |
| WRT11 | writes through a symlink | link to `real` | `real` updated; link intact |
| WRT12 | dangling symlink | | error result |
| WRT13 | parent is a file | `file.txt/x` | error result |
| WRT14 | concurrent writes to one path | 20 goroutines | file is exactly one writer's content; `-race` clean |
| WRT15 | `Details` | | path, created, bytes |
| WRT16 | content with CRLF, NUL, invalid UTF-8 | | written byte for byte |
| WRT17 | tool annotations | | `Destructive`; not `Parallel` |
| EDT01 | single edit | | applied; file correct |
| EDT02 | `oldText` not found | | error naming edit 1; file unchanged |
| EDT03 | `oldText` appears twice | | error with count; file unchanged |
| EDT04 | empty `oldText` | | error; unchanged |
| EDT05 | `edits` empty | | error |
| EDT06 | two edits, both found | | both applied |
| EDT07 | second edit fails | first valid, second missing | **nothing written** |
| EDT08 | edits matched against the original | edit 1 inserts text that contains edit 2's `oldText` | edit 2 still matches only the original occurrence (once) and is applied there |
| EDT09 | overlapping ranges | two edits touching the same text | error naming both; unchanged |
| EDT10 | adjacent (touching) ranges | | allowed |
| EDT11 | edits listed out of file order | | applied correctly |
| EDT12 | `oldText` equals `newText` | | `no changes` error |
| EDT13 | `newText` empty (deletion) | | text removed |
| EDT14 | `oldText` with regex characters | `a.b(c)*` | matched literally |
| EDT15 | multi-line `oldText` | | works |
| EDT16 | CRLF file, LF `oldText` | | matched via conversion; file stays CRLF throughout |
| EDT17 | CRLF file, exact `\r\n` `oldText` | | works |
| EDT18 | LF file, `oldText` with `\r\n` | | not found |
| EDT19 | BOM preserved | | BOM still first |
| EDT20 | whitespace-only difference | `oldText` differs by a space | not found (no fuzzy matching) |
| EDT21 | case difference | | not found |
| EDT22 | file changed between read and write | modify the file via a test hook | error asking to re-read |
| EDT23 | file over 10 MiB | | error result |
| EDT24 | missing file, directory, binary file | | error results |
| EDT25 | permissions preserved | 0755 | unchanged |
| EDT26 | atomic on failure | rename fails | original intact |
| EDT27 | `Details.diff` | | valid unified diff; `added` and `removed` correct |
| EDT28 | huge file diff omitted | 6,000 lines | `diff_omitted:true` |
| EDT29 | concurrent edits to one path | 20 goroutines, distinct unique targets | all applied or cleanly failed; `-race` clean |
| EDT30 | edit and write on one path concurrently | | serialised; file valid |
| EDT31 | unicode `oldText` | | works |
| EDT32 | `oldText` at the very start and the very end of the file | | works |
| EDT33 | property: apply then verify | random files and edits | result contains each `newText` once and every other byte unchanged |
| EDT34 | `oldText` occurs twice with overlapping matches | `aa` in `aaa` | error naming 2 occurrences; file unchanged |
| EDT35 | edit through a symlink | link to a file | the target is edited; the link stays a link |
| EDT36 | edit a file that is not regular | a named pipe | error result; does not block |
| LST01 | list a directory | | rows sorted by name |
| LST02 | default path `.` | | WorkDir listing |
| LST03 | pattern filter | `*.go` | matching names only |
| LST04 | invalid pattern | `[` | error result |
| LST05 | not a directory | a file path | error result |
| LST06 | missing path | | error result |
| LST07 | directories end in `/` | | yes |
| LST08 | symlinks show target | | `name -> target` |
| LST09 | dangling symlink | | shown, size `?` |
| LST10 | hidden files included | | yes |
| LST11 | more than 500 entries | 800 files | 500 rows; `Truncated`; says 300 omitted |
| LST12 | empty directory | | `(no entries)` |
| LST13 | bytewise sort | `B`, `a`, `é` | `B`, `a`, `é` |
| LST14 | permission denied | dir 0000 (skip as root) | error result |
| LST15 | tool annotations | | `ReadOnly`, `Parallel` |
| DIF01 | no change | equal strings | empty diff, 0, 0 |
| DIF02 | one line changed | | `-` and `+` lines; counts 1, 1 |
| DIF03 | insertion and deletion | | hunks correct |
| DIF04 | context lines | 3 | three lines either side |
| DIF05 | two far-apart changes | | two hunks |
| DIF06 | no trailing newline marker | | `\ No newline at end of file` |
| DIF07 | applying the diff reproduces `after` | property over random inputs | true |

## Fuzz and property tests

| Name | Asserts |
|---|---|
| `FuzzEditApply` | for arbitrary content and edit lists: never panics; on success the result equals the independent reference application; on failure the file is unchanged |
| `FuzzUnifiedDiff` | never panics; applying the diff to `before` yields `after` |
| `FuzzResolve` | never panics; the result is always absolute and clean or an error |

## Out of scope

- Image reading, notebooks, PDF. A `glob` and `grep` (C06). Anything under `tools/search`.
- File watching, file-change tracking across tools.

## Acceptance

```
go run ./scripts/accept C05
```
