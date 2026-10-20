---
chunk: C10
title: "Compaction, context limit, usage ledger"
packages: [./compaction, ./usage]
budgets: { ./compaction: 600, ./usage: 200 }
depends: [C08, C09]
---

# C10 — Compaction, the context limit, and the usage ledger

## Goal

Keep long sessions inside the model's window without losing the thread: estimate
context size, decide when to compact against a per-session `context_limit` (D25), cut at
a safe boundary, summarise the old span, and record a `compaction` entry in the session.
Also provide the usage ledger and cost fallback (D26) that the footer, `/cost`, and any
cost cap read.

## Read first

- `docs/ARCHITECTURE.md` sections 5 and 6.
- `session/entry.go`, `session/context.go` (C08): the `compaction` entry and how
  `Messages()` honours it.
- `agent/agent.go` (C09): `Hooks.BeforeTurn` is where compaction is invoked.
- `provider/model.go`: the catalog, for cost.
- `docs/TESTING.md` section 3, "Sessions and compaction".

## Files

| File | Purpose |
|---|---|
| `compaction/estimate.go` | token estimation |
| `compaction/cut.go` | `CutPoint` |
| `compaction/summary.go` | the summarisation prompt, input serialisation, file-operation extraction |
| `compaction/compact.go` | `Compactor`, `Compact`, `Run` |
| `compaction/prune.go` | request-time tool-output pruning |
| `compaction/limit.go` | `EffectiveLimit`, `ShouldCompact` |
| `usage/ledger.go` | the ledger and `CostFor` |
| `*/*_test.go` | |

## Interfaces

```go
// package compaction
func Estimate(msgs []message.Message) int // tokens, a heuristic

type Settings struct {
    Enabled          bool // default true
    ContextLimit     int  // 0 means the model's own window
    ModelWindow      int  // from the catalog; 0 if unknown
    Reserve          int  // tokens kept free; default 16384, or 10% of the limit if smaller
    KeepRecent       int  // tokens of recent history kept verbatim; default 20000
    SummaryMaxTokens int  // default 4000
    InputMaxTokens   int  // largest span sent to the summariser; default 100000
}
func EffectiveLimit(s Settings) int           // min of the non-zero limits; 0 if both are unknown
func ShouldCompact(s Settings, msgs []message.Message, last *provider.Usage) bool

func CutPoint(msgs []message.Message, keepRecent int) (cut int, ok bool)
// cut is the index of the first message to keep.

var ErrNothingToCompact = errors.New("compaction: nothing to compact")

type Compactor struct {
    Provider provider.Provider
    Model    string
    Settings Settings
}
type Result struct {
    Summary        string
    FirstKept      int // index into the input messages
    TokensBefore   int
    TokensAfter    int
    ReadFiles      []string
    ModifiedFiles  []string
    Usage          provider.Usage // cost of the summarisation itself
}
func (c *Compactor) Compact(ctx context.Context, msgs []message.Message, instructions string) (*Result, error)

// Run compacts the current branch of a session and appends the compaction entry.
func Run(ctx context.Context, st *session.Store, c *Compactor, instructions string) (*Outcome, error)
type Outcome struct {
    Entry        session.Entry
    TokensBefore int
    TokensAfter  int
    Replaced     int
}

func PruneToolOutput(msgs []message.Message, keepLast, maxChars int) []message.Message

// package usage
type Ledger struct{ /* ... */ }
func NewLedger(cat func(id string) (provider.Model, bool)) *Ledger
func (l *Ledger) Add(model string, u provider.Usage) provider.Usage // returns u with cost filled in
func (l *Ledger) Total() provider.Usage
func (l *Ledger) ByModel() map[string]provider.Usage
func (l *Ledger) Rebuild(entries []session.Entry) // idempotent: replaces its contents
func CostFor(m provider.Model, u provider.Usage) (usd float64, source string)
```

## Behaviour

### Estimation

1. `Estimate` returns `ceil(runes / 4)` summed per message, plus 4 tokens of overhead per
   message, plus the length of each tool call's name and arguments (same rule). It never
   panics, is monotonic (adding a message never reduces it), and runs in linear time.
2. When the last response reported `PromptTokens`, `ShouldCompact` uses
   `last.PromptTokens + Estimate(messages added since)` instead of estimating everything.
   Usage that is larger than the window, negative, or zero is ignored in favour of the estimate.

### Limit and trigger

3. `EffectiveLimit` is the smaller non-zero of `ContextLimit` and `ModelWindow`; both zero
   gives 0 (compaction never triggers automatically).
4. `ShouldCompact` is true when `Enabled`, the limit is known, and the estimated size is
   greater than `limit - Reserve`. `Reserve` is clamped to at most half the limit. If a limit
   smaller than the system prompt alone is set, it still returns true once, and `Compact`
   reports `ErrNothingToCompact` when there is nothing it can cut (so the caller does not loop).

### Cut point

5. `CutPoint(msgs, keepRecent)` walks from the end accumulating `Estimate` until the sum reaches
   `keepRecent`. The cut is the **nearest user message at or before that point**, so the kept part
   starts at a user message. A leading system message is never cut away and never counted in the
   result's index (the cut is always after it).
