---
chunk: C07
title: The bash tool
packages: [./tools/bash]
budgets: { ./tools/bash: 400 }
depends: [C04]
---

# C07 — The `bash` tool

## Goal

A shell tool that cannot hang the agent, leak processes, flood the context, or corrupt
the transcript. The prototype works for simple commands. It does not kill child
processes, can block forever on a backgrounded child, and mixes up output ordering.

## Read first

- `tools/bash/bash.go` (the prototype, 123 lines).
- `tool/tool.go` as changed in C04 (`Progress`, `Details`, `Truncated`).
- `docs/TESTING.md` section 3, "Tools: search and bash".

Known prototype defects this chunk fixes:

- On timeout or cancel only the shell is killed; its children keep running.
- `CombinedOutput` waits until every holder of the pipe closes it, so `sleep 100 &` makes the
  call hang for 100 seconds after the shell has exited.
- Output over the cap keeps the **head** and drops the tail, which is usually the part that
  matters (the error at the end).
- `-l` (login shell) runs profile scripts: slow, and they can print or prompt.
- The command inherits stdin, so a command that reads input can hang or steal the terminal.
- A cancelled call returns a Go error, which is a different shape from a timeout.

## Files

| File | Change | Purpose |
|---|---|---|
| `tools/bash/bash.go` | change | the tool and options |
| `tools/bash/run.go` | create | process start, group kill, output capture |
| `tools/bash/output.go` | create | the capped, tail-biased buffer and UTF-8 repair |
| `tools/bash/proc_unix.go` | create | `//go:build !windows` process-group helpers |
| `tools/bash/proc_other.go` | create | `//go:build windows`: returns an error result `bash is not supported on this platform` |
| `tools/bash/*_test.go` | create or change | tests skip if `bash` is not on `PATH` |

## Interfaces

```go
type Option func(*options)
func WithTimeout(d time.Duration) Option         // default 120s
func WithMaxTimeout(d time.Duration) Option      // default 600s: the cap on a requested timeout
func WithMaxOutputBytes(n int) Option            // default 51200
func WithShell(path string) Option               // default "bash"
func WithLoginShell(on bool) Option              // default false
func WithClock(c Clock) Option                   // for progress throttling; tests inject a fake
func WithEnv(extra map[string]string) Option
func WithPipeGrace(d time.Duration) Option       // default 200ms
func Bash(opts ...Option) tool.Tool

type Clock interface{ Now() time.Time }          // satisfied by testkit's clock
```

Arguments: `command` (required string), `timeout` (integer seconds; default 120; capped at
the max timeout).

## Behaviour

1. The command runs as `<shell> -c <command>` (`-lc` only with `WithLoginShell`), in
   `Context.WorkDir`. A missing or non-directory `WorkDir` is an error result
   `working directory <path> does not exist`. A missing shell is an error result
   `shell not found: <shell>`.
2. **stdin is `/dev/null`.** A command that reads input sees EOF immediately.
3. The environment is the parent's, plus `NOVA=1`, `TERM=dumb`, `NO_COLOR=1`,
   `PAGER=cat`, `GIT_PAGER=cat`, `GIT_TERMINAL_PROMPT=0`, then `WithEnv` values.
4. The process starts in **its own process group** (`Setpgid`). On timeout or on `ctx`
   cancellation the whole group gets `SIGTERM`, then `SIGKILL` after 2 seconds if anything is
   still alive. No process started by the command may survive the call (verified by test).
5. stdout and stderr are the **same pipe**, so their relative order is preserved.
6. After the shell exits, wait up to `PipeGrace` for the pipe to close. If a backgrounded
   child still holds it, stop reading, kill the group, and note it
   (`(background processes were still running and were stopped)`). A call must return within
   `PipeGrace` plus a small constant of the shell exiting.
7. **Output cap.** Keep at most `maxOutputBytes`, split as the first 20% and the last 80%,
   with `... (<n> bytes omitted) ...` between. `Truncated` is true when anything was dropped.
   Memory use is bounded by the cap, not by the amount of output (a command that prints
   1 GiB must not use 1 GiB).
8. Invalid UTF-8 in the output is replaced with U+FFFD. NUL bytes are replaced with U+FFFD.
   Carriage returns used for progress bars are collapsed: a `\r` not followed by `\n` keeps
   only the text after the last `\r` on that line.
9. **Result shapes.**
   - exit 0: `Content` is the output (or `(no output)`), `IsError` false.
   - exit non-zero: `Content` is the output then `(exit code N)`, `IsError` true.
   - killed by a signal: `(terminated by signal <name>)`, `IsError` true.
   - timeout: output then `error: command timed out after <d>`, `IsError` true.
   - cancelled: output then `error: command cancelled`, `IsError` true. **Not** a Go error.
10. `Details` is `{"exit_code":n|null, "signal":"…"|null, "duration_ms":n, "timed_out":bool,
    "cancelled":bool, "truncated":bool}`.
11. **Progress.** While the command runs, call `tctx.Progress` with the new output at most
    every 100 ms (by the injected clock) and at most 4 KiB per update. Progress is
    best-effort: dropped updates never affect the final result.
12. `Annotations`: `Destructive` and `OpenWorld` true; not `Parallel`.
13. A `timeout` of zero or negative uses the default. A timeout above the max is capped.

