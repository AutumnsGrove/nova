# Archive: the build-out of Nova (C00 to C23)

This directory is **frozen history** and a **regression contract**. It is not a work queue.

- `chunks/` holds one spec per build chunk, C00 to C23 (with C22b), each with a test matrix: one row per
  behaviour, each with an id. They record how the work was assigned and what each package promises.
- `IMPLEMENTATION.md` is the original build plan. It stops at C21: **C22 (in-app login), C22b (login over the
  protocol), and C23 (`nova update`) were added later and are not in it**; their specs are in `chunks/`.
- `NEXTSTEPS.md` is the hand-off log the agents kept during the build. Its banner says where each section went.

## What is still live

`make ci` runs `matrixcheck` and `linecount` over every spec in `chunks/`:

- every matrix row must still have a test, and every package must stay inside the budget its spec lists;
- you may **add** rows, tests, and budget lines; you may not delete or weaken a row;
- if a row is wrong because behaviour changed on purpose, change the row in the same commit and say why.

`go run ./scripts/accept <chunk>` re-runs the full gate for one chunk.

Do not write a chunk spec for a new change. New work arrives as issues; see `AGENTS.md`. Design decisions, with
reasons, are in `docs/DECISIONS.md`.
