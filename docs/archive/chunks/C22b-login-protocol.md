---
chunk: C22b
title: "Login over the protocol (serve/rpc)"
packages: [./proto, ./mode]
budgets: { ./proto: 700, ./mode: 2540 }
depends: [C22]
---

# C22b — Login over the protocol

## Goal

A `serve` or `rpc` client can sign the server host in to a provider: list the providers and their
methods, start a sign-in, finish it, and sign out. C22 built the flows in `auth/providerauth` for the
TUI; this chunk exposes the same seam as protocol commands, so a headless host needs no shell to
authenticate.

## Read first

- `auth/providerauth/providerauth.go` (C22): `All`, `Get`, `Resolve`, `Provider.Begin`/`Save`/`Delete`,
  and the `Session` interface (`Prompt`, `URL`, `Code`, `Wait`, `Submit`, `Close`).
- `tui/ui/login.go` (C22): the local state machine this mirrors.
- `proto/codes.go` and `mode/commands.go`: how a command is named, dispatched, and answered.
- `docs/PROTOCOL.md` and `docs/TESTING.md` sections 3, 4, and 6.

## Files

| File | Create or change | Purpose |
|---|---|---|
| `proto/codes.go` | change | `CmdListProviders`, `CmdLogin`, `CmdLoginSubmit`, `CmdLoginCancel`, `CmdLogout`, `EventLoginEnd` |
| `proto/codec_test.go` | change | the new event name decodes to `Event` |
| `mode/login.go` | create | the five handlers and the in-flight flow table |
| `mode/commands.go` | change | dispatch the new commands |
| `mode/server.go` | change | the flow table on `Server`; `Close` cancels open flows |
| `mode/login_test.go` | create | the matrix rows |
| `docs/PROTOCOL.md` | change | document the commands and `login_end` |

## Interfaces

```go
// proto
const (
	CmdListProviders = "list_providers"
	CmdLogin         = "login"        // Name = provider id, Args = method (optional), Text = key (api_key only)
	CmdLoginSubmit   = "login_submit" // Name = provider id, Text = pasted redirect URL or input
	CmdLoginCancel   = "login_cancel" // Name = provider id
	CmdLogout        = "logout"       // Name = provider id
)

const EventLoginEnd = "login_end" // ID = provider id; Error set on failure, never the credential
```

`login` for an `api_key` method saves at once and answers `{provider, saved: true}`. For `subscription`
and `device` it answers `{provider, method, url, code, prompt}` and waits in the background; the outcome
is one `login_end` event.

## Behaviour

1. `list_providers` answers an array of `{id, name, methods, ready, source}` for every registered provider.
   `ready` and `source` come from `providerauth.Resolve` with the server's config key, `os.Getenv`, and home.
2. `login` requires `Name`. An unknown provider is `not_found`; an unsupported method is `bad_request`.
   An empty `Args` picks the provider's only method, and is `bad_request` when there is more than one.
3. `login` with the `api_key` method requires `Text`, saves it through `Provider.Save`, and answers without
   starting a flow. An empty key is `bad_request`.
4. `login` with an interactive method calls `Begin` with an empty `OpenBrowser` (a server never opens a
   browser; the client gets the URL), registers the flow under the provider id, and starts one goroutine
   that calls `Wait` and then `Save`.
5. One flow per provider: a second `login` for a provider with a flow in progress is `busy`.
6. When the flow finishes the server emits exactly one `login_end` with `ID` set to the provider id. On
   success `Error` is nil; on failure `Error` carries the code `provider_error` and a message.
7. `login_submit` feeds `Session.Submit`. With no flow it is `not_found`; a rejected input is `bad_request`
   and the flow stays open so the client can retry.
8. `login_cancel` closes the flow and removes it. It emits no `login_end`. With no flow it is `not_found`.
9. `logout` calls `Provider.Delete`, and answers `{provider, remaining}` where `remaining` is the source
   still configured (`env`, `config`) or empty. An unknown provider is `not_found`.
10. `Server.Close` cancels every open flow, and no goroutine outlives it.
11. A credential never appears in any response, event, or error message, including a failed save.

## Matrix

| ID | Case | Input / setup | Expected |
|---|---|---|---|
| LGP01 | list providers | `list_providers` on a fresh home | every registered provider appears with its methods; `ready` false |
| LGP02 | list reflects a saved key | save a key, then `list_providers` | that provider is `ready` with source `store` |
| LGP03 | unknown provider | `login` for `nope` | `not_found` |
| LGP04 | missing name | `login` with no `Name` | `bad_request` |
| LGP05 | api key saves | `login` api_key with a key | ok, `saved: true`, the store holds the key |
| LGP06 | api key empty | `login` api_key with empty `Text` | `bad_request`, nothing saved |
| LGP07 | method required when ambiguous | provider with two methods, empty `Args` | `bad_request` |
| LGP08 | unsupported method | method the provider lacks | `bad_request` |
| LGP09 | interactive begins | fake provider with a `Session` | response holds url/code/prompt; no event yet |
| LGP10 | no browser opened | interactive `login` | `Config.OpenBrowser` handed to `Begin` is nil |
| LGP11 | one flow per provider | second `login` while one runs | `busy` |
| LGP12 | flow completes | fake session `Wait` returns a credential | one `login_end` with nil `Error`; the store holds the key |
| LGP13 | flow fails | fake session `Wait` returns an error | one `login_end` with `provider_error`; nothing saved |
| LGP14 | submit feeds the flow | `login_submit` with input | the fake session's `Submit` receives it; ok |
| LGP15 | submit rejected | fake `Submit` returns an error | `bad_request`; the flow is still open |
| LGP16 | submit with no flow | `login_submit` for an idle provider | `not_found` |
| LGP17 | cancel | `login_cancel` during a flow | ok; the session is closed; no `login_end` |
| LGP18 | cancel with no flow | `login_cancel` for an idle provider | `not_found` |
| LGP19 | after cancel, login again | cancel, then `login` | ok (the slot is free) |
| LGP20 | logout deletes | save, then `logout` | ok; `remaining` empty; the store is empty |
| LGP21 | logout reports a remaining source | env key set, then `logout` | `remaining` is `env` |
| LGP22 | logout unknown | `logout` for `nope` | `not_found` |
| LGP23 | close cancels flows | open a flow, then `Server.Close` | the session is closed; `NoLeaks` passes |
| LGP24 | no secret on the wire | api_key login and a failing save, scanning every frame | the key string appears in no response or event |
| LGP25 | login_end decodes | encode then decode `login_end` | round-trips as an `Event` |
| LGP26 | list honours the config key | OpenCode Go key set in the loaded config | `ready` with source `config` |
| LGP27 | list marks the current provider | current model from OpenCode Go, then an unknown id | `current` is OpenCode Go, then OpenRouter (the router's fallback) |
| LGP28 | list carries the default model | `list_providers` | each row has its provider's `default_model` |

## Fuzz and property tests

| Name | Asserts |
|---|---|
| `FuzzLoginCommand` | any `login`/`login_submit`/`logout` command never panics and always yields a response with a stable code |
| `TestProperty_LGP_OneLoginEnd` | across random interleavings of login, submit, and cancel there is at most one `login_end` per started flow, and none after a cancel |

## Out of scope

- A TUI path for a remote server: the TUI only attaches in-process today (`tui/client.FromPipe`), so there
  is nothing to route. When a remote TUI exists, it calls these commands instead of `providerauth`.
- A new provider, a new auth method, or refresh tokens.
- Multi-user `serve`: a login writes the server host's credential store for the whole process.

## Acceptance

```
go run ./scripts/accept C22b
```
