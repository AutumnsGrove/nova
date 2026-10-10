---
chunk: C03
title: "OpenRouter preset, login, model catalog"
packages: [./provider/openrouter, ./auth]
budgets: { ./provider/openrouter: 300, ./auth: 500 }
depends: [C02]
---

# C03 — OpenRouter, login, and the model catalog

## Goal

Make OpenRouter the working core provider: a preset on `openaicompat` with routing
options and cost, an OAuth PKCE login so `nova login` works the way Ori's does, a secure
key store, and the code-defined model catalog.

## Read first

- `provider/openrouter/openrouter.go` (the prototype preset, 64 lines).
- `docs/ARCHITECTURE.md` section 8; `docs/DECISIONS.md` D21 and D22.
- **Fetch and read, before writing any code:**
  1. OpenRouter's current OAuth PKCE documentation (the `/auth` page, the code-exchange
     endpoint, the request and response fields, and the key's lifetime).
  2. OpenRouter's Ori documentation (`openrouter.ai/docs/guides/ori/harness`), to learn how
     Ori hands a harness its credentials and model.
  If you cannot fetch them, **stop** and list the questions in the `Open questions` section of `NEXTSTEPS.md`. Do not guess
  endpoints or field names. The endpoints below are from memory and must be confirmed.

## Files

| File | Change | Purpose |
|---|---|---|
| `provider/openrouter/openrouter.go` | change | `New`, options |
| `provider/openrouter/routing.go` | create | typed routing options |
| `provider/openrouter/catalog.go` | create | model catalog entries |
| `provider/openrouter/*_test.go` | create | |
| `auth/pkce.go` | create | verifier, challenge, state |
| `auth/login.go` | create | the loopback login flow |
| `auth/store.go` | create | key store |
| `auth/resolve.go` | create | credential resolution order |
| `auth/*_test.go` | create | uses `httptest` as a fake OpenRouter |

## Interfaces

```go
// package openrouter
type Config struct {
    APIKey      string
    APIKeyFunc  func(ctx context.Context) (string, error)
    BaseURL     string            // default https://openrouter.ai/api/v1
    Referer     string            // default https://github.com/AutumnsGrove/nova
    Title       string            // default "Nova"
    HTTPClient  *http.Client
}
func New(cfg Config) *openaicompat.Provider   // Name() == "openrouter"

type Routing struct {
    Order             []string
    Only              []string
    Ignore            []string
    AllowFallbacks    *bool
    Sort              string   // "price", "throughput", "latency"
    Quantizations     []string
    RequireParameters *bool
    DataCollection    string   // "allow", "deny"
    ZDR               *bool
    MaxPrice          *Price
    PreferredMinThroughput, PreferredMaxLatency *float64
}
type Price struct{ Prompt, Completion float64 } // USD per million tokens
func (r Routing) Options() map[string]any // {"provider": {...}} for Request.ProviderOptions; empty Routing gives nil

// package auth
type Store interface {
    Load() (key string, err error)   // ErrNoKey if none
    Save(key string) error
    Delete() error
}
func NewFileStore(path string) Store // default path: os.UserConfigDir()/nova/auth.json, mode 0600
var ErrNoKey = errors.New("auth: no stored key")

type LoginOptions struct {
    AuthURL     string        // default https://openrouter.ai/auth
    ExchangeURL string        // default https://openrouter.ai/api/v1/auth/keys
    HTTPClient  *http.Client
    OpenBrowser func(url string) error // nil means print the URL only
    Out         io.Writer
    Timeout     time.Duration // default 5 minutes
    Listen      string        // default "127.0.0.1:0"
}
func Login(ctx context.Context, opts LoginOptions) (key string, err error)

func Resolve(cfgKey string, env func(string) string, store Store) (key, source string, err error)
// order: cfgKey, env OPENROUTER_API_KEY, store; source is "config", "env", or "store"
```

## Behaviour

### openrouter

1. `New` returns an `openaicompat.Provider` named `openrouter` with the base URL, the
   attribution headers `HTTP-Referer` and `X-Title`, and the key function wired in.
2. `Routing.Options()` renders exactly the documented OpenRouter field names:
   `order`, `only`, `ignore`, `allow_fallbacks`, `sort`, `quantizations`,
   `require_parameters`, `data_collection`, `zdr`, `max_price` (`{prompt, completion}`),
   `preferred_min_throughput`, `preferred_max_latency`. Unset fields are omitted. If every
   field is unset the result is `nil`.
3. The catalog registers a small set of models with context window and pricing, including
   `deepseek/deepseek-v4.1` (confirm the exact slug from OpenRouter's model list; do not
   guess), one large and one small model from each of two other families. A comment on
   each entry states where the numbers came from. Catalog entries are data, with a test
   that every entry has a non-empty ID, slug, provider, and a positive context window.

### auth

4. **PKCE.** The verifier is 43 to 128 characters of the unreserved set
   `[A-Za-z0-9-._~]`, from `crypto/rand`. The challenge is `base64url(sha256(verifier))`
   without padding. The method is `S256`. Verifiers are never logged.
