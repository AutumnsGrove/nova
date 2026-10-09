---
chunk: CNN
title: Short title
packages: [./path/to/pkg]
budgets: { ./path/to/pkg: 500 }
depends: [C00]
---

# CNN — Title

## Goal

Two sentences. What exists when this chunk is done, and why it matters.

## Read first

- The exact files to read, and the sections of the docs. Nothing else.

## Files

| File | Create or change | Purpose |
|---|---|---|

## Interfaces

The exact Go signatures. Code, not prose. Anything not here is private.

```go
```

## Behaviour

1. Numbered rules. Each one is testable and maps to at least one matrix row.

## Matrix

The part that matters most. One row per behaviour, each with a unique ID
(two to four capital letters and two digits). A test's name must contain its row
ID. See `docs/TESTING.md` section 4.

| ID | Case | Input / setup | Expected |
|---|---|---|---|

## Fuzz and property tests

| Name | Asserts |
|---|---|

## Out of scope

What not to build.

## Acceptance

```
go run ./scripts/accept CNN
```
