---
chunk: C11
title: Config, context files, skills, prompt templates, trust
packages: [./config]
budgets: { ./config: 900 }
depends: [C04]
---

# C11 — Configuration and resources

## Goal

Load Nova's settings and the resources that shape a session (project instructions,
skills, prompt templates) with a clear merge order, a trust gate for project-level
files, and warnings, not crashes, for anything malformed.

## Read first

- `docs/ARCHITECTURE.md` sections 7 (permissions YAML) and 9 (configuration and resources).
- `docs/DECISIONS.md` D32 and D33.
- `permission/policy.go` (C04): the `Policy` type the config produces.
- `docs/TESTING.md` section 3, "Configuration".

## Files

| File | Purpose |
|---|---|
| `config/config.go` | the `Config` struct, defaults, `Load`, merge, env overrides |
| `config/warn.go` | unknown-key detection |
| `config/trust.go` | the trust store |
| `config/context.go` | `AGENTS.md` and `CLAUDE.md` discovery |
| `config/skills.go` | skill discovery and parsing |
| `config/templates.go` | prompt templates and argument expansion |
| `config/prompt.go` | system prompt assembly |
| `config/paths.go` | standard directories |
| `config/*_test.go` | |

The only dependency is `gopkg.in/yaml.v3`.

## Interfaces

```go
type Config struct {
    Model        string                 `yaml:"model"`
    Thinking     string                 `yaml:"thinking"`      // off | low | medium | high
    ContextLimit int                    `yaml:"context_limit"`
    MaxTurns     int                    `yaml:"max_turns"`
    Provider     ProviderConfig         `yaml:"provider"`
    Permissions  permission.Policy      `yaml:"permissions"`
    Tools        ToolsConfig            `yaml:"tools"`
    Extensions   ExtensionsConfig       `yaml:"extensions"`
    Hooks        []HookConfig           `yaml:"hooks"`
    Compaction   CompactionConfig       `yaml:"compaction"`
    Retry        RetryConfig            `yaml:"retry"`
    Theme        string                 `yaml:"theme"`
    Keybindings  map[string]string      `yaml:"keybindings"`
    SystemPrompt string                 `yaml:"system_prompt"` // text appended to the base prompt
}
type ProviderConfig struct {
    OpenRouter struct {
        Routing map[string]any `yaml:"routing"` // passed through; typed helper in provider/openrouter
        BaseURL string         `yaml:"base_url"`
    } `yaml:"openrouter"`
}
type ToolsConfig struct {
    Active []string `yaml:"active"` // empty means the default set
    Bash   struct{ TimeoutSeconds, MaxOutputBytes int } `yaml:"bash"`
}
type ExtensionsConfig struct {
    Disable           []string `yaml:"disable"`
    Dirs              []string `yaml:"dirs"`
    ScrubEnv          []string `yaml:"scrub_env"`            // extra variables hidden from extension processes
    PassEnv           []string `yaml:"pass_env"`             // variables explicitly passed through
    CompleteMaxPerRun int      `yaml:"complete_max_per_run"` // default 20
}
type HookConfig struct {
    On    string            `yaml:"on"`
    Match map[string]string `yaml:"match"`
    Run   []string          `yaml:"run"`
}
type CompactionConfig struct {
    Enabled    *bool `yaml:"enabled"`
    Reserve    int   `yaml:"reserve"`
    KeepRecent int   `yaml:"keep_recent"`
}
type RetryConfig struct{ Max int; BaseMS, CapMS int }

type Warning struct{ Source, Message string }

type LoadOptions struct {
    CWD         string
    Home        string                 // for the user config; default from os
    Env         func(string) string    // default os.Getenv
    Flags       map[string]any         // dotted keys, e.g. "model": "x", "bash.timeout_seconds": 30
    Trusted     func(dir string) bool  // nil means "not trusted"
    ReadFile    func(string) ([]byte, error) // default os.ReadFile (tests inject)
}
func Load(opt LoadOptions) (*Config, []Warning, error)
func Defaults() Config

// trust
type TrustStore interface {
    IsTrusted(dir string) bool
    Trust(dir string) error
    Revoke(dir string) error
}
func NewTrustStore(path string) TrustStore

// resources
type ContextFile struct{ Path, Content string; Truncated bool }
func LoadContext(cwd, home string, opt ContextOptions) ([]ContextFile, []Warning)
type ContextOptions struct{ Names []string; MaxFileBytes, MaxTotalBytes int; ReadFile func(string) ([]byte, error) }

type Skill struct{ Name, Description, Dir, Body string; Source string }
func LoadSkills(dirs []string) ([]Skill, []Warning)

type Template struct{ Name, Description, Body, Path string }
func LoadTemplates(dirs []string) ([]Template, []Warning)
func (t Template) Expand(args string) string
func SplitArgs(s string) ([]string, error)

func BuildSystemPrompt(in PromptInput) string
type PromptInput struct {
    Base     string
    Context  []ContextFile
    Skills   []Skill
    Tools    []tool.Info
    Extra    string
    CWD      string
    Date     time.Time
}
```

