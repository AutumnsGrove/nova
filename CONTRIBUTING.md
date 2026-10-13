# Contributing to Nova

Nova is built one chunk at a time. Read [`AGENTS.md`](AGENTS.md) first: it is the
contract for every session, and it names the file you are about to edit.

## Where to start

- [`AGENTS.md`](AGENTS.md) — how work is assigned, the working rhythm, and the rules.
- [`docs/IMPLEMENTATION.md`](docs/IMPLEMENTATION.md) — the plan and the chunks.
- [`docs/TESTING.md`](docs/TESTING.md) — the layers, the failure-mode catalog, and
  the definition of done.
- [`NEXTSTEPS.md`](NEXTSTEPS.md) — what is done, what is next, and what the last
  session learned.

## The matrix rule

Every chunk spec has a test matrix. Each row has an ID, and the test's name must
contain it as a whole component (`TestRead_RDF08_OffsetBeyondEnd`). A chunk is not
done until `go run ./scripts/accept <chunk>` passes; that runs `matrixcheck`, which
fails if a row has no test. You may add rows and tests; you may not delete or
weaken one.

## Before you commit

```
go test -short ./...
go test -race ./...
go run ./scripts/accept <chunk>
```

Commits are semantic and one line (`feat:`, `fix:`, `test:`, `docs:`, `refactor:`).
Take a chunk in many small green steps and commit after each one.

## Dependencies

Third-party modules are allowed when they remove code or risk. Every module is
listed, with the packages that may import it, in `scripts/importcheck`, and each
addition gets a line in [`docs/DECISIONS.md`](docs/DECISIONS.md) (D46). Never
import a UI library outside `tui/`, `tui/cli/`, and `bundled/`.
