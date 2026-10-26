# Nova — Integration

How `her-go`, `polaris`, and `ivy` adopt Nova, and the migration strategy for
each. Integration begins at M8; this document is the target, not current work.
`examples/embed/` is the compiling, tested proof that the harness layer works
on its own; read it first, then this document for the per-project mapping.

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

A compiling, tested version of everything below lives in **`examples/embed/`**. It imports only the
harness layer (`agent`, `tool`, `provider/*`, `message`, `proto`) and `scripts/importcheck` fails if
it ever reaches into the application shell (`mode`, `config`, `session`, `tui`, `bundled`), so it cannot
rot into a sample nobody builds. Read it before writing an adapter; this section is the summary.

The shape, roughly:

```go
// 1. A provider the app builds from a key it holds. No config.Config, no nova login.
p := openrouter.New(openrouter.Config{APIKey: os.Getenv("OPENROUTER_API_KEY")})

// 2. A registry the app fills, with its own domain tools.
reg := tool.NewRegistry()
reg.MustRegister(mySearchTool); reg.MustRegister(fs.Read())

// 3. The app's dependency bundle rides on ToolData and is type-asserted back
//    inside a tool. Nova never imports the app's types.
cfg := agent.Config{
    Provider:  p,
    Model:     chosenModel,
    System:    mySystemPrompt,
    Tools:     reg.View("my_search", "read"), // per-run subset
    ToolData:  myDeps,
    Emit:      func(e proto.Event) { myEventBus.Publish(mapEvent(e)) },
    OnMessage: myStore.Append,                 // the app owns history
    Hooks: agent.Hooks{
        ToolCall:   func(ctx context.Context, c agent.ToolCall) *agent.CallDecision { return myPermission(c) },
        ToolResult: func(ctx context.Context, c agent.ToolCall, r tool.Result) tool.Result { return myRedact(r) },
        TurnEnd:    func(ctx context.Context, t agent.Turn) agent.TurnDecision { return myNudges(t) },
    },
}

// 4. The app keeps history itself; Run takes it and returns what is new.
res, err := agent.New(cfg).Run(ctx, myHistory, message.Message{Role: message.RoleUser, Content: prompt})
fmt.Println(res.Answer, res.Usage.PromptTokens+res.Usage.CompletionTokens, res.Usage.CostUSD)
```

Notes the example makes concrete:

- **History is the caller's.** `OnMessage` fires once per message the run produces (not for the history
  you passed in and not for the system prompt), so appending in the callback and passing the slice back
  never doubles a message.
- **Events are mapped, not leaked.** `Emit` hands over `proto.Event`; the app translates to its own type
  in one small function so its UI does not depend on the wire shape.
- **Control lives in hooks.** Ending a run early, injecting a nudge after a tool batch, and blocking a
  call are `TurnEnd` and `ToolCall`, not edits to the loop. `examples/embed` ends a run on a pending
  question recorded by a tool, the Polaris pattern.
- **Tool scoping is `Registry.View`.** It returns a registry sharing the tool values but with its own
  registration state, so two concurrent runs can offer different tools without racing.

## Sub-agents

Fanning out child agents is a primitive, not something each adopter rewrites. `agent/sub` runs a
child loop from a `sub.Parent` (the seams a child inherits) and a `sub.Task` (the prompt, the tool
view, an optional model, and the depth):

```go
pool := sub.New(sub.Parent{
    Provider: p, Model: model, ModelID: modelID, Tools: reg,
    Policy: policy, Ask: ask, ToolCall: dropBash, ToolResult: redact,
    Emit: s.emit, AddUsage: tctx.AddUsage,   // cost lands in the parent's run total
}, sub.Options{MaxDepth: 3, MaxConcurrent: 4})

res, err := pool.Spawn(ctx, sub.Task{
    System: myResearcherPrompt,
    Prompt: task,
    Tools:  []string{"search", "fetch"},      // the child sees nothing else
    Depth:  sub.DepthOf(tctx.Data),            // 0 for a tool the user called
})
```

The primitive owns the six things that are easy to get wrong: the scoped tool view, a fresh
`tool.Context.Data` per child (`sub.Depth{N+1}` unless a task supplies its own), event tagging, usage
folded into the parent exactly once, the depth and concurrency caps, and cancellation that follows
the parent's `ctx` with no leaked goroutine. A child's events arrive on `Parent.Emit` tagged with a
child `run` id; the run-level events do not (see [`PROTOCOL.md`](PROTOCOL.md#child-agent-events)).

`bundled/subagent` is a thin wrapper over it, so the in-process and extension paths share one
implementation.

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
  `offered(ctx)`). A per-run tool filter is `Registry.View`: build the registry once and pass
  `reg.View(names...)` as `agent.Config.Tools` for each run, as `examples/embed` does.
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

- [x] **Streaming, with reasoning deltas.** `text_delta` and `thinking_delta` are in the
      protocol (`proto/codes.go`), all three dialects stream them (the C02 stream tests), and
      an RPC client consumes them (`mode/rpc_conc_test.go`). An adopter still maps
      `proto.Event` to its own transport; `examples/embed` shows the shape.
- [x] **Per-call token and cost accounting.** `provider.Usage` (prompt, completion,
      reasoning, cached, cache write, provider cost) reaches `Result.Usage` and the `usage`
      event, and a nested `Context.Complete` reports through `AddUsage` (LPI07, D117).
- [x] **`ctx` cancellation aborting provider and tool work.** The run's context reaches every
      request and tool, a call cancelled before it starts still records a "cancelled"
      result, and `agent/sub` cancels children with the parent (LPY09).
- [x] **Per-run tool scoping.** `Registry.View` shares the tool values and holds its own
      registration state, so concurrent runs can offer different subsets (LPY01,
      `examples/embed`).
- [x] **Tool output transformation.** `Hooks.ToolResult` rewrites a result before it is
      recorded (LPH04), and a policy-denied call still passes through it (LPH20).
- [x] **Image content parts end to end.** Prompt attachments, tool-produced images, capability
      gating, and inline TUI rendering (D115 to D130, [`IMAGE.md`](IMAGE.md); SVA13 to SVA15,
      LPI04 to LPI08, IMG01 to IMG10).
- [ ] **Deferred tool loading** (her-go's `use_tools`). Not built; a per-run `View` is the
      current answer.
- [ ] **Model fallback as a harness policy.** A request routes to the provider its model names,
      and provider-level fallback passes through `ProviderOptions` (`openrouter.Routing`); a
      model-level fallback is the application's policy today.
- [ ] **Trace output equivalent to the current `tool.yaml` trace specs.** Not built;
      `tool.Context.Progress` is what a tool has.
- [ ] **Session/context behaviour equal to the current compaction.** Compared per adopter;
      Nova summarises at a cut point and never prunes the request (D134).
- [ ] **No new required config for the app's existing providers.** Only an adapter can answer
      this; it is the first Polaris workstream.

## Migration order

1. **Nova standalone** (M0–M4): prove it as a coding agent first.
2. **ivy**: build "Ask Ivy" on Nova directly; the gate validates the provider
   design.
3. **her-go**: port the least-coupled agent (worker or memory) first, then the
   driver.
4. **polaris**: add a parallel `runWithNova` behind a flag, compare against the
   existing loop in tests/benchmark, then switch.