6. A cut never separates an assistant message with tool calls from its results.
7. If the only candidate is index 0 (or there is no user message before the point) the function
   falls back to a **turn split**: the nearest assistant message that begins a tool-call group
   (an assistant message with tool calls, or any assistant message) at or before the point and
   after index 0. If that also fails, `ok` is false.
8. If everything fits within `keepRecent`, `ok` is false.

### Compaction

9. `Compact` selects the span `msgs[:cut]` (excluding a leading system message). If the span
   contains an earlier summary (a user message wrapped in `<summary>`), the summariser is asked to
   **update** that summary instead of starting over.
10. The span is serialised as plain text, `[user]`, `[assistant]`, `[tool <name>]` blocks. Each tool
    result is cut to 2,000 characters in this text. If the text is estimated above `InputMaxTokens`,
    the span is split at message boundaries into chunks that fit, each is summarised, and the
    partial summaries are merged by one more call.
11. The summarisation prompt asks for these sections, in order: **Goal**, **Constraints and
    preferences**, **Progress (done, in progress, blocked)**, **Key decisions**, **Next steps**,
    **Critical context** (names, paths, identifiers, error text needed to continue). `instructions`
    is appended as extra focus. The model is told to write in the third person and to preserve exact
    identifiers.
12. The files the span read or modified are extracted **by code, not by the model**, from tool calls
    named `read` (read) and `write` or `edit` (modified), deduplicated and sorted, and appended to
    the summary as `<read-files>` and `<modified-files>` blocks.
13. An empty or whitespace-only summary is an error (`compaction: empty summary`). A provider error
    is returned as is. In both cases nothing is written to the session.
14. `TokensAfter` is `Estimate` of the resulting context (summary plus kept messages).
15. `Run` loads `st.Messages` with their entry ids for the current branch, calls `Compact`, maps
    `FirstKept` to the entry id of that message, and appends one `compaction` entry
    (`Summary`, `FirstKept`, `TokensBefore`, `Usage`) with the current leaf as its parent.
    The compaction entry's usage is the summarisation cost.
16. Compacting a branch that already ends in a compaction entry with no messages after it returns
    `ErrNothingToCompact`.
17. A `custom` entry with `Keep: true` before the cut is untouched.

### Pruning

18. `PruneToolOutput` returns a copy in which every tool result **older than the last `keepLast`
    tool results** and longer than `maxChars` is replaced by
    `[output pruned: <n> characters; run the tool again if you need it]`. `ToolCallID` and `Name`
    are kept, so the transcript still validates. The input is not modified.

### Ledger and cost

19. `CostFor` returns `u.CostUSD` with source `provider` when it is positive. Otherwise, with a
    known model, `(prompt-cached) * InPerM/1e6 + cached * InPerM/1e6 * 0.1 + completion * OutPerM/1e6`
    with source `catalog`, where `cached` is `CachedTokens` capped at `PromptTokens`. An unknown
    model or no price gives `0, ""`.
20. `Ledger.Add` fills in `CostUSD` and `CostSource` through `CostFor`, adds to the total and to
    the model's entry, and returns the filled-in usage. It is safe for concurrent use.
21. `Rebuild` replaces the ledger's contents from the `message` and `compaction` entries'
    usage, in file order. Calling it twice gives the same result (idempotent). Entries without
    usage contribute nothing.

## Matrix

