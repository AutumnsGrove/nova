---
chunk: C04
title: "Tool contract, registry, permission policy"
packages: [./tool, ./permission]
budgets: { ./tool: 600, ./permission: 300 }
depends: [C01]
---

# C04 — Tools and permissions

## Goal

Harden the tool contract and registry (argument validation, panic safety, filtered
views) and add the permission policy that decides whether a call runs, asks, or is
denied. Both are small and security-relevant, so they are specified tightly.

## Read first

- `tool/tool.go`, `tool/registry.go`, `tool/registry_test.go` (the prototype).
- `docs/ARCHITECTURE.md` sections 3 (Tool) and 7 (Permissions).

## Files

| File | Change | Purpose |
|---|---|---|
| `tool/tool.go` | change | `Result`, `Context`, `Update` additions |
| `tool/registry.go` | change | validation, recovery, views, unregister |
| `tool/schema.go` | create | a small JSON Schema subset validator |
| `tool/*_test.go` | create or change | |
| `permission/policy.go` | create | `Policy`, `Rule`, `Decide` |
| `permission/glob.go` | create | path globs and string globs |
| `permission/shell.go` | create | shell control-operator detection |
| `permission/*_test.go` | create | |

## Interfaces

```go
// package tool
type Result struct {
    Content   string
    IsError   bool
    Truncated bool
    Details   json.RawMessage // structured data for renderers; never sent to the model
}
type Update struct{ Text string }

type Context struct {
    WorkDir   string
    Roots     []string // if non-empty, file tools must stay inside these directories
    Data      any
    SessionID string
    Emit      func(Update) // nil-safe: use tctx.Progress(...)
    Complete  func(ctx context.Context, req provider.Request) (*provider.Response, error)
}
func (c *Context) Progress(text string) // calls Emit if set

type Registry struct{ /* ... */ }
func (r *Registry) Unregister(name string) bool
func (r *Registry) Replace(t Tool) error      // registers or replaces
func (r *Registry) View(names ...string) *Registry // a registry holding only these tools; unknown names ignored
func (r *Registry) Info(name string) (Info, bool)
func (r *Registry) Execute(ctx context.Context, name string, args json.RawMessage, tctx *Context) Result

// ValidateArgs checks args (raw JSON) against a JSON Schema subset. The error text is
// meant for the model: "edits[1].oldText: required property is missing".
func ValidateArgs(schema map[string]any, args json.RawMessage) error
```

```go
// package permission
type Action string
const (Allow Action = "allow"; Ask Action = "ask"; Deny Action = "deny")

type Rule struct {
    Tool   string            // glob over tool names; "*" matches all
    Match  map[string]string // argument name -> glob over the argument's string value
    Paths  []string          // path globs; match any path-like argument
    Action Action
}
type Policy struct {
    Default  Action // "" means: annotation-based defaults apply
    Headless Action // what Ask becomes with no one to ask; default Deny
    Rules    []Rule
}
type Call struct {
    Tool    string
    Args    json.RawMessage
    Info    tool.Info
    WorkDir string
}
type Decision struct {
    Action Action
    Rule   int    // index of the deciding rule, -1 if none
    Reason string // human-readable, includes the rule
}
func (p *Policy) Validate() error
func (p *Policy) Decide(c Call, interactive bool) Decision

func MatchPath(pattern, path string) bool   // "**" crosses "/", "*" and "?" do not
func MatchString(pattern, s string) bool    // "*" matches any run of characters including "/"
```

## Behaviour

### Registry and tools

1. `Execute` on an unknown tool returns an error result naming the tool and listing up to
   ten registered names (so the model can correct itself).
2. `Execute` validates `args` against `Info.Parameters` with `ValidateArgs` **before**
   running. A failure returns an error result `invalid arguments: <message>`; the tool is
   not called. Empty or `null` args are treated as `{}`. Args that are not a JSON object
   are an error.
3. `Execute` returns an error result `cancelled` if `ctx` is already cancelled, without
   running the tool.
