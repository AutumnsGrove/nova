---
chunk: C23
title: "The update command: refresh from a checkout or the latest release"
packages: [./update, ./cmd/nova]
budgets: { ./update: 450, ./cmd/nova: 1300 }
depends: [C19]
---

# C23 — The update command

## Goal

`nova update` replaces the running binary. When it runs inside a Nova source checkout it
rebuilds from that checkout, so a developer picks up their own changes with one command;
anywhere else it installs the latest GitHub release with the same checksum verification that
`install.sh` uses. The mode is detected, and `--source`, `--release`, `--check`, and
`--flavour` let the user override it.

## Read first

- `install.sh` and `cmd/nova/release_test.go` (C19) — the release archive layout, the
  checksum file, and the fake release server the tests reuse.
- `cmd/nova/version.go`, `cmd/nova/exec_min.go`, `cmd/nova/exec_tui.go` — the build metadata
  and the `full`/`min` flavour.
- `docs/TESTING.md` sections 3, 4, and 6.
- `scripts/importcheck` — `update` is a new top-level package and may import only the
  standard library.

## Files

| File | Create or change | Purpose |
|---|---|---|
| `update/update.go` | create | `Options`, `Result`, `Run`, `Detect`, mode selection |
| `update/release.go` | create | resolve, download, verify, and extract a release |
| `update/source.go` | create | build from a checkout with the local Go toolchain |
| `update/install.go` | create | stage and atomically replace the target binary |
| `update/update_test.go`, `update/install_test.go` | create | the package-level matrix rows |
| `cmd/nova/update.go` | create | the `nova update` cobra command |
| `cmd/nova/update_test.go` | create | the command rows |
| `cmd/nova/main.go` | change | register `updateCommand` |
| `cmd/nova/help.go` | change | group `update` under Setup and mention it in the examples |
| `cmd/nova/help_c19_test.go` | change | the help-token list gains `update` |

`update` is a new top-level package. It is not in the layer table, so the layer rule does
not constrain it, and it imports only the standard library (no third-party module, no other
Nova package). The command reads the environment and the executable path; the package never
touches `os.Getenv` or `os.Executable` itself, so every behaviour below is testable with a
temp directory, a local `httptest` server, and a fake command runner.

## Interfaces

```go
// Package update replaces the running nova binary from a source checkout or
// from a GitHub release.
package update

// Mode says where a new binary comes from.
type Mode int

const (
	Auto Mode = iota // a checkout at Dir if there is one, else a release
	Source
	Release
)

// ExecFunc runs a command and returns its combined output. It is a field on
// Options so tests never need the real go toolchain.
type ExecFunc func(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error)

// Options configures one run. Zero values are filled with sensible defaults.
type Options struct {
	Mode    Mode
	Dir     string // source checkout; empty means the current directory
	Version string // release tag; empty means the latest
	Flavour string // "full" or "min"
	Target  string // the binary to replace
	Check   bool   // report only; never write
	Force   bool   // reinstall even when the version is unchanged

	BaseURL  string             // release base; default https://github.com/AutumnsGrove/nova
	APIURL   string             // release API; default <BaseURL>/releases/latest
	GOOS     string             // default runtime.GOOS
	GOARCH   string             // default runtime.GOARCH
	HTTP     *http.Client       // release downloads
	Exec     ExecFunc           // build and sanity commands
	LookPath func(string) (string, error)
	Env      []string           // build environment; default os.Environ()
	Out      io.Writer          // progress and messages
	Current  string             // the running version, for --check
}

// Result describes what happened.
type Result struct {
	Mode    Mode
	From    string
	To      string
	Changed bool
	Message string
}

// Run performs the update described by opt.
func Run(ctx context.Context, opt Options) (Result, error)

// Detect finds a Nova source checkout at or above dir. It returns the module
// root and true, or "" and false.
func Detect(dir string) (string, bool)
```

The command is a thin consumer:

```go
// cmd/nova
type updateFunc func(context.Context, update.Options) (update.Result, error)

func updateCommand(stderr io.Writer, exit *int, run updateFunc) *cobra.Command
```

## Behaviour

1. `Detect` walks up from `dir`. A directory is a Nova checkout when it holds a `go.mod`
   whose module line is exactly `module github.com/AumnsGrove/nova` **and** a `cmd/nova`
   directory. It returns the nearest such ancestor, or `("", false)`.
2. `Mode` `Auto` uses `Detect` on `Dir` (or the current directory when `Dir` is empty):
   a checkout means `Source` with that root, otherwise `Release`. `Mode` `Source` requires a
   checkout and errors when there is none. `Mode` `Release` never looks at `Dir`.
3. `Flavour` is `full` (the default) or `min`; anything else is an error. `min` selects
   `-tags notui` for a source build and the `nova-min` archive for a release.
4. A release resolves its version: `Version` when set; otherwise a `GET` of `APIURL` whose
   JSON `tag_name` is used. A non-200 response or a body without `tag_name` is an error. A
   pinned `Version` makes no API request.
5. A release validates the platform: only `linux` and `darwin` on `amd64` and `arm64` are
   supported; any other pair is an error naming it, before any download.
6. The archive is `nova_<version>_<goos>_<goarch>.tar.gz` for `full` and
   `nova-min_<version>_<goos>_<goarch>.tar.gz` for `min`. The archive and `checksums.txt`
   are fetched from `<BaseURL>/releases/download/<version>/`.
