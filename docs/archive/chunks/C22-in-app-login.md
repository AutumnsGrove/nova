---
chunk: C22
title: "In-app login: provider picker, OAuth, and API keys"
packages: [./auth, ./auth/providerauth, ./auth/opencode, ./mode/providers, ./tui/dialog, ./tui/ui]
budgets: { ./auth: 560, ./auth/providerauth: 400, ./auth/opencode: 760, ./mode/providers: 400, ./tui/dialog: 2050, ./tui/ui: 2350 }
depends: [C17, C20]
---

# C22 — In-app login: provider picker, OAuth, and API keys

## Goal

Signing in stops being a command you leave the app to run. `/login` opens a provider picker,
drives the provider's OAuth, device-code, or API-key flow in a dialog, and saves the credential
in the same stores `nova login` already uses; `/logout` removes one. The provider set is data:
each provider is one file that registers itself, so adding a third is one file and no change to
the picker or the UI.

This is **C22a**, the client-side half chosen by the human on 2026-10-13. A protocol `login`
command that lets `serve`/`rpc` clients sign in the server host (**C22b**) is deliberately out of
scope; the registry below is shaped so it can wrap it later.

## Read first

- `docs/TESTING.md` sections 3 to 6.
- `auth/login.go`, `auth/pkce.go`, `auth/store.go`, `auth/resolve.go` (the OpenRouter flow and
  its store), and `auth/opencode/opencode.go`, `auth/opencode/store.go` (the device flow).
- `mode/providers/providers.go` (how the provider and its keys are built today).
- `tui/firstrun/firstrun.go` (the existing masked-paste login screen; its bullet rendering is the
  precedent) and `tui/dialog/dialog.go`, `tui/dialog/input.go`, `tui/dialog/models.go`
  (the overlay, the text input, and the picker shape).
- `tui/ui/bridge.go` (intents and `applyLocalIntent`), `tui/ui/editor.go` (`localCommand`), and
  `tui/ui/model.go` (`Init`, `Update`, `scheduleTick`).
- Pi's reference behaviour, for shape only: `docs/slash-commands.md` and `docs/providers.md` in
  `@earendil-works/pi-coding-agent`, and the `OAuthSelectorComponent` in its bundle.

## Files

| File | Change | Purpose |
|---|---|---|
| `auth/login.go` | change | split the PKCE flow into `Begin`/`Submit`/`Wait`/`Close`; `Login` is built on them |
| `auth/providerauth/providerauth.go` | create | the registry, `Provider`, `Session`, `Status`, `Resolve` |
| `auth/providerauth/openrouter.go` | create | the OpenRouter provider, one file |
| `auth/providerauth/opencodego.go` | create | the OpenCode Go provider, one file |
| `auth/providerauth/*_test.go` | create | registry, resolve, and fake-provider rows |
| `auth/opencode/opencode.go` | change | add `Complete`; `Login` is built on it |
| `auth/opencode/store.go` | change | add `DeleteKey` |
| `mode/providers/providers.go` | change | resolve the OpenCode Go key per request, as OpenRouter already does |
| `tui/dialog/login.go` | create | provider picker, method picker, masked secret input |
| `tui/dialog/login_test.go` | create | the dialog rows |
| `tui/ui/login.go` | create | the login state machine, messages, and `tea.Cmd`s |
| `tui/ui/bridge.go` | change | `intentLogin` and friends, handled by `applyLocalIntent` |
| `tui/ui/editor.go` | change | `login` and `logout` in `localCommand` |
| `tui/ui/*_test.go` | change | the UI rows |
| `docs/TUI.md` | change | document `/login` and `/logout` |
| `docs/chunks/C15`, `C16`, `C17`, `C21` | change | raise the shared `./tui/ui` (and C17 `./tui/dialog`) budgets to the new counts |

`assets/logo/gen`, `proto`, and `mode/commands.go` are not touched. C22b will add the protocol
command.

## Interfaces