4. `Execute` **recovers panics** in the tool and returns an error result
   `internal error in <tool>: <panic value>`. This holds for both sequential and parallel
   callers (the agent loop no longer needs its own recover).
5. A tool that returns a Go error produces `error: <tool>: <err>` as before.
6. `Execute` is safe for concurrent use. `Register`, `Replace`, and `Unregister` may run
   concurrently with `Execute`.
7. `Names` and `Defs` keep registration order; `Replace` keeps the original position;
   `Unregister` removes it.
8. `View` shares the underlying tools but not the registry state: registering on the view
   does not affect the parent.
9. `Context.Progress` is a no-op when `Emit` is nil.

### ValidateArgs (JSON Schema subset)

10. Supported keywords: `type` (`object`, `array`, `string`, `integer`, `number`, `boolean`,
    `null`, or an array of these), `properties`, `required`, `additionalProperties` (bool),
    `enum`, `items`, `minimum`, `maximum`, `minLength`, `maxLength`, `minItems`,
    `maxItems`. Other keywords are ignored, never an error.
11. `integer` accepts a JSON number with no fractional part (`3` and `3.0`), not `3.5` or
    a numeric string. `number` accepts any JSON number. Numbers are compared without losing
    precision for values up to 2^53.
12. Error messages include a path: `field`, `field[2]`, `field.sub`, and the problem. The
    **first** problem found, in schema order, is reported.
13. `additionalProperties: false` rejects unknown properties and names the first one.
    The default allows them.
14. A schema that is nil or empty accepts anything. A malformed schema (for example
    `type: 5`) is ignored rather than failing every call, and a debug log line is written
    once.

### Permission policy

15. `Decide` evaluates every rule that matches the call. If any match, the **most
    restrictive** wins (`deny` over `ask` over `allow`), regardless of rule order. The
    decision's `Rule` is the index of the first rule with the winning action.
16. A rule matches when **all** of its present parts match: `Tool` (via `MatchString`);
    every entry of `Match` (the argument must exist, be a string, and match); `Paths` (at
    least one path-like argument matches at least one pattern).
17. Path-like arguments are the string values of the keys `path`, `file`, `file_path`,
    `dir`, `cwd`, and every string in an array under `paths`. Each is resolved against
    `WorkDir`, cleaned, and matched both as an absolute path and, when inside `WorkDir`,
    as a path relative to it. A path that escapes `WorkDir` with `..` is matched in its
    cleaned absolute form, so `../../etc/passwd` cannot masquerade as relative.
