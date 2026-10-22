# Nova — Integration

How `her-go`, `polaris`, and `ivy` adopt Nova, and the migration strategy for
each. Integration begins at M8; this document is the target, not current work.

## Strategy

These programs embed Nova as a **library**. None of them import `tui/` or `bundled/`
(the only packages that use `charm.land`); a consumer depends on
`github.com/AutumnsGrove/nova` and compiles none of the UI. Note that the shared
`go.mod` may still list the charm modules; they are not built into the consumer.
The loop is `agent.New(cfg).Run(...)` (see `docs/archive/chunks/C09-agent-loop.md`); the old
`agent.Run(ctx, cfg, history)` function is kept as a compatibility wrapper.

- **Adapter-first, no big bang.** Each project keeps its existing types and adds
  a thin Nova layer. Port one loop, prove it, then switch.
- **Nova is a versioned module.** Consumers use a local `replace` while it is
  unstable, then a git tag.
  ```
  require github.com/AutumnsGrove/nova v0.x.y
  replace github.com/AutumnsGrove/nova => ../nova
  ```
- **The application owns its dependencies.** Everything domain-specific lives on
  `tool.Context.Data` and in hooks. Nova never imports a consumer's packages.

## Shared integration shape

```go
provider := openrouter.New("")            // or the app's own Provider

reg := tool.NewRegistry()
for _, t := range myDomainTools { reg.MustRegister(t) }
reg.MustRegister(fs.Read()); reg.MustRegister(bash.Bash())

cfg := agent.Config{
    Provider: provider,
    Model:    chosenModel,
    System:   mySystemPrompt,
    Tools:    reg,
    WorkDir:  workDir,
    ToolData: myDeps,                     // the app's dependency bundle
    Emit:     func(e proto.Event) { myEventBus.Publish(e) },
    Hooks: agent.Hooks{
        ToolCall:   func(ctx context.Context, c agent.ToolCall) *agent.CallDecision { return myPermission(c) },
        ToolResult: func(ctx context.Context, c agent.ToolCall, r tool.Result) tool.Result { return myRedact(r) },
        TurnEnd:    func(ctx context.Context, t agent.Turn) agent.TurnDecision { return myNudges(t) },
    },
}

res, err := agent.Run(ctx, cfg, history)
```

## Decision models

A decision model answers typed questions about a shared state; it does not
generate text. The harness keeps the vendor behind a role, so an app asks for
`oracle` or `verify` and never names a model.

```yaml
decide:
  allow_aliases: false          # -latest / ~ ids are refused by default
  roles:
    oracle:
      model: typesafe/jev-1.14  # pin a version-level id
      fallback: typesafe/jev-1.13
      timeout_ms: 2500
```

Build the client once and pass it to the loop; tools reach it through
`tool.Context.Decide`, exactly as they reach `Complete`. (`decideor` is an alias
for `github.com/AutumnsGrove/nova/decide/openrouter`.)

```go
client := decideor.New(decideor.Config{
    APIKeyFunc: func(ctx context.Context) (string, error) { return myKey(ctx) },
})
decider := decide.NewRoles(decide.RolesConfig{
    Decider: client,
    Roles: map[string]decide.Role{
        "oracle": {Model: "typesafe/jev-1.14", Fallback: "typesafe/jev-1.13", Timeout: 2500 * time.Millisecond},
    },
})

cfg := agent.Config{
    // ...
    Decide: decider.Decide,
}

// in a tool:
resp, err := tctx.Decide(ctx, decide.Request{
    Model: "oracle",
    State: evidence, // a string, or []decide.Source
    Questions: map[string]decide.Question{
        "done": {Instructions: "is this turn complete?", Criteria: map[string]string{"yes": "...", "no": "..."}},
    },
})
if err == nil {
    tctx.AddUsage(resp.Usage) // counts in the run's totals
}
```

`decide/openrouter` picks the surface from the model id: a Jev id uses
`{base}/systemone` and every other id uses `{host}/api/alpha/decisions`, so an
app never chooses a surface. A role timeout is a typed, retryable error; a
fallback is tried while the caller's context is still live. `decide.List` reads
the live roster (`output_modalities=decisions`) for a benchmark, and
`Response.Model` records the build that actually answered.

The same request covers `noul` (a probability) and `score` (an ordered set of
`Levels`), not only `choice`. A bundled extension gets `Env.Decide`; a
subprocess extension declares `model:decide` and calls `sdk.Host.Decide` (the
`decide` frame). Print, serve, and rpc clients use the `decide` command, which
carries the same request; `/decide` shows the configured roles and
`/decide models` reads the live roster.

## ivy — "Ask Ivy"

Ivy is the best first adopter: greenfield loop, three read-only tools, and a
`llm.Gate` that proves the provider-decorator design.

- **Provider.** Implement a small `provider.Provider` that delegates to
  `llm.Gate`'s chat path. The gate stays the single chokepoint; Nova never sees
  accounts, caps, or vetting.
  - `feature "ask"` is already defined with `endpoint: chat, vetted: true,
    multi: true`. The decorator's `Stream` performs the admission, writes the
    ledger row, then calls the underlying OpenAI-compatible request.
  - Step/spend caps map to an explicitly set `agent.Config.MaxTurns` (there is no default cap) plus the gate's reservation.
- **Tools.** `search_mail`, `read_mail`, `think` as `tool.Tool` implementations.
  `read_mail` records the message ids it actually read into `ToolData`; the final
  answer's citations are verified against that set (and optionally the DB).
- **Withheld mail.** The tools apply the same visibility rules the rest of ivy
  uses, so withheld messages never reach the model.