```go
// package auth

// Flow is one PKCE sign-in in progress. Begin binds the callback listener and
// builds the authorization URL; Wait blocks for the loopback callback or a
// value passed to Submit, then exchanges the code for a key.
type Flow struct { /* private */ }

// Begin starts a flow. opts carries the endpoints, browser opener, and timeout
// the old Login used. It returns the flow and the URL to open.
func Begin(opts BeginOptions) (*Flow, string, error)

// Submit hands the flow the redirect URL the browser landed on. It fails on a
// value without a matching code and state, and leaves Wait blocked.
func (f *Flow) Submit(pasted string) error

// Wait blocks until the callback or a Submit, then exchanges the code. It
// returns the API key.
func (f *Flow) Wait(ctx context.Context) (string, error)

// Close releases the listener and is safe to call more than once.
func (f *Flow) Close() error

// Login is Begin, print, open, Wait: the existing one-shot behaviour.

// package auth/opencode

// Complete finishes a device login after RequestDeviceCode returned. Login is
// RequestDeviceCode, print, open, Complete.
func Complete(ctx context.Context, cfg Config, device DeviceCode) (string, error)

// DeleteKey removes a stored key. Deleting a missing key is not an error.
func DeleteKey(path string) error

// package providerauth

// Method is one way a provider authenticates.
type Method string

const (
	OAuth  Method = "subscription" // browser sign-in (PKCE)
	Device Method = "device"       // device code
	APIKey Method = "api_key"      // a pasted key
)

// Credential is a saved login.
type Credential struct {
	Method Method
	Key    string
}

// Session is a started interactive sign-in. Begin returns one; the dialog
// renders it, Submit and Close feed it, and Wait finishes it.
type Session interface {
	Prompt() string
	URL() string
	Code() string
	Wait(ctx context.Context) (Credential, error)
	Submit(input string) error
	Close() error
}

// BeginFunc starts one method's flow.
type BeginFunc func(ctx context.Context, cfg Config) (Session, error)

// Config is what a provider needs to begin a flow.
type Config struct {
	Home string
	Env  func(string) string
}

// Provider describes one provider's authentication. Each provider file
// registers one.
type Provider struct {
	ID           string
	Name         string
	Methods      []Method
	EnvVars      []string
	DefaultModel string
	Begin        map[Method]BeginFunc
	Save         func(home string, c Credential) (path string, err error)
	Load         func(home string) (Credential, bool, error)
	Delete       func(home string) error
}

// Register adds a provider, replacing an entry with the same id.
func Register(p Provider)

// All returns every registered provider in registration order.
func All() []Provider

// Get returns a provider by id.
func Get(id string) (Provider, bool)

// Status is a provider's resolved authentication.
type Status struct {
	Provider Provider
	Method   Method
	Source   string // config | env | store | ""
	Ready    bool
}

// Resolve reports config, then env, then store. cfgKey is the provider's key
// from config, empty when the caller has none.
func Resolve(p Provider, cfgKey string, env func(string) string, home string) Status

// package dialog

// ProviderInfo is one row of the provider picker. Value is the provider id.
type ProviderInfo struct {
	ID      string
	Name    string
	Methods []string
	Ready   bool
	Source  string
}

// NewProviderPicker builds the /login and /logout provider list.
func NewProviderPicker(title string, items []ProviderInfo) Dialog

// NewMethodPicker builds the authentication-method list. Value is the chosen
// method label, so the dialog package stays free of auth imports.
func NewMethodPicker(provider string, methods []string) Dialog

// NewSecretInput builds a masked single-line input. Value is the text.
func NewSecretInput(title, placeholder string) Dialog
```

`tui/ui` gains, on `Model`: a `login *loginState` field, the intents `intentLogin`,
`intentLoginMethod`, and `intentLogout`, and the messages `loginBegunMsg`, `loginDoneMsg`,
`logoutDoneMsg`.

## Behaviour

**The registry**

1. `auth/providerauth` holds one process-wide registry. Each provider is a single file that
   calls `Register` from `init`; the package never switches on a provider id. `All` is in
   registration order and `Get` is by id. Registering a duplicate id replaces the entry.
2. Only OpenRouter (`openrouter`) and OpenCode Go (`opencode-go`) are registered. OpenRouter
   offers `OAuth` and `APIKey`; OpenCode Go offers `Device` and `APIKey`. Environment variables
   are `OPENROUTER_API_KEY` and `OPENCODE_GO_API_KEY`, and the default models are
   `deepseek/deepseek-v4.1` and `opencode-go/deepseek-v4.1-flash`.
3. `Resolve` tries config, then environment, then store, and reports the first that resolves and
   names it in `Source`. It reads the store on every call, so a credential saved in the same
   process resolves on the next call.
4. The credential stores are the existing ones: OpenRouter in
   `config.UserConfigDir(home)/auth.json`, OpenCode Go in `opencode.DefaultKeyPath(home)`. A save
   is atomic and mode 0600 and returns the path; a delete removes the stored credential only and
   is not an error when nothing is stored.

**The commands**

5. `/login` with no argument opens `Select provider to configure:` with one row per registered
   provider, each showing the display name, its methods, and a ready marker and source when it is
   already configured. Enter selects; Esc closes.
