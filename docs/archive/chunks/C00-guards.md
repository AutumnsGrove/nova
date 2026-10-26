---
chunk: C00
title: "Guards"
packages: [./scripts/spec, ./scripts/matrixcheck, ./scripts/linecount, ./scripts/importcheck, ./scripts/accept]
budgets: { ./scripts/spec: 250, ./scripts/matrixcheck: 250, ./scripts/linecount: 120, ./scripts/importcheck: 250, ./scripts/accept: 200 }
depends: []
---

# C00 — Guards

## Goal

Four small programs that make "done" a check instead of a judgement, plus a
Makefile and a CI workflow. Every later chunk's acceptance depends on these, so they
are built first and tested hardest.

## Read first

- `docs/TESTING.md` sections 4 and 6.
- `docs/ARCHITECTURE.md` section 2 (layout, layer rules, build flavours).
- `docs/chunks/TEMPLATE.md` (the spec format these programs parse).

## Files

| File | Purpose |
|---|---|
| `scripts/spec/spec.go` | Parse a chunk spec: front matter and matrix rows. Shared by the programs. |
| `scripts/spec/spec_test.go` | |
| `scripts/matrixcheck/main.go` | `matrixcheck <spec.md> <pkg>...` |
| `scripts/matrixcheck/main_test.go` | |
| `scripts/linecount/main.go` | `linecount <spec.md>` checks package budgets |
| `scripts/linecount/main_test.go` | |
| `scripts/importcheck/main.go` | Layer rules and the UI import boundary |
| `scripts/importcheck/main_test.go` | |
| `scripts/accept/main.go` | `accept <CNN>` runs the definition of done |
| `scripts/accept/main_test.go` | |
| `Makefile` | `test`, `test-full`, `race`, `accept`, `fmt`, `vet`, `ci` |
| `.github/workflows/ci.yml` | Runs the `ci` target on push and pull request |

The programs use the standard library, plus `gopkg.in/yaml.v3` for the front matter.

## Interfaces

```go
// package spec
type Spec struct {
    Chunk    string
    Title    string
    Packages []string
    Budgets  map[string]int
    Depends  []string
    Rows     []Row   // every row of every table under "## Matrix"
    Path     string
}
type Row struct {
    ID   string // matches ^[A-Z]{2,4}[0-9]{2,3}$
    Line int
    Case string
}

func Load(path string) (*Spec, error)
func Find(chunk string) (string, error)   // docs/chunks/<chunk>-*.md; error if zero or many
```

Command contracts:

```
matrixcheck <spec.md> <pkg>...     exit 0 ok; exit 1 problems (printed, one per line); exit 2 usage or I/O error
linecount <spec.md>                exit 0 ok; exit 1 over budget; exit 2 usage or I/O error
importcheck [-root dir]            exit 0 ok; exit 1 violations; exit 2 error
accept <CNN> [-from step]          exit 0 all steps pass; exit 1 a step failed; exit 2 error
```

## Behaviour

1. **Spec parsing.** Front matter is YAML between the first two `---` lines. A row is
   any Markdown table line in the section that starts at a line beginning with `## Matrix` and ends at
   the next line beginning with `## ` whose first cell is an ID. Headings are recognised only at the
   start of a line, so prose that mentions `## Matrix` is not a heading. Header and separator rows are ignored.
   Rows outside the Matrix section are ignored.
2. **Duplicate IDs** in one spec are an error.
3. **matrixcheck** lists tests with `go test -list '.*' <pkg>...` (including packages'
   `_test.go` files and any build tags passed through `-tags`). A test satisfies a row if
   its name, split on `_`, contains the ID as a whole component. Subtests do not count;
   the top-level `TestXxx` function name must carry the ID.
4. matrixcheck fails if any row ID has no satisfying test, **or** if a test name
   contains a component that looks like an ID (`^[A-Z]{2,4}[0-9]{2,3}$`) and is
   not in the spec. (A test may cite several IDs.)
5. **linecount** counts, per package listed in `budgets`, the lines of non-test `.go`
   files, excluding blank lines and lines that contain only a comment. A package over
   budget fails. A package in `packages` with no budget is an error.
