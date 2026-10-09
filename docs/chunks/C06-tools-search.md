---
chunk: C06
title: Search tools
packages: [./tools/search]
budgets: { ./tools/search: 700 }
depends: [C04]
---

# C06 — Search tools: `glob` and `grep`

## Goal

Two read-only search tools with no external dependency (no ripgrep): file-name search
with `**` patterns and content search with regular expressions. Both respect
`.gitignore`, skip binary files, and cap their output so one call can never flood the
context.

## Read first

- `tool/tool.go` as changed in C04.
- `permission/glob.go` from C04 (reuse `MatchPath` for `glob` patterns).
- `tools/fs/fs.go` for `resolve` (copy its containment rules; do not import `tools/fs`).
- `docs/TESTING.md` section 3, "Tools: search and bash".

## Files

| File | Purpose |
|---|---|
| `tools/search/glob.go` | the `glob` tool |
| `tools/search/grep.go` | the `grep` tool |
| `tools/search/walk.go` | the directory walker shared by both |
| `tools/search/ignore.go` | `.gitignore` parsing and matching |
| `tools/search/path.go` | path resolution with `Roots` containment |
| `tools/search/*_test.go` | |

## Interfaces

```go
func Glob() tool.Tool
func Grep() tool.Tool

// internal
type walker struct{ /* root, ignore stack, options */ }
type walkEntry struct{ Path, Rel string; IsDir bool; Size int64 }
func walk(ctx context.Context, root string, opt walkOptions, fn func(walkEntry) error) error
type walkOptions struct {
    NoIgnore bool // do not apply .gitignore
    Hidden   bool // include dot-files and dot-directories
    FollowSymlinks bool // default false
    MaxDepth int  // 0 means unlimited
}

type ignoreSet struct{ /* ... */ }
func parseIgnore(base string, data []byte) *ignoreSet
func (s *ignoreSet) match(rel string, isDir bool) (ignored, decided bool)
```

Constants: `maxGlobResults = 1000`, `maxGrepMatches = 200`, `maxGrepContext = 10`,
`maxGrepFileBytes = 2 MiB`, `maxGrepLineChars = 500`, `binarySniff = 8000`.

### Tool arguments

`glob`: `pattern` (required; e.g. `**/*.go`), `path` (default `.`), `hidden` (bool),
`no_ignore` (bool).

`grep`: `pattern` (required; RE2 regular expression), `path` (default `.`; a file or a
directory), `glob` (optional file-name filter, same syntax as `glob`), `case_insensitive`
(bool), `fixed_strings` (bool; treat `pattern` literally), `context` (int 0–10),
`max_results` (int, default 200), `hidden` (bool), `no_ignore` (bool).

## Behaviour

### Walking

1. The walker visits a directory tree depth-first with entries sorted bytewise by name, so
   output is deterministic. It never follows symlinks by default (a symlink to a directory is
   not descended; a symlink to a file is reported as a file only when `FollowSymlinks`).
2. `.git/` is always skipped. Dot-files and dot-directories are skipped unless `Hidden`.
3. `.gitignore` files are honoured at every level (patterns are relative to the file's
   directory), unless `NoIgnore`. `.git/info/exclude` and global ignore files are **not**
   read. The ignore file's own errors never fail the walk: an unreadable file or an invalid
   line is skipped.
4. The walker checks `ctx` between entries and returns `ctx.Err()` promptly.
5. A directory that cannot be read is skipped and counted; the tool's result notes
   `(N directories could not be read)`.
6. A path that is a file (for `grep`) is searched directly; ignore rules do not apply to an
   explicitly named file.
7. Containment: if `Context.Roots` is non-empty, `path` must resolve inside a root (same rule
   as `tools/fs`). Symlinks are never followed out of a root.

### `.gitignore` syntax (the supported subset)

8. Blank lines and lines starting with `#` are ignored; `\#` and `\!` at the start escape the
   character. Trailing unescaped spaces are stripped; `\ ` keeps one.
9. A leading `!` negates a pattern. A later matching pattern overrides an earlier one. A file
   cannot be re-included if one of its parent directories is excluded.
10. A pattern with a `/` at the start or in the middle is anchored to the directory of the
    ignore file; otherwise it matches at any depth. A trailing `/` matches directories only.
11. `*` and `?` do not match `/`. `**` matches across directories: `a/**/b` matches `a/b` and
    `a/x/y/b`; a leading `**/` matches at any depth; a trailing `/**` matches everything inside.