- **Trace.** `Emit` feeds the "quiet trace" of what was looked at.
- **No action tools.** Nova's tool set for this loop is read-only by
  construction; a proposed action is returned as data for a UI button.

Open question: whether Nova should ship a generic `capability`/`vetted` notion or
leave all of it in the decorator. Current plan: leave it in the decorator; Nova
stays policy-free.

## her-go

her-go already has a shared loop (`agent_engine`) and YAML tools, so it is the
closest migration.

- **Loop.** Map `EngineConfig` hooks onto Nova's:
  | her-go | Nova |
  |---|---|
  | `OnLoopStart` / `OnLoopExit` / `OnDone` | `Emit` + `Hooks.TurnEnd` / `Hooks.AgentEnd` |
  | `PreTool` (dry-run, caps) | `Hooks.ToolCall` |
  | `PostToolResult` (mutation) | `Hooks.ToolResult` |
  | `PostTool` (observe) | `Emit` (`tool_end`) |
  | `ContinuationMsg` / windows | `Hooks.TurnEnd` (or a future core continuation option) |
  | `TraceCallback` | `Emit`, formatted from `tool.yaml` trace specs |
  | `ActiveToolGuard` | `Hooks.ToolCall` + per-run tool filtering |
- **Tools.** Reuse `tools/<name>/tool.yaml` directly with Nova's manifest loader.
  The large `tools.Context` becomes `tool.Context.Data`: handlers type-assert the
  bundle they need. This is the main mechanical change.
- **Agents.** her-go has five agents sharing tools with per-agent scoping
  (`agent: [main, memory, ...]` in each manifest). Nova's manifest `agent` field
  and a per-run registry view reproduce this.
- **Model clients.** `llm.Client` wraps as a Nova `Provider` (thin adapter), or
  is replaced by `openaicompat` reading the same config. The former is safer
  first.
- **Events.** Map `tui.Event` types from Nova's `Event` stream. her-go already
  builds its TUI on the `charm.land` stack (Bubble Tea, Lipgloss, Bubbles, Huh),
  so it can keep its frontend and later adopt Nova's when it matures.
- **Deferred loading.** her-go's `use_tools` becomes Nova's deferred-tool loader
  (M1).

## polaris

Polaris has the largest bespoke loop; most of it becomes hooks.

- **Loop.** Replace the bespoke loop with `agent.Agent.Run`. Keep `Result` and the
  gateway contract.
- **Research nudges.** `trackResearchCall` and `trackSearchQuery` move into
  `Hooks.TurnEnd`; they inject user-role messages and decide when to stop.
- **Pseudo tool calls.** `Config.TextFilter` is the live half: a stateful filter holds the first
  chunks until it knows the answer is not a pseudo call, then releases or drops them.
  `Hooks.StreamDelta` can further pass, rewrite, hold, or drop each chunk the filter shows. Both are
  live-only, so `Result.Answer` and the session still contain the original text for
  `parsePseudoToolCalls` to dispatch and correct.
  ```go
  cfg.TextFilter = func() agent.StreamFilter { return mySniffer() }
  ```
- **Concurrent dispatch.** Replace `dispatchToolCallsConcurrently` with Nova's
  parallel mode (it already preserves order and recovers panics). Keep the
  code-exec serialization rule as a `Hooks.ToolCall`/dispatch policy.
- **Tool gating.** Polaris gates tools by configured API keys (`catalog.go`'s
  `offered(ctx)`). Nova needs a per-run tool filter — add `Config.FilterTools` or
  let the app build a per-run registry view. (Tracked as an M1 item.)
- **Images.** Polaris's `ChatMessage.ImageURLs` maps to `message.Message.Parts`,
  `ctx.AddPendingImageMessage` to `tool.Context.Inject` (or `tool.Result.Parts`),
  and `ModelConfig.Multimodal` to `provider.Model.InputModalities`. The contract,
  the normalization limits, and the tool seams are in [`IMAGE.md`](IMAGE.md);
  `view_image`'s describe mode uses `Context.Complete`, its see mode returns
  `Result.Parts`.
- **State.** `tools.Context`'s many closures become `ToolData`. Citations, cards,
  and charts are app state mutated by tools and read after the run.
- **Streaming.** `ctx.Emit("token" | "reasoning" | "tool_call" | ...)` maps from
  Nova's `Emit` events (`text_delta`, `thinking_delta`, `tool_call`).
- **Wrap-up.** Polaris's forced wrap-up at `maxTurns` is `agent.Config.WrapUp`: one synthetic
  wrap-up instruction, one final request with no tools, and a caller fallback when the model still
  calls a tool. A `TurnEnd` hook cannot extend the budget.

## Compatibility checklist

Before a project switches, confirm Nova preserves:

- [ ] Streaming to the existing transport, with reasoning deltas.
- [ ] Model fallback and per-call token/cost accounting.
- [ ] `ctx` cancellation aborting in-flight provider and tool work.
- [ ] Per-agent / per-run tool scoping and deferred loading.
- [ ] Tool output transformation (redaction, rewrites).
- [ ] Image content parts end to end: prompt attachments, tool-produced images,
      model capability gating, and inline TUI rendering (`IMAGE.md`).
- [ ] Trace output equivalent to the current `tool.yaml` trace specs.
- [ ] Session/context behavior equal to the current compaction.
- [ ] No new required config for the app's existing providers.

## Migration order

1. **Nova standalone** (M0–M4): prove it as a coding agent first.
2. **ivy**: build "Ask Ivy" on Nova directly; the gate validates the provider
   design.
3. **her-go**: port the least-coupled agent (worker or memory) first, then the
   driver.
4. **polaris**: add a parallel `runWithNova` behind a flag, compare against the
   existing loop in tests/benchmark, then switch.
