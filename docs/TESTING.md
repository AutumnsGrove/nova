# Nova — Testing

Two requirements shaped this document.

1. **Test extensively from the first commit, without a browser.** The last project's
   Playwright end-to-end suite worked but made the development machine crawl. Nova is
   a terminal program and a protocol server, so nothing here needs a browser, and
   almost nothing needs a terminal.
2. **The first pass must cover failure, not just success.** On Ivy, the agent was not
   told to test anything but the happy path, and the edge cases that mattered were
   found only in a second review by a larger model. Nova puts that review at plan
   time: the edge cases are written into each chunk spec as a test matrix, and a
   script fails the build if a matrix row has no test.

Tests use the standard library only (plus `x/exp/golden` and `catwalk` in `tui/`).

## 1. The layers

| Layer | What it tests | How | Cost |
|---|---|---|---|
| **L0 unit** | one function or type | table-driven, no I/O | microseconds |
| **L1 component** | a package with fakes at its edges | in-process fake provider, temp dirs, fake clock | milliseconds |
| **L2 scenario** | the **real `nova` binary**, end to end | spawn it in `--mode rpc` against a local fake provider server, script a conversation, assert events and files | tens of milliseconds |
| **L3 cassette** | fidelity to a real provider | replay recorded HTTP; recorded only on demand | milliseconds |
| **L4 TUI** | views and key handling | call `Update` with messages; compare `View()` to golden files | milliseconds |
| **L5 PTY smoke** | the TUI really starts and quits | a pseudo-terminal, three or four tests; named `TestPTY…`, skipped unless `NOVA_PTY=1` | seconds; CI only |
| **L6 live** | a real provider | `-tags live`, needs a key | never in CI |

L2 is the workhorse and replaces Playwright. Because the protocol is the only front
door (D13), driving the binary over stdio exercises the same path the TUI, the
GitHub Action, and a web backend use. No terminal, no browser, no network.

### Commands

```
go test -short ./...                 # L0 + L1 + L4. Under 30 seconds. Run constantly.
go test ./...                        # adds L2 and L3. Under 2 minutes.
go test -race ./...                  # always before finishing a chunk
NOVA_PTY=1 go test ./tui/... -run PTY  # L5 pseudo-terminal smoke tests, CI only
go test -tags live ./provider/...    # L6, manual
go test -run X -update ./...         # regenerate golden files
go test -record ./provider/...       # re-record cassettes
```

CI runs tests with `-p 2` so it does not saturate a laptop-class runner.

## 2. `testkit`

A package that every test uses. Its own tests are strict, because everything else
depends on it.

- **`testkit.Provider`**: an in-process `provider.Provider` driven by a script. Each
  call to `Stream` pops the next scripted turn. A turn can emit text, reasoning,
  tool calls (whole or split across chunks), usage, a finish reason, an injected delay,
  or an error at a chosen point in the stream. It records every request it received.
- **`testkit.Server`**: the same scripts served over HTTP as real SSE from an
  `httptest.Server`, so the genuine `openaicompat` code and the binary are exercised.
  It can also misbehave on purpose: cut the connection mid-event, send malformed JSON,
  stall, return 429 with `Retry-After`, return 500, send a very large chunk.
- **`testkit.Run`**: builds `nova` once per test binary (`TestMain`), starts it with a
  temp working directory, config, and home, and returns a client with `Send`,
  `Events`, `Expect`, and `Kill`.
- **`testkit.Clock`**: a fake clock, injected wherever time matters (retry backoff,
  timeouts, thinking duration).
- **`testkit.FS`**: helpers to build a temp tree, including symlinks, binary files,
  CRLF files, unreadable files, and very large files.
- **`testkit.Golden`**: compare a string to `testdata/<name>.golden`; `-update` rewrites it.
- **`testkit.NoLeaks`**: fails a test if goroutines started by it are still running
  after cleanup.

### Scenario files

An L2 test can be written in code or as a YAML scenario, which is easier for an agent
to extend and for a person to read.

```yaml
# scenarios/edit_file.yaml
name: model edits a file then answers
files:
  main.go: "package main\n\nfunc main() {}\n"
provider:
  - say: "I'll add a comment."
    tool_calls:
      - { id: c1, name: edit, args: { path: main.go, edits: [ { oldText: "func main() {}", newText: "// entry\nfunc main() {}" } ] } }
    usage: { prompt: 120, completion: 30 }
  - say: "Done."
steps:
  - send: { command: prompt, text: "add a comment to main" }
  - expect_events:
      - { type: run_start }
      - { type: tool_end, call_id: c1, result: { is_error: false } }
      - { type: run_end, reason: done }
expect_files:
  main.go: "package main\n\n// entry\nfunc main() {}\n"
expect_requests:
  - { calls: 2, last_message_role: tool }
```

`expect_events` matches a subsequence, so a test is not broken by an unrelated new
event. `expect_files` compares exact bytes. `expect_requests` asserts what the
provider saw.

## 3. Failure-mode catalog

This is the checklist the larger model uses when writing a chunk's matrix. An agent
implementing a chunk does not need to invent edge cases; it reads its matrix. The
catalog is here so that every matrix is built from the same list and nothing is
forgotten.