| ID | Case | Input / setup | Expected |
|---|---|---|---|
| EST01 | empty | | 0 |
| EST02 | known text | 400 runes | 100 plus overhead |
| EST03 | multibyte text | 400 `é` | counted by runes, same as 400 ASCII |
| EST04 | tool calls counted | assistant with a call with 400 chars of args | adds about 100 |
| EST05 | monotonic | append any message | never decreases (property) |
| EST06 | very large input | 50 MiB message | completes quickly; no overflow |
| EST07 | linear time | double the input | about double the time (not asserted tightly) |
| LIM01 | limit is the smaller non-zero value | `ContextLimit` 100k, window 200k | 100k |
| LIM02 | only the model window known | | window |
| LIM03 | neither known | | 0; never compacts |
| LIM04 | below the threshold | est 50k, limit 100k, reserve 16k | false |
| LIM05 | above the threshold | est 90k | true |
| LIM06 | exactly at the threshold | est equals `limit - reserve` | false |
| LIM07 | disabled | `Enabled` false | false |
| LIM08 | uses reported usage | last prompt tokens 95k | true even if the estimate is small |
| LIM09 | ignores nonsense usage | prompt tokens larger than the window, negative, zero | falls back to the estimate |
| LIM10 | adds the estimate of new messages | usage 80k plus a 20k message | true |
| LIM11 | `Reserve` clamped to half the limit | reserve 90k, limit 100k | threshold 50k |
| LIM12 | limit below the system prompt | limit 1k, 5k system prompt | true; `Compact` then returns `ErrNothingToCompact` |
| CUT01 | cuts at a user boundary | 10 turns, keep 3 turns' worth | the cut index is a user message |
| CUT02 | never splits a tool pair | tool results between user messages | cut at the user message before the group |
| CUT03 | everything fits | small history | `ok` false |
| CUT04 | system message preserved | leading system message | cut index greater than 0; the system message is not in the span |
| CUT05 | one huge turn, only one user message | user, then 30 tool turns | turn-split fallback at an assistant boundary |
| CUT06 | fallback never splits a pair | | the kept part starts at an assistant message with all its results after it |
| CUT07 | no valid cut | one user message and one short reply, tiny keepRecent | `ok` false |
| CUT08 | `keepRecent` zero | | cuts at the last user message |
| CUT09 | assistant-only history | | `ok` false |
| CUT10 | the kept part always passes `message.Validate` after the summary is prepended | property over random histories | true |
| CUT11 | the cut index is never inside a tool-call group | property over random histories | true |
| CMP01 | summary produced and entry shape | fake provider returns a summary | `Result.Summary` set; `FirstKept` valid |
| CMP02 | the prompt contains the required sections | inspect the request | all six headings present |
| CMP03 | `instructions` appended | | present in the prompt |
| CMP04 | earlier summary updated, not restarted | span begins with a `<summary>` message | prompt asks to update it and contains it |
| CMP05 | tool results truncated in the summariser input | 10,000-char result | cut to 2,000 |
| CMP06 | span larger than `InputMaxTokens` | | chunked; merge call made; one summary returned |
| CMP07 | read and modified files extracted by code | calls to read, write, edit | `<read-files>` and `<modified-files>` appended; sorted; deduplicated |
| CMP08 | a file both read and edited | | appears in both lists (read and modified are independent) |
| CMP09 | empty summary | provider returns `""` and `"  "` | error; no entry |
| CMP10 | provider error | | returned; no entry; the session is unchanged |
| CMP11 | cancelled | | `context.Canceled`; no entry |
| CMP12 | `TokensAfter` smaller than `TokensBefore` | | true |
| CMP13 | `Run` appends one compaction entry | | parent is the old leaf; `FirstKept` is an entry id on the branch |
| CMP14 | context after `Run` | `st.Messages` | summary then the kept messages (via C08) |
| CMP15 | second compaction over the first | | only the later entry applies; the earlier summary is carried into the new one |
| CMP16 | nothing to compact | right after a compaction | `ErrNothingToCompact` |
| CMP17 | empty session | | `ErrNothingToCompact` |
| CMP18 | session file never loses entries | | all earlier entries still in the file |
| CMP19 | `keep` custom entry before the cut | | still present in `Entries()` |
| CMP20 | cost of summarisation recorded | | the entry's `Usage` is set |
| CMP21 | tool pair split by a provider error mid-summary | | no partial write |
| PRN01 | old long results pruned | 10 results, `keepLast` 3 | 7 pruned if long |
| PRN02 | short results kept | under `maxChars` | unchanged |
| PRN03 | recent results kept | the last 3 | unchanged |
| PRN04 | ids and names preserved | | transcript still validates |
| PRN05 | input not modified | compare before and after | unchanged |
| PRN06 | `keepLast` zero | | all long results pruned |
| PRN07 | no tool results | | returns an equal copy |
| LED01 | adds usage and returns cost-filled usage | provider cost 0.01 | source `provider` |
| LED02 | catalog cost fallback | known model, no cost | computed; source `catalog` |
| LED03 | cached tokens discounted | | as in Behaviour 19 |
| LED04 | cached tokens above prompt tokens | | capped; no negative |
| LED05 | unknown model | | cost 0; source `""` |
| LED06 | by-model breakdown | two models | separate entries |
| LED07 | total across models | | sum |
| LED08 | `Rebuild` from entries | message and compaction entries with usage | totals match |
| LED09 | `Rebuild` is idempotent | twice | same result |
| LED10 | `Rebuild` ignores entries without usage | | unchanged totals |
| LED11 | concurrent `Add` | 20 goroutines; `-race` | exact total |
| LED12 | `CostFor` with a negative or NaN input | | 0; no panic |
| LED13 | `CostFor` with a NaN, +Inf, -Inf or negative provider cost | | NaN and +Inf cost 0; the others fall back to the catalog |
| LED14 | `Rebuild` while another goroutine reads | `-race` | a reader sees the old or the new totals, never a partial one |

## Fuzz and property tests

| Name | Asserts |
|---|---|
| `FuzzCutPoint` | never panics; if `ok`, the cut is in range and the kept part validates after a summary is prepended |
| `FuzzEstimate` | never panics; non-negative |
| `TestProperty_CMP_NeverLosesEntries` | after any compaction the session file only grew |

## Out of scope

- Deciding **when** in the loop to call compaction beyond providing `ShouldCompact` and
  `Run` (C12 wires them to `Hooks.BeforeTurn`).
- A slash command and its UI (C12, C16). Accurate tokenisation (the heuristic is deliberate).

## Acceptance

```
go run ./scripts/accept C10
```
