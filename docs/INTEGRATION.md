# Nova — Integration

How `her-go`, `polaris`, and `ivy` adopt Nova, and the migration strategy for
each. Integration begins at M5; this document is the target, not current work.

## Strategy

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
    OnEvent:  func(e agent.Event) { myEventBus.Publish(e) },
    BeforeToolCall: func(c agent.ToolCall) *agent.Block { return myPermission(c) },
    AfterToolCall:  func(c agent.ToolCall, r tool.Result) tool.Result { return myRedact(r) },
    FinishTurn:     func(t agent.Turn) agent.TurnDecision { return myNudges(t) },
}

res, err := agent.Run(ctx, cfg, history)
```

## ivy — "Ask Ivy"

Ivy is the best first adopter: greenfield loop, three read-only tools, and a
`llm.Gate` that proves the provider-decorator design.

- **Provider.** Implement a small `provider.Provider` that delegates to
  `llm.Gate`'s chat path. The gate stays the single chokepoint; Nova never sees
  accounts, caps, or vetting.
  - `feature "ask"` is already defined with `endpoint: chat, vetted: true,
    multi: true`. The decorator's `Stream` performs the admission, writes the
    ledger row, then calls the underlying OpenAI-compatible request.
  - Step/spend caps map to `agent.Config.MaxTurns` plus the gate's reservation.
- **Tools.** `search_mail`, `read_mail`, `think` as `tool.Tool` implementations.
  `read_mail` records the message ids it actually read into `ToolData`; the final
  answer's citations are verified against that set (and optionally the DB).
- **Withheld mail.** The tools apply the same visibility rules the rest of ivy
  uses, so withheld messages never reach the model.
- **Trace.** `OnEvent` feeds the "quiet trace" of what was looked at.
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
  | `OnLoopStart` / `OnLoopExit` / `OnDone` | `OnEvent` + `FinishTurn` |
  | `PreTool` (dry-run, caps) | `BeforeToolCall` |
  | `PostToolResult` (mutation) | `AfterToolCall` |
  | `PostTool` (observe) | `OnEvent` (`EventToolResult`) |
  | `ContinuationMsg` / windows | `FinishTurn` (or a future core continuation option) |
  | `TraceCallback` | `OnEvent`, formatted from `tool.yaml` trace specs |
  | `ActiveToolGuard` | `BeforeToolCall` + per-run tool filtering |
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

- **Loop.** Replace `agent.Run`'s loop with `agent.Run`. Keep `Result` and the
  gateway contract.
- **Research nudges.** `trackResearchCall` and `trackSearchQuery` move into
  `FinishTurn`; they inject user-role messages and decide when to stop.
- **Pseudo tool calls.** `parsePseudoToolCalls` runs in `AfterToolCall` /
  `TransformContext`: a text-form tool call is dispatched and a corrective
  message injected.
- **Concurrent dispatch.** Replace `dispatchToolCallsConcurrently` with Nova's
  parallel mode (it already preserves order and recovers panics). Keep the
  code-exec serialization rule as a `BeforeToolCall`/dispatch policy.
- **Tool gating.** Polaris gates tools by configured API keys (`catalog.go`'s
  `offered(ctx)`). Nova needs a per-run tool filter — add `Config.FilterTools` or
  let the app build a per-run registry view. (Tracked as an M1 item.)
- **State.** `tools.Context`'s many closures become `ToolData`. Citations, cards,
  and charts are app state mutated by tools and read after the run.
- **Streaming.** `ctx.Emit("token" | "reasoning" | "tool_call" | ...)` maps from
  Nova's `OnEvent` and `OnTextDelta`/`OnReasoningDelta`.
- **Wrap-up.** Polaris's forced wrap-up at `maxTurns` becomes a `FinishTurn`
  behavior or a core "final answer with no tools" option.

## Compatibility checklist

Before a project switches, confirm Nova preserves:

- [ ] Streaming to the existing transport, with reasoning deltas.
- [ ] Model fallback and per-call token/cost accounting.
- [ ] `ctx` cancellation aborting in-flight provider and tool work.
- [ ] Per-agent / per-run tool scoping and deferred loading.
- [ ] Tool output transformation (redaction, rewrites).
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