5. **Login flow.** Start a listener on `Listen` (default a random loopback port). Build the
   auth URL with `callback_url` (the listener's address), `code_challenge`, and
   `code_challenge_method=S256`, plus a random `state` if the OpenRouter documentation
   supports it (confirm; if it does not, bind the flow to the single-use listener instead).
   Open the browser if `OpenBrowser` is set and always print the URL to `Out`. Wait for the
   callback, check `state`, exchange the `code` and verifier at `ExchangeURL`, and return
   the key. The listener accepts one callback and then closes.
6. The callback handler answers with a small HTML page ("You can close this tab") and never
   echoes request parameters into the page.
7. Failures: the user denies (an `error` parameter) → a clear error; the wrong `state` → an
   error and the page says so; no callback within `Timeout` → a timeout error; the exchange
   returns a non-2xx or a body without a key → an error that includes the status and a
   truncated body; `ctx` cancelled → `ctx.Err()`. In every case the listener is closed.
8. **Headless.** If `OpenBrowser` is nil the flow still works: the URL is printed and the
   user opens it elsewhere, provided the callback can reach the listener.
9. **Store.** `NewFileStore` writes `{"openrouter_key": "..."}` atomically (write a temp
   file in the same directory with mode 0600, then rename) and creates the directory with
   mode 0700. `Load` on a missing file returns `ErrNoKey`. `Load` on a file whose mode is
   more permissive than 0600 still loads but returns a non-fatal `*PermissionWarning`
   (wrapped with the key) so the caller can warn. `Delete` of a missing file is not an error.
   The key never appears in an error message or a log line.
10. **Resolve** returns the first non-empty value in the order config, environment,
    store, with the source name. If none, it returns `ErrNoKey`. Whitespace around a key is
    trimmed. An environment variable set to only whitespace counts as unset.

## Matrix

| ID | Case | Input / setup | Expected |
|---|---|---|---|
| ORT01 | provider name and base URL | `New(Config{APIKey:"k"})` | name `openrouter`; default URL |
| ORT02 | attribution headers sent | request to a test server | `HTTP-Referer` and `X-Title` present |
| ORT03 | custom referer and title | | used |
| ORT04 | key function used per request | function returns a new value each call | each request has its value |
| ORT05 | routing renders documented fields | all fields set | exact names and types |
| ORT06 | routing omits unset fields | only `Order` set | only `order` present |
| ORT07 | empty routing gives nil | `Routing{}` | `nil` |
| ORT08 | bool pointers distinguish false from unset | `AllowFallbacks: ptr(false)` | `allow_fallbacks:false` present |
| ORT09 | `max_price` shape | | `{prompt, completion}` numbers |
| ORT10 | routing options reach the wire | server records body | `provider` object present |
| ORT11 | catalog entries valid | every entry | non-empty fields, positive window |
| ORT12 | catalog has the default model | | registered under its stable ID |
| PKC01 | verifier length and alphabet | 1,000 verifiers | 43–128 chars, only unreserved characters |
| PKC02 | verifiers are unique | 1,000 verifiers | no duplicates |
| PKC03 | challenge for a known vector | RFC 7636 appendix B vector | matches the published challenge |
| PKC04 | challenge has no padding or `+` or `/` | | true |
| LOG01 | happy path | fake OpenRouter returns a key | `Login` returns it |
| LOG02 | auth URL has the required parameters | capture `OpenBrowser` arg | `callback_url`, `code_challenge`, method `S256` |
| LOG03 | exchange sends the verifier | fake records the POST | code and verifier present, matching the challenge |
| LOG04 | state mismatch | callback with a wrong state | error; page says so; no exchange made |
| LOG05 | user denies | callback with `error=access_denied` | clear error; no exchange |
| LOG06 | timeout | no callback, 50 ms timeout | timeout error; listener closed |
| LOG07 | context cancelled | | `context.Canceled`; listener closed |
| LOG08 | exchange returns 401 | | error with status; no key |
| LOG09 | exchange returns 200 with no key | | error |
| LOG10 | exchange returns malformed JSON | | error with truncated body |
| LOG11 | callback with no code | | error |
| LOG12 | second callback is refused | two hits | first handled; second gets a closed connection or 404 |
| LOG13 | callback page does not reflect input | `code=<script>` | page body does not contain the script text |
| LOG14 | listener binds to loopback only | | address is `127.0.0.1` |
| LOG15 | `OpenBrowser` fails | returns an error | URL still printed; flow continues |
| LOG16 | `OpenBrowser` nil | | URL printed; flow works with a manual hit |
| LOG17 | no goroutine or port leak after any failure | | `NoLeaks`; port reusable |
| STO01 | save then load | | same key |
| STO02 | file mode is 0600; directory 0700 | | verified with `Stat` |
| STO03 | load missing | | `ErrNoKey` |
| STO04 | save is atomic | simulate a failed rename | original file intact |
| STO05 | permissive mode warning | file at 0644 | loads; returns `*PermissionWarning` |
| STO06 | delete missing is fine | | nil |
| STO07 | corrupt file | not JSON | error that does not contain the file's contents |
| STO08 | key never appears in errors | every error path | `strings.Contains(err.Error(), key)` is false |
| STO09 | concurrent saves | 10 goroutines | file always valid JSON; `-race` clean |
| RES01 | order | all three set | config wins |
| RES02 | env over store | env and store set | env |
| RES03 | store only | | store, source `store` |
| RES04 | none | | `ErrNoKey` |
| RES05 | whitespace trimmed; whitespace-only env ignored | `"  k\n"`, `"   "` | `k`; falls through |
| RES06 | source names | | `config`, `env`, `store` |

## Fuzz and property tests

| Name | Asserts |
|---|---|
| `FuzzCallbackQuery` | arbitrary query strings to the callback handler never panic and never produce a key |
| `FuzzRoutingOptions` | any `Routing` renders valid JSON |

## Out of scope

- A refresh-token flow (confirm first whether OpenRouter issues one).
- Other providers, the `/responses` API, the `nova login` command itself (C12 wires it).
- Reading Ori's environment variables, **unless** the documentation fetched above makes it
  a few lines. If it does, add a matrix row and a test; if not, note it in `NEXTSTEPS.md`.

## Acceptance

```
go run ./scripts/accept C03
```