12. Character classes `[abc]`, `[a-z]`, and `[!a]` work. A pattern that would be invalid is
    skipped.

### `glob`

13. `pattern` is matched against each path **relative to `path`**, with `/` separators, using
    `permission.MatchPath` semantics. A pattern without `/` (for example `*.go`) matches the
    base name at any depth. Only files are returned (not directories), unless the pattern
    ends in `/`, in which case only directories.
14. Results are relative to `path`, sorted bytewise, at most 1,000. Beyond that `Truncated` is
    true and the output ends `... (N more matches omitted; narrow the pattern)`.
15. No matches returns `no files match <pattern>` (not an error).
16. An invalid pattern is an error result.
17. `Details` is `{"count":n, "truncated":bool}`.

### `grep`

18. `pattern` is compiled with Go's `regexp` (RE2); `case_insensitive` adds `(?i)`;
    `fixed_strings` quotes the pattern. A compile error is an error result containing the
    parser's message. An empty pattern is an error result.
19. Files are scanned line by line. A file is skipped if it has a NUL byte in the first 8,000
    bytes, or is larger than 2 MiB (counted in a "skipped" note). Lines longer than 500
    characters are shown cut with `…`, but the match is tested on the full line (up to 1 MiB
    per line; longer lines are skipped and counted).
20. Output lines are `relpath:line:text` for matches and `relpath-line-text` for context
    lines, with `--` between non-adjacent groups. The same line is never printed twice when
    contexts overlap.
21. `glob`, when given, filters files by relative path using the `glob` semantics.
22. Stop after `max_results` matches (default 200, maximum 1,000): `Truncated` is true and the
    output ends `... (more matches omitted; narrow the search)`. Context lines do not count as
    matches.
23. No matches returns `no matches for <pattern>` (not an error).
24. Invalid UTF-8 in a line is replaced with U+FFFD in the output; matching is on the bytes.
25. `Details` is `{"matches":n, "files":f, "truncated":bool, "skipped":s}`.
26. Both tools are `ReadOnly` and `Parallel`.

## Matrix

