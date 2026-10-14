# Contributing to Nova

Nova's initial build-out is finished; the work now is making it better. Changes start as a
[GitHub issue](https://github.com/AutumnsGrove/nova/issues) and are made test-first.
[`AGENTS.md`](AGENTS.md) is the full guide (for people and for agents); this is the short version.

## Where to start

- [`AGENTS.md`](AGENTS.md): how a change goes, the test-driven rules, the repo map, the hard rules, and the
  definition of done.
- [`docs/TESTING.md`](docs/TESTING.md): the test layers, the failure-mode catalog, and the gates.
- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) and [`docs/DECISIONS.md`](docs/DECISIONS.md): the design, and why
  it is the way it is. Check the decisions before reversing something that looks odd.
- [`docs/PROTOCOL.md`](docs/PROTOCOL.md): the one protocol every front door speaks.

## How a change goes

1. Pick or file an issue. A good one names the symptom, the environment, the steps to reproduce, and what you
   expected; for a feature, the behaviour wanted and the test cases that would prove it.
2. Write a failing test first, and watch it fail for the right reason. A bug fix starts with a test that
   reproduces the bug.
3. Make it pass with the smallest change, then run the gate.
4. Update the docs the change touches (`docs/PROTOCOL.md` for a command or event, `docs/DECISIONS.md` for a
   decision, `CHANGELOG.md` for anything a user would notice).

## Before you commit

```
make ci
```

That runs `gofmt`, `go vet`, both builds (full and `-tags notui`), the whole suite under `-race`, the import
rules, and the guards that keep the solved areas steady. If you touched `tui/splash`, also run
`make race-splash`. `make test` is the fast loop while you work.

Commits are semantic and one line (`feat:`, `fix:`, `test:`, `docs:`, `refactor:`), one concern each, green at
every commit. Cite the issue number.

## The solved areas

The areas built during the build-out each have a spec with a test matrix in
[`docs/archive/chunks/`](docs/archive/chunks/). The matrices still guard them: `make ci` fails if a matrix row has
no test, or a package outgrows its line budget. Add rows and tests freely; never delete or weaken one.

## Dependencies

Third-party modules are allowed when they remove code or risk. Every module is listed, with the packages that may
import it, in `scripts/importcheck`, and each addition gets a line in [`docs/DECISIONS.md`](docs/DECISIONS.md)
(D46). Never import a UI library outside `tui/`, `tui/cli/`, and `bundled/`.