18. **Shell control operators.** For an `allow` rule whose `Match` includes a string
    argument that contains any of `;`, `&&`, `||`, `|`, `&`, `` ` ``, `$(`, `<(`, `>`, `<`,
    `\n`, or `\r`, the rule is treated as **not matching**. `deny` and `ask` rules still
    match such strings. (A rule `git status*` must not allow `git status; rm -rf /`.)
19. If no rule matches: when `Default` is empty, use the tool's annotations: read-only and
    not open-world → `Allow`; destructive or open-world → `Ask`; otherwise `Ask`. When
    `Default` is set explicitly, use it for everything, with no annotation relaxation.
20. If the action is `Ask` and `interactive` is false, the result is `Headless` (default
    `Deny`). `Headless` may not be `Ask`; `Validate` rejects that.
21. `Validate` rejects: an empty `Tool`; an action other than allow, ask, or deny; an
    invalid glob (unbalanced `[`) in `Tool`, `Match`, or `Paths`; and `Headless: ask`. A rule
    with only `Tool` and `Action` is valid: it is a per-tool rule.
22. `MatchPath`: `**` matches zero or more path segments; `*` and `?` do not match `/`;
    `[abc]` and `[a-z]` character classes work; a trailing `/` in the pattern matches the
    directory and everything under it; matching is case-sensitive; patterns and paths are
    compared with `/` separators.
23. `MatchString`: `*` matches any run including empty and including `/`; `?` matches
    exactly one character; `[...]` classes work; everything else is literal; matching is
    case-sensitive and on the whole string.
24. The `Decision.Reason` names the rule (`rule 3: deny write **/.env*`) or the default
    that applied, so the UI can show why.

## Matrix

| ID | Case | Input / setup | Expected |
|---|---|---|---|
| REG01 | register then execute | echo tool | result content |
| REG02 | unknown tool | `Execute("nope")` | error result naming `nope` and listing known tools (max 10) |
| REG03 | duplicate registration | | error from `Register` |
| REG04 | nil tool, empty name | | errors |
| REG05 | `Replace` keeps position | replace the middle tool | order unchanged; new behaviour |
| REG06 | `Replace` of a missing tool registers it | | present at the end |
| REG07 | `Unregister` removes and reports | present, absent | true, false; `Names` updated |
| REG08 | invalid args rejected before the tool runs | required field missing | error result; tool not called (counter) |
| REG09 | empty and `null` args | for a tool with no required fields | treated as `{}`; runs |
| REG10 | args not an object | `[1]`, `"x"`, `5` | error result |
| REG11 | already-cancelled context | | `cancelled`; tool not called |
| REG12 | tool panics (sequential caller) | | error result `internal error in …`; no panic escapes |
| REG13 | tool panics with a non-string value | `panic(42)`, `panic(err)` | message includes the value |
| REG14 | tool returns an error | | `error: <tool>: <err>` |
| REG15 | concurrent `Execute` and `Register` | `-race`, 50 goroutines | no race |
| REG16 | `View` selects tools | `View("a","c")` | only those; unknown names ignored |
| REG17 | `View` is independent | register on the view | parent unchanged |
| REG18 | `Defs` order | | registration order |
| REG19 | `Progress` with nil `Emit` | | no panic |
| REG20 | `Progress` with `Emit` | | update delivered |
| REG21 | `Info` lookup | | found / not found |
| REG22 | `Result.Details` passes through | | unchanged |
| SCH01 | type string, integer, number, boolean, array, object, null | one valid and one invalid value each | accept / reject |
| SCH02 | integer accepts `3.0`, rejects `3.5` and `"3"` | | as stated |
| SCH03 | large integers | 2^53 and 2^53+1 | accepted as integers; no float rounding error |
| SCH04 | `required` missing | | `field: required property is missing` |
| SCH05 | nested path in message | `edits[1].oldText` missing | message contains `edits[1].oldText` |
| SCH06 | `additionalProperties:false` | extra key | rejected naming the key |
| SCH07 | `additionalProperties` default | extra key | accepted |
| SCH08 | `enum` | value outside | rejected, message lists allowed values |
| SCH09 | `items` | array with one bad element | rejected with index |
| SCH10 | `minimum`/`maximum` on integer and number | boundary values | inclusive |
| SCH11 | `minLength`/`maxLength` count runes, not bytes | `"é"` with `maxLength:1` | accepted |
| SCH12 | `minItems`/`maxItems` | | enforced |
| SCH13 | `type` as an array | `["string","null"]` | accepts both |
| SCH14 | unknown keywords ignored | `pattern`, `format`, `$ref` | accepted |
| SCH15 | malformed schema ignored | `type: 5` | accepts anything; logs once |
| SCH16 | nil and empty schema | | accepts anything |
| SCH17 | first problem in schema order | two problems | the first |
| SCH18 | `null` value for a non-null type | | rejected |
| SCH19 | duplicate keys in args JSON | `{"a":1,"a":2}` | last wins, no panic |
| SCH20 | deeply nested args (depth 1,000) | | no stack overflow; returns an error or accepts |
| PRM01 | no rules, read-only tool, `Default` empty | | allow |
| PRM02 | no rules, destructive tool | | ask |
| PRM03 | no rules, open-world tool | | ask |
| PRM04 | `Default: allow` | destructive tool | allow (no annotation relaxation either way) |
| PRM05 | `Default: deny` | read-only tool | deny |
| PRM06 | allow rule matches tool name | `bash` | allow; rule index and reason set |
| PRM07 | tool glob | `Tool: "mcp__*"` | matches `mcp__x__y` |
| PRM08 | most restrictive wins regardless of order | allow rule then deny rule, both match | deny |
| PRM09 | most restrictive wins, reversed order | | deny |
| PRM10 | ask beats allow | | ask |
| PRM11 | `Match` on an argument | `command: "git status*"` against `git status -s` | matches |
| PRM12 | `Match` argument missing or not a string | | rule does not match |
| PRM13 | allow rule with shell operator `;` | `git status; rm -rf /` | allow rule does **not** match |
| PRM14 | allow rule with `&&`, `\|\|`, `\|`, `&`, `` ` ``, `$(`, `<(`, `>`, `<`, newline, CR | one case each | not matched |
| PRM15 | deny rule with shell operator | `rm *` vs `ls; rm -rf x` | deny rule still matches |
| PRM16 | `Paths` match relative path | `**/.env*` vs `path: "app/.env.local"` | matches |
| PRM17 | `Paths` match absolute path | `/etc/**` vs `path: "/etc/passwd"` | matches |
| PRM18 | `..` escape | `WorkDir=/w`, `path: "../../etc/passwd"`, rule `/etc/**` | matches via the cleaned absolute path |
| PRM19 | path-like keys | `path`, `file`, `file_path`, `dir`, `cwd`, `paths: [..]` | each checked |
| PRM20 | non-path keys ignored | `content: "/etc/passwd"` | does not trigger a `Paths` rule |
| PRM21 | all conditions must hold | `Tool` matches, `Paths` does not | no match |
| PRM22 | ask, interactive | | ask |
| PRM23 | ask, not interactive, `Headless` empty | | deny |
| PRM24 | ask, not interactive, `Headless: allow` | | allow |
| PRM25 | allow and deny are unaffected by interactivity | | unchanged |
| PRM26 | reason names the rule | | contains the rule index and tool |
| PRM27 | `Validate`: empty tool, bad action, bad glob, `Headless: ask` | | each rejected |
| PRM28 | `Validate`: a valid policy | | nil |
| PRM29 | empty path argument | `path: ""` | not matched against any `Paths` rule |
| PRM30 | path with NUL byte | | not matched; no panic |
| GLB01 | `*` does not cross `/` in `MatchPath` | `src/*.go` vs `src/a/b.go` | false |
| GLB02 | `**` crosses | `src/**/*.go` vs `src/a/b/c.go` | true |
| GLB03 | `**` matches zero segments | `src/**/x.go` vs `src/x.go` | true |
| GLB04 | `?` single non-slash char | | as stated |
| GLB05 | character classes | `[a-c]x`, `[!a]x` or `[^a]x` | as stated |
| GLB06 | trailing slash matches directory contents | `build/` vs `build/a/b.o` | true |
| GLB07 | case sensitivity | | case-sensitive |
| GLB08 | leading `**/` matches at the root | `**/x` vs `x` | true |
| GLB09 | `MatchString` `*` crosses `/` | `git *` vs `git a/b c` | true |
| GLB10 | `MatchString` whole-string | `git` vs `git status` | false |
| GLB11 | unbalanced `[` | | `Validate` error; `Match*` returns false |
| GLB12 | empty pattern | | matches only the empty string |
| GLB13 | pathological pattern | `*a*a*a*a*a*a*a*b` vs a long `aaaa…` | returns in under 100 ms (no exponential backtracking) |
| GLB14 | Unicode | `é*` vs `éclair` | true |

## Fuzz and property tests

| Name | Asserts |
|---|---|
| `FuzzValidateArgs` | never panics for any schema and args; deterministic |
| `FuzzMatchPath` | never panics; never takes more than 100 ms for inputs up to 1 KiB |
| `FuzzMatchString` | same |
| `FuzzDecide` | never panics; result is always one of allow, ask, deny; `Headless` is never `ask` in the result |
| `TestProperty_PRM_MostRestrictive` | for random rule lists, permuting the rules never changes the decision's action |

## Out of scope

- Reading a policy from YAML (C11). Prompting the user (a client concern).
- Wiring the policy into the loop (C09).

## Acceptance

```
go run ./scripts/accept C04
```