## Behaviour

### Merge order and loading

1. Precedence, lowest to highest: **defaults** → **user config** (`<home>/.config/nova/config.yaml`)
   → **project config** (`<cwd>/.nova/config.yaml`, found by walking up to the nearest
   directory containing `.nova/` or `.git`) → **environment** → **flags**.
2. The project config is read **only if `Trusted(projectDir)` is true**. If it exists but the
   project is not trusted, a warning says so and it is skipped. Same for project extensions
   directories and project hooks.
3. Each layer is decoded into a generic map, the maps are merged (deep for maps, **replace**
   for lists and scalars), and the merged map is decoded into `Config` once. An explicitly written
   zero or `false` therefore overrides a higher-precedence default. (This is why no field needs a
   pointer to distinguish "unset" from "zero"; `Compaction.Enabled` is a pointer only so the
   *default* can be true.)
4. Environment: `NOVA_MODEL`, `NOVA_THINKING`, `NOVA_CONTEXT_LIMIT`, `NOVA_MAX_TURNS`, `NOVA_THEME`.
   An empty or whitespace-only variable is ignored. A value that does not parse (for example a
   non-numeric `NOVA_MAX_TURNS`) is skipped with a warning.
5. Flags are a dotted-key map applied last. An unknown flag key is a warning.
6. A missing config file is not an error. An unreadable file or invalid YAML is a **warning**
   naming the file and line, and that layer is skipped; loading continues. `Load` returns an
   error only for a programmer mistake (an invalid `LoadOptions`).
7. **Unknown keys** are a warning naming the dotted path, never an error. (Detect by decoding
   again with known-fields checking and reporting each unknown key.)
8. After merging, normalisation: `~` expands to the home directory in path-like values;
   `thinking` must be one of the allowed values or it becomes `""` with a warning;
   negative numbers become zero with a warning; `Permissions.Validate()` is called and its
   error becomes a warning, replacing the policy with the default policy.
9. JSON files named `config.json` are accepted in the same places if no YAML exists there.

### Trust

10. `NewTrustStore(path)` keeps a JSON list of trusted absolute, cleaned directories. A directory
    is trusted if it, or any ancestor, is in the list. `Trust` writes atomically (temp file, rename,
    mode 0600). A corrupt file is treated as empty and rewritten on the next `Trust`. `Revoke` of an
    unknown directory is not an error. Paths are compared after `filepath.EvalSymlinks` so a
    symlink cannot borrow a trusted path.
11. The home directory itself being trusted does **not** trust every project under it unless the
    user added it explicitly (ancestor matching applies to explicit entries only, and `Trust`
    refuses to add `/` or the home directory without an `allowBroad` confirmation in the caller).
    Implement as: `Trust` returns `ErrTooBroad` for `/` and for the home directory.

### Context files

12. `LoadContext` looks for each name (default `AGENTS.md`, `CLAUDE.md`) in `<home>/.config/nova/`,
    then in every directory from the outermost ancestor of `cwd` (stopping at `home`'s parent or
    the filesystem root) down to `cwd`. Output order is global first, then outermost to innermost.
13. A file larger than `MaxFileBytes` (default 64 KiB) is cut, with `Truncated: true` and a trailing
    marker; the total is capped at `MaxTotalBytes` (default 256 KiB), dropping the **outermost**
    files first and warning. A binary file (NUL in the first 8 KiB), an unreadable file, or an empty
    file is skipped (the first two with a warning). A directory named `AGENTS.md` is skipped.
14. The same real file reached through two paths (symlinks) is included once.
15. Context files are only read from the project tree if the project is trusted **or** they are
    in the user's home config; for an untrusted project, only the global file is loaded and a
    warning notes that project instructions were skipped. (Context files are instructions to the
    model, so they are subject to the same trust rule as project config.)

### Skills