### Provider and streaming

- Stream ends without `[DONE]`. Stream ends mid-event (partial line).
- A chunk with an empty `choices` array (usage-only chunk). A chunk with `null` content.
- A tool call split across many chunks: name first, arguments in fragments, id late.
- Two tool calls interleaved by `index`. A tool call with no id. Duplicate ids.
- Arguments that are invalid JSON, empty, or `null`.
- A tool call with an empty name.
- Reasoning deltas under different field names (`reasoning`, `reasoning_content`).
- Usage in the last chunk, in a separate chunk, missing entirely.
- HTTP 401, 403, 404, 408, 429 with and without `Retry-After`, 500, 502, 503.
- A 200 response whose body is an error object. A non-JSON error body (HTML).
- Connection refused, DNS failure, reset mid-stream, a stall past the timeout.
- Context cancelled before the request, during connect, mid-stream, after the last chunk.
- A very large single chunk. Many tiny chunks. Unicode split across chunk boundaries.
- Finish reasons: `stop`, `length`, `tool_calls`, `content_filter`, unknown, missing.
- An assistant message with both text and tool calls. With neither.
- Extra unknown fields everywhere (must be ignored).

### Tools: filesystem

- Path: empty, `.`, `..`, absolute, outside the working directory, with `//`, trailing
  slash, a NUL byte, very long, Unicode and spaces, a Windows-style separator.
- A symlink to a file, to a directory, a dangling symlink, a symlink loop.
- Missing file, a directory where a file is expected and the reverse.
- Permission denied (read and write), a read-only file system.
- Empty file, a file with no trailing newline, CRLF and mixed line endings, a BOM.
- Binary file (NUL bytes). A file larger than the read cap. A file that grows while read.
- Non-UTF-8 bytes. Very long lines. A line count exactly at the limit, one over.
- `offset` or `limit`: zero, negative, beyond the end, non-integer.
- `edit`: `old_text` absent, present once, present many times, overlapping, equal to
  `new_text`, empty, containing regex characters, spanning lines, differing only by
  whitespace or line endings. An edit on a file changed since it was read.
- `write`: new file, overwrite, parent directory missing (create or error, as specified),
  disk full, writing to a path that is a directory.
- Atomicity: a failed write leaves the original intact.
- Concurrent calls on the same path.

### Tools: search and bash

- `glob`/`grep`: no matches, a huge number of matches (cap), a pattern with special
  characters, an invalid regex, case flags, binary files skipped, `.gitignore` honoured
  and overridable, hidden files, a symlink loop, a very deep tree.
- `bash`: exit code zero, non-zero, killed by a signal, command not found, a command
  that prints nothing, a lot (cap), only stderr, binary output, invalid UTF-8.
- A timeout (and that the **whole process group** is killed, including children).
- Context cancel mid-command. A command that reads stdin (must not hang). A command that
  backgrounds a child and exits. A very long single line. Environment is as specified.
- Working directory missing or not a directory.

### Agent loop

- No tools registered. A tool name the model invents. Arguments that fail schema validation.
- Zero, one, many tool calls; the same tool many times; a tool call after a final answer.
- A tool that is slow, returns instantly, panics, returns an error, returns empty content,
  returns enormous content. A panic in **sequential** mode (not only parallel).
- Parallel completion order differs from call order; results still appended in call order.
- A hook that panics, returns nil, blocks, blocks everything, takes longer than its deadline.
- `MaxTurns` exactly reached; one below; `MaxTurns` of zero (default) and negative.
- Cancel before the first request, during streaming, during a tool, between turns.
- Provider error on turn one, on a later turn, after tool results were appended.
- Steering message arriving during a tool call, during streaming, after the last turn.
- Follow-up queued while idle. Abort with queued messages. Abort twice. Prompt while busy.
- Retry: succeeds on attempt two, exhausts, a non-retryable error is not retried, backoff
  uses the fake clock, cancel during backoff.
- Usage: summed correctly across turns, kept when a later call fails, absent from a provider.
- Events: exactly one `run_start` and one `run_end`; order is exact; none after `run_end`.

### Sessions and compaction

- A new, empty file. An existing file. A file that does not end in a newline.
- A corrupt line in the middle; a truncated final line; a line that is valid JSON but the
  wrong shape; an unknown entry type (must be preserved and skipped, not dropped).
- A duplicate entry id; a `parent` that does not exist; a cycle.
- Appending concurrently from two goroutines; from two processes (advisory lock).
- Branching: from the leaf, from the root, from a middle entry; navigating to a missing id.
- Resume reconstructs exactly the active branch's messages, including tool pairs.
- Compaction: cut point at a user message; **never between a tool call and its result**;
  an empty history; a history shorter than one turn; a summary call that fails; one that
  returns empty; a second compaction over the first; a `keep` entry survives.
- Context estimate: no usage data; usage that is wrong (larger than the window).
- Context limit smaller than the system prompt alone.

### Protocol and modes

- Framing: a line with `\r\n`; a line with U+2028 inside a string; a line at the size limit
  and one over; two frames in one read; one frame across many reads; an empty line.