6. `/login <id>` skips the picker: one method begins it, two methods open
   `Select authentication method for <name>:` with the method labels. An unknown id sets the
   notice `unknown provider: <id>` and opens nothing.
7. `/logout` opens the same picker titled `Select provider to logout:`. Choosing a provider
   deletes its stored credential and sets `logged out of <name>`; when another source still
   resolves, the notice names it. Esc closes.
8. While the pickers are open the editor does not receive keys and the next queued extension
   dialog stays queued, as with every other local dialog.

**API key**

9. The `APIKey` method opens a masked input titled `Paste your <name> key`. Typing renders
   bullets; Enter saves through the provider's `Save`; Esc cancels and saves nothing.
10. The notice after a save is `logged in to <name>; credentials saved to <path>`. The key itself
    never appears in a notice, a transcript, a log, or any rendered line.

**OAuth and device**

11. `OAuth` calls `auth.Begin` inside a `tea.Cmd` and pushes a dialog titled
    `Sign in with <name>` that shows `Approve access in your browser. If it did not open, visit:`,
    the URL, and a second masked input
    `If the browser runs on another machine, paste the URL it was redirected to:`. The browser
    opener is called with the URL. A background command calls `Wait`.
12. Enter in the paste input calls `Submit`. A malformed value shows `not a valid redirect URL`
    and leaves the dialog and `Wait` open; a valid value completes the flow.
13. `Device` calls `opencode.RequestDeviceCode` in a `tea.Cmd` and pushes a dialog showing
    `Open <url> and enter code <code>` and opening the browser. A background command calls
    `opencode.Complete`.
14. Esc at any point calls `Close` and discards the flow. A flow closed before it finishes writes
    no credential, even if the provider later answers.
15. A failed flow shows the error text in the dialog with `enter to retry, esc to close`.

**After a login**

16. When a credential is saved, the model sends `get_commands` and `list_models` to refresh the
    catalog, and, when the provider has a default model, opens the model picker so the choice is
    immediate.
17. `mode/providers` resolves the OpenCode Go key inside the request path, as OpenRouter already
    does, so a key saved at runtime is used by the next request with no restart.

**Purity and failure modes**

18. `Update` and every dialog do no I/O. `Begin`, `Wait`, `Submit`, `Complete`, `Save`, `Load`,
    and `Delete` all run inside `tea.Cmd` functions, so the model stays testable without a
    terminal or the network.
19. A cancelled or failed flow leaves no credential and no half-written store; the existing
    atomic-save rules hold. Nothing here panics: every error is a value shown in the dialog or
    the notice.
20. Adding a provider is one new file in `auth/providerauth` and nothing else. A test registers a
    fake provider and drives the whole flow end to end with no network.

## Matrix