16. A skill is a directory containing `SKILL.md`. The file has YAML front matter with `name` and
    `description`, then a Markdown body. `name` must match `^[a-z0-9]+(-[a-z0-9]+)*$`, at most 64
    characters, and equal the directory name; `description` is required, at most 1,024 characters.
17. A skill that fails these rules is skipped with a warning naming the path and the rule.
    A duplicate name keeps the **first** found, scanning `dirs` in the given order (callers pass
    project before user), with a warning.
18. `Body` is not injected into the system prompt; only `name` and `description` are (see
    `BuildSystemPrompt`). A skill file over 256 KiB is skipped with a warning.
19. Skill directories are searched one level deep and in `dirs` as given; hidden directories and
    non-directories are ignored; a symlinked skill directory is followed once.

### Prompt templates

20. A template is `<name>.md` in a templates directory. Optional YAML front matter may give
    `description`. The name is the file name without the extension and must match
    `^[a-z0-9][a-z0-9-_]*$`. A duplicate keeps the first, with a warning.
21. `Expand(args)` first splits `args` with `SplitArgs` (shell-like: whitespace separated, single
    and double quotes group, backslash escapes inside double quotes and outside quotes; an unclosed
    quote is an error and then the whole string is treated as one argument). Then it substitutes in
    the body: `$1`...`$9` (the nth argument, empty if absent), `$@` and `$ARGUMENTS` (all arguments
    joined by single spaces), and `\$` as a literal `$`. Unknown `$x` sequences stay unchanged.
    Substitution is single-pass: the inserted text is not itself expanded.

### System prompt

22. `BuildSystemPrompt` produces, in order: the base prompt; a block describing the working
    directory and the date; a block of context files (each under a heading with its path); a block
    listing each skill as `- <name>: <description>` with the rule that the model should read the
    skill's `SKILL.md` when relevant; each tool's one-line description and any prompt guideline;
    then `Extra`. Missing parts are omitted entirely (no empty headings). The output is
    deterministic for the same input.

## Matrix