6. **importcheck** loads every package with `go list -deps -json ./...` (and again with
   `-tags notui`) and enforces:
   a. the layer rules in `ARCHITECTURE.md` section 2;
   b. no package other than `./tui/...` and `./bundled/...` imports a path beginning
      `charm.land/` or `github.com/charmbracelet/`, in non-test **or** test files;
   c. nothing outside `./tui/...` and `./bundled/...` is reachable only through them
      (checked by building with `-tags notui`);
   d. every third-party module is in the **allowed-imports table**, which is data at the top of the file:
      one line per module naming the packages allowed to import it. The initial table: `gopkg.in/yaml.v3`
      (any non-UI package), `github.com/spf13/cobra` (`./cmd/nova`), `golang.org/x/net/html`
      (`./bundled/webfetch`), and the charm modules, `charm.land/...` and `github.com/charmbracelet/...`
      (`./tui/...`, `./bundled/...`, with `charm.land/fang` also in `./tui/cli`). A violation message names
      the module, the package, and says to record the addition in `docs/DECISIONS.md` (D46).
7. **accept** runs, in order, stopping at the first failure and printing the failing
   command's output: `gofmt -l`, `go vet`, `go build ./...`, `go build -tags notui ./...`,
   `go test -short -race -p 2` on the spec's packages, `go test -race -p 2` on the same,
   `matrixcheck`, `linecount`, `importcheck`. A step whose package does not exist yet is
   an error, not a skip.
8. `accept` prints a one-line summary per step (`ok`/`FAIL`, duration).

## Matrix