- Invalid JSON; valid JSON of the wrong type; a missing `type`; an unknown `type`.
- Two commands with the same `id`. A response for an unknown `id`.
- A `ui_request` the client cannot answer. A `ui_response` for an unknown or expired id.
- The client disconnects mid-run (the run must be cancelled and resources released).
- Backpressure: a client that stops reading.
- A command while busy. `abort` while idle. `shutdown` while running.
- Print mode: exit codes for success, tool error only, provider failure, cancel.
- Serve: no token on a non-loopback address; a wrong token; two clients; a client that drops.

### Extensions

- A manifest that is missing fields, has a bad name, bad version, bad capability.
- An extension that never sends `register`; that registers twice; that registers a tool
  name already taken; one that registers an invalid schema.
- A hook that does not answer before its deadline; that answers twice; that answers an
  unknown id; that sends output on stdout that is not a frame.
- An extension that crashes at start, mid-call, repeatedly (restart limit, then disabled).
- A capability used but not declared. A `complete` call that would exceed a cap.
- Reload during a run; reload with one extension broken; replay of `keep` entries in order.
- Install: a bad URL; a repository with no manifest; a failing build; a changed commit;
  a lockfile that disagrees; cancelling at the trust prompt leaves nothing behind.
- A declarative tool: argument with a space, a quote, `;`, `$(…)`, a leading dash, or a
  newline (never interpreted by a shell); a missing argument; a timeout; output over the cap.

### Configuration

- Missing files; empty; invalid YAML; unknown keys (warn, do not fail); wrong types.
- Merge order (defaults, user, project, flags, environment). Environment overrides.
- An untrusted project's config, extensions, and hooks are not loaded.
- A context file that is huge, binary, or unreadable.

### TUI

- Resize to tiny and back; zero width. Very long lines; wide characters; combining marks;
  ANSI in tool output; a tab character.
- A streaming message that arrives faster than the frame rate. A transcript of 10,000
  items. Expanding and collapsing during streaming.
- A dialog while a run is streaming. Two dialogs queued. `Esc` at every level.
- No colour support; a light theme; a narrow terminal for the splash screen.

## 4. The matrix rule

Every chunk spec has a table. Each row is one behaviour to test and has an ID.

```
| ID    | Case                                 | Input / setup                         | Expected                              |
| RDF01 | read a normal file                   | 3-line file                           | numbered lines, no truncation         |
| RDF08 | read past the end                    | offset 10 on a 3-line file            | error result naming 3 lines           |
```

Rules:

1. A test's name contains its row ID as a whole component: `TestRead_RDF08_OffsetBeyondEnd`.
   An ID is two to four capital letters and two or three digits.
2. `go run ./scripts/matrixcheck docs/chunks/C05-tools-fs.md ./tools/fs` (C00) parses the
   spec, finds every row ID, and **fails if any ID has no test whose name contains
   it**, or if a test cites an ID that is not in the spec.
3. The chunk's acceptance command runs `matrixcheck`. A chunk is not done until it passes.
4. The implementing agent may add rows (a new ID and a new test) but may not delete or
   weaken one. Changes to a spec are made by the human or the larger model.

This turns "did you test the edge cases" from a judgement into a check.

## 5. Other required techniques

- **Fuzz targets** for every parser that takes untrusted bytes: SSE, the JSONL framer,
  tool-argument decoding, the session loader, the manifest parser, the YAML tool
  templates. Each fuzz target has a seed corpus of the nasty cases above and runs for a
  few seconds in CI.
- **Property tests** (table of random cases from a seeded generator) for: edit
  application (apply, then check the result contains `new_text` exactly once and nothing
  else changed), compaction cut points (never splits a tool pair), the session tree
  (the active branch is always a path from root to leaf).
- **`-race`** on every package, always.
- **Leak checks** (`testkit.NoLeaks`) on anything that starts goroutines or processes.
- **Golden files** for TUI views and for the protocol event sequence of each scenario.
- **Coverage floor**: 85% for core packages, 70% for `tui/`, measured with
  `go test -cover`. A floor is a smoke alarm, not a goal; the matrix is the goal.
- **Determinism**: no test depends on wall-clock time, map order, the network, or the
  machine's locale. Seeds are fixed and printed on failure.

## 6. Definition of done for any chunk

A chunk is done only when all of these pass, run by `scripts/accept <chunk>`:

```
gofmt -l .                      # no output
go vet ./...
go build ./... && go build -tags notui ./...
go test -short -race ./<pkgs>
go test -race ./<pkgs>
go run ./scripts/matrixcheck <spec> <pkgs>
go run ./scripts/linecount <pkgs>      # within the package budget
go run ./scripts/importcheck           # layer rules and the UI import boundary
```

and the agent has updated `NEXTSTEPS.md` (see `AGENTS.md`), listing anything it
could not satisfy and why. An empty list is a valid result. A silent gap is not.

## 7. What not to test

- Do not test the Go standard library, or the exact wording of log messages.
- Do not assert on timing with real sleeps; use the fake clock.
- Do not make a test depend on another test's side effects.
- Do not write a test that passes if the code under test is deleted.