7. `checksums.txt` holds `<hex>  <name>` lines. The archive's entry must exist and equal
   the SHA-256 of the downloaded bytes. A mismatch or a missing entry is an error, and
   nothing is extracted or installed.
8. Only the archive member whose cleaned path is exactly `nova` (or `nova-min`) is staged;
   every other member is ignored. An archive with no such member is an error. A member such
   as `../nova` is not the exact path and is never written.
9. A staged binary is run with `version` before it is installed; a non-zero exit or an
   execution error is an error, and the target is untouched. This is the only code the
   update ever executes from a release.
10. Installing stages the new binary in the target's directory and then renames it over the
    target, so the replace is atomic on one filesystem, the new file is mode 0755, and a
    failure at any earlier step leaves the old binary in place.
11. A source build runs `go build -o <staged> [-tags notui] ./cmd/nova` in the checkout with
    `CGO_ENABLED=0` in the environment. `go` must be found by `LookPath`; missing `go` or a
    failed build is an error and the target is untouched. The staged binary is sanity-checked
    like a release and then installed by rule 10.
12. `Check` reports only: it resolves what would happen and writes no file. `Result.Changed`
    is false.
13. When the resolved release version already equals `Current` and `Force` is false, the run
    reports that it is up to date and writes nothing. `Force` reinstalls the same version.
14. `Result.Message` is one line: the source root that was rebuilt, the old and new release
    versions, that the release is current, or, for `Check` in a checkout, the root and how to
    rebuild.
15. The command maps flags and environment to `Options`: `--source <dir>` forces `Source`,
    `--release` forces `Release`, a positional argument is a release tag, `--check` sets
    `Check`, `--force` sets `Force`, and `--flavour` sets `Flavour` (default
    `buildFlavour()`). `NOVA_RELEASE_BASE`, `NOVA_API_URL`, `NOVA_VERSION`, and
    `NOVA_FLAVOUR` override the corresponding fields when the flag is not set. `Target` is
    `os.Executable()`. A failure prints `nova update: <error>` to stderr and exits 1; success
    exits 0.

## Matrix

| ID | Case | Input / setup | Expected |
|---|---|---|---|
| UPD01 | detect a checkout | `go.mod` module line plus `cmd/nova` | the module root |
| UPD02 | plain directory | no `go.mod` | `false` |
| UPD03 | nested directory | a subdirectory of a checkout | the module root |
| UPD04 | another module | `go.mod` with a different module | `false` |
| UPD05 | force source | `Mode` `Source` at a checkout | a source build |
| UPD06 | force release inside a checkout | `Mode` `Release` | a release, no build |
| UPD07 | resolve the latest release | API returns `v0.2.0` | that tag |
| UPD08 | pinned version | `Version` set | no API request |
| UPD09 | archive name | full and min, os, arch | the two names |
| UPD10 | checksum mismatch | tampered archive bytes | error; target unchanged |
| UPD11 | missing checksum entry | `checksums.txt` without the archive | error; target unchanged |
| UPD12 | download failure | server returns 500 | error; target unchanged |
| UPD13 | extra tar members | `LICENSE` beside `nova` | only the binary is installed |
| UPD14 | path traversal member | a member named `../nova` | error; nothing written outside |
| UPD15 | a release replaces the target | valid release | new contents; mode 0755 |
| UPD16 | already current | `Current` equals the tag, no `Force` | no write; up to date |
| UPD17 | force the same version | `Current` equals the tag, `Force` | the target is replaced |
| UPD18 | check writes nothing | `Check` | no write; `Changed` false |
| UPD19 | unsupported platform | `GOOS=windows` | error naming the pair |
| UPD20 | sanity check fails | the staged `version` fails | error; target unchanged |
| UPD21 | source build command | a checkout | `go build -o … ./cmd/nova` with `CGO_ENABLED=0` in the checkout |
| UPD22 | min flavour source build | `Flavour=min` | `-tags notui` present |
| UPD23 | go is missing | `LookPath` fails | error naming `go` |
| UPD24 | build failure | `Exec` returns an error | error; target unchanged |
| UPD25 | a source build replaces the target | a checkout | new contents; mode 0755 |
| UPD26 | source without a checkout | `Mode` `Source`, plain dir | error; target unchanged |
| UPD27 | command help | `nova update --help` | documents the flags |
| UPD28 | command exit codes | run error, then success | 1, then 0 |
| UPD29 | flags and environment map to Options | `--check --source X --flavour min` and `NOVA_*` | each field set |

## Fuzz and property tests

| Name | Asserts |
|---|---|
| `FuzzDetect` | arbitrary directory names never panic; a result, when true, names an ancestor |
| `FuzzChecksums` | arbitrary `checksums.txt` bytes never panic and never match a random digest |

## Out of scope

- Windows and any architecture outside the release matrix; no `.zip`, no `.exe`.
- A background update check, an update notification, or updating while the TUI runs.
- Downgrades and version pinning beyond a single `--release <tag>` argument.
- `git pull`; a source build uses the checkout as it is.
- Changing `install.sh`; it keeps its shell implementation, and the two share only the
  release layout and the environment variable names.
- Any secret handling: the update is public, unauthenticated, and sends no key.

## Acceptance

```
go run ./scripts/accept C23
```
