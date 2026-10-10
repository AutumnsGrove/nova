---
chunk: C19
title: Polish, first run, doctor, examples, release
packages: [./cmd/nova, ./doctor, ./config, ./tui/firstrun, ./tui/cli]
budgets: { ./cmd/nova: 500, ./doctor: 300, ./config: 1000, ./tui/firstrun: 250, ./tui/cli: 150 }
depends: [C16, C17, C18]
---

# C19 — Polish, first run, doctor, examples, release

## Goal

Everything between "it works" and "someone else can install it and be productive": a
styled `--help`, a first-run flow that gets a new user from nothing to a working session,
`nova doctor`, the embedded documentation and the `extension-author` skill that let Nova
write its own extensions, ten small examples, a favicon variant of the logo, and a release
pipeline with checksummed binaries and an install script.

## Read first

- `docs/EXTENSIONS.md` section "Letting Nova write its own extensions".
- `docs/BRAND.md` ("Still to do": the favicon) and `assets/logo/gen/main.go`.
- `cmd/nova/*` (C12), `config/*` (C11), `mode/*`, `tui/ui/*` and `tui/dialog/*` (C15 to C17).
- `auth/login.go` (C03), `ext/*` (C13), `bundled/*` (C18).
- `docs/TESTING.md` sections 3 and 6.

## Files

| File | Change | Purpose |
|---|---|---|
| `tui/cli/cli.go` (`!notui`) | create | `Execute(root *cobra.Command) error` using `fang` for styled help and errors |
| `cmd/nova/exec_tui.go` (`!notui`), `exec_min.go` (`notui`) | create | pick `cli.Execute` or plain `root.Execute` |
| `cmd/nova/help.go` | create | grouped help text, examples, and links shared by both builds |
| `cmd/nova/version.go` | create | version stamping and `nova version` |
| `cmd/nova/doctor.go` | create | the subcommand |
| `cmd/nova/docs.go` | change | `docs list`, `docs search`, topics from the embedded set |
| `doctor/doctor.go` | create | the checks |
| `config/write.go` | create | `SetUser`: change one dotted key in the user config, preserving the rest |
| `tui/firstrun/firstrun.go` | create | the first-run flow |
| `bundled/skills/extension-author/SKILL.md` | create | the built-in skill |
| `config/skills.go` | change | load embedded built-in skills at the lowest precedence |
| `examples/extensions/*` | create | ten examples, each with a README and a test |
| `assets/logo/gen/main.go` | change | `-favicon` writes SVG and PNG favicons |
| `assets/logo/nova-favicon.svg`, `nova-favicon-{16,32,180,512}.png` | generate | |
| `.github/workflows/release.yml` | create | build, checksum, publish |
| `install.sh` | create | download, verify, install |
| `CHANGELOG.md`, `CONTRIBUTING.md` | create | |
| `*/*_test.go` | create | |

The CLI is cobra (C12). The full build styles help and errors with `charm.land/fang/v2` through `tui/cli`, so
`cmd/nova` itself never imports a UI library and a `notui` build prints plain cobra help (D45).

## Interfaces

```go
// package doctor
type Status int // OK, Warn, Fail
type Check struct{ Name string; Status Status; Detail, Fix string }
type Options struct {
    CWD, Home string
    Env       func(string) string
    Offline   bool
    HTTP      *http.Client
    Host      HostStatus        // optional: extension status
    Out       io.Writer
}
func Run(ctx context.Context, opt Options) []Check
func Worst(checks []Check) Status

// package config
func SetUser(home, key string, value any) error // dotted key, for example "model" or "tools.bash.timeout_seconds"

// package firstrun
type Step int // Login, Model, Done
func Needed(hasKey bool, cfg *config.Config) []Step
// the TUI flow is a Bubble Tea sub-model used by ui; see Behaviour
```

## Behaviour

### Help and version

1. `nova --help` and `nova help [command]` print grouped usage: modes, session flags, subcommands, examples, and links to the docs.
   In the full build, fang styles headings, flags, and errors (no colour when `NO_COLOR` or not a terminal); in a `notui` build,
   plain cobra text. The content is identical across builds. Width adapts to the terminal (default 80) and never exceeds it.
2. `nova help <subcommand>` documents each subcommand and its flags. An unknown subcommand is a usage error (exit 2) suggesting the closest
   known name by edit distance.
3. `nova version` prints `nova v0.1.0 (commit abc1234, built 2026-10-09, go1.26, linux/amd64, full)`; `--json` prints the same fields as
   JSON. The values come from link-time variables (`-X main.version`, `main.commit`, `main.date`) and `runtime/debug.BuildInfo`; a plain
   `go build` shows `dev` and the VCS revision when available. The flavour is `full` or `min` by build tag.

### First run