## Matrix

| ID | Case | Input / setup | Expected |
|---|---|---|---|
| BSH01 | simple command | `echo hi` | `hi\n`; not an error |
| BSH02 | no output | `true` | `(no output)` |
| BSH03 | non-zero exit | `exit 3` | `(exit code 3)`; `IsError` |
| BSH04 | stderr captured | `echo e >&2` | `e` in the output |
| BSH05 | order of stdout and stderr preserved | alternating writes | same order |
| BSH06 | command not found | `nosuchcmd` | exit 127 shape; message from the shell |
| BSH07 | working directory used | `pwd` | WorkDir |
| BSH08 | missing working directory | | error result naming it |
| BSH09 | `WorkDir` is a file | | error result |
| BSH10 | shell not found | `WithShell("/no/bash")` | error result |
| BSH11 | stdin is closed | `cat` | returns immediately with no output |
| BSH12 | `read x` does not hang | `read x; echo got` | returns; no hang |
| BSH13 | environment set | `echo $NOVA $TERM $NO_COLOR` | `1 dumb 1` |
| BSH14 | `WithEnv` overrides | | applied |
| BSH15 | pager disabled | `git log` style command using `$PAGER` | `cat` |
| BSH16 | killed by signal | `kill -9 $$` | `(terminated by signal SIGKILL)` |
| BSH17 | timeout | `sleep 10`, timeout 1 s (fake or short real) | timeout result; returns in about 1 s |
| BSH18 | timeout kills the whole group | `sleep 30 & sleep 30` | no `sleep` process survives |
| BSH19 | timeout kills grandchildren | `bash -c 'sleep 30 & wait'` | none survive |
| BSH20 | SIGTERM ignored then SIGKILL | `trap '' TERM; sleep 30` | killed within grace |
| BSH21 | cancel mid-command | cancel `ctx` after 100 ms | cancelled result (not a Go error); group killed |
| BSH22 | cancel before start | | cancelled result; nothing started |
| BSH23 | backgrounded child holding the pipe | `sleep 30 &; echo done` | returns within `PipeGrace`; output `done`; note about background processes; child killed |
| BSH24 | backgrounded child with output redirected | `sleep 30 >/dev/null 2>&1 &` | returns at once; the child is killed with the group when the call ends |
| BSH25 | `nohup` child | | killed with the group |
| BSH26 | output under the cap | | verbatim |
| BSH27 | output over the cap | 200 KiB | head 20%, marker, tail 80%; `Truncated` |
| BSH28 | the tail is kept | error printed last | present in the result |
| BSH29 | huge output stays bounded in memory | prints 500 MiB | completes; peak heap under 20 MiB (measure) |
| BSH30 | a single very long line | 5 MiB without a newline | capped; no hang |
| BSH31 | invalid UTF-8 | `printf '\xff'` | U+FFFD |
| BSH32 | NUL bytes | `printf 'a\0b'` | replaced |
| BSH33 | carriage-return progress | `printf 'a\rb\rc\n'` | `c` |
| BSH34 | `\r\n` preserved as a newline | | one line break |
| BSH35 | binary output | `head -c 100 /dev/urandom` | valid UTF-8 result; no error |
| BSH36 | empty command | `""` | error result `command is required` |
| BSH37 | whitespace-only command | `"   "` | handled by the shell; no panic |
| BSH38 | `timeout` zero or negative | | default used |
| BSH39 | `timeout` above the max | 99999 | capped at the max |
| BSH40 | `timeout` as a string | `"5"` | rejected by schema validation (C04) |
| BSH41 | progress updates | prints over 500 ms | at least one update; none within 100 ms of another (fake clock) |
| BSH42 | progress dropped without `Emit` | nil `Emit` | no panic |
| BSH43 | `Details` | | all fields |
| BSH44 | annotations | | `Destructive`, `OpenWorld`; not `Parallel` |
| BSH45 | login shell option | `WithLoginShell(true)` | the flag is `-lc` |
| BSH46 | no goroutine or process leak after every case above | | `NoLeaks`; no stray processes |
| BSH47 | non-UTF-8 command | | passed through unchanged |
| BSH48 | very long command (1 MiB) | | runs or reports the OS limit; no panic |
| BSH49 | concurrent calls | 20 goroutines | independent results; `-race` clean |
| BSH50 | exit code above 255 | `exit 256` | `(exit code 0)` as the OS reports; no panic |

A process started by the command is killed with its group when the call ends, even if it
redirected its output. A long-running server is not a use of this tool (see Out of scope).

## Fuzz and property tests

| Name | Asserts |
|---|---|
| `FuzzOutputBuffer` | for any sequence of writes and any cap: length never exceeds cap plus the marker; tail bytes are always the last bytes written |
| `FuzzSanitizeOutput` | result is always valid UTF-8 with no NUL |
| `TestProperty_BSH_GroupKill` | for random trees of spawned children and a timeout, no process in the group survives |

## Out of scope

- Background jobs and long-running servers (`run_in_background`, job output and kill tools).
  A later chunk if needed.
- Windows support. Interactive commands, TTY allocation.
- Command allow-listing (that is the permission policy, C04).

## Acceptance

```
go run ./scripts/accept C07
```