| ID | Case | Input / setup | Expected |
|---|---|---|---|
| SGN01 | registry holds both providers | `All` | openrouter then opencode-go; `Get` finds each |
| SGN02 | unknown id | `Get("nope")` | present false |
| SGN03 | duplicate id replaces | register a second `openrouter` | one entry, the new one |
| SGN04 | a new provider file is picked up | register a fake provider | `All` includes it with no other change |
| SGN05 | resolve config first | config key and env and store set | Source config |
| SGN06 | resolve env second | env and store set | Source env |
| SGN07 | resolve store third | only store set | Source store |
| SGN08 | nothing set | all empty | Ready false, Source "" |
| SGN09 | resolve is not cached | `Resolve`, then `Save`, then `Resolve` | the second call is Ready |
| SGN10 | `/login` opens the picker | model, empty transcript | `Select provider to configure:` with both rows |
| SGN11 | picker shows ready state | OpenRouter key in the store | its row says ready and the source |
| SGN12 | `/login openrouter` | command | method picker with subscription and API key |
| SGN13 | `/login opencode-go` | command | method picker with device and API key |
| SGN14 | `/login <id> <method>` | `login openrouter api_key` | the masked input opens directly |
| SGN15 | unknown provider | `/login nope` | notice names it, no dialog |
| SGN16 | Esc closes the picker | key | overlay empty, editor focused |
| SGN17 | `/logout` opens the picker | command | `Select provider to logout:` |
| SGN18 | API key input is masked | type `sk-test` | the view has bullets and not the text |
| SGN19 | Enter saves the key | fake provider | `Save` called once with the key |
| SGN20 | Esc cancels the key | fake provider | `Save` not called |
| SGN21 | the store is 0600 | real store | mode 0600 and the key round-trips |
| SGN22 | the notice names the path not the key | after a save | path present, key absent |
| SGN23 | OAuth begins | fake `Begin` returns a session | dialog shows the URL |
| SGN24 | the browser opens | `OpenBrowser` set | called with the URL |
| SGN25 | pasted redirect completes | Enter a valid redirect | `Submit` called, credential saved |
| SGN26 | malformed redirect stays open | Enter `garbage` | error shown, dialog still open, `Wait` still blocked |
| SGN27 | local callback completes | session completes on its own | credential saved, dialog closes |
| SGN28 | device dialog shows code | fake RequestDeviceCode | URL and code both shown |
| SGN29 | device poll completes | fake Complete | credential saved |
| SGN30 | Esc closes the flow | open an OAuth dialog, Esc | `Close` called, nothing saved |
| SGN31 | a closed flow cannot save later | Close, then let Wait finish | no credential written |
| SGN32 | a failed flow retries | session returns an error | error text and `enter to retry` |
| SGN33 | login refreshes the catalog | after a save | `get_commands` and `list_models` sent |
| SGN34 | default model opens the picker | provider has one | model picker on top |
| SGN35 | logout deletes the store | real store | key gone; env and config untouched |
| SGN36 | logout with another source | env still set | notice says still configured from env |
| SGN37 | live OpenCode Go key | `Build`, then save, then stream | the request carries the new key |
| SGN38 | no secret in any rendered line | a full flow | the key never appears in a view or notice |
| SGN39 | a fake provider needs no network | fake `Begin`/`Wait`/`Save` | the flow completes offline |
| SGN40 | `/login` while a run is active | run streaming, `/login` | allowed; the run is not disturbed |
| SGN41 | `ForModel`, OpenRouter | the OpenRouter default model | the `openrouter` provider |
| SGN42 | `ForModel`, OpenCode Go | the OpenCode Go default model | the `opencode-go` provider (the catalog name is `opencodego`) |
| SGN43 | `ForModel`, unknown model | an id the catalog does not know | the `openrouter` provider, as the request router does |
| SGN44 | `ConfigKeyFrom` | OpenCode Go with a config key; OpenRouter; a nil config | the key; empty; empty |
| SGN45 | the picker asks the server | `/login` | one `list_providers` command, no local credential read |
| SGN46 | a config key shows as ready | `list_providers` answers a `config`-sourced row | the picker row reads `ready (config)` |
| SGN47 | the picker fits its widest row | a ready row with a long note | the whole note is visible at the dialog's own width |
| SGN48 | the picker width is capped | a very long row, 60 columns available | the width never exceeds 60 |
| SGN49 | no picker when the current model works | login while the server reports the current provider ready | no `list_models`, no dialog, and a notice that points to `/model` |
| SGN50 | picker when the current model does not work | login while the current provider is not ready | the model picker opens |
| SGN51 | logout switches the model | the provider serving the current model is signed out and another is ready | `set_model` to the other provider's default model, and a notice naming it |
| SGN52 | logout with nothing to switch to | the only provider is signed out | no `set_model`, and a notice that points to `/login` |
| SGN53 | logout of another provider | the provider signed out does not serve the current model | the model is left alone |
| SGN54 | logout while an env key remains | the provider still resolves from the environment | the model is left alone; the notice names `env` |
| SGN55 | logout while a config key remains | the server still resolves a key from its config | the notice names `config`; the model is left alone |

## Fuzz and property tests

| Name | Asserts |
|---|---|
| `FuzzRedirect` | any pasted string: never panics; only a redirect URL with a matching code and state returns no error |
| `TestProperty_SGN_Secrets` | for a seeded sweep of keys, no rendered dialog, notice, or session line contains the key |
| `TestProperty_SGN_Registry` | registering N fake providers, `All` is stable and `Get` finds exactly the registered ones |

## Out of scope

- C22b: the protocol `login`/`logout` command so a `serve`/`rpc` client can sign in the server
  host. The registry is the seam it will use.
- Providers other than OpenRouter and OpenCode Go.
- OAuth token refresh, token revocation, and non-key credentials.
- Ambient cloud credentials (AWS, GCP) and reading a key from a `!command`.
- Installing providers, editing a provider catalog, or a `models.json`.
- A reduced-motion or theme setting.

## Acceptance

```
go run ./scripts/accept C22
```

Then, by hand: `go run ./cmd/nova` with no key, run `/login`, finish an OpenRouter sign-in, and
run `/logout`; confirm the picker, the browser, and the notices. Never print the key.