4. `nova` in a terminal with no API key resolvable (`auth.Resolve` returns `ErrNoKey`) starts the first-run flow **before** the chat screen:
   (a) a welcome with the splash, (b) **Login**: `Log in with OpenRouter` (runs `auth.Login`, shows the URL, opens the browser) or
   `Paste a key` (masked input; the key is saved with `auth.NewFileStore`), (c) **Model**: a picker with the catalog's default highlighted,
   saved with `config.SetUser("model", id)`. Each step can be skipped with `esc` (the app then starts and the first prompt reports the missing
   key as in C12). A failure shows the error and offers retry or skip.
5. The flow runs again only when the key is missing; it never runs in print, json, rpc, or serve mode (those print the `no API key` message
   and exit 1).
6. `config.SetUser` edits `<home>/.config/nova/config.yaml`, creating it (mode 0600, directory 0700) if absent. It preserves other keys,
   comments, and key order (using `yaml.Node`), changes only the named dotted key (creating intermediate maps), writes atomically (temp file,
   rename), and refuses a key that does not exist in the `Config` schema. A corrupt existing file is not overwritten: the call fails with an
   error and writes nothing.
7. Secrets are never written to the config file; only the auth store holds the key.

### Doctor

8. `nova doctor` runs these checks and prints a table, `ok`, `warn`, or `fail` each with a one-line detail and, for non-ok, a fix:
   - **version**: the build.
   - **api key**: whether one resolves and from where (`config`, `env`, `store`), **never the value**; fail if none.
   - **network**: a `GET` of OpenRouter's model list with a 5 second timeout and the key if present; warn on timeout; skipped with `--offline`.
   - **git**: present on `PATH` (warn if missing; needed for `nova ext install`).
   - **bash**: present (fail if missing; the `bash` tool needs it).
   - **terminal**: `TERM`, `COLORTERM` (truecolor yes or no), size, UTF-8 locale; warn on `dumb` or a non-UTF-8 locale.
   - **config**: loads; lists warnings; fail only on an unreadable file.
   - **directories**: the config, state, and sessions directories exist or can be created and are writable.
   - **trust**: whether the current directory is trusted (informational).
   - **extensions**: each installed extension's manifest validity and, if started, whether it registers within the timeout.
   - **project**: context files found; skills loaded; warnings.
9. Exit code is 0 for all ok or warn, 1 if any fail. `--json` prints the checks as JSON. Doctor never modifies anything and never prints
   a secret. No check takes longer than its timeout; the whole run is bounded by 30 seconds.

### Documentation and the author skill

10. `nova docs` lists topics; `nova docs <topic>` prints one; `nova docs search <terms>` ranks topics by term frequency in title and body
    (title matches weigh 5×) and prints the top 5 with a one-line excerpt. Topics are the embedded `docs/*.md`, the example READMEs, and the
    SDK package documentation. Unknown topics list the topics. Output is plain text with no colour when piped.
11. The binary embeds `EXTENSIONS.md`, `PROTOCOL.md`, the `ext/sdk` documentation, and `examples/extensions/` (sources and READMEs). The
    **authoring context** (those files together) must estimate under **40,000 tokens** (`compaction.Estimate`), checked by a test.
12. `bundled/skills/extension-author/SKILL.md` is embedded and appears as a built-in skill (`Source: "builtin"`, lowest precedence, overridable
    by a user or project skill of the same name). Its body tells the model the workflow: run `nova docs extensions` and `nova docs protocol`;
    pick the nearest example (`nova docs examples`); write `extension.yaml` and the code; run `nova ext check <dir>`; install locally with
    `nova ext install ./<dir>`; `/reload`; test by using it; and a short list of rules (register only at start, keep state in entries, truncate
    large output, errors as results, no shell interpolation). Under 1,500 tokens.

### Examples

13. `examples/extensions/` holds ten examples, each under 100 lines of code, with a `README.md` (what it shows, how to run it) and a test
    that runs `ext.Check` and, where meaningful, a scenario against the real binary:
    `hello` (declarative tool), `word-count` (declarative tool with arguments), `block-rm` (command hook that blocks dangerous `rm`),
    `hello-go` (SDK tool and command), `path-guard` (Go hook blocking writes outside the project), `status-segment` (footer segment updated
    on each turn), `clock-widget` (a text widget), `ask-demo` (a command that opens a form), `session-notes` (entries and replay), and
    `summarize` (a command using `Complete`). Every example passes `nova ext check`.

### Favicon

14. `go run ./assets/logo/gen -favicon` writes `nova-favicon.svg` and PNGs at 16, 32, 180, and 512 pixels. The favicon is the **star of the
    full mark plus its warm companion cell**, simplified so it reads at 16 pixels: a cell star with arm length 6 and one warm cell, on a
    transparent background (SVG) and on the tile colour (PNG for 180 and 512). PNGs are rendered with the standard library `image/png`, nearest
    neighbour, with no anti-aliasing. Output is deterministic.