| ID | Case | Input / setup | Expected |
|---|---|---|---|
| SPC01 | parse front matter | a spec with chunk, packages, budgets, depends | all fields populated |
| SPC02 | no front matter | a spec starting with `#` | error naming the file |
| SPC03 | malformed YAML | `packages: [` | error that includes the YAML message |
| SPC04 | rows are read from the Matrix section only | a table with an ID-like first cell under `## Behaviour` | not returned as a row |
| SPC05 | header and separator rows ignored | `\| ID \| Case \|` and `\|---\|---\|` | not returned |
| SPC06 | two matrix tables in one section | rows from both returned in order | line numbers correct |
| SPC07 | duplicate ID | `MSG01` twice | `Load` returns an error naming both lines |
| SPC08 | ID with bad shape | `x1`, `ABCDE01`, `AB1` | not treated as rows |
| SPC09 | `Find` with no match / two matches | no file / `C05-a.md` and `C05-b.md` | distinct errors |
| SPC10 | CRLF line endings | spec saved with `\r\n` | parses identically |
| SPC13 | quoted title containing a colon | `title: "TUI rendering: thinking block"` | parses; the title keeps the colon |
| SPC14 | unquoted title containing a colon | `title: TUI rendering: x` | error naming the file and the YAML message (a real defect in an early draft of C16) |
| SPC11 | heading text inside prose | `` `## Matrix` `` mentioned mid-line in Behaviour | not treated as the Matrix section |
| SPC12 | no Matrix section | spec without one | zero rows; `matrixcheck` then reports the spec has no matrix and exits 1 |
| MCK01 | every row has a test | spec with 2 rows, tests `TestA_X01_..`, `TestB_X02_..` | exit 0 |
| MCK02 | a row has no test | row `X03` absent in tests | exit 1; output names `X03` |
| MCK03 | a test cites an unknown ID | `TestC_X99_..` not in spec | exit 1; output names `X99` and the test |
| MCK04 | one test cites two IDs | `TestD_X01_X02_Both` | satisfies both rows |
| MCK05 | ID only in a subtest | `t.Run("X01", ...)` inside `TestE` | row is **not** satisfied |
| MCK06 | ID embedded in a word | `TestFX01Foo` (no underscores) | not a match |
| MCK07 | ID as the last component | `TestG_X01` | match |
| MCK08 | no packages given | `matrixcheck spec.md` | exit 2, usage |
| MCK09 | package fails to compile | broken test file | exit 2 with the compiler output, not a false pass |
| MCK10 | spec not found | bad path | exit 2 |
| MCK11 | tests behind a build tag | `//go:build pty` test cites a row | satisfied only when `-tags pty` is passed through |
| LCT01 | within budget | 100 code lines, budget 200 | exit 0 |
| LCT02 | over budget | 201 code lines, budget 200 | exit 1; output shows count and budget |
| LCT03 | blank and comment-only lines excluded | file of 50 code, 50 blank, 50 comment | counts 50 |
| LCT04 | test files excluded | `x_test.go` with 500 lines | not counted |
| LCT05 | package with no budget | in `packages`, not in `budgets` | exit 2 |
| LCT06 | block comments | `/* ... */` spanning lines | those lines excluded |
| LCT07 | a code line with a trailing comment | `x := 1 // note` | counted as code |
| ICK01 | core imports charm | `agent` imports `charm.land/lipgloss/v2` | exit 1; names both packages |
| ICK02 | `tui` imports charm | | exit 0 |
| ICK03 | `message` imports `provider` | upward import | exit 1; cites the layer rule |
| ICK04 | `agent` imports `message` | allowed downward import | exit 0 |
| ICK05 | a test file in the core imports charm | `_test.go` in `tool` | exit 1 |
| ICK06 | a package imports a module not in the table | `github.com/x/y` | exit 1; the message points to D46 |
| ICK07 | core imports `gopkg.in/yaml.v3` | | exit 0 |
| ICK12 | `cmd/nova` imports cobra | | exit 0 |
| ICK13 | a core package other than `cmd/nova` imports cobra | | exit 1 |
| ICK14 | `tui/cli` imports fang; `cmd/nova` imports fang directly | | exit 0; exit 1 |
| ICK15 | a module allowed only for one package is imported by another | `x/net/html` in `tool` | exit 1 |
| ICK16 | `examples/embed` imports the app shell | `mode`, `config`, `session`, `bundled` | exit 1; names the importer and the package |
| ICK17 | `examples/embed` imports the harness layer | `agent`, `message`, `provider`, `tool` | exit 0; a test file may still import `testkit` |
| ICK08 | package reachable only via `tui` | core package imported solely by `tui` | exit 0 |
| ICK09 | `-tags notui` build breaks | core file referencing a `tui` symbol | exit 1 |
| ICK10 | `bundled` imports charm | | exit 0 |
| ICK11 | `bundled` imported by core `agent` | a core package importing bundled | exit 1 |
| ACC01 | all steps pass | a fixture module that is clean | exit 0; summary lists every step `ok` |
| ACC02 | gofmt dirty | one unformatted file | exit 1; stops at the first step; prints the file |
| ACC03 | a test fails | failing test in a listed package | exit 1; prints the test output |
| ACC04 | unknown chunk | `accept C99` | exit 2 |
| ACC05 | `-from step` | `accept C01 -from matrixcheck` | earlier steps skipped, later ones run |
| ACC06 | steps run in the documented order | recorded command log | matches the order in Behaviour 7 |
| ACC07 | package listed in spec does not exist | | exit 1 naming the package |

Test strategy: each program has a `run(args, stdout, stderr, env) int` function so tests
call it directly. Fixtures for `matrixcheck`, `importcheck`, and `accept` are small Go
modules written to a temp directory by the test (`testkit` does not exist yet, so use a
local helper). Do not shell out to `go` in a test unless the fixture needs it; ACC tests
may use a stub `go` on `PATH` that records its arguments.

## Fuzz and property tests

| Name | Asserts |
|---|---|
| `FuzzSpecLoad` | `Load` never panics on arbitrary bytes and returns either a spec or an error |
| `FuzzRowID` | the ID regexp accepts exactly the documented shape |

## Out of scope

- Mutation testing, coverage gating (the Makefile may report coverage; it is not a gate).
- Any code outside `scripts/`, the Makefile, and the workflow.

## Makefile

```
test:       go test -short -p 2 ./...
test-full:  go test -p 2 ./...
race:       go test -race -p 2 ./...
fmt:        gofmt -l .
vet:        go vet ./...
accept:     go run ./scripts/accept $(C)
ci:         fmt vet build build-notui race importcheck matrixcheck-all linecount-all
```

`matrixcheck-all` and `linecount-all` loop over every spec in `docs/chunks/` whose
packages exist and run the programs. A spec whose packages do not exist yet is skipped
in `ci` (and nowhere else).

## Acceptance

```
go run ./scripts/accept C00
```