| ID | Case | Input / setup | Expected |
|---|---|---|---|
| WLK01 | deterministic order | files `b`, `a`, `c/d` | `a`, `b`, `c/d` every time |
| WLK02 | `.git` skipped | | no entries from `.git/` |
| WLK03 | hidden skipped by default | `.env`, `.hidden/x` | absent |
| WLK04 | hidden included on request | `Hidden` | present |
| WLK05 | symlink to a directory not followed | | not descended |
| WLK06 | symlink loop | `a/b -> ..` | terminates |
| WLK07 | symlink to a file | | skipped by default |
| WLK08 | context cancelled mid-walk | | returns `context.Canceled` promptly |
| WLK09 | unreadable directory | mode 0000 (skip as root) | skipped; count noted |
| WLK10 | deep tree | depth 200 | no stack overflow |
| WLK11 | `MaxDepth` | 2 | deeper entries absent |
| WLK12 | `Roots` containment | `path` outside | error result |
| WLK13 | a file named explicitly is searched even if ignored | | included |
| IGN01 | simple name pattern | `*.log` | matches at any depth |
| IGN02 | anchored pattern | `/build` | only at the ignore file's directory |
| IGN03 | pattern with a middle slash | `a/b` | anchored |
| IGN04 | directory-only pattern | `out/` | matches the directory, not a file named `out` |
| IGN05 | negation | `*.log`, `!keep.log` | `keep.log` included |
| IGN06 | later rule wins | `!x`, then `x` | ignored |
| IGN07 | cannot re-include under an excluded parent | `build/`, `!build/keep` | still ignored |
| IGN08 | `**/foo` | | matches at any depth |
| IGN09 | `foo/**` | | matches everything inside, not `foo` itself |
| IGN10 | `a/**/b` | `a/b`, `a/x/y/b` | both |
| IGN11 | comments and blanks | | ignored |
| IGN12 | escaped `\#` and `\!` | | literal |
| IGN13 | trailing spaces | `x ` and `x\ ` | stripped; kept |
| IGN14 | character classes | `[ab].txt`, `[!a].txt` | as stated |
| IGN15 | nested `.gitignore` | in `sub/` | applies to `sub/` only, relative to it |
| IGN16 | nested file overrides parent | negation in `sub/.gitignore` | applies |
| IGN17 | invalid line | `[` | skipped; others still apply |
| IGN18 | `NoIgnore` | | ignored files appear |
| IGN19 | `*` does not cross `/` | `a/*.txt` vs `a/b/c.txt` | not ignored |
| IGN20 | CRLF `.gitignore` | | patterns work |
| IGN21 | BOM at the start of `.gitignore` | | first pattern works |
| IGN22 | very long pattern | 10 KiB | no panic or hang |
| GLT01 | `*.go` matches base names at any depth | `a.go`, `x/b.go` | both |
| GLT02 | `**/*.go` | | all `.go` files |
| GLT03 | `src/**/*.go` | | only under `src` |
| GLT04 | anchored `src/*.go` | `src/a/b.go` | not matched |
| GLT05 | directories only | pattern `**/` ending in `/` | directories |
| GLT06 | no matches | | `no files match …`; not an error |
| GLT07 | invalid pattern | `[` | error result |
| GLT08 | results sorted and relative | | as stated |
| GLT09 | 1,500 matches | | 1,000 returned; `Truncated`; omitted note |
| GLT10 | `path` argument | subdirectory | results relative to it |
| GLT11 | `path` missing or a file | | error result |
| GLT12 | gitignore honoured | | ignored files absent |
| GLT13 | `Details` | | count and truncated |
| GLT14 | annotations | | `ReadOnly`, `Parallel` |
| GRP01 | simple match | | `file:3:text` |
| GRP02 | no matches | | `no matches for …` |
| GRP03 | invalid regex | `(` | error result with the parser message |
| GRP04 | empty pattern | | error result |
| GRP05 | `case_insensitive` | | matches both cases |
| GRP06 | `fixed_strings` | `a.b(` | literal |
| GRP07 | regex special characters without `fixed_strings` | `a.b` | `.` is any character |
| GRP08 | `context` | 2 | context lines marked with `-`; groups separated by `--` |
| GRP09 | overlapping contexts | matches 2 lines apart, context 3 | no duplicated lines |
| GRP10 | match at the first and last line with context | | no out-of-range |
| GRP11 | `context` above 10 | 99 | capped at 10 |
| GRP12 | `glob` filter | `*.go` | only Go files searched |
| GRP13 | `path` is a file | | that file only; ignore rules not applied |
| GRP14 | `max_results` | 200 matches in total, limit 5 | 5 matches; `Truncated` |
| GRP15 | `max_results` above 1,000 | | capped at 1,000 |
| GRP16 | context lines do not count as matches | limit 2, context 2 | 2 matches |
| GRP17 | binary file skipped | NUL in first 8 KiB | skipped; counted |
| GRP18 | file over 2 MiB skipped | | skipped; counted |
| GRP19 | very long line | 100,000 chars | match found; output cut to 500 with `…` |
| GRP20 | line over 1 MiB | | skipped and counted; no memory blow-up |
| GRP21 | CRLF file | | `\r` not in output |
| GRP22 | invalid UTF-8 | `\xff` | replaced in output; match still works on bytes |
| GRP23 | no trailing newline on the last line | | last line searchable |
| GRP24 | gitignore honoured and overridable | `no_ignore` | as stated |
| GRP25 | hidden files | `hidden` | searched |
| GRP26 | cancel during a large search | | returns promptly |
| GRP27 | catastrophic regex | `(a+)+$` on a long `aaaa…b` | completes (RE2 is linear); under 1 s |
| GRP28 | multibyte characters in the pattern | `é` | works |
| GRP29 | `Details` | | matches, files, truncated, skipped |
| GRP30 | annotations | | `ReadOnly`, `Parallel` |
| GRP31 | match at a line that is also over 500 chars, with `context` | | both shown cut |
| GRP32 | `Roots` containment | | error result outside a root |

## Fuzz and property tests

| Name | Asserts |
|---|---|
| `FuzzParseIgnore` | never panics or hangs; every parsed pattern can be matched without panic |
| `FuzzGlobPattern` | any pattern against any path returns quickly (under 100 ms for 1 KiB) |
| `TestProperty_IGN_Subset` | for random ignore files and trees, the set returned with `NoIgnore` is a superset of the set returned without it |

## Out of scope

- Multi-line regex, replacement, `.ignore` or `.rgignore` files, global gitignore, archives.
- Content search inside binary files, and ranking.
- A semantic or index-backed search (that is the `semantic-search` extension).

## Acceptance

```
go run ./scripts/accept C06
```