### Release

15. `.github/workflows/release.yml` runs on a tag matching `v*`. For `linux` and `darwin` on `amd64` and `arm64` it builds the **full** and
    the **`notui`** binaries with `CGO_ENABLED=0`, `-trimpath`, and `-ldflags "-s -w -X main.version=<tag> -X main.commit=<sha> -X
    main.date=<UTC date>"`, packs `nova_<version>_<os>_<arch>.tar.gz` and `nova-min_<version>_<os>_<arch>.tar.gz` (the binary, `LICENSE`,
    `README.md`), writes `checksums.txt` (SHA-256), and attaches everything to the GitHub release. It runs `scripts/accept` for every spec first
    and fails the release if any fails. Windows is not built in the first release (the `bash` tool is Unix only).
16. `install.sh` (POSIX `sh`): detects OS and architecture (an unsupported pair is an error that names it); resolves the version (default
    latest; `NOVA_VERSION` overrides) from the release API; downloads the archive and `checksums.txt` over HTTPS; **verifies the SHA-256 and
    aborts on mismatch before extracting anything**; installs to `NOVA_INSTALL_DIR` (default `$HOME/.local/bin`) without `sudo`; sets mode 0755;
    prints a PATH hint if the directory is not on `PATH`; supports `NOVA_FLAVOUR=min`; is idempotent (re-running upgrades in place, keeping the
    old binary until the new one is verified); cleans its temporary directory on every exit path. It never executes downloaded content other
    than the final `nova version` sanity check. `NOVA_RELEASE_BASE` overrides the download base for tests.
17. `CHANGELOG.md` follows Keep a Changelog; `CONTRIBUTING.md` points to `AGENTS.md`, `docs/IMPLEMENTATION.md`, and the matrix rule.

## Matrix

