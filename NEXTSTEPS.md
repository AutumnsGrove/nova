# Nova: next steps

Start every session here: read this file and `AGENTS.md`, then do the first item under **Start here**.
This file is edited by hand. Each agent updates it as its last step (see `AGENTS.md`); so do you.

## Start here

**State:** planning is complete (PR #1). No chunk has been started and no product code has been written. The M0
prototype at the repository root is the starting point for several chunks.

**Next:** run **C00** (`docs/chunks/C00-guards.md`). It builds the guard scripts everything else depends on. Then
C01, then C02, C04, and C08 (they are independent but run one at a time), then C05, C06, C07, C11, C09, C10, C03, C12.
The full order and dependencies are in `docs/IMPLEMENTATION.md`.

**Everyday prompt:** *Read NEXTSTEPS.md and AGENTS.md, then do the first item under Start here.*

**Instructions from the human:** none yet.

## Status

| Chunk | Title | Status | Depends on | Date | Cost |
|---|---|---|---|---|---|
| C00 | Guards | todo | — | | |
| C01 | Message, provider contract, testkit | todo | C00 | | |
| C02 | openaicompat streaming provider | todo | C01 | | |
| C03 | OpenRouter preset, login, model catalog | todo | C02 | | |
| C04 | Tool contract, registry, permission policy | todo | C01 | | |
| C05 | Filesystem tools | todo | C04 | | |
| C06 | Search tools | todo | C04 | | |
| C07 | The bash tool | todo | C04 | | |
| C08 | Protocol types and the session store | todo | C01 | | |
| C09 | The agent loop | todo | C01, C04, C08 | | |
| C10 | Compaction, context limit, usage ledger | todo | C08, C09 | | |
| C11 | Config, context files, skills, prompt templates, trust | todo | C04 | | |
| C12 | The server core, modes, and the nova command | todo | C09, C10, C11 | | |
| C13 | The extension host | todo | C12 | | |
| C14 | The serve mode and the GitHub Action | todo | C12 | | |
| C15 | TUI foundation | todo | C12 | | |
| C16 | TUI rendering: thinking block, tool cards, diffs, themes, splash | todo | C15 | | |
| C17 | TUI dialogs, widgets, status, extension UI bridge | todo | C15, C13 | | |
| C18 | Bundled extensions | todo | C13, C17 | | |
| C19 | Polish, first run, doctor, examples, release | todo | C16, C17, C18 | | |

Status values: `todo`, `in-progress`, `done`, `partial` (something unsatisfied, listed below), `blocked`.

## Last session

Newest first. Nothing yet.

## Spec corrections to apply

- C00: `packages`/`budgets` omitted `./scripts/spec`, where the SPC01–SPC14 tests live, so
  `accept C00` could not run or count them. Applied: added `./scripts/spec` with budget 250 (human-approved
  2026-10-09).

## Decisions to record

None yet.

## Open questions for the human

- C03 needs OpenRouter's OAuth PKCE details and Ori's behaviour read from their current documentation (the specs say so).
- Multi-user `serve` is assumed out of scope (one trusted agent behind a bearer token).

## Cost

Total: $0.00