| ID | Case | Input / setup | Expected |
|---|---|---|---|
| CFG01 | defaults only | no files | `Defaults()` |
| CFG02 | user overrides defaults | `model: a` | `a` |
| CFG03 | project overrides user (trusted) | | project value |
| CFG04 | project config skipped when untrusted | | user value; a warning says why |
| CFG05 | env overrides files | `NOVA_MODEL` | env value |
| CFG06 | flags override env | | flag value |
| CFG07 | deep merge of maps | user routing `{a:1}`, project `{b:2}` | both keys |
| CFG08 | lists replaced | user `active:[a,b]`, project `[c]` | `[c]` |
| CFG09 | explicit false and zero override | `compaction.enabled: false`, `max_turns: 0` over non-zero lower layers | false and 0 |
| CFG10 | missing files | | no error, no warning |
| CFG11 | invalid YAML | `a: [` | warning naming file and line; layer skipped |
| CFG12 | unreadable file | | warning; continues |
| CFG13 | unknown key | `modle: x` | warning naming `modle` |
| CFG14 | unknown nested key | `tools.bash.nope` | warning with the dotted path |
| CFG15 | wrong type | `max_turns: "ten"` | warning; default kept |
| CFG16 | env empty or whitespace | `NOVA_MODEL=" "` | ignored |
| CFG17 | env not parseable | `NOVA_MAX_TURNS=abc` | warning; ignored |
| CFG18 | unknown flag key | | warning |
| CFG19 | `~` expansion | `extensions.dirs: ["~/x"]` | home-relative |
| CFG20 | invalid `thinking` | `extreme` | `""` and a warning |
| CFG21 | negative numbers | | zero and a warning |
| CFG22 | invalid permissions | `headless: ask` | warning; default policy |
| CFG23 | `config.json` accepted | no YAML | loaded |
| CFG24 | YAML wins over JSON in one directory | both | YAML |
| CFG25 | project root detection | `.git` two levels up | that directory's `.nova/config.yaml` |
| CFG26 | no project root | | no project layer; no error |
| CFG27 | merge is stable and deterministic | load 100 times | identical |
| CFG28 | very large config | 5 MiB YAML | loads or warns; no panic |
| CFG29 | YAML anchors and aliases | | work; "billion laughs" input bounded (error or warning, no hang) |
| CFG30 | duplicate keys | `a: 1` twice | warning |
| TRU01 | untrusted by default | | false |
| TRU02 | trust then check | | true |
| TRU03 | ancestor trusted | trust `/a`, check `/a/b/c` | true |
| TRU04 | sibling not trusted | trust `/a/b`, check `/a/c` | false |
| TRU05 | symlink cannot borrow trust | link to a trusted dir | follows the real path; trusted only if the real path is |
| TRU06 | revoke | | false afterwards; unknown dir not an error |
| TRU07 | corrupt file | not JSON | treated as empty; next `Trust` rewrites it |
| TRU08 | file mode and atomic write | | 0600; failed rename leaves the old file |
| TRU09 | too broad | `/` and the home directory | `ErrTooBroad` |
| TRU10 | relative path | | cleaned to absolute |
| TRU11 | concurrent trusts | `-race` | file valid |
| CTF01 | global then outer to inner | files in home config, `/a`, `/a/b` | that order |
| CTF02 | both names present | `AGENTS.md` and `CLAUDE.md` | both, in the configured name order |
| CTF03 | file over 64 KiB | | cut; `Truncated`; marker |
| CTF04 | total over 256 KiB | | outermost dropped first; warning |
| CTF05 | binary file | | skipped with a warning |
| CTF06 | empty file | | skipped silently |
| CTF07 | unreadable file | | skipped with a warning |
| CTF08 | a directory named like a context file | | skipped |
| CTF09 | symlink to the same file reached twice | | once |
| CTF10 | untrusted project | | only the global file; a warning |
| CTF11 | cwd has no context files | | empty list; no warnings |
| CTF12 | stops at the filesystem root and does not loop | cwd `/` | terminates |
| CTF13 | BOM and CRLF | | content normalised: BOM removed; CRLF kept as is |
| SKL01 | valid skill | | loaded with name and description |
| SKL02 | name does not match the directory | | skipped; warning |
| SKL03 | bad name characters, uppercase, too long | | skipped; warning |
| SKL04 | missing description | | skipped; warning |
| SKL05 | description over 1,024 characters | | skipped; warning |
| SKL06 | no front matter | | skipped; warning |
| SKL07 | malformed front matter | | skipped; warning |
| SKL08 | duplicate across directories | project and user | first kept; warning |
| SKL09 | file over 256 KiB | | skipped; warning |
| SKL10 | hidden directory and stray files | | ignored |
| SKL11 | symlinked skill directory | | followed once |
| SKL12 | `Body` loaded but not placed in the prompt | | `BuildSystemPrompt` omits the body |
| TPL01 | template loaded | | name from the file name |
| TPL02 | front matter description | | read |
| TPL03 | bad name | `Foo.md` | skipped; warning |
| TPL04 | duplicate | | first kept; warning |
| TPL05 | `$1` `$2` | | substituted |
| TPL06 | missing positional | `$3` with two args | empty |
| TPL07 | `$@` and `$ARGUMENTS` | | all args joined by spaces |
| TPL08 | `\$` literal | | `$` |
| TPL09 | unknown `$x` | | unchanged |
| TPL10 | single pass | an argument containing `$1` | not re-expanded |
| TPL11 | `SplitArgs` quotes | `a "b c" 'd e' f\ g` | `a`, `b c`, `d e`, `f g` |
| TPL12 | unclosed quote | `a "b` | error; `Expand` treats the whole string as one argument |
| TPL13 | empty args | | no arguments; `$@` empty |
| TPL14 | arguments with newlines and Unicode | | preserved |
| TPL15 | very long body | 1 MiB | works |
| SYS01 | full prompt order | all parts | base, environment, context, skills, tools, extra |
| SYS02 | empty parts omitted | no skills | no skills heading |
| SYS03 | deterministic | same input twice | identical output |
| SYS04 | context files shown with paths | | each under its path |
| SYS05 | skill list has no bodies | | only name and description |
| SYS06 | date and cwd | | present |

## Fuzz and property tests

| Name | Asserts |
|---|---|
| `FuzzLoadYAML` | arbitrary bytes as a config file never panic or hang; the result is a config plus warnings |
| `FuzzSplitArgs` | never panics; joining the result and splitting again is stable |
| `FuzzExpand` | never panics; output length is bounded by body length plus the sum of argument lengths times the number of placeholders |
| `TestProperty_CFG_MergeAssociative` | merging layers A, B, C in order equals merging (A, B) then C |

## Out of scope

- Reading `models.json` or a model catalog from config (the catalog is code, C03).
- Theme and keybinding application (TUI chunks). Extension and hook execution (C13).
- A `nova config` command (C12).

## Acceptance

```
go run ./scripts/accept C11
```