| ID | Case | Input / setup | Expected |
|---|---|---|---|
| CLH01 | `--help` content | | modes, flags, subcommands, examples |
| CLH02 | same content in `notui` and full builds | compare stripped text | identical |
| CLH03 | `NO_COLOR` and non-terminal | | no escapes |
| CLH04 | width adapts | 40, 80, 200 | no line over the width |
| CLH05 | `help <subcommand>` | each subcommand | documented |
| CLH06 | unknown subcommand | `nova doctr` | exit 2; suggests `doctor` |
| CLH07 | no suggestion when nothing is close | `nova zzzz` | exit 2; no suggestion |
| VER01 | version line | stamped build | all fields |
| VER02 | `--json` | | same fields as JSON |
| VER03 | plain `go build` | | `dev` and the VCS revision when available |
| VER04 | flavour | both tags | `full` and `min` |
| FRS01 | no key starts the flow | terminal | welcome, login step |
| FRS02 | OpenRouter login success | fake OAuth server | key saved; model step |
| FRS03 | paste a key | | masked input; saved with mode 0600 |
| FRS04 | paste an empty or whitespace key | | rejected |
| FRS05 | login failure | | error shown; retry or skip |
| FRS06 | skip a step with `esc` | | app starts; first prompt reports the missing key |
| FRS07 | model step | | catalog picker; default highlighted; saved via `SetUser` |
| FRS08 | not run with a key | | skipped |
| FRS09 | not run in print, json, rpc, serve | | message and exit 1 |
| FRS10 | not run without a terminal | | skipped |
| FRS11 | the key never appears in logs, config, or the screen | | verified |
| FRS12 | the flow runs again only when the key is missing | | yes |
| CFW01 | set a new key in a new file | | created with mode 0600; directory 0700 |
| CFW02 | change an existing key | | only that key changes |
| CFW03 | comments and order preserved | | byte-identical elsewhere |
| CFW04 | nested dotted key | `tools.bash.timeout_seconds` | intermediate maps created |
| CFW05 | unknown key | | error; nothing written |
| CFW06 | corrupt file | | error; file untouched |
| CFW07 | atomic write | failed rename | original intact |
| CFW08 | value types | int, bool, string, list | written correctly |
| CFW09 | concurrent calls | `-race` | file valid |
| CFW10 | secrets refused | key names containing `key` or `token` | error |
| DRX01 | all ok | healthy fixture | exit 0; every check ok |
| DRX02 | missing key | | fail; the fix text; value never printed |
| DRX03 | key source reported | config, env, store | each |
| DRX04 | network timeout | stalled server | warn |
| DRX05 | `--offline` | | network check skipped |
| DRX06 | git and bash missing | `PATH` emptied | warn; fail |
| DRX07 | terminal checks | `TERM=dumb`, `COLORTERM` unset, non-UTF-8 locale | warn each |
| DRX08 | config warnings | invalid key | warn with details |
| DRX09 | unwritable directories | read-only home | fail naming the directory |
| DRX10 | trust status | | shown |
| DRX11 | extension checks | one valid, one broken | ok; fail |
| DRX12 | project context | context files and skills | listed |
| DRX13 | exit codes | warn only; one fail | 0; 1 |
| DRX14 | `--json` | | valid JSON with every check |
| DRX15 | never modifies anything | filesystem snapshot before and after | identical |
| DRX16 | never prints a secret | all outputs | the key and tokens absent |
| DRX17 | bounded runtime | everything stalls | finishes within 30 s (fake clock) |
| DOC01 | topic list | | embedded topics |
| DOC02 | print a topic | `extensions` | the document |
| DOC03 | unknown topic | | lists topics |
| DOC04 | search ranking | a title match and a body match | title match first |
| DOC05 | top 5 with excerpts | | at most 5 |
| DOC06 | no results | | message |
| DOC07 | piped output | | no colour |
| DOC08 | embedded set | | includes the examples and the SDK documentation |
| DOC09 | authoring context size | | under 40,000 estimated tokens |
| SKA01 | skill embedded and listed | | `extension-author`, `Source: builtin` |
| SKA02 | lowest precedence | user skill of the same name | the user's wins |
| SKA03 | body contains the workflow steps | | each step present |
| SKA04 | body size | | under 1,500 estimated tokens |
| SKA05 | in the system prompt as name and description only | | no body |
| EXM01 | ten examples exist | | the ten names |
| EXM02 | each under 100 lines of code | | verified |
| EXM03 | each has a README | | present |
| EXM04 | each passes `ext.Check` | | no problems |
| EXM05 | `hello` and `word-count` run | scenario against the binary | tool answers |
| EXM06 | `block-rm` blocks | model calls `rm -rf` | blocked with the reason |
| EXM07 | `path-guard` blocks outside writes | | blocked |
| EXM08 | `status-segment` and `clock-widget` | | frames sent |
| EXM09 | `ask-demo` | | form opens and returns |
| EXM10 | `session-notes` replay | restart | notes restored |
| EXM11 | `summarize` | | `Complete` used; usage counted |
| EXM12 | `hello-go` builds with the SDK | | builds |
| FAV01 | SVG written | | valid SVG; transparent background |
| FAV02 | PNG sizes | 16, 32, 180, 512 | files with those dimensions |
| FAV03 | star and one warm cell | decode the 16 px PNG | a warm pixel present; star centre bright |
| FAV04 | deterministic | twice | identical bytes |
| FAV05 | 16 px legibility | the star's arms are at least 2 pixels long | true |
| REL01 | workflow structure | parse the YAML | triggers on `v*`; matrix linux and darwin by amd64 and arm64; both flavours |
| REL02 | acceptance gate | | runs accept for all specs before publishing |
| REL03 | flags | | `CGO_ENABLED=0`, `-trimpath`, `-s -w`, the version variables |
| REL04 | archive names and contents | dry run of the packaging step | as specified |
| REL05 | checksums file | | one line per archive |
| ISH01 | happy path | fake release server | binary installed 0755 in the default directory |
| ISH02 | checksum mismatch | tampered archive | aborts before extraction; nothing installed |
| ISH03 | unsupported OS or architecture | | clear error naming it |
| ISH04 | `NOVA_VERSION` | | that version |
| ISH05 | `NOVA_FLAVOUR=min` | | the min archive |
| ISH06 | `NOVA_INSTALL_DIR` | | used; created if missing |
| ISH07 | PATH hint | directory not on `PATH` | printed |
| ISH08 | idempotent upgrade | run twice with a newer version | upgraded in place; the old binary kept until verified |
| ISH09 | no `sudo` | | never invoked |
| ISH10 | temporary directory cleaned | every exit path | removed |
| ISH11 | download failure | server returns 500 | error; nothing changed |
| ISH12 | only the sanity check runs downloaded code | | `nova version` after install |
| ISH13 | `sh` compatibility | run with `dash` | works |

## Fuzz and property tests

| Name | Asserts |
|---|---|
| `FuzzSetUser` | arbitrary dotted keys and values never panic and never leave a partially written file |
| `FuzzDocsSearch` | arbitrary query text never panics; results are at most 5 |
| `TestProperty_DRX_NoSecrets` | for random environments containing key-like values, no output contains them |

## Out of scope

- Windows builds, package managers (Homebrew, apt), auto-update, telemetry, signed binaries (checksums only for the first release).
- A GUI installer. Translating the docs.

## Acceptance

```
go run ./scripts/accept C19
go run ./assets/logo/gen -favicon && git diff --exit-code assets/logo
```
